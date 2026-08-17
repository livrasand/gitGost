package http

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/config"

	"github.com/gin-gonic/gin"
)

func isLocalhostOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}

func localhostCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if isLocalhostOrigin(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type")
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' https://esm.sh https://cdn.jsdelivr.net https://unpkg.com https://cdnjs.cloudflare.com https://mentacaptchaeu.eu.pythonanywhere.com 'unsafe-inline'; "+
				"style-src 'self' https://fonts.googleapis.com https://cdnjs.cloudflare.com 'unsafe-inline'; "+
				"font-src 'self' https://fonts.gstatic.com; "+
				"img-src 'self' data: blob: https://* http://*; "+
				"object-src 'none'; "+
				"frame-ancestors 'none'; "+
				"connect-src 'self' capacitor://localhost http://localhost:* https://localhost https://gitgost.livrasand.com https://api.github.com https://raw.githubusercontent.com https://github.com https://gitlab.com https://codeberg.org https://en.wikipedia.org https://www.wikidata.org https://mentacaptchaeu.eu.pythonanywhere.com",
		)
		c.Next()
	}
}

const (
	adminLimiterStoreMax   = 10000
	prCheckLimiterStoreMax = 10000
)

var (
	adminLimiterStore = newBoundedMap[[]time.Time](adminLimiterStoreMax, adminLimiterWin)
	adminLimiterMax   = 10
	adminLimiterWin   = time.Minute
)

func adminLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(adminLimiterStore, ip, time.Now(), adminLimiterWin, adminLimiterMax)
		if count > adminLimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "admin rate limit exceeded"})
			return
		}
		c.Next()
	}
}

var (
	prCheckLimiterStore = newBoundedMap[[]time.Time](prCheckLimiterStoreMax, prCheckLimiterWin)
	prCheckLimiterMax   = 30
	prCheckLimiterWin   = time.Minute
)

func prCheckLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(prCheckLimiterStore, ip, time.Now(), prCheckLimiterWin, prCheckLimiterMax)
		if count > prCheckLimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "PR check rate limit exceeded"})
			return
		}
		c.Next()
	}
}

var (
	v2LimiterStore = newBoundedMap[[]time.Time](prCheckLimiterStoreMax, v2LimiterWin)
	v2LimiterMax   = 30
	v2LimiterWin   = time.Minute
)

func v2Limiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(v2LimiterStore, ip, time.Now(), v2LimiterWin, v2LimiterMax)
		if count > v2LimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "v2 rate limit exceeded"})
			return
		}
		c.Next()
	}
}

const maxPushSize = 100 * 1024 * 1024

func sizeLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxPushSize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Push too large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPushSize)
		c.Next()
	}
}

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

func isValidRepoName(name string) bool {
	if len(name) == 0 || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") {
		return false
	}
	return true
}

func anonymousAuthMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.Contains(c.Request.URL.Path, "git-receive-pack") ||
			strings.Contains(c.Request.URL.Path, "git-upload-pack") ||
			strings.Contains(c.Request.URL.Path, "info/refs") {
			c.Next()
			return
		}

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
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.SetTrustedProxies([]string{})
	r.Use(gin.Recovery())
	r.Use(securityHeaders())
	r.Use(localhostCORS())
	r.GET("/health", HealthHandler)
	r.GET("/metrics", MetricsHandler)
	r.GET("/VERIFY", VerifyHandler)
	r.GET("/gitgost-bin", BinaryHandler)
	r.POST("/v1/pageviews", EthicalMetricsPageviewHandler)
	r.GET("/v1/sites/:site/metrics", EthicalMetricsMetricsHandler)
	r.GET("/privacy", EthicalMetricsPrivacyHandler)
	r.GET("/manifest", EthicalMetricsManifestHandler)
	r.GET("/version", EthicalMetricsVersionHandler)
	r.GET("/badges/:badge", BadgeHandler)
	r.GET("/badge/:owner/:repo", BadgePRCountHandler)
	r.GET("/install", InstallScriptHandler)
	r.StaticFile("/repo.html", "./web/repo.html")
	r.StaticFile("/profile.html", "./web/profile.html")
	r.StaticFile("/sw.js", "./web/sw.js")
	r.StaticFile("/.well-known/security.txt", "./web/.well-known/security.txt")
	r.Static("/assets", "./web/assets")
	r.StaticFile("/ethicalmetrics.js", "./web/ethicalmetrics.js")

	v1 := r.Group("/v1")
	v1.Use(sizeLimitMiddleware())
	v1.Use(validationMiddleware())
	v1.Use(anonymousAuthMiddleware(cfg.APIKey))
	{
		refsHandler := func(c *gin.Context) {
			service := c.Query("service")
			if service == "git-receive-pack" {
				ReceivePackDiscoveryHandler(c)
			} else if service == "git-upload-pack" {
				UploadPackDiscoveryHandler(c)
			} else {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unsupported service"})
			}
		}

		gh := v1.Group("/gh")
		{
			gh.GET("/:owner/:repo/info/refs", refsHandler)
			gh.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
			gh.POST("/:owner/:repo/git-upload-pack", UploadPackHandler)
			gh.GET("/:owner/:repo/issues/templates", GetIssueTemplatesHandler)
			gh.POST("/:owner/:repo/issues/anonymous", CreateAnonymousIssueHandler)
			gh.POST("/:owner/:repo/issues/:number/comments/anonymous", CreateAnonymousCommentHandler)
			gh.POST("/:owner/:repo/pulls/:number/comments/anonymous", CreateAnonymousPRCommentHandler)
			gh.POST("/:owner/:repo/discussions/:number/comments/anonymous", CreateAnonymousDiscussionCommentHandler)
		}

		gl := v1.Group("/gl")
		{
			gl.GET("/:owner/:repo/info/refs", refsHandler)
			gl.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
			gl.POST("/:owner/:repo/git-upload-pack", UploadPackHandler)
			gl.POST("/:owner/:repo/issues/anonymous", CreateAnonymousIssueHandler)
			gl.POST("/:owner/:repo/issues/:number/comments/anonymous", CreateAnonymousCommentHandler)
			gl.POST("/:owner/:repo/pulls/:number/comments/anonymous", CreateAnonymousPRCommentHandler)
		}

		cb := v1.Group("/cb")
		{
			cb.GET("/:owner/:repo/info/refs", refsHandler)
			cb.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
			cb.POST("/:owner/:repo/git-upload-pack", UploadPackHandler)
			cb.POST("/:owner/:repo/issues/anonymous", CreateAnonymousIssueHandler)
			cb.POST("/:owner/:repo/issues/:number/comments/anonymous", CreateAnonymousCommentHandler)
			cb.POST("/:owner/:repo/pulls/:number/comments/anonymous", CreateAnonymousPRCommentHandler)
		}
	}

	r.GET("/v1/moderation/report", ReportHashHandler)
	r.POST("/v1/moderation/report", ReportHashHandler)

	v2 := r.Group("/v2")
	v2.Use(sizeLimitMiddleware(), v2Limiter(), anonymousAuthMiddleware(cfg.APIKey))
	{
		v2.POST("/jobs", CreateRemoteJobHandler)
		v2.GET("/jobs/:id", GetRemoteJobHandler)
		v2.DELETE("/jobs/:id", DeleteRemoteJobHandler)
	}

	api := r.Group("/api")
	{
		api.GET("/stats", StatsHandler)
		api.GET("/recent-prs", RecentPRsHandler)
		api.GET("/pr-status/:hash", PRStatusHandler)
		api.GET("/pr/:hash/status", prCheckLimiter(), PRCheckHandler)
		api.GET("/search", SearchHandler)
		api.GET("/users/search", prCheckLimiter(), UsersSearchHandler)
		api.GET("/code/search", prCheckLimiter(), CodeSearchHandler)
		api.GET("/github/packages/:owner", prCheckLimiter(), GitHubPackagesHandler)
		api.GET("/users/profile", prCheckLimiter(), UserProfileHandler)
		api.GET("/users/repos", prCheckLimiter(), UserReposHandler)
		api.GET("/users/readme", prCheckLimiter(), UserReadmeHandler)
		api.GET("/users/starred", prCheckLimiter(), UserStarredHandler)
		api.GET("/users/orgs", prCheckLimiter(), UserOrgsHandler)
		api.GET("/users/events", prCheckLimiter(), UserEventsHandler)
		api.GET("/users/contributions", prCheckLimiter(), UserContributionsHandler)
		api.GET("/trending/:provider", TrendingHandler)
		api.GET("/cb-proxy/*path", prCheckLimiter(), CodebergProxyHandler)
		api.GET("/gl-notes/:owner/:repo/:number", GitLabIssueNotesProxyHandler)
		api.GET("/gl-commit-count/:owner/:repo", GitLabCommitCountHandler)
		api.GET("/gl-avatar", GitLabAvatarHandler)
		api.GET("/gl-commits/:owner/:repo", GitLabCommitsHandler)
		api.GET("/gl-commit-detail/:owner/:repo/:sha", GitLabCommitDetailHandler)
		api.GET("/gh-discussions/:owner/:repo", GitHubDiscussionsProxyHandler)
		api.GET("/gh-discussion/:owner/:repo/:number", GitHubDiscussionDetailProxyHandler)
		api.GET("/gh-wiki/:owner/:repo/:page", GitHubWikiProxyHandler)
		api.GET("/gl-wiki/:owner/:repo", GitLabWikiProxyHandler)
	}

	r.GET("/appeal", AppealStartHandler)
	r.POST("/appeal", AppealStartHandler)
	r.GET("/appeal/:ticket", AppealViewHandler)
	r.POST("/appeal/:ticket", AppealViewHandler)

	admin := r.Group("/admin")
	admin.Use(adminLimiter())
	{
		admin.POST("/panic", PanicHandler)
		admin.POST("/rollback", RollbackBurstHandler)
		admin.GET("/appeals", AdminAppealsHandler)
		admin.POST("/appeals/:ticket/resolve", AdminAppealResolveHandler)
	}

	r.GET("/api/status", ServiceStatusHandler)

	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet {
			parts := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
			isProvider := len(parts) > 0 && (parts[0] == "gh" || parts[0] == "gl" || parts[0] == "cb")
			if isProvider && len(parts) >= 2 {
				if len(parts) == 2 {
					c.File("./web/profile.html")
					return
				}
				profileViews := map[string]bool{
					"stars": true, "followers": true, "following": true,
					"members": true, "projects": true, "packages": true,
				}
				if len(parts) == 3 && profileViews[parts[2]] {
					c.File("./web/profile.html")
					return
				}
				c.File("./web/repo.html")
				return
			}
			knownPages := map[string]bool{
				"explore": true, "cli": true, "git-extension": true,
				"settings": true, "issue": true, "comment": true,
				"pr-comment": true, "legal": true, "about": true,
				"stats": true,
			}
			if len(parts) == 1 && knownPages[parts[0]] {
				c.File("./web/index.html")
				return
			}
		}
		c.File("./web/index.html")
	})

	return r
}
