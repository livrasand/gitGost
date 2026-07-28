package http

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEthicalMetricsNormalizesCategories(t *testing.T) {
	if got := normalizeEthicalCategory("Chrome 142", "Chrome", "Firefox"); got != "Other" {
		t.Fatalf("browser = %q", got)
	}
	if got := normalizeEthicalCategory("MacBookPro18,4", "Desktop", "Mobile", "Tablet"); got != "Other" {
		t.Fatalf("device = %q", got)
	}
}

func TestEthicalMetricsDNT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/pageviews", nil)
	ctx.Request.Header.Set("DNT", "1")
	EthicalMetricsPageviewHandler(ctx)
	if ctx.Writer.Status() != 204 {
		t.Fatalf("status = %d", ctx.Writer.Status())
	}
}
