package http

import (
	"gitGost/internal/config"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// maxPushSize is the maximum allowed push size
const maxPushSize = 100 * 1024 * 1024 // 100MB

// sizeLimitMiddleware checks the request size
func sizeLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxPushSize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Push too large"})
			return
		}
		c.Next()
	}
}

// validationMiddleware validates owner and repo parameters
func validationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := c.Param("owner")
		repo := c.Param("repo")

		if !isValidRepoName(owner) || !isValidRepoName(repo) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid repo name"})
			return
		}

		c.Next()
	}
}

// isValidRepoName checks if a repository name is valid
func isValidRepoName(name string) bool {
	if len(name) == 0 || len(name) > 100 {
		return false
	}
	// Allow alphanumeric, -, _, .
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	// No path traversal
	if strings.Contains(name, "..") || strings.Contains(name, "/") {
		return false
	}
	return true
}

// anonymousAuthMiddleware permite acceso sin autenticación para git-receive-pack
// pero requiere API key para otros endpoints si está configurada
func anonymousAuthMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Permitir siempre git-receive-pack sin autenticación (anonimato)
		if strings.Contains(c.Request.URL.Path, "git-receive-pack") ||
			strings.Contains(c.Request.URL.Path, "info/refs") {
			c.Next()
			return
		}

		// Para otros endpoints, verificar API key si está configurada
		if apiKey == "" {
			c.Next()
			return
		}

		providedKey := c.GetHeader("X-Gitgost-Key")
		if providedKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key required"})
			return
		}

		if providedKey != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}

		c.Next()
	}
}

func SetupRouter(cfg *config.Config) *gin.Engine {
	// Deshabilitar logs de Gin para proteger privacidad
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Solo recovery, sin logger para proteger anonimato
	r.Use(gin.Recovery())

	// Health y metrics (no auth)
	r.GET("/health", HealthHandler)
	r.GET("/metrics", MetricsHandler)

	// Static assets
	r.Static("/assets", "./web/assets")

	// API routes - ANONIMAS para git operations
	v1 := r.Group("/v1")
	v1.Use(sizeLimitMiddleware())
	v1.Use(validationMiddleware())
	v1.Use(anonymousAuthMiddleware(cfg.APIKey))
	{
		gh := v1.Group("/gh")
		{
			// Git Smart HTTP - info/refs (discovery)
			gh.GET("/:owner/:repo/info/refs", func(c *gin.Context) {
				service := c.Query("service")
				if service == "git-receive-pack" {
					ReceivePackDiscoveryHandler(c)
				} else {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Only git-receive-pack is supported"})
				}
			})

			// Git Smart HTTP - receive-pack (push)
			gh.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
		}
	}

	// SPA fallback
	r.NoRoute(func(c *gin.Context) {
		c.File("./web/index.html")
	})

	return r
}
