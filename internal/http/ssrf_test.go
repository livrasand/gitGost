package http

import (
	"net/http"
	"testing"
	"time"
)

func TestSafeRedirectsAllowsSameHostHTTPS(t *testing.T) {
	policy := safeSameHostRedirects(3)
	first, _ := http.NewRequest(http.MethodGet, "https://codeberg.org/api/v1/x", nil)
	next, _ := http.NewRequest(http.MethodGet, "https://codeberg.org/api/v1/y", nil)
	if err := policy(next, []*http.Request{first}); err != nil {
		t.Fatalf("same-host https redirect should be allowed: %v", err)
	}
}

func TestSafeRedirectsBlocksCrossHost(t *testing.T) {
	policy := safeSameHostRedirects(3)
	first, _ := http.NewRequest(http.MethodGet, "https://codeberg.org/", nil)
	internal, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err := policy(internal, []*http.Request{first}); err == nil {
		t.Fatal("redirect to internal IP must be blocked")
	}
	evil, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/", nil)
	if err := policy(evil, []*http.Request{first}); err == nil {
		t.Fatal("redirect to different host must be blocked")
	}
}

func TestSafeRedirectsBlocksHTTPDowngrade(t *testing.T) {
	policy := safeSameHostRedirects(3)
	first, _ := http.NewRequest(http.MethodGet, "https://gitlab.com/", nil)
	downgraded, _ := http.NewRequest(http.MethodGet, "http://gitlab.com/", nil)
	if err := policy(downgraded, []*http.Request{first}); err == nil {
		t.Fatal("redirect downgrade to http must be blocked")
	}
}

func TestSafeRedirectsMaxHops(t *testing.T) {
	policy := safeSameHostRedirects(3)
	first, _ := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	via := []*http.Request{first, first, first}
	next, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	if err := policy(next, via); err == nil {
		t.Fatal("must block after max redirects")
	}
}

func TestNewSafeHTTPClientHasPolicy(t *testing.T) {
	client := newSafeHTTPClient(time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("safe client must set CheckRedirect policy")
	}
}
