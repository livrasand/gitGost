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

// authMiddleware checks for valid API key
func authMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			// If no API key is set, skip authentication
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
	r := gin.New()
	r.Use(gin.Recovery())

	// Health and metrics endpoints (no auth required)
	r.GET("/health", HealthHandler)
	r.GET("/metrics", MetricsHandler)

	// Static assets (no auth required)
	r.Static("/assets", "./web/assets")

	// API routes with authentication
	v1 := r.Group("/v1")
	v1.Use(sizeLimitMiddleware())
	v1.Use(authMiddleware(cfg.APIKey))
	{
		gh := v1.Group("/gh")
		{
			gh.GET("/:owner/:repo/info/refs", func(c *gin.Context) {
				if c.Query("service") == "git-receive-pack" {
					ReceivePackDiscoveryHandler(c)
				} else {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid service"})
				}
			})
			gh.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
		}
	}

	// SPA fallback: serve index.html for unmatched routes (no auth required)
	r.NoRoute(func(c *gin.Context) {
		c.File("./web/index.html")
	})

	return r
}
