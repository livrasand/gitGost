package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func ctxWith(headers map[string]string, remote string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c
}

func TestFromPrefersFlyClientIP(t *testing.T) {
	c := ctxWith(map[string]string{
		"Fly-Client-IP":   "203.0.113.7",
		"X-Forwarded-For": "198.51.100.1",
	}, "10.0.0.1:1234")
	if got := From(c); got != "203.0.113.7" {
		t.Errorf("From = %q, want 203.0.113.7", got)
	}
}

func TestFromFallsBackToForwardedFor(t *testing.T) {
	c := ctxWith(map[string]string{"X-Forwarded-For": "198.51.100.1, 10.0.0.2"}, "10.0.0.1:1234")
	if got := From(c); got != "198.51.100.1" {
		t.Errorf("From = %q, want 198.51.100.1", got)
	}
}

func TestFromFallsBackToRemoteAddr(t *testing.T) {
	c := ctxWith(nil, "192.0.2.9:4000")
	if got := From(c); got != "192.0.2.9" {
		t.Errorf("From = %q, want 192.0.2.9", got)
	}
}

func TestFromIgnoresInvalidHeaders(t *testing.T) {
	c := ctxWith(map[string]string{"Fly-Client-IP": "not-an-ip"}, "192.0.2.9:4000")
	if got := From(c); got != "192.0.2.9" {
		t.Errorf("From = %q, want 192.0.2.9", got)
	}
}

func TestRateLimitKeyIPv4(t *testing.T) {
	if got := RateLimitKey("203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("got %q", got)
	}
	if got := RateLimitKey("::ffff:203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("mapped IPv4: got %q", got)
	}
}

func TestRateLimitKeyIPv6SameSubnet(t *testing.T) {
	a := RateLimitKey("2001:db8:1:2:aaaa:bbbb:cccc:dddd")
	b := RateLimitKey("2001:db8:1:2:1111:2222:3333:4444")
	if a != b {
		t.Errorf("addresses in the same /64 got different keys: %q vs %q", a, b)
	}
	if a != "2001:db8:1:2::/64" {
		t.Errorf("key = %q, want 2001:db8:1:2::/64", a)
	}
}

func TestRateLimitKeyIPv6DifferentSubnet(t *testing.T) {
	a := RateLimitKey("2001:db8:1:2::1")
	b := RateLimitKey("2001:db8:1:3::1")
	if a == b {
		t.Errorf("different /64 subnets share key %q", a)
	}
}

func TestRateLimitKeyUnparseable(t *testing.T) {
	if got := RateLimitKey("garbage"); got != "garbage" {
		t.Errorf("got %q", got)
	}
}
