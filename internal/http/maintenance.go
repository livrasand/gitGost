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

// maintenanceAllowedPath decide qué rutas siguen disponibles en modo
// mantenimiento: salud, estado, anuncios (el banner debe seguir visible
// durante la propia página de mantenimiento; se sirve 503 solo en HTML),
// admin (para restaurar), git smart HTTP (clone/fetch y el mensaje de
// suspensión de push) y los assets de la propia página de mantenimiento.
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

// maintenanceMiddleware intercepta todo el tráfico cuando el modo
// mantenimiento está activo y responde 503: JSON para la API y HTML
// (web/maintenance.html) para navegadores. El registro debe hacerse
// antes de declarar las rutas (gin encadena la cadena global en ese
// momento) y también aplica a rutas no registradas (NoRoute).
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

// serveMaintenancePage escribe la página de mantenimiento con status 503.
// El archivo se lee por petición (consistente con el index de NoRoute).
func serveMaintenancePage(c *gin.Context) {
	body, err := os.ReadFile(maintenancePagePath)
	if err != nil {
		utils.Log("maintenance: cannot read %s: %v (serving fallback)", maintenancePagePath, err)
		body = []byte(maintenanceFallbackPage)
	}
	c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", body)
}
