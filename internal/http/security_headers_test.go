package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersDoNotLeakServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Simulate a Server header set earlier in the chain; securityHeaders must remove it.
	r.Use(func(c *gin.Context) {
		c.Header("Server", "Fly/abc")
		c.Next()
	})
	r.Use(securityHeaders())
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := w.Header().Get("Server"); got != "" {
		t.Errorf("Server header = %q, want none", got)
	}
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS header missing")
	}
}
