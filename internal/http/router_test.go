package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsValidRepoName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid simple", "owner", true},
		{"valid with dash", "owner-name", true},
		{"valid with underscore", "owner_name", true},
		{"valid with dot", "owner.name", true},
		{"empty string", "", false},
		{"too long", string(make([]byte, 101)), false},
		{"with slash", "owner/repo", false},
		{"path traversal", "owner/../repo", false},
		{"double dot", "owner..repo", false},
		{"uppercase", "Owner", true},
		{"with numbers", "owner123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidRepoName(tt.input)
			if result != tt.expected {
				t.Errorf("isValidRepoName(%q) = %v; want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSizeLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sizeLimitMiddleware())
	r.POST("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	smallBody := make([]byte, 1024)
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Body = &mockReadCloser{data: smallBody}
	req.ContentLength = int64(len(smallBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Small request failed with status %d", w.Code)
	}

	largeBody := make([]byte, maxPushSize+1)
	req, _ = http.NewRequest("POST", "/test", nil)
	req.Body = &mockReadCloser{data: largeBody}
	req.ContentLength = int64(len(largeBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Errorf("Large request should be rejected, got status %d", w.Code)
	}
}

type mockReadCloser struct {
	data []byte
	pos  int
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.pos >= len(m.data) {
		return 0, io.EOF
	}
	n = copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockReadCloser) Close() error {
	return nil
}

func TestAnonymousAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Sin API key: todas las rutas pasan sin credenciales.
	r := gin.New()
	r.Use(anonymousAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Request sin credenciales debe pasar, got status %d", w.Code)
	}

	// Las rutas del protocolo smart HTTP de Git también pasan siempre.
	r = gin.New()
	r.Use(anonymousAuthMiddleware())
	r.GET("/v1/gh/:owner/:repo/info/refs", func(c *gin.Context) {
		c.String(200, "ok")
	})
	r.POST("/v1/gh/:owner/:repo/git-receive-pack", func(c *gin.Context) {
		c.String(200, "ok")
	})

	req, _ = http.NewRequest("GET", "/v1/gh/owner/repo/info/refs?service=git-receive-pack", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Git info/refs should allow anonymous access, got status %d", w.Code)
	}

	req, _ = http.NewRequest("POST", "/v1/gh/owner/repo/git-receive-pack", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Git receive-pack should allow anonymous access, got status %d", w.Code)
	}
}

func TestProviderSlugFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/v1/gh/owner/repo/issues/5/reactions", "gh"},
		{"/v1/gl/owner/repo/issues/5/reactions", "gl"},
		{"/v1/cb/owner/repo/issues/5/comments/12/reactions", "cb"},
		{"/v1/gh/owner/repo/issues/5/comments/12/reactions", "gh"},
		{"/v2/jobs", ""},
		{"/", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := providerSlugFromPath(tt.path); got != tt.expected {
				t.Errorf("providerSlugFromPath(%q) = %q; want %q", tt.path, got, tt.expected)
			}
		})
	}
}
