package http

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// Gate anti-scraping para los endpoints de navegación scrapeables. El
// visitante obtiene una sesión anti-scraping resolviendo un CAPTCHA de Menta.
//
// Tabla de decisión aplicada:
//   - Browsing de repos en /api (gh-proxy, gh-raw, blame, file-history,
//     release-asset, package-file, wikis, discussions, notes, commits,
//     compare, avatars, cb/gl proxies): -> captchaGate()
//   - /explore, /trending y search: -> captchaGate() en /api/trending y las
//     rutas de búsqueda.
//   - Núcleo de Git (/v1/...), /v2, /admin y el resto de la API: sin CAPTCHA
//     (protegidos con rate limit u otro mecanismo; la CLI no puede resolver
//     un CAPTCHA y nunca necesita credenciales).
//
// La sesión de gate es una cookie firmada HMAC-SHA256 sin estado en servidor
// y con TTL incorporado (24h): tras expirar el middleware re-desafía.
const (
	captchaGateCookieName    = "gost_gate"
	captchaGateCookieMaxAge  = 60 * 60 * 24 // 24h
	captchaGateCookieVersion = "g1"
)

var (
	captchaGateSecret    atomic.Value // []byte
	captchaGateSecretLen = 32
)

// InitCaptchaGate permite fijar el secret de firma (usado por tests y por
// despliegues multi-instancia vía GITGOST_GATE_KEY para que las cookies
// emitidas por una instancia sean válidas en las demás).
func InitCaptchaGate(secret []byte) {
	if len(secret) > 0 {
		captchaGateSecret.Store(secret)
	}
}

func captchaGateKey() []byte {
	if v := captchaGateSecret.Load(); v != nil {
		return v.([]byte)
	}
	// Derivar de GITGOST_GATE_KEY si está configurado; si no, generar un
	// secret efímero por proceso (las cookies dejan de valer al reiniciar:
	// aceptable para un gateway que re-desafía a los usuarios).
	if k := strings.TrimSpace(os.Getenv("GITGOST_GATE_KEY")); k != "" {
		sum := sha256.Sum256([]byte("gost-gate:" + k))
		captchaGateSecret.Store(sum[:])
		return sum[:]
	}
	key := make([]byte, captchaGateSecretLen)
	if _, err := rand.Read(key); err != nil {
		// Último recurso determinista (nunca deberíamos llegar aquí).
		captchaGateSecret.Store([]byte("gost-gate-fallback"))
		return []byte("gost-gate-fallback")
	}
	captchaGateSecret.Store(key)
	return key
}

// captchaGateEnabled es true solo cuando el CAPTCHA está activo (endpoint de
// Menta configurado o modo enforce). Si está deshabilitado, el gate no-op y
// todo el tráfico pasa libremente — el mismo modo permisivo que ya usa
// verifyMentaCaptcha para las submissions.
func captchaGateEnabled() bool {
	return mentaAPIEndpoint != "" || mentaEnforceCaptcha
}

func gateCookieMAC(exp int64) string {
	mac := hmac.New(sha256.New, captchaGateKey())
	mac.Write([]byte(captchaGateCookieVersion))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func newGateCookieValue() string {
	exp := time.Now().Add(time.Duration(captchaGateCookieMaxAge) * time.Second).Unix()
	return captchaGateCookieVersion + "|" + strconv.FormatInt(exp, 10) + "|" + gateCookieMAC(exp)
}

func validGateCookie(value string) bool {
	parts := strings.Split(value, "|")
	if len(parts) != 3 || parts[0] != captchaGateCookieVersion {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	expected := gateCookieMAC(exp)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) == 1
}

func captchaGateClearance(c *gin.Context) bool {
	if !captchaGateEnabled() {
		return true
	}
	cookie, err := c.Cookie(captchaGateCookieName)
	if err != nil || cookie == "" {
		return false
	}
	return validGateCookie(cookie)
}

// captchaGate rechaza con 403 {captcha_required:true} las peticiones sin
// cookie de sesión válida: el frontend la resuelve con el widget y reintenta.
func captchaGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if captchaGateClearance(c) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":            "captcha verification required",
			"captcha_required": true,
		})
	}
}

// gateDocumentChallengeRedirect redirige (302) al challenge page ligero
// /challenge.html?next=..., preservando la ruta de destino para volver a
// ella tras resolver el CAPTCHA (el next se valida en el challenge page:
// solo rutas relativas del mismo origen).
func gateDocumentChallengeRedirect(c *gin.Context) {
	next := c.Request.URL.RequestURI()
	if next == "" {
		next = "/"
	}
	c.Redirect(http.StatusFound, "/challenge.html?next="+url.QueryEscape(next))
}

// serveDocumentWithGate sirve un documento HTML solo tras verificar la
// sesión de gate; si el visitante no la tiene, redirige (302) al challenge
// page ligero /challenge.html en lugar de servir index/repo/profile
// (equivale al flujo de Menta: la verificación ocurre en una página
// independiente y mínima, no encima de la app completa una vez cargada). El
// overlay dentro de cada página sigue existiendo como fallback para
// sesiones que expiran a mitad de navegación o documentos servidos por el
// service worker. Con gate deshabilitado es transparente.
func serveDocumentWithGate(c *gin.Context, fullPath string) {
	if !captchaGateClearance(c) {
		gateDocumentChallengeRedirect(c)
		return
	}
	c.File(fullPath)
}

// CaptchaGateTokenHandler valida el token del CAPTCHA de Menta y emite la
// cookie de gate. Es la mitad "accept" del challenge del frontend para
// navegar /explore, /trending, la búsqueda y las páginas de repositorio.
//
// Acepta dos tipos de prueba, ambos resueltos por Menta:
//   - captcha_token:    token corto del flujo widget (/init→/challenge→/redeem),
//     verificado (y consumido) contra {MENTA}/verify.
//   - shield_clearance: clearance del flujo universal /shield/*, verificado
//     contra {MENTA}/shield/verify (no lo consume: es reutilizable).
func CaptchaGateTokenHandler(c *gin.Context) {
	var req struct {
		CaptchaToken    string `json:"captcha_token"`
		ShieldClearance string `json:"shield_clearance"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	valid := false
	if strings.TrimSpace(req.ShieldClearance) != "" {
		valid = verifyMentaShieldClearance(req.ShieldClearance)
	} else {
		valid = verifyMentaCaptcha(req.CaptchaToken)
	}
	if !valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "captcha verification failed", "captcha_required": true})
		return
	}
	setCaptchaGateCookie(c, newGateCookieValue(), captchaGateCookieMaxAge)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func setCaptchaGateCookie(c *gin.Context, value string, maxAgeSeconds int) {
	// Secure solo cuando venimos por HTTPS (fly instancia TLS delante del
	// proceso); en pruebas/localhost así permanece funcional sobre http.
	secure := c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     captchaGateCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		// HttpOnly=false a propósito: la cookie protege datos ya públicos y
		// el frontend la comprueba (document.cookie) para decidir si mostrar
		// el widget ANTES del primer fetch, evitando una petición de más.
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}
