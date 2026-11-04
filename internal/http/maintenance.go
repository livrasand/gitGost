package http

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/utils"
)

const maintenancePagePath = "./web/maintenance.html"

const maintenanceFallbackPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>gitGost — Site temporarily offline</title>
</head>
<body style="font-family: sans-serif; max-width: 42rem; margin: 4rem auto; padding: 0 1rem;">
<h1>gitGost</h1>
<p>The site is temporarily offline due to heavy automated traffic.</p>
<p>Repositories remain available via git access:</p>
<pre>git clone https://gitgost.livrasand.com/&lt;owner&gt;/&lt;repo&gt;.git</pre>
</body>
</html>
`

func maintenanceAllowedPath(path string) bool {
	if path == "/health" || path == "/api/status" || path == "/api/announcement" ||
		path == "/announcement.js" {
		return true
	}
	if strings.HasPrefix(path, "/admin/") {
		return true
	}
	if strings.HasSuffix(path, "/info/refs") ||
		strings.HasSuffix(path, "/git-upload-pack") ||
		strings.HasSuffix(path, "/git-receive-pack") {
		return true
	}
	if strings.HasPrefix(path, "/assets/") {
		return true
	}
	return false
}

func maintenanceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isMaintenanceMode() {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if maintenanceAllowedPath(path) {
			c.Next()
			return
		}
		c.Header("Retry-After", "300")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Robots-Tag", "noindex, nofollow")

		apiPrefix := strings.HasPrefix(path, "/api/") ||
			strings.HasPrefix(path, "/v1/") ||
			strings.HasPrefix(path, "/v2/") ||
			strings.HasPrefix(path, "/node/")
		isPageRequest := (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && !apiPrefix

		if !isPageRequest {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":       "maintenance",
				"retry_after": 300,
			})
			return
		}
		serveMaintenancePage(c)
		c.Abort()
	}
}

func serveMaintenancePage(c *gin.Context) {
	body, err := os.ReadFile(maintenancePagePath)
	if err != nil {
		utils.Log("maintenance: cannot read %s: %v (serving fallback)", maintenancePagePath, err)
		body = []byte(maintenanceFallbackPage)
	}
	c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", body)
}
