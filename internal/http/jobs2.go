package http

import (
	"bytes"
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
	"time"

	"github.com/livrasand/gitGost/internal/git"

	"github.com/gin-gonic/gin"
)

// Fase 2 — jobs remotos de descarga con Range Requests server-side.
//
// El servidor clona el repositorio por capas, crea un bundle completo y lo
// sube a Openbin (presign + PUT directo a Filebase + confirm), que actúa como
// CDN con TTL. El cliente descarga el bundle por rangos de bytes directo de
// Filebase (resume a nivel byte) y materializa el repo con `git clone`.

const (
	remoteJobsMax = 100
	remoteJobsTTL = 24 * time.Hour
)

// Estados de un job remoto.
const (
	rjQueued  = "queued"
	rjRunning = "running"
	rjReady   = "ready"
	rjFailed  = "failed"
)

// remoteJobResult es el artefacto final de un job remoto: un bundle en Openbin.
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

// remoteJob es el estado de un trabajo de descarga en el servidor. El worker
// publica copias nuevas del struct (nunca muta un valor ya publicado), así los
// lectores del boundedMap siempre ven estados coherentes.
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

// remoteJobs es la cola de trabajos remotos en memoria (TTL: los resultados
// dejan de estar disponibles pasadas 24 h).
var remoteJobs = newBoundedMap[*remoteJob](remoteJobsMax, remoteJobsTTL)

// openbinClient permite subir bundles grandes sin el timeout corto del proxy.
var openbinClient = &http.Client{Timeout: 30 * time.Minute}

// CreateRemoteJobHandler crea un trabajo de descarga y lo ejecuta en background.
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

	id := newRemoteJobID()
	job := &remoteJob{ID: id, Status: rjQueued, URL: req.URL, Created: time.Now()}
	remoteJobs.Set(id, job)
	go runRemoteJob(job)
	c.JSON(http.StatusOK, gin.H{"id": id, "status": rjQueued})
}

// GetRemoteJobHandler devuelve el estado actual de un trabajo remoto.
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

// DeleteRemoteJobHandler elimina un trabajo remoto y limpia su directorio
// temporal. Si el worker aún corre, su publicación posterior simplemente
// reinserta un resultado que el TTL evictará.
func DeleteRemoteJobHandler(c *gin.Context) {
	id := c.Param("id")
	if job, ok := remoteJobs.Get(id); ok && job.TmpDir != "" {
		_ = os.RemoveAll(job.TmpDir)
	}
	remoteJobs.Delete(id)
	c.Status(http.StatusNoContent)
}

// runRemoteJob ejecuta el trabajo en background: clona por capas, crea el
// bundle, lo sube a Openbin y publica el resultado.
func runRemoteJob(job *remoteJob) {
	publish := func(status, progress, errMsg string, result *remoteJobResult) {
		next := *job
		next.Status = status
		next.Progress = progress
		next.Error = errMsg
		next.Result = result
		remoteJobs.Set(job.ID, &next)
	}
	publish(rjRunning, "Preparando descarga...", "", nil)

	dir, err := os.MkdirTemp("", "gitgost-bundle-")
	if err != nil {
		publish(rjFailed, "", fmt.Sprintf("crear directorio temporal: %v", err), nil)
		return
	}
	defer os.RemoveAll(dir)

	jobCopy := *job
	jobCopy.TmpDir = dir
	remoteJobs.Set(job.ID, &jobCopy)

	publish(rjRunning, "Descargando repositorio por capas...", "", nil)
	bundlePath, defaultBranch, err := git.CreateBundle(job.URL, dir)
	if err != nil {
		publish(rjFailed, "", err.Error(), nil)
		return
	}

	size, err := fileSize(bundlePath)
	if err != nil {
		publish(rjFailed, "", fmt.Sprintf("tamaño del bundle: %v", err), nil)
		return
	}
	hash, err := sha256File(bundlePath)
	if err != nil {
		publish(rjFailed, "", fmt.Sprintf("hash del bundle: %v", err), nil)
		return
	}

	publish(rjRunning, "Subiendo bundle a Openbin...", "", nil)
	result, err := openbinUpload(bundlePath, bundleFilename(job.URL))
	if err != nil {
		publish(rjFailed, "", err.Error(), nil)
		return
	}
	result.Size = size
	result.Sha256 = hash
	result.DefaultBranch = defaultBranch

	publish(rjReady, "Bundle listo", "", result)
}

// openbinUpload sube un bundle a Openbin con el flujo presign:
// 1) POST /api/upload?mode=presign → URL firmada de Filebase + token
// 2) PUT del objeto directo a Filebase (streaming, sin pasar por Vercel)
// 3) POST /api/upload/confirm → CID + bin con expiración
func openbinUpload(bundlePath, filename string) (*remoteJobResult, error) {
	base := strings.TrimRight(os.Getenv("OPENBIN_URL"), "/")
	if base == "" {
		base = "https://openbin.livrasand.com"
	}
	expiresIn := os.Getenv("OPENBIN_BUNDLE_TTL")
	if expiresIn == "" {
		expiresIn = "604800" // 7 días por defecto
	}

	hash, err := sha256File(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("calcular sha256 del bundle: %w", err)
	}
	size, err := fileSize(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("tamaño del bundle: %w", err)
	}

	// 1) Pedir la URL firmada de subida.
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("mode", "presign")
	_ = mw.WriteField("sha256", hash)
	_ = mw.WriteField("size", strconv.FormatInt(size, 10))
	_ = mw.WriteField("filename", filename)
	_ = mw.WriteField("expires_in", expiresIn)
	_ = mw.Close()

	var presign openbinBinResponse
	if err := openbinPost(base+"/api/upload", mw.FormDataContentType(), &form, &presign); err != nil {
		return nil, fmt.Errorf("pedir subida a Openbin: %w", err)
	}

	// Dedup de Openbin: si el bundle ya está subido, no hace falta el PUT.
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

	// 2) Subir el objeto directo a Filebase.
	if err := openbinPut(presign.PresignedURL, bundlePath); err != nil {
		return nil, fmt.Errorf("subir bundle a Filebase: %w", err)
	}

	// 3) Confirmar y registrar el bin.
	confirmBody, _ := json.Marshal(map[string]string{
		"uploadToken": presign.UploadToken,
		"sha256":      hash,
	})
	var confirm openbinBinResponse
	if err := openbinPost(base+"/api/upload/confirm", "application/json", bytes.NewReader(confirmBody), &confirm); err != nil {
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

// openbinBinResponse es la respuesta de los endpoints de Openbin (presign y confirm).
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

// openbinPost hace un POST a Openbin y decodifica la respuesta JSON.
func openbinPost(rawURL, contentType string, body io.Reader, out any) error {
	resp, err := openbinClient.Post(rawURL, contentType, body)
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

// openbinPut sube el bundle a la URL firmada de Filebase (streaming).
func openbinPut(rawURL, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequest(http.MethodPut, rawURL, f)
	if err != nil {
		return err
	}
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

// validRepoURL valida una URL https de repositorio en los hosts soportados.
func validRepoURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
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

// bundleFilename deriva un nombre de archivo para el bundle en Openbin.
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

// newRemoteJobID genera un identificador corto y aleatorio para el job.
func newRemoteJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// fileSize devuelve el tamaño en bytes de un archivo.
func fileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// sha256File calcula el hash sha256 de un archivo sin cargarlo en memoria.
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
