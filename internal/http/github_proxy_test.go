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

func TestGitHubAPIProxyHandlerServesCacheWhenRateLimited(t *testing.T) {
	oldTransport := http.DefaultTransport
	requests := 0
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			header := http.Header{}
			header.Set("Content-Type", "application/json")
			header.Set("ETag", `"abc"`)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(`{"id":1}`)),
				Request:    req,
			}, nil
		}
		if revalid := req.Header.Get("If-None-Match"); revalid != `"abc"` {
			t.Fatalf("If-None-Match = %q", revalid)
		}
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"rate limit exceeded"}`)),
			Request:    req,
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()
	t.Setenv("GITHUB_TOKEN", "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/gh-proxy/*path", GitHubAPIProxyHandler)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/gh-proxy/repos/owner/repo/languages", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Body.String() != `{"id":1}` {
			t.Fatalf("request %d: response = %d %q", i+1, w.Code, w.Body.String())
		}
	}
	if requests != 2 {
		t.Fatalf("upstream requests = %d, want 2 (initial + revalidation)", requests)
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
