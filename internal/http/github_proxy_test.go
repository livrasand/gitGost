package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGitHubAPIProxyHandler(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.github.com" {
			t.Fatalf("proxy target host = %q, want api.github.com", req.URL.Host)
		}
		if req.URL.Path != "/repos/owner/repo/issues/1/comments" || req.URL.RawQuery != "per_page=100" {
			t.Fatalf("proxy target = %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		if req.Header.Get("Authorization") != "token client-token" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`[{"id":1}]`)),
			Request: req,
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/gh-proxy/*path", GitHubAPIProxyHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/gh-proxy/repos/owner/repo/issues/1/comments?per_page=100", nil)
	req.Header.Set("Authorization", "token client-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != `[{"id":1}]` {
		t.Fatalf("response = %d %q", w.Code, w.Body.String())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}