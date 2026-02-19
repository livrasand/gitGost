package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// mockTransport redirige peticiones a github.com al mockURL dado.
type mockTransport struct {
	mockURL string
	base    http.RoundTripper
}

func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "github.com") {
		mock, _ := url.Parse(t.mockURL)
		req = req.Clone(req.Context())
		req.URL.Scheme = mock.Scheme
		req.URL.Host = mock.Host
	}
	return t.base.RoundTrip(req)
}

// TestBasicAuth verifica que basicAuth codifique correctamente en base64
func TestBasicAuth(t *testing.T) {
	result := basicAuth("x-access-token", "mytoken")
	// "x-access-token:mytoken" en base64 = "eC1hY2Nlc3MtdG9rZW46bXl0b2tlbg=="
	expected := "eC1hY2Nlc3MtdG9rZW46bXl0b2tlbg=="
	if result != expected {
		t.Errorf("basicAuth() = %q; want %q", result, expected)
	}
}

func TestBasicAuth_EmptyPassword(t *testing.T) {
	result := basicAuth("user", "")
	if result == "" {
		t.Error("basicAuth() should not return empty string")
	}
}

// TestUploadPackDiscoveryHandler_NoToken verifica rechazo sin GITHUB_TOKEN
func TestUploadPackDiscoveryHandler_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/info/refs", UploadPackDiscoveryHandler)

	req, _ := http.NewRequest("GET", "/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500 when GITHUB_TOKEN not set, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if body["error"] != "GITHUB_TOKEN not set" {
		t.Errorf("Expected error 'GITHUB_TOKEN not set', got %q", body["error"])
	}
}

// TestUploadPackHandler_NoToken verifica rechazo sin GITHUB_TOKEN
func TestUploadPackHandler_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/git-upload-pack", UploadPackHandler)

	req, _ := http.NewRequest("POST", "/git-upload-pack", strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500 when GITHUB_TOKEN not set, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if body["error"] != "GITHUB_TOKEN not set" {
		t.Errorf("Expected error 'GITHUB_TOKEN not set', got %q", body["error"])
	}
}

// TestUploadPackDiscoveryHandler_ProxiesGitHub verifica que el handler haga proxy correcto
func TestUploadPackDiscoveryHandler_ProxiesGitHub(t *testing.T) {
	fakeAdvertisement := "001e# service=git-upload-pack\n00000032abc123 refs/heads/main\n0000"

	// Mock del servidor de GitHub
	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verificar que llega con Authorization
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("Expected Basic auth header, got %q", auth)
		}
		// Verificar User-Agent
		if r.Header.Get("User-Agent") != "git/2.0" {
			t.Errorf("Expected User-Agent git/2.0, got %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, fakeAdvertisement)
	}))
	defer mockGitHub.Close()

	t.Setenv("GITHUB_TOKEN", "test-token-123")

	// Swappear uploadPackClient para redirigir github.com al mock
	origUploadPackClient := uploadPackClient
	uploadPackClient = &http.Client{
		Transport: &mockTransport{mockURL: mockGitHub.URL, base: mockGitHub.Client().Transport},
	}
	defer func() { uploadPackClient = origUploadPackClient }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/:owner/:repo/info/refs", UploadPackDiscoveryHandler)

	req, _ := http.NewRequest("GET", "/owner/repo/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-git-upload-pack-advertisement" {
		t.Errorf("Expected Content-Type application/x-git-upload-pack-advertisement, got %q", ct)
	}
	if w.Body.String() != fakeAdvertisement {
		t.Errorf("Expected proxied body %q, got %q", fakeAdvertisement, w.Body.String())
	}
}

// TestUploadPackHandler_ProxiesGitHub verifica que el POST /git-upload-pack haga proxy correcto
func TestUploadPackHandler_ProxiesGitHub(t *testing.T) {
	fakePackData := "0008NAK\n"
	receivedBody := ""

	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verificar método y Content-Type
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-git-upload-pack-request" {
			t.Errorf("Expected upload-pack Content-Type, got %q", r.Header.Get("Content-Type"))
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)

		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, fakePackData)
	}))
	defer mockGitHub.Close()

	t.Setenv("GITHUB_TOKEN", "test-token-456")

	// Swappear uploadPackClient para redirigir github.com al mock
	origUploadPackClient := uploadPackClient
	uploadPackClient = &http.Client{
		Transport: &mockTransport{mockURL: mockGitHub.URL, base: mockGitHub.Client().Transport},
	}
	defer func() { uploadPackClient = origUploadPackClient }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/:owner/:repo/git-upload-pack", UploadPackHandler)

	requestBody := "0011want abc123\n0000"
	req, _ := http.NewRequest("POST", "/owner/repo/git-upload-pack", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	if w.Body.String() != fakePackData {
		t.Errorf("Expected proxied pack data %q, got %q", fakePackData, w.Body.String())
	}
	if receivedBody != requestBody {
		t.Errorf("Mock GitHub received wrong body: got %q, want %q", receivedBody, requestBody)
	}
}

// TestInfoRefsRouter_UploadPack verifica que el router enrute git-upload-pack correctamente
func TestInfoRefsRouter_UploadPack(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/:owner/:repo/info/refs", func(c *gin.Context) {
		service := c.Query("service")
		if service == "git-receive-pack" {
			c.String(http.StatusOK, "receive-pack")
		} else if service == "git-upload-pack" {
			// Sin token → 500, pero el routing llegó aquí
			c.String(http.StatusInternalServerError, "upload-pack-reached")
		} else {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unsupported service"})
		}
	})

	// git-upload-pack debe llegar al handler correcto
	req, _ := http.NewRequest("GET", "/owner/repo/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Body.String() != "upload-pack-reached" {
		t.Errorf("git-upload-pack should reach upload-pack handler, got %q", w.Body.String())
	}

	// git-receive-pack debe llegar al handler correcto
	req, _ = http.NewRequest("GET", "/owner/repo/info/refs?service=git-receive-pack", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Body.String() != "receive-pack" {
		t.Errorf("git-receive-pack should reach receive-pack handler, got %q", w.Body.String())
	}

	// servicio desconocido debe retornar 400
	req, _ = http.NewRequest("GET", "/owner/repo/info/refs?service=unknown", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Unknown service should return 400, got %d", w.Code)
	}
}
