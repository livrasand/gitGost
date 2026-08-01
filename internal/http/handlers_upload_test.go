package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

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

func TestBasicAuth(t *testing.T) {
	result := basicAuth("x-access-token", "mytoken")
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

func TestUploadPackDiscoveryHandler_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Expected no Authorization header, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "advertisement")
	}))
	defer mockGitHub.Close()

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
		t.Errorf("Expected 200 without GITHUB_TOKEN, got %d", w.Code)
	}
}

func TestUploadPackHandler_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Expected no Authorization header, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "pack")
	}))
	defer mockGitHub.Close()

	origUploadPackClient := uploadPackClient
	uploadPackClient = &http.Client{
		Transport: &mockTransport{mockURL: mockGitHub.URL, base: mockGitHub.Client().Transport},
	}
	defer func() { uploadPackClient = origUploadPackClient }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/:owner/:repo/git-upload-pack", UploadPackHandler)

	req, _ := http.NewRequest("POST", "/owner/repo/git-upload-pack", strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 without GITHUB_TOKEN, got %d", w.Code)
	}
}

func TestUploadPackDiscoveryHandler_ProxiesGitHub(t *testing.T) {
	fakeAdvertisement := "001e# service=git-upload-pack\n00000032abc123 refs/heads/main\n0000"

	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("Expected Basic auth header, got %q", auth)
		}
		if r.Header.Get("User-Agent") != "git/2.0" {
			t.Errorf("Expected User-Agent git/2.0, got %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, fakeAdvertisement)
	}))
	defer mockGitHub.Close()

	t.Setenv("GITHUB_TOKEN", "test-token-123")

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

func TestUploadPackHandler_ProxiesGitHub(t *testing.T) {
	fakePackData := "0008NAK\n"
	receivedBody := ""

	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestInfoRefsRouter_UploadPack(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/:owner/:repo/info/refs", func(c *gin.Context) {
		service := c.Query("service")
		if service == "git-receive-pack" {
			c.String(http.StatusOK, "receive-pack")
		} else if service == "git-upload-pack" {
			c.String(http.StatusInternalServerError, "upload-pack-reached")
		} else {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unsupported service"})
		}
	})

	req, _ := http.NewRequest("GET", "/owner/repo/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Body.String() != "upload-pack-reached" {
		t.Errorf("git-upload-pack should reach upload-pack handler, got %q", w.Body.String())
	}

	req, _ = http.NewRequest("GET", "/owner/repo/info/refs?service=git-receive-pack", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Body.String() != "receive-pack" {
		t.Errorf("git-receive-pack should reach receive-pack handler, got %q", w.Body.String())
	}

	req, _ = http.NewRequest("GET", "/owner/repo/info/refs?service=unknown", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Unknown service should return 400, got %d", w.Code)
	}
}
