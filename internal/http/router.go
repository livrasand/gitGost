package http

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/config"
	nodepkg "github.com/livrasand/gitGost/internal/node"

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
		// El origen de Menta se deriva de la configuración (MENTA_API_ENDPOINT)
		// para que el navegador pueda resolver el proof-of-work directamente
		// contra Menta sin que la URL esté hardcodeada en el HTML.
		mentaOrigin := ""
		if mentaAPIEndpoint != "" {
			if u, err := url.Parse(mentaAPIEndpoint); err == nil {
				mentaOrigin = u.Scheme + "://" + u.Host
			}
		}
		if mentaOrigin == "" {
			mentaOrigin = "https://mentacaptchaeu.eu.pythonanywhere.com"
		}
		scriptSrc := "script-src 'self' https://esm.sh https://cdn.jsdelivr.net https://unpkg.com https://cdnjs.cloudflare.com https://static.cloudflareinsights.com " + mentaOrigin + " 'unsafe-inline'"
		connectSrc := "connect-src 'self' capacitor://localhost http://localhost:* https://localhost https://gitgost.livrasand.com https://en.wikipedia.org https://www.wikidata.org https://cloudflareinsights.com https://static.cloudflareinsights.com " + mentaOrigin
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				scriptSrc+"; "+
				"style-src 'self' https://fonts.googleapis.com https://cdnjs.cloudflare.com 'unsafe-inline'; "+
				"font-src 'self' https://fonts.gstatic.com; "+
				"img-src 'self' data: blob: https://* http://*; "+
				"object-src 'none'; "+
				"frame-ancestors 'none'; "+
				connectSrc,
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
	prCheckLimiterMax = 30
	prCheckLimiterWin = time.Minute
)

// Los proxies de forge (gh/cb/gl) sirven todas las peticiones del frontend
// (vista código, commits, README, sidebar...), que disparan ráfagas de decenas
// de llamadas al abrir una página. Compartir el limitador estricto de
// prCheckLimiter provocaba 429 inmediatos, así que tienen uno propio más alto.
var (
	proxyLimiterMax = 240
	proxyLimiterWin = time.Minute
)

func proxyLimiter() gin.HandlerFunc {
	return prCheckWindowLimiter("proxy rate limit exceeded", proxyLimiterMax, proxyLimiterWin)
}

func prCheckWindowLimiter(message string, max int, win time.Duration) gin.HandlerFunc {
	store := newBoundedMap[[]time.Time](prCheckLimiterStoreMax, win)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(store, ip, time.Now(), win, max)
		if count > max {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": message})
			return
		}
		c.Next()
	}
}

func prCheckLimiter() gin.HandlerFunc {
	return prCheckWindowLimiter("PR check rate limit exceeded", prCheckLimiterMax, prCheckLimiterWin)
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

// v1WriteLimiter acota por IP las escrituras anónimas del protocolo Git
// (issues, comentarios y reacciones): los clientes Git no llevan CAPTCHA ni
// llave API — su única defensa anti-spam es la tarifa de peticiones.
const (
	v1WriteLimiterMax = 10
	v1WriteLimiterWin = time.Minute
)

var v1WriteLimiterStore = newBoundedMap[[]time.Time](prCheckLimiterStoreMax, v1WriteLimiterWin)

func v1WriteLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(v1WriteLimiterStore, ip, time.Now(), v1WriteLimiterWin, v1WriteLimiterMax)
		if count > v1WriteLimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "anonymous write rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// gitFetchLimiter acota por IP la lectura Git (clone/fetch vía info/refs y
// upload-pack): mantiene el uso del CLI fluido y frena el scraping masivo del
// protocolo smart HTTP. El push (git-receive-pack) se descarta del contador:
// ya tiene su propio límite de 5 PRs/hora dentro del handler.
const (
	gitFetchLimiterMax = 60
	gitFetchLimiterWin = time.Minute
)

var gitFetchLimiterStore = newBoundedMap[[]time.Time](prCheckLimiterStoreMax, gitFetchLimiterWin)

func gitFetchLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Query("service") == "git-receive-pack" {
			c.Next()
			return
		}
		ip := c.ClientIP()
		count := windowAdd(gitFetchLimiterStore, ip, time.Now(), gitFetchLimiterWin, gitFetchLimiterMax)
		if count > gitFetchLimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "git fetch rate limit exceeded"})
			return
		}
		c.Next()
	}
}

var (
	captchaLimiterStore = newBoundedMap[[]time.Time](prCheckLimiterStoreMax, captchaLimiterWin)
	captchaLimiterMax   = 30
	captchaLimiterWin   = time.Minute
)

func captchaLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(captchaLimiterStore, ip, time.Now(), captchaLimiterWin, captchaLimiterMax)
		if count > captchaLimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "captcha rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// gateStatusLimiter acota la consulta de estado del gate. Es una respuesta
// ligera, pero se limita aparte para no gastar la cuota del challenge.
const (
	gateStatusLimiterMax = 120
	gateStatusLimiterWin = time.Minute
)

var gateStatusLimiterStore = newBoundedMap[[]time.Time](prCheckLimiterStoreMax, gateStatusLimiterWin)

func gateStatusLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		count := windowAdd(gateStatusLimiterStore, ip, time.Now(), gateStatusLimiterWin, gateStatusLimiterMax)
		if count > gateStatusLimiterMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "gate status rate limit exceeded"})
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

// gitSmartHTTPSuffixes son los sufijos exactos del protocolo smart HTTP de
// Git. Solo estos caminos quedan exentos de cualquier comprobación; usar
// strings.Contains permitía que cualquier ruta futura con wildcard que
// contuviese estas cadenas se saltase la lógica silenciosamente.
var gitSmartHTTPSuffixes = []string{
	"/info/refs",
	"/git-upload-pack",
	"/git-receive-pack",
}

func isGitSmartHTTPPath(path string) bool {
	for _, suffix := range gitSmartHTTPSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func anonymousAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isGitSmartHTTPPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		c.Next()
	}
}

func SetupRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	if cfg.MaintenanceMode {
		InitMaintenance(true, "MAINTENANCE_MODE env")
	}
	if cfg.SponsorBanner {
		InitSponsorBanner(true, "SPONSOR_BANNER env")
	}
	r := gin.New()
	r.SetTrustedProxies([]string{})
	r.Use(gin.Recovery())
	r.Use(securityHeaders())
	r.Use(localhostCORS())
	r.Use(maintenanceMiddleware())
	r.GET("/health", HealthHandler)
	r.GET("/metrics", MetricsHandler)
	r.GET("/api/config", ConfigHandler)

	// GitGost Node Protocol: los nodos de almacenamiento inician la conexión
	// (saliente, sin puertos abiertos) y mantienen el socket vivo.
	r.GET("/node/ws", nodepkg.WSHandler)

	// API endpoint for listing nodes (optional ZKP authentication)
	r.GET("/api/nodes", anonymousAuthMiddleware(), zkpAuthMiddlewareOptional(), nodepkg.ListHandler)

	// API endpoints for node operations (require ZKP authentication)
	nodesAPI := r.Group("/api/nodes", anonymousAuthMiddleware(), zkpAuthMiddleware())
	{
		nodesAPI.POST("/pair", nodepkg.PairHandler)
		nodesAPI.GET("/:id/repos", nodepkg.NodeReposHandler)
		nodesAPI.POST("/:id/repos", nodepkg.NodeRepoCreateFullHandler)
		nodesAPI.GET("/:id/refs/:repo", nodepkg.NodeRefsHandler)
		nodesAPI.POST("/:id/repo/create", nodepkg.NodeRepoCreateHandler)
		nodesAPI.POST("/:id/repo/delete", nodepkg.NodeRepoDeleteHandler)
		nodesAPI.POST("/:id/fs/read", nodepkg.NodeFSReadHandler)
		nodesAPI.POST("/:id/fs/write", nodepkg.NodeFSWriteHandler)
		nodesAPI.POST("/:id/fs/list", nodepkg.NodeFSListHandler)
		nodesAPI.POST("/:id/cmd/ping", nodepkg.NodeCmdHandler)
	}

	ggAPI := r.Group("/api/gg", zkpAuthMiddlewareOptional())
	{
		ggAPI.GET("/repos/:owner/:repo/tree", nodepkg.GGRepoTreeHandler)
		ggAPI.GET("/repos/:owner/:repo/raw", nodepkg.GGRepoRawHandler)
		ggAPI.GET("/repos/:owner/:repo/info", nodepkg.GGRepoInfoHandler)
		ggAPI.GET("/repos/:owner/:repo/branches", nodepkg.GGRepoBranchesHandler)
		ggAPI.GET("/repos/:owner/:repo/tags", nodepkg.GGRepoTagsHandler)
	}

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
	// Challenge page ligero (flujo tipo Menta): el gate redirige aquí a los
	// documentos HTML sin sesión de gate en vez de servir la app completa.
	r.StaticFile("/challenge.html", "./web/challenge.html")
	r.StaticFile("/robots.txt", "./web/robots.txt")
	r.StaticFile("/llms.txt", "./web/llms.txt")
	r.GET("/repo.html", func(c *gin.Context) { serveDocumentWithGate(c, "./web/repo.html") })
	r.GET("/profile.html", func(c *gin.Context) { serveDocumentWithGate(c, "./web/profile.html") })
	r.StaticFile("/sw.js", "./web/sw.js")
	r.StaticFile("/colors.json", "./web/colors.json")
	r.StaticFile("/robots.json", "./web/robots.json")
	r.StaticFile("/.well-known/security.txt", "./web/.well-known/security.txt")
	r.Static("/assets", "./web/assets")
	r.StaticFile("/ethicalmetrics.js", "./web/ethicalmetrics.js")
	r.StaticFile("/zkp-client.js", "./web/zkp-client.js")
	r.StaticFile("/bip39-words.js", "./web/bip39-words.js")

	v1 := r.Group("/v1")
	v1.Use(sizeLimitMiddleware())
	v1.Use(validationMiddleware())
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
			gh.GET("/:owner/:repo/info/refs", gitFetchLimiter(), refsHandler)
			gh.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
			gh.POST("/:owner/:repo/git-upload-pack", gitFetchLimiter(), UploadPackHandler)
			gh.GET("/:owner/:repo/issues/templates", GetIssueTemplatesHandler)
			gh.POST("/:owner/:repo/issues/anonymous", v1WriteLimiter(), CreateAnonymousIssueHandler)
			gh.POST("/:owner/:repo/issues/:number/reactions", v1WriteLimiter(), CreateReactionHandler)
			gh.POST("/:owner/:repo/issues/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousCommentHandler)
			gh.POST("/:owner/:repo/issues/:number/comments/:comment_id/reactions", v1WriteLimiter(), CreateReactionHandler)
			gh.POST("/:owner/:repo/pulls/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousPRCommentHandler)
			gh.POST("/:owner/:repo/discussions/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousDiscussionCommentHandler)
		}

		gl := v1.Group("/gl")
		{
			gl.GET("/:owner/:repo/info/refs", gitFetchLimiter(), refsHandler)
			gl.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
			gl.POST("/:owner/:repo/git-upload-pack", gitFetchLimiter(), UploadPackHandler)
			gl.POST("/:owner/:repo/issues/anonymous", v1WriteLimiter(), CreateAnonymousIssueHandler)
			gl.POST("/:owner/:repo/issues/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousCommentHandler)
			gl.POST("/:owner/:repo/issues/:number/reactions", v1WriteLimiter(), CreateReactionHandler)
			gl.POST("/:owner/:repo/issues/:number/comments/:comment_id/reactions", v1WriteLimiter(), CreateReactionHandler)
			gl.POST("/:owner/:repo/pulls/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousPRCommentHandler)
		}

		cb := v1.Group("/cb")
		{
			cb.GET("/:owner/:repo/info/refs", gitFetchLimiter(), refsHandler)
			cb.POST("/:owner/:repo/git-receive-pack", ReceivePackHandler)
			cb.POST("/:owner/:repo/git-upload-pack", gitFetchLimiter(), UploadPackHandler)
			cb.POST("/:owner/:repo/issues/anonymous", v1WriteLimiter(), CreateAnonymousIssueHandler)
			cb.POST("/:owner/:repo/issues/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousCommentHandler)
			cb.POST("/:owner/:repo/issues/:number/reactions", v1WriteLimiter(), CreateReactionHandler)
			cb.POST("/:owner/:repo/issues/:number/comments/:comment_id/reactions", v1WriteLimiter(), CreateReactionHandler)
			cb.POST("/:owner/:repo/pulls/:number/comments/anonymous", v1WriteLimiter(), CreateAnonymousPRCommentHandler)
		}
	}

	r.GET("/v1/moderation/report", ReportHashHandler)
	r.POST("/v1/moderation/report", ReportHashHandler)

	v2 := r.Group("/v2")
	v2.Use(sizeLimitMiddleware(), v2Limiter())
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
		api.GET("/issue-status/:owner/:repo/:number", IssueStatusHandler)
		api.GET("/pr/:hash/status", prCheckLimiter(), PRCheckHandler)
		// Extracción de browse/download muy scrapeada: requiere sesión de
		// CAPTCHA (cookie "gost_gate" emitida tras resolver Menta). La CLI no
		// pasa por aquí; consigue contenido por /v1 (upload-pack) sin CAPTCHA.
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
		api.GET("/gh-proxy/*path", proxyLimiter(), GitHubAPIProxyHandler)
		api.GET("/gh-raw/*path", proxyLimiter(), GitHubRawProxyHandler)
		api.GET("/gh-avatar/*path", proxyLimiter(), GitHubAvatarProxyHandler)
		api.GET("/blame/:provider/:owner/:repo", proxyLimiter(), BlameHandler)
		api.GET("/file-history/:provider/:owner/:repo", proxyLimiter(), FileHistoryHandler)
		api.GET("/release-asset/:provider/:owner/:repo", proxyLimiter(), ReleaseAssetDownloadHandler)
		api.GET("/package-file/:provider/:owner/:repo", proxyLimiter(), PackageFileDownloadHandler)
		api.GET("/trending/:provider", proxyLimiter(), TrendingHandler)
		api.GET("/cb-proxy/*path", proxyLimiter(), CodebergProxyHandler)
		api.GET("/gl-proxy/*path", proxyLimiter(), GitLabProxyHandler)
		api.GET("/gl-notes/:owner/:repo/:number", proxyLimiter(), GitLabIssueNotesProxyHandler)
		api.GET("/gl-commit-count/:owner/:repo", proxyLimiter(), GitLabCommitCountHandler)
		api.GET("/gl-avatar", proxyLimiter(), GitLabAvatarHandler)
		api.GET("/gl-commits/:owner/:repo", proxyLimiter(), GitLabCommitsHandler)
		api.GET("/gl-commit-detail/:owner/:repo/:sha", proxyLimiter(), GitLabCommitDetailHandler)
		api.GET("/compare/:provider/:owner/:repo", proxyLimiter(), CompareHandler)
		api.GET("/gh-discussions/:owner/:repo", proxyLimiter(), GitHubDiscussionsProxyHandler)
		api.GET("/gh-discussion/:owner/:repo/:number", proxyLimiter(), GitHubDiscussionDetailProxyHandler)
		api.GET("/gh-wiki/:owner/:repo/:page", proxyLimiter(), GitHubWikiProxyHandler)
		api.GET("/gl-wiki/:owner/:repo", proxyLimiter(), GitLabWikiProxyHandler)
	// Ruta aparte del proxy /captcha/*path para no chocar el catch-all:
	// el frontend POSTs aquí el token del widget y hereda la cookie.
		api.POST("/captcha-gate", captchaLimiter(), CaptchaGateTokenHandler)
		api.GET("/captcha-gate/status", gateStatusLimiter(), CaptchaGateStatusHandler)
		api.Any("/captcha/*path", captchaLimiter(), MentaCaptchaProxyHandler)
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
		admin.POST("/sponsor", SponsorBannerHandler)
		admin.GET("/appeals", AdminAppealsHandler)
		admin.POST("/appeals", AdminAppealsHandler)
		admin.POST("/appeals/:ticket/resolve", AdminAppealResolveHandler)
	}

	r.GET("/api/status", ServiceStatusHandler)
	zkp := r.Group("/api/zkp")
	zkp.Use(zkpSecurity(), prCheckWindowLimiter("ZKP rate limit exceeded", 30, time.Minute))
	{
		zkp.POST("/register", ZKPRegisterHandler)
		zkp.POST("/challenge", ZKPChallengeHandler)
		zkp.POST("/verify", ZKPVerifyHandler)
	}

	r.NoRoute(func(c *gin.Context) {
		// Las rutas de API desconocidas deben fallar con 404 JSON; servir el
		// índice HTML aquí hacía que el frontend intentase parsear HTML como
		// JSON cuando el cliente hablaba con un backend más antiguo.
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "unknown API endpoint"})
			return
		}
		if c.Request.Method == http.MethodGet {
			if c.Request.URL.Path == "/" {
				serveDocumentWithGate(c, "./web/index.html")
				return
			}
			parts := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
			isProvider := len(parts) > 0 && (parts[0] == "gh" || parts[0] == "gl" || parts[0] == "cb" || parts[0] == "gg")
			if isProvider && len(parts) >= 2 {
				if len(parts) == 2 {
					serveDocumentWithGate(c, "./web/profile.html")
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
				serveDocumentWithGate(c, "./web/repo.html")
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
		// Ruta desconocida: responder 404 real (soft-404 con 200 hacía que
		// rutas como /.env parecieran existir y confundía a escáneres y
		// clientes HTTP), pero sirviendo el índice para navegación humana.
		body, err := os.ReadFile("./web/index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "index unavailable")
			return
		}
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", body)
	})

	return r
}
