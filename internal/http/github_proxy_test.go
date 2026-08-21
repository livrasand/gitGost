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
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`[{"id":1}]`)),
			Request:    req,
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

func TestGitHubAPIProxyHandlerRotatesRateLimitedToken(t *testing.T) {
	oldTransport := http.DefaultTransport
	var tokens []string
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		tokens = append(tokens, req.Header.Get("Authorization"))
		status := http.StatusTooManyRequests
		body := `{"message":"rate limit exceeded"}`
		if len(tokens) == 2 {
			status = http.StatusOK
			body = `[{"id":1}]`
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()
	t.Setenv("GITHUB_TOKEN", "first-token")
	t.Setenv("GITHUB2_TOKEN", "second-token")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/gh-proxy/*path", GitHubAPIProxyHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/gh-proxy/repos/owner/repo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != `[{"id":1}]` {
		t.Fatalf("response = %d %q", w.Code, w.Body.String())
	}
	if len(tokens) != 2 || tokens[0] == tokens[1] {
		t.Fatalf("tokens used = %v", tokens)
	}
}

func TestGitLabProxyHandler(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "gitlab.com" || req.URL.EscapedPath() != "/api/v4/projects/owner%2Frepo" {
			t.Fatalf("proxy target = %s%s", req.URL.Host, req.URL.EscapedPath())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":1}`)),
			Request:    req,
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()
	t.Setenv("GITLAB_TOKEN", "gl-token")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/gl-proxy/*path", GitLabProxyHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/gl-proxy/api/v4/projects/owner%2Frepo", nil)
	req.URL.RawPath = "/api/gl-proxy/api/v4/projects/owner%2Frepo"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != `{"id":1}` {
		t.Fatalf("response = %d %q", w.Code, w.Body.String())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
