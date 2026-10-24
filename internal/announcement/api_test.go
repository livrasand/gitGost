package announcement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupGin(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/announcement", RateLimit(), Handler)
	return r
}

func TestHandlerEmpty(t *testing.T) {
	reset()
	r := setupGin(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/announcement", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", w.Code)
	}
}

func TestHandlerActive(t *testing.T) {
	reset()
	defer reset()
	mustProcess(t, `{"action":"post","type":"caution","title":"Title here","message":"Msg here","link":"https://github.com/livrasand/gitGost/issues/219"}`, time.Now().UTC())
	r := setupGin(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/announcement", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("bad Cache-Control: %q", cc)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["type"] != "caution" || !strings.Contains(w.Body.String(), "Title here") {
		t.Fatalf("unexpected payload: %s", w.Body.String())
	}
}

func TestRateLimitPerIP(t *testing.T) {
	reset()
	defer reset()
	apiLimiterMu.Lock()
	apiLimiterStore = make(map[string]*limiterEntry, apiLimiterSize)
	apiLimiterMu.Unlock()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/l", rateLimitWith(3, time.Minute), func(c *gin.Context) { c.Status(200) })
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/l", nil))
		if w.Code != 200 {
			t.Fatalf("req %d: got %d", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/l", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("req 4: got %d, want 429", w.Code)
	}
	// Otra IP no se ve afectada.
	w2 := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/l", nil)
	req.RemoteAddr = "203.0.113.99:1234"
	r.ServeHTTP(w2, req)
	if w2.Code != 200 {
		t.Fatalf("other IP got %d, want 200", w2.Code)
	}
}
