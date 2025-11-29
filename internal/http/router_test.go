package http

import (
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

	// Small request should succeed
	smallBody := make([]byte, 1024) // 1KB
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Body = &mockReadCloser{data: smallBody}
	req.ContentLength = int64(len(smallBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Small request failed with status %d", w.Code)
	}

	// Large request should be rejected
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

// mockReadCloser implements io.ReadCloser for testing
type mockReadCloser struct {
	data []byte
	pos  int
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.pos >= len(m.data) {
		return 0, nil
	}
	n = copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockReadCloser) Close() error {
	return nil
}

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Test without API key set (should allow)
	r := gin.New()
	r.Use(authMiddleware(""))
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Request without API key should succeed when none is set, got status %d", w.Code)
	}

	// Test with API key set
	r = gin.New()
	r.Use(authMiddleware("test-key"))
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	// No API key provided
	req, _ = http.NewRequest("GET", "/test", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("Request without API key should fail when key is required, got status %d", w.Code)
	}

	// Wrong API key
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Gitgost-Key", "wrong-key")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("Request with wrong API key should fail, got status %d", w.Code)
	}

	// Correct API key
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Gitgost-Key", "test-key")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Request with correct API key should succeed, got status %d", w.Code)
	}
}
