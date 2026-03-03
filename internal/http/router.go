package http

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/livrasand/gitGost/internal/config"

	"github.com/gin-gonic/gin"
)

// adminLimiterState holds per-IP sliding-window counters for admin endpoints.
var (
	adminLimiterMu    sync.Mutex
	adminLimiterStore = make(map[string][]time.Time)
	adminLimiterMax   = 10
	adminLimiterWin   = time.Minute
)

// adminLimiter enforces a strict per-IP rate limit on admin endpoints.
func adminLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()
		cutoff := now.Add(-adminLimiterWin)
		adminLimiterMu.Lock()
		times := adminLimiterStore[ip]
		valid := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		valid = append(valid, now)
		adminLimiterStore[ip] = valid
		exceeded := len(valid) > adminLimiterMax
		adminLimiterMu.Unlock()
		if exceeded {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "admin rate limit exceeded"})
			return
		}
		c.Next()
	}
}

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
			strings.Contains(c.Request.URL.Path, "git-upload-pack") ||
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
	// Disable proxy header trust so c.ClientIP() uses the real TCP connection IP.
	// If this service runs behind a known reverse proxy, replace with its CIDR(s).
	r.SetTrustedProxies([]string{})

	// Solo recovery, sin logger para proteger anonimato
	r.Use(gin.Recovery())

	// Health y metrics (no auth)
	r.GET("/health", HealthHandler)
	r.GET("/metrics", MetricsHandler)

	// Transparencia y verificación matemática (no auth, solo datos públicos)
	r.GET("/VERIFY", VerifyHandler)
	r.GET("/gitgost-bin", BinaryHandler)

	// Badges
	r.GET("/badges/:badge", BadgeHandler)
	r.GET("/badge/:owner/:repo", BadgePRCountHandler)

	// Static pages
	r.StaticFile("/approach.html", "./web/approach.html")
	r.StaticFile("/guidelines.html", "./web/guidelines.html")
	r.StaticFile("/karma.html", "./web/karma.html")

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
				} else if service == "git-upload-pack" {
					UploadPackDiscoveryHandler(c)
				} else {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unsupported service"})
				}
			})

			// Git Smart HTTP - receive-pack (push)
			gh.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)

			// Git Smart HTTP - upload-pack (fetch/pull)
			gh.POST("/:owner/:repo/git-upload-pack", UploadPackHandler)

			// Issues y comentarios anónimos
			gh.POST("/:owner/:repo/issues/anonymous", CreateAnonymousIssueHandler)
			gh.POST("/:owner/:repo/issues/:number/comments/anonymous", CreateAnonymousCommentHandler)

			// Comentarios anónimos en Pull Requests
			gh.POST("/:owner/:repo/pulls/:number/comments/anonymous", CreateAnonymousPRCommentHandler)
		}
	}

	// Reportes de hash (sin validación de owner/repo en path)
	r.GET("/v1/moderation/report", ReportHashHandler)
	r.POST("/v1/moderation/report", ReportHashHandler)

	// API routes - Public stats
	api := r.Group("/api")
	{
		api.GET("/stats", StatsHandler)
		api.GET("/recent-prs", RecentPRsHandler)
		api.GET("/pr-status/:hash", PRStatusHandler)
	}

	// Admin endpoints — protected by strict per-IP rate limiting
	admin := r.Group("/admin")
	admin.Use(adminLimiter())
	{
		admin.POST("/panic", PanicHandler)
		admin.POST("/rollback", RollbackBurstHandler)
	}

	// Service status (para el frontend)
	r.GET("/api/status", ServiceStatusHandler)

	// SPA fallback
	r.NoRoute(func(c *gin.Context) {
		c.File("./web/index.html")
	})

	return r
}
