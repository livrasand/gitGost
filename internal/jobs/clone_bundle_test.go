package jobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeBundleServer simula el servidor gitGost de Fase 2: acepta la creación
// del job remoto, reporta el bundle listo y sirve el bundle con soporte de
// Range (registrando los headers Range recibidos).
func fakeBundleServer(t *testing.T, bundlePath string) (*httptest.Server, *[]string) {
	t.Helper()
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("leer bundle: %v", err)
	}
	hash, err := sha256File(bundlePath)
	if err != nil {
		t.Fatalf("sha256 del bundle: %v", err)
	}

	var mu sync.Mutex
	var ranges []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/jobs":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "testjob"})
		case r.Method == http.MethodGet && r.URL.Path == "/v2/jobs/testjob":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ready",
				"result": map[string]any{
					"downloadUrl": srv.URL + "/bundle",
					"size":        int64(len(data)),
					"sha256":      hash,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/bundle":
			mu.Lock()
			rng := r.Header.Get("Range")
			if rng != "" {
				ranges = append(ranges, rng)
			}
			mu.Unlock()
			if strings.HasPrefix(rng, "bytes=") {
				n, _ := strconv.ParseInt(strings.TrimPrefix(rng, "bytes="), 10, 64)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", n, int64(len(data))-1, int64(len(data))))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(data[n:])
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &ranges
}

// withServerURL fija GITGOST_SERVER para que serverBase() apunte al fake.
func withServerURL(t *testing.T, u string) {
	t.Helper()
	old := os.Getenv("GITGOST_SERVER")
	_ = os.Setenv("GITGOST_SERVER", u)
	t.Cleanup(func() {
		if old != "" {
			_ = os.Setenv("GITGOST_SERVER", old)
		} else {
			_ = os.Unsetenv("GITGOST_SERVER")
		}
	})
}

// withHome fija GITGOST_HOME (directorio de datos del cliente) a un tempdir.
func withHome(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("GITGOST_HOME")
	_ = os.Setenv("GITGOST_HOME", dir)
	t.Cleanup(func() {
		if old != "" {
			_ = os.Setenv("GITGOST_HOME", old)
		} else {
			_ = os.Unsetenv("GITGOST_HOME")
		}
	})
}

// makeBundle crea un bundle git real desde un repo de prueba.
func makeBundle(t *testing.T) string {
	t.Helper()
	src := buildRepo(t, 3)
	bundle := filepath.Join(t.TempDir(), "repo.bundle")
	if err := runGit(src, "bundle", "create", bundle, "--all"); err != nil {
		t.Fatalf("crear bundle: %v", err)
	}
	return bundle
}

// TestRunCloneBundleFull valida el flujo completo de Fase 2: job remoto,
// descarga del bundle, materialización con git clone y origin original.
func TestRunCloneBundleFull(t *testing.T) {
	bundle := makeBundle(t)
	srv, _ := fakeBundleServer(t, bundle)
	withServerURL(t, srv.URL)
	withHome(t, t.TempDir())

	s := testStore(t)
	dest := filepath.Join(t.TempDir(), "dest")
	id, err := s.Create(&Job{
		Operation: "clone",
		URL:       srv.URL + "/v1/gh/acme/widgets",
		Origin:    "git@github.com:acme/widgets.git",
		Target:    dest,
		CWD:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Run(s, id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != StateCompleted {
		t.Errorf("estado = %s, se esperaba completed", job.State)
	}
	if repoShallow(dest) {
		t.Error("el repo materializado no debería ser shallow")
	}
	if n := gitRevCount(dest); n != 3 {
		t.Errorf("commits = %d, se esperaba 3", n)
	}
	out, _ := gitOutput(dest, "branch", "--show-current")
	if strings.TrimSpace(out) != "main" {
		t.Errorf("rama actual = %q, se esperaba main", out)
	}
	origin, _ := gitOutput(dest, "remote", "get-url", "origin")
	if strings.TrimSpace(origin) != "https://github.com/acme/widgets.git" {
		t.Errorf("origin = %q, se esperaba la URL original https", origin)
	}
}

// TestDownloadBundleResumes valida el resume por bytes: con la primera mitad
// del bundle en cache, la descarga pide Range desde ese punto y el archivo
// final coincide con el hash esperado.
func TestDownloadBundleResumes(t *testing.T) {
	bundle := makeBundle(t)
	data, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatalf("leer bundle: %v", err)
	}
	hash, err := sha256File(bundle)
	if err != nil {
		t.Fatalf("sha256 del bundle: %v", err)
	}
	srv, ranges := fakeBundleServer(t, bundle)
	withServerURL(t, srv.URL)
	withHome(t, t.TempDir())

	s := testStore(t)
	id, err := s.Create(&Job{Operation: "clone", URL: srv.URL + "/v1/gh/a/b", Target: filepath.Join(t.TempDir(), "d"), CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Checkpoint: primera mitad del bundle ya descargada en cache.
	dest := bundleCachePath(job.ID)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("crear cache: %v", err)
	}
	half := int64(len(data) / 2)
	if err := os.WriteFile(dest, data[:int(half)], 0o644); err != nil {
		t.Fatalf("escribir checkpoint: %v", err)
	}

	if err := downloadBundle(s, job, srv.URL+"/bundle", dest, int64(len(data)), hash); err != nil {
		t.Fatalf("downloadBundle: %v", err)
	}
	got, err := sha256File(dest)
	if err != nil {
		t.Fatalf("sha256 final: %v", err)
	}
	if got != hash {
		t.Error("el archivo final no coincide con el bundle original")
	}
	if len(*ranges) == 0 {
		t.Fatal("no se envió ninguna petición Range")
	}
	want := fmt.Sprintf("bytes=%d-", half)
	if (*ranges)[0] != want {
		t.Errorf("Range = %q, se esperaba %q", (*ranges)[0], want)
	}
}

// TestDownloadBundleHashMismatchRetries valida que un bundle corrupto (mismo
// tamaño, hash distinto) se descarta y se descarga de nuevo una vez.
func TestDownloadBundleHashMismatchRetries(t *testing.T) {
	bundle := makeBundle(t)
	data, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatalf("leer bundle: %v", err)
	}
	hash, err := sha256File(bundle)
	if err != nil {
		t.Fatalf("sha256 del bundle: %v", err)
	}

	var mu sync.Mutex
	calls := 0
	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	corrupted[len(corrupted)-1] ^= 0xFF

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bundle" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			_, _ = w.Write(corrupted)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	withServerURL(t, srv.URL)
	withHome(t, t.TempDir())

	s := testStore(t)
	id, err := s.Create(&Job{Operation: "clone", URL: srv.URL + "/v1/gh/a/b", Target: filepath.Join(t.TempDir(), "d"), CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	dest := bundleCachePath(job.ID)
	if err := downloadBundle(s, job, srv.URL+"/bundle", dest, int64(len(data)), hash); err != nil {
		t.Fatalf("downloadBundle: %v", err)
	}
	got, err := sha256File(dest)
	if err != nil {
		t.Fatalf("sha256 final: %v", err)
	}
	if got != hash {
		t.Error("el archivo final no coincide con el bundle original")
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 2 {
		t.Errorf("descargas = %d, se esperaban 2 (corrupta + reintento)", n)
	}
}
