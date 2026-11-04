package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/livrasand/gitGost/internal/git"

	"github.com/gin-gonic/gin"
)

const (
	remoteJobsMax = 100
	remoteJobsTTL = 24 * time.Hour

	remoteJobMaxConcurrent = 3
	remoteJobTimeout       = 30 * time.Minute
	maxBundleSize          = 4 * 1024 * 1024 * 1024
)

const (
	rjQueued    = "queued"
	rjRunning   = "running"
	rjReady     = "ready"
	rjFailed    = "failed"
	rjCancelled = "cancelled"
)

type remoteJobResult struct {
	Slug          string `json:"slug"`
	Cid           string `json:"cid"`
	Size          int64  `json:"size"`
	Sha256        string `json:"sha256"`
	DownloadURL   string `json:"downloadUrl"`
	DirectURL     string `json:"directUrl"`
	DefaultBranch string `json:"defaultBranch"`
	Filename      string `json:"filename"`
	ExpiresAt     string `json:"expiresAt"`
}

type remoteJob struct {
	ID       string
	Status   string
	URL      string
	Progress string
	Result   *remoteJobResult
	Error    string
	TmpDir   string
	Created  time.Time
}

var remoteJobs = newBoundedMap[*remoteJob](remoteJobsMax, remoteJobsTTL)

var remoteJobSlots = make(chan struct{}, remoteJobMaxConcurrent)

var remoteJobCancels sync.Map

var openbinClient = newSafeHTTPClient(30 * time.Minute)

func CreateRemoteJobHandler(c *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cuerpo JSON inválido"})
		return
	}
	if !validRepoURL(req.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL de repositorio inválida"})
		return
	}

	select {
	case remoteJobSlots <- struct{}{}:
	default:
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "demasiados jobs en curso, inténtalo más tarde"})
		return
	}

	id := newRemoteJobID()
	job := &remoteJob{ID: id, Status: rjQueued, URL: req.URL, Created: time.Now()}
	remoteJobs.Set(id, job)
	go runRemoteJob(job)
	c.JSON(http.StatusOK, gin.H{"id": id, "status": rjQueued})
}

func GetRemoteJobHandler(c *gin.Context) {
	id := c.Param("id")
	job, ok := remoteJobs.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job no encontrado"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":       job.ID,
		"status":   job.Status,
		"progress": job.Progress,
		"error":    job.Error,
		"result":   job.Result,
	})
}

func DeleteRemoteJobHandler(c *gin.Context) {
	id := c.Param("id")
	if job, ok := remoteJobs.Get(id); ok && job.Status != rjCancelled {
		next := *job
		next.Status = rjCancelled
		next.Progress = "Cancelado"
		remoteJobs.Set(id, &next)
	}
	if v, ok := remoteJobCancels.LoadAndDelete(id); ok {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
	}
	c.Status(http.StatusNoContent)
}

func jobCancelled(id string) bool {
	cur, ok := remoteJobs.Get(id)
	return ok && cur.Status == rjCancelled
}

func runRemoteJob(job *remoteJob) {
	defer func() { <-remoteJobSlots }()

	ctx, cancel := context.WithTimeout(context.Background(), remoteJobTimeout)
	defer cancel()
	remoteJobCancels.Store(job.ID, cancel)
	defer remoteJobCancels.Delete(job.ID)

	if jobCancelled(job.ID) {
		return
	}

	dir := ""
	publish := func(status, progress, errMsg string, result *remoteJobResult) {
		next := *job
		next.Status = status
		next.Progress = progress
		next.Error = errMsg
		next.Result = result
		next.TmpDir = dir
		remoteJobs.Set(job.ID, &next)
	}
	publish(rjRunning, "Preparando descarga...", "", nil)

	var err error
	dir, err = os.MkdirTemp("", "gitgost-bundle-")
	if err != nil {
		publish(rjFailed, "", fmt.Sprintf("crear directorio temporal: %v", err), nil)
		return
	}
	defer os.RemoveAll(dir)

	publish(rjRunning, "Descargando repositorio por capas...", "", nil)
	bundlePath, defaultBranch, err := git.CreateBundle(ctx, job.URL, dir)
	if err != nil {
		if !jobCancelled(job.ID) {
			publish(rjFailed, "", err.Error(), nil)
		}
		return
	}
	if jobCancelled(job.ID) {
		return
	}

	size, err := fileSize(bundlePath)
	if err != nil {
		publish(rjFailed, "", fmt.Sprintf("tamaño del bundle: %v", err), nil)
		return
	}
	if size > maxBundleSize {
		publish(rjFailed, "", fmt.Sprintf("el bundle excede el tamaño máximo de %d bytes", maxBundleSize), nil)
		return
	}
	hash, err := sha256File(bundlePath)
	if err != nil {
		publish(rjFailed, "", fmt.Sprintf("hash del bundle: %v", err), nil)
		return
	}

	publish(rjRunning, "Subiendo bundle a Openbin...", "", nil)
	result, err := openbinUpload(ctx, bundlePath, bundleFilename(job.URL), size, hash)
	if err != nil {
		if !jobCancelled(job.ID) {
			publish(rjFailed, "", err.Error(), nil)
		}
		return
	}
	if jobCancelled(job.ID) {
		return
	}
	result.DefaultBranch = defaultBranch

	publish(rjReady, "Bundle listo", "", result)
}

func openbinUpload(ctx context.Context, bundlePath, filename string, size int64, hash string) (*remoteJobResult, error) {
	base := strings.TrimRight(os.Getenv("OPENBIN_URL"), "/")
	if base == "" {
		base = "https://openbin.livrasand.com"
	}
	expiresIn := os.Getenv("OPENBIN_BUNDLE_TTL")
	if expiresIn == "" {
		expiresIn = "604800"
	}

	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("mode", "presign")
	_ = mw.WriteField("sha256", hash)
	_ = mw.WriteField("size", strconv.FormatInt(size, 10))
	_ = mw.WriteField("filename", filename)
	_ = mw.WriteField("mime", "application/octet-stream")
	_ = mw.WriteField("expires_in", expiresIn)
	_ = mw.Close()

	var presign openbinBinResponse
	if err := openbinPost(ctx, base+"/api/upload", mw.FormDataContentType(), &form, &presign); err != nil {
		return nil, fmt.Errorf("pedir subida a Openbin: %w", err)
	}

	if presign.AlreadyExists {
		if presign.DownloadURL == "" {
			return nil, fmt.Errorf("Openbin devolvió un bin sin downloadUrl")
		}
		return &remoteJobResult{
			Slug:        presign.Slug,
			Cid:         presign.Cid,
			Size:        size,
			Sha256:      hash,
			DownloadURL: presign.DownloadURL,
			DirectURL:   presign.DirectURL,
			Filename:    presign.Filename,
			ExpiresAt:   presign.ExpiresAt,
		}, nil
	}
	if presign.PresignedURL == "" || presign.UploadToken == "" {
		return nil, fmt.Errorf("Openbin no devolvió URL firmada (mode=%q)", presign.Mode)
	}

	if err := openbinPut(ctx, presign.PresignedURL, bundlePath, size); err != nil {
		return nil, fmt.Errorf("subir bundle a Filebase: %w", err)
	}

	confirmBody, _ := json.Marshal(map[string]string{
		"uploadToken": presign.UploadToken,
		"sha256":      hash,
	})
	var confirm openbinBinResponse
	if err := openbinPost(ctx, base+"/api/upload/confirm", "application/json", bytes.NewReader(confirmBody), &confirm); err != nil {
		return nil, fmt.Errorf("confirmar subida en Openbin: %w", err)
	}
	if confirm.Slug == "" || confirm.Cid == "" || confirm.DownloadURL == "" {
		return nil, fmt.Errorf("Openbin devolvió una confirmación incompleta")
	}
	return &remoteJobResult{
		Slug:        confirm.Slug,
		Cid:         confirm.Cid,
		Size:        size,
		Sha256:      hash,
		DownloadURL: confirm.DownloadURL,
		DirectURL:   confirm.DirectURL,
		Filename:    confirm.Filename,
		ExpiresAt:   confirm.ExpiresAt,
	}, nil
}

type openbinBinResponse struct {
	Mode          string `json:"mode"`
	AlreadyExists bool   `json:"alreadyExists"`
	PresignedURL  string `json:"presignedUrl"`
	UploadToken   string `json:"uploadToken"`
	ExpiresIn     int    `json:"expiresIn"`
	Slug          string `json:"slug"`
	Cid           string `json:"cid"`
	Filename      string `json:"filename"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	URL           string `json:"url"`
	DirectURL     string `json:"directUrl"`
	DownloadURL   string `json:"downloadUrl"`
	ExpiresAt     string `json:"expiresAt"`
}

func openbinPost(ctx context.Context, rawURL, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := openbinClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decodificar respuesta: %w", err)
	}
	return nil
}

func openbinPut(ctx context.Context, rawURL, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := openbinClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func validRepoURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return false
	}
	switch u.Hostname() {
	case "github.com", "gitlab.com", "codeberg.org":
	default:
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return false
	}
	return isValidRepoName(parts[0]) && isValidRepoName(strings.TrimSuffix(parts[1], ".git"))
}

func bundleFilename(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "repo.bundle"
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "repo.bundle"
	}
	return parts[0] + "-" + strings.TrimSuffix(parts[1], ".git") + ".bundle"
}

func newRemoteJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

func fileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
