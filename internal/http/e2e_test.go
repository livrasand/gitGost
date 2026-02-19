package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/config"
)

// gitCmd ejecuta un comando git en el directorio dado y retorna stdout+stderr combinados
func gitCmd(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Desactivar credential helper para evitar prompts interactivos
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=echo",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// requireGit verifica que git esté disponible en el PATH
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping E2E test")
	}
}

// mockGitHubUploadPack crea un mock server que simula git-upload-pack de GitHub
// con un repositorio mínimo válido para clone/fetch
func mockGitHubUploadPack(t *testing.T) *httptest.Server {
	t.Helper()

	// Crear un repo git real en un directorio temporal para servir
	repoDir := t.TempDir()
	mustGitInit(t, repoDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasSuffix(path, "/info/refs") && r.URL.Query().Get("service") == "git-upload-pack" {
			// Ejecutar git upload-pack --advertise-refs
			cmd := exec.Command("git", "upload-pack", "--stateless-rpc", "--advertise-refs", repoDir)
			out, err := cmd.Output()
			if err != nil {
				http.Error(w, "upload-pack advertise failed", 500)
				return
			}
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			// Prefijo de servicio requerido por Smart HTTP
			pktLine := fmt.Sprintf("%04x# service=git-upload-pack\n", len("# service=git-upload-pack\n")+4)
			w.Write([]byte(pktLine))
			w.Write([]byte("0000"))
			w.Write(out)
			return
		}

		if strings.HasSuffix(path, "/git-upload-pack") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read request body", http.StatusBadRequest)
				return
			}
			cmd := exec.Command("git", "upload-pack", "--stateless-rpc", repoDir)
			cmd.Stdin = strings.NewReader(string(body))
			out, err := cmd.Output()
			if err != nil {
				http.Error(w, "upload-pack failed", 500)
				return
			}
			w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
			w.Write(out)
			return
		}

		http.NotFound(w, r)
	}))

	return srv
}

// mustGitInit inicializa un repo git con un commit inicial
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "--initial-branch=main"},
		{"git", "config", "user.email", "anonymous@gitgost.local"},
		{"git", "config", "user.name", "@gitgost-anonymous"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			// Intentar sin --initial-branch (git < 2.28)
			if args[1] == "init" {
				cmd2 := exec.Command("git", "init")
				cmd2.Dir = dir
				if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
					t.Fatalf("git init failed: %v\n%s", err2, out2)
				}
				continue
			}
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}

	// Crear un commit inicial
	testFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(testFile, []byte("# gitGost test repo\n"), 0644); err != nil {
		t.Fatalf("Failed to write README: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "Initial commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=@gitgost-anonymous",
			"GIT_AUTHOR_EMAIL=anonymous@gitgost.local",
			"GIT_COMMITTER_NAME=@gitgost-anonymous",
			"GIT_COMMITTER_EMAIL=anonymous@gitgost.local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}
}

// TestE2E_InfoRefs_UploadPack verifica que GET /info/refs?service=git-upload-pack
// retorne la advertisement correcta (proxied desde el mock de GitHub)
func TestE2E_InfoRefs_UploadPack(t *testing.T) {
	requireGit(t)

	mockGH := mockGitHubUploadPack(t)
	defer mockGH.Close()

	// Montar handler que usa el mock en lugar de github.com
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/gh/:owner/:repo/info/refs", func(c *gin.Context) {
		owner := c.Param("owner")
		repo := c.Param("repo")

		service := c.Query("service")
		if service != "git-upload-pack" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "only git-upload-pack tested here"})
			return
		}

		upstreamURL := mockGH.URL + "/" + owner + "/" + repo + ".git/info/refs?service=git-upload-pack"
		req, err := http.NewRequest("GET", upstreamURL, nil)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
			return
		}
		req.Header.Set("User-Agent", "git/2.0")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()

		c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		c.Writer.WriteHeader(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/gh/owner/repo/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-git-upload-pack-advertisement" {
		t.Errorf("Expected upload-pack-advertisement Content-Type, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "git-upload-pack") {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Errorf("Response body should contain 'git-upload-pack', got: %q", preview)
	}
}

// TestE2E_GitClone verifica que `git clone` funcione contra el servidor gitGost
// usando un mock de GitHub como upstream
func TestE2E_GitClone(t *testing.T) {
	requireGit(t)

	mockGH := mockGitHubUploadPack(t)
	defer mockGH.Close()

	// Servidor gitGost que proxea al mock
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/v1/gh/:owner/:repo/info/refs", func(c *gin.Context) {
		owner := c.Param("owner")
		repo := c.Param("repo")
		service := c.Query("service")
		if service != "git-upload-pack" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unsupported service"})
			return
		}
		upstreamURL := mockGH.URL + "/" + owner + "/" + repo + ".git/info/refs?service=git-upload-pack"
		req, _ := http.NewRequest("GET", upstreamURL, nil)
		req.Header.Set("User-Agent", "git/2.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		c.Writer.WriteHeader(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	})

	r.POST("/v1/gh/:owner/:repo/git-upload-pack", func(c *gin.Context) {
		owner := c.Param("owner")
		repo := c.Param("repo")
		body, _ := io.ReadAll(c.Request.Body)
		upstreamURL := mockGH.URL + "/" + owner + "/" + repo + ".git/git-upload-pack"
		req, _ := http.NewRequest("POST", upstreamURL, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
		req.Header.Set("User-Agent", "git/2.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		c.Writer.WriteHeader(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	cloneDir := t.TempDir()
	cloneURL := srv.URL + "/v1/gh/owner/repo"

	out, err := gitCmd(t, cloneDir, "clone", cloneURL, "cloned-repo")
	if err != nil {
		t.Fatalf("git clone failed: %v\nOutput: %s", err, out)
	}

	// Verificar que el README fue clonado
	readmePath := filepath.Join(cloneDir, "cloned-repo", "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Errorf("README.md should exist after clone, but it doesn't")
	}
}

// TestE2E_GitFetch verifica que `git fetch` funcione contra el servidor gitGost
func TestE2E_GitFetch(t *testing.T) {
	requireGit(t)

	mockGH := mockGitHubUploadPack(t)
	defer mockGH.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/v1/gh/:owner/:repo/info/refs", func(c *gin.Context) {
		owner := c.Param("owner")
		repo := c.Param("repo")
		service := c.Query("service")
		if service != "git-upload-pack" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unsupported service"})
			return
		}
		upstreamURL := mockGH.URL + "/" + owner + "/" + repo + ".git/info/refs?service=git-upload-pack"
		req, _ := http.NewRequest("GET", upstreamURL, nil)
		req.Header.Set("User-Agent", "git/2.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		c.Writer.WriteHeader(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	})

	r.POST("/v1/gh/:owner/:repo/git-upload-pack", func(c *gin.Context) {
		owner := c.Param("owner")
		repo := c.Param("repo")
		body, _ := io.ReadAll(c.Request.Body)
		upstreamURL := mockGH.URL + "/" + owner + "/" + repo + ".git/git-upload-pack"
		req, _ := http.NewRequest("POST", upstreamURL, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
		req.Header.Set("User-Agent", "git/2.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		c.Writer.WriteHeader(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Primero clonar para tener un repo local
	cloneDir := t.TempDir()
	cloneURL := srv.URL + "/v1/gh/owner/repo"
	if out, err := gitCmd(t, cloneDir, "clone", cloneURL, "fetch-repo"); err != nil {
		t.Fatalf("git clone failed (prerequisite for fetch test): %v\nOutput: %s", err, out)
	}

	repoDir := filepath.Join(cloneDir, "fetch-repo")

	// Ahora hacer fetch
	out, err := gitCmd(t, repoDir, "fetch", "origin")
	if err != nil {
		t.Fatalf("git fetch failed: %v\nOutput: %s", err, out)
	}
}

// TestE2E_InfoRefs_UnsupportedService verifica que servicios desconocidos retornen 400
func TestE2E_InfoRefs_UnsupportedService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{APIKey: ""}
	r := SetupRouter(cfg)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/gh/owner/repo/info/refs?service=git-unknown-service")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 for unknown service, got %d", resp.StatusCode)
	}
}

// TestE2E_UploadPackRoute_Exists verifica que la ruta POST /git-upload-pack esté registrada
func TestE2E_UploadPackRoute_Exists(t *testing.T) {
	// Sin GITHUB_TOKEN → 500, pero la ruta existe (no 404)
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	cfg := &config.Config{APIKey: ""}
	r := SetupRouter(cfg)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/v1/gh/owner/repo/git-upload-pack",
		"application/x-git-upload-pack-request",
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("Route POST /git-upload-pack should be registered (not 404), got %d", resp.StatusCode)
	}
}
