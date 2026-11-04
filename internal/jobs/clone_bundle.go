package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errRemoteJobsUnsupported = errors.New("remote jobs unsupported")

var remotePollInterval = 2 * time.Second

var (
	remotePollTimeout = 30 * time.Minute
	maxPollErrors     = 3
)

var jobHTTPClient = &http.Client{Timeout: 60 * time.Second}

var prefixHost = map[string]string{
	"gh": "github.com",
	"gl": "gitlab.com",
	"cb": "codeberg.org",
}

type remoteJobState struct {
	ID       string               `json:"id"`
	Status   string               `json:"status"`
	Progress string               `json:"progress"`
	Error    string               `json:"error"`
	Result   *remoteJobBundleInfo `json:"result"`
}

type remoteJobBundleInfo struct {
	Slug          string `json:"slug"`
	Cid           string `json:"cid"`
	Size          int64  `json:"size"`
	Sha256        string `json:"sha256"`
	DownloadURL   string `json:"downloadUrl"`
	DirectURL     string `json:"directUrl"`
	Filename      string `json:"filename"`
	DefaultBranch string `json:"defaultBranch"`
}

func serverBase() string {
	if v := os.Getenv("GITGOST_SERVER"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://gitgost.livrasand.com"
}

func runCloneBundle(s *Store, job *Job) error {
	if job.Target == "" {
		return fmt.Errorf("job de clone sin directorio destino")
	}

	originalURL, err := originalRepoURL(job.URL)
	if err != nil {
		return errRemoteJobsUnsupported
	}

	_ = s.SetProgress(job.ID, "Creando job de descarga en el servidor...")
	id, err := createRemoteJob(originalURL)
	if err != nil {
		if errors.Is(err, errRemoteJobsUnsupported) {
			return err
		}
		return fmt.Errorf("crear job remoto: %w", err)
	}

	result, err := waitRemoteJob(s, job, id)
	if err != nil {
		return err
	}
	if result == nil || result.DownloadURL == "" {
		return fmt.Errorf("el job remoto %s no devolvió una URL de descarga", id)
	}

	cacheFile := bundleCachePath(job.ID)
	if err := downloadBundle(s, job, result.DownloadURL, cacheFile, result.Size, result.Sha256); err != nil {
		return fmt.Errorf("descargar bundle: %w", err)
	}

	if err := materializeClone(cacheFile, job.Target, originForJob(job, originalURL), result.DefaultBranch); err != nil {
		return err
	}
	if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("eliminar bundle del cache: %w", err)
	}
	_ = s.SetProgress(job.ID, "Clone completado")
	return nil
}

func originForJob(job *Job, fallback string) string {
	if job.Origin != "" {
		return job.Origin
	}
	return fallback
}

func originalRepoURL(rewritten string) (string, error) {
	u, err := url.Parse(rewritten)
	if err != nil {
		return "", fmt.Errorf("URL reescrita inválida: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", fmt.Errorf("URL reescrita no reconocida: %s", rewritten)
	}
	host, ok := prefixHost[parts[1]]
	if !ok {
		return "", fmt.Errorf("host no soportado: %s", parts[1])
	}
	return fmt.Sprintf("https://%s/%s/%s.git", host, parts[2], parts[3]), nil
}

func createRemoteJob(repoURL string) (string, error) {
	body, _ := json.Marshal(map[string]string{"url": repoURL})
	req, err := http.NewRequest(http.MethodPost, serverBase()+"/v2/jobs", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := jobHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return "", errRemoteJobsUnsupported
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decodificar respuesta: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("el servidor no devolvió un id de job")
	}
	return out.ID, nil
}

func waitRemoteJob(s *Store, job *Job, id string) (*remoteJobBundleInfo, error) {
	deadline := time.Now().Add(remotePollTimeout)
	consecutiveErrors := 0
	for {
		st, err := getRemoteJob(id)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxPollErrors {
				return nil, fmt.Errorf("el servidor no responde al consultar el job %s: %w", id, err)
			}
			_ = s.SetProgress(job.ID, fmt.Sprintf(
				"Servidor no responde (intento %d/%d), reintentando...", consecutiveErrors, maxPollErrors))
			time.Sleep(remotePollInterval)
			continue
		}
		consecutiveErrors = 0

		switch st.Status {
		case "ready":
			if st.Result == nil {
				return nil, fmt.Errorf("el job remoto %s está listo pero sin resultado", id)
			}
			return st.Result, nil
		case "failed":
			msg := fmt.Sprintf("el servidor no pudo crear el bundle (%s)", id)
			if st.Error != "" {
				msg += ": " + st.Error
			}
			return nil, errors.New(msg)
		case "cancelled":
			return nil, fmt.Errorf("el job remoto %s fue cancelado", id)
		default:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf(
					"timeout esperando el bundle del job %s (más de %s)", id, remotePollTimeout)
			}
			msg := fmt.Sprintf("Servidor: %s", st.Status)
			if st.Progress != "" {
				msg += " — " + st.Progress
			}
			_ = s.SetProgress(job.ID, msg)
			time.Sleep(remotePollInterval)
		}
	}
}

func getRemoteJob(id string) (*remoteJobState, error) {
	req, err := http.NewRequest(http.MethodGet, serverBase()+"/v2/jobs/"+id, nil)
	if err != nil {
		return nil, err
	}

	resp, err := jobHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var st remoteJobState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("decodificar estado: %w", err)
	}
	return &st, nil
}

func downloadBundle(s *Store, job *Job, downloadURL, dest string, size int64, sha string) error {
	if st, err := os.Stat(dest); err == nil && st.Size() == size {
		ok, err := hashMatches(dest, sha)
		if err != nil {
			return err
		}
		if ok {
			_ = s.SetProgress(job.ID, "Bundle ya descargado en cache")
			return nil
		}
		_ = os.Remove(dest)
	}

	if err := downloadWithRetry(s, job, downloadURL, dest, size); err != nil {
		return err
	}
	ok, err := hashMatches(dest, sha)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_ = s.SetProgress(job.ID, "Bundle con hash incorrecto; descargando de nuevo...")
	_ = os.Remove(dest)
	if err := downloadWithRetry(s, job, downloadURL, dest, size); err != nil {
		return err
	}
	if ok, err := hashMatches(dest, sha); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("el bundle descargado no coincide con el sha256 esperado")
	}
	return nil
}

func downloadWithRetry(s *Store, job *Job, rawURL, dest string, size int64) error {
	var lastErr error
	backoff := retryBase
	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 {
			_ = s.SetState(job.ID, StateRetrying)
			_ = s.SetProgress(job.ID, fmt.Sprintf(
				"Reintentando descarga en %s (intento %d/%d)...", backoff, attempt, retryMax))
			time.Sleep(backoff)
			backoff *= 2
		}
		lastErr = downloadRange(rawURL, dest, size, progressFn(s, job.ID))
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func downloadRange(rawURL, dest string, size int64, progress func(string)) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("crear cache: %w", err)
	}

	partial := int64(0)
	if st, err := os.Stat(dest); err == nil {
		partial = st.Size()
	}
	if size > 0 && partial >= size {
		partial = 0
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if partial > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", partial))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		if partial > 0 {
			partial = 0
		}
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(partial, io.SeekStart); err != nil {
		return err
	}
	if partial == 0 {
		if err := f.Truncate(0); err != nil {
			return err
		}
	}

	buf := make([]byte, 256*1024)
	total := partial
	lastMB := int64(-1)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			total += int64(n)
			if progress != nil {
				mb := total / (1024 * 1024)
				if mb != lastMB {
					lastMB = mb
					if size > 0 {
						progress(fmt.Sprintf("Descargando bundle: %d / %d MB", mb, size/(1024*1024)))
					} else {
						progress(fmt.Sprintf("Descargando bundle: %d MB", mb))
					}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

func materializeClone(bundlePath, target, originURL, defaultBranch string) error {
	if _, err := os.Stat(filepath.Join(target, ".git")); os.IsNotExist(err) {
		if err := runGit("", "clone", bundlePath, target); err != nil {
			return fmt.Errorf("materializar repo desde bundle: %w", err)
		}
	}
	if defaultBranch != "" {
		cur, _ := gitOutput(target, "branch", "--show-current")
		if strings.TrimSpace(cur) != defaultBranch {
			if err := runGit(target, "checkout", defaultBranch); err != nil {
				return fmt.Errorf("cambiar a la rama por defecto %s: %w", defaultBranch, err)
			}
		}
	}
	_ = runGit(target, "remote", "remove", "origin")
	if err := runGit(target, "remote", "add", "origin", originURL); err != nil {
		return fmt.Errorf("configurar remote origin: %w", err)
	}
	return nil
}

func bundleCachePath(jobID int64) string {
	return filepath.Join(jobDataDir(), "cache", fmt.Sprintf("%d.bundle", jobID))
}

func jobDataDir() string {
	if v := os.Getenv("GITGOST_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gitgost"
	}
	return filepath.Join(home, ".gitgost")
}

func hashMatches(path, want string) (bool, error) {
	got, err := sha256File(path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(got, want), nil
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
