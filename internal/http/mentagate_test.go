package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

// gateDocument escribe un index.html temporal y devuelve su ruta.
func gateDocument(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(path, []byte("<html>ok</html>"), 0o600); err != nil {
		t.Fatalf("could not write the test document: %v", err)
	}
	return path
}

// withGateEnabled activa el gate con un secreto fijo y devuelve una función
// que restaura el estado global.
func withGateEnabled(t *testing.T, secret string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousEndpoint, previousEnforce := mentaAPIEndpoint, mentaEnforce
	mentaAPIEndpoint = "https://menta.example"
	mentaEnforce = true
	InitCaptchaGate([]byte(secret))
	t.Cleanup(func() {
		mentaAPIEndpoint, mentaEnforce = previousEndpoint, previousEnforce
		captchaGateSecret = atomic.Value{}
	})
}

func gateCookieHeader(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == captchaGateCookieName {
			return cookie
		}
	}
	return nil
}

func TestGateClearsInvalidCookie(t *testing.T) {
	withGateEnabled(t, "test-secret")

	document := gateDocument(t)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		serveDocumentWithGate(c, document)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: captchaGateCookieName, Value: "g2|1792694494|deadbeef"})
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected redirect to the challenge page, got %d", recorder.Code)
	}
	cookie := gateCookieHeader(t, recorder)
	if cookie == nil {
		t.Fatal("expected the gate cookie to be cleared")
	}
	if cookie.MaxAge >= 0 {
		t.Errorf("expected the gate cookie to expire, got MaxAge %d", cookie.MaxAge)
	}
}

func TestGateKeepsValidCookie(t *testing.T) {
	withGateEnabled(t, "test-secret")

	document := gateDocument(t)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		serveDocumentWithGate(c, document)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: captchaGateCookieName, Value: newGateCookieValue()})
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected the page to be served, got %d", recorder.Code)
	}
	if cookie := gateCookieHeader(t, recorder); cookie != nil {
		t.Error("expected no cookie change for a valid session")
	}
}

func TestGateRedirectsWithoutCookie(t *testing.T) {
	withGateEnabled(t, "test-secret")

	document := gateDocument(t)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		serveDocumentWithGate(c, document)
	})

	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected redirect to the challenge page, got %d", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); !strings.HasPrefix(location, "/challenge.html") {
		t.Errorf("expected a redirect to the challenge page, got %q", location)
	}
	if cookie := gateCookieHeader(t, recorder); cookie != nil {
		t.Error("expected no cookie change when there is no session")
	}
}

func TestValidGateCookieRejectsForeignKey(t *testing.T) {
	withGateEnabled(t, "test-secret")
	value := newGateCookieValue()

	InitCaptchaGate([]byte("another-secret"))
	if validGateCookie(value) {
		t.Error("expected a cookie signed with another key to be rejected")
	}
}

func TestGateStatusHandler(t *testing.T) {
	withGateEnabled(t, "test-secret")

	r := gin.New()
	r.GET("/api/captcha-gate/status", CaptchaGateStatusHandler)

	t.Run("without session", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/captcha-gate/status", nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", recorder.Code)
		}
		var body struct {
			Enabled bool `json:"enabled"`
			Cleared bool `json:"cleared"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("could not parse the response: %v", err)
		}
		if !body.Enabled || body.Cleared {
			t.Errorf("expected an enabled gate without clearance, got %+v", body)
		}
	})

	t.Run("with valid session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/captcha-gate/status", nil)
		req.AddCookie(&http.Cookie{Name: captchaGateCookieName, Value: newGateCookieValue()})
		recorder := httptest.NewRecorder()
		r.ServeHTTP(recorder, req)

		var body struct {
			Cleared bool `json:"cleared"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("could not parse the response: %v", err)
		}
		if !body.Cleared {
			t.Error("expected the session to be reported as cleared")
		}
	})

	t.Run("with stale session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/captcha-gate/status", nil)
		req.AddCookie(&http.Cookie{Name: captchaGateCookieName, Value: "g2|1792694494|deadbeef"})
		recorder := httptest.NewRecorder()
		r.ServeHTTP(recorder, req)

		var body struct {
			Cleared bool `json:"cleared"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("could not parse the response: %v", err)
		}
		if body.Cleared {
			t.Error("expected a stale session to be reported as not cleared")
		}
		cookie := gateCookieHeader(t, recorder)
		if cookie == nil || cookie.MaxAge >= 0 {
			t.Error("expected the stale cookie to be cleared")
		}
	})
}
