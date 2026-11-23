package http

import (
	"testing"
)

func TestGateCookieRoundTrip(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	value := newGateCookieValue()
	if !validGateCookie(value) {
		t.Error("expected a freshly issued gate cookie to be valid")
	}
	if validGateCookie("g2|9999999999|badmac") {
		t.Error("expected an invalid gate cookie to be rejected")
	}
}

func TestGateCookieRejectsForeignKey(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	value := newGateCookieValue()
	InitCaptchaGate([]byte("another-secret"))
	if validGateCookie(value) {
		t.Error("expected a cookie signed with another key to be rejected")
	}
}
