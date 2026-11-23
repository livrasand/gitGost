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
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	captchaGateCookieName    = "gost_gate"
	captchaGateCookieMaxAge  = 60 * 60 * 24
	captchaGateCookieVersion = "g2"
)

var sameOriginNextPattern = regexp.MustCompile(`^/(?:[a-z][a-z0-9+\-.]*:|\/\/|\\)`)

var captchaGateSecret atomic.Value

func InitCaptchaGate(secret []byte) {
	if len(secret) == 0 {
		return
	}
	sum := sha256.Sum256([]byte("gost-gate:" + string(secret)))
	captchaGateSecret.Store(sum[:])
}

func captchaGateKey() []byte {
	if v := captchaGateSecret.Load(); v != nil {
		return v.([]byte)
	}
	if k := strings.TrimSpace(os.Getenv("GITGOST_GATE_KEY")); k != "" {
		sum := sha256.Sum256([]byte("gost-gate:" + k))
		captchaGateSecret.Store(sum[:])
		return sum[:]
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		captchaGateSecret.Store([]byte("gost-gate-fallback"))
		return []byte("gost-gate-fallback")
	}
	captchaGateSecret.Store(key)
	return key
}

func captchaGateEnabled() bool {
	return false
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
	if validGateCookie(cookie) {
		return true
	}
	clearCaptchaGateCookie(c)
	return false
}

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

func gateDocumentChallengeRedirect(c *gin.Context) {
	next := c.Request.URL.RequestURI()
	if next == "" {
		next = "/"
	}
	c.Redirect(http.StatusFound, "/challenge.html?next="+url.QueryEscape(next))
}

func serveDocumentWithGate(c *gin.Context, fullPath string) {
	c.File(fullPath)
}

func setCaptchaGateCookie(c *gin.Context, value string, maxAgeSeconds int) {
	secure := c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     captchaGateCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func clearCaptchaGateCookie(c *gin.Context) {
	secure := c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     captchaGateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func CaptchaGateTokenHandler(c *gin.Context) {
	setCaptchaGateCookie(c, newGateCookieValue(), captchaGateCookieMaxAge)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func CaptchaGateStatusHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled": captchaGateEnabled(),
		"cleared": captchaGateClearance(c),
	})
}
