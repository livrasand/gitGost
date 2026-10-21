package http

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/utils"
)

const (
	captchaGateCookieName    = "gost_gate"
	captchaGateCookieMaxAge  = 60 * 60 * 24
	captchaGateCookieVersion = "g2"
)

var (
	captchaGateSecret atomic.Value
	mentaAPIEndpoint  string
	mentaAPIKey       string
	mentaEnforce      bool
)

func InitMentaConfig(apiEndpoint, apiKey string, enforce bool) {
	mentaAPIEndpoint = strings.TrimRight(apiEndpoint, "/")
	mentaAPIKey = apiKey
	mentaEnforce = enforce
	if mentaAPIEndpoint == "" {
		if enforce {
			utils.Log("WARNING: MENTA_CAPTCHA_ENFORCE=true but MENTA_API_ENDPOINT unset")
		} else {
			utils.Log("WARNING: Menta CAPTCHA verification DISABLED")
		}
	}
}

func InitCaptchaGate(secret []byte) {
	if len(secret) > 0 {
		captchaGateSecret.Store(secret)
	}
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
	return mentaAPIEndpoint != "" || mentaEnforce
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
	if !captchaGateClearance(c) {
		gateDocumentChallengeRedirect(c)
		return
	}
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

func verifyMentaCaptcha(token string) bool {
	if !captchaGateEnabled() {
		return true
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if mentaAPIEndpoint == "" {
		return false
	}
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, mentaAPIEndpoint+"/verify", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if mentaAPIKey != "" {
		req.Header.Set("X-API-Key", mentaAPIKey)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Menta verify failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	var result struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	return result.Valid
}

// shield clearance cache
var shieldClearanceCache = map[string]int64{}
var shieldCacheMu sync.Mutex

func parseShieldClearanceClaims(token string) (string, int64, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", 0, false
	}
	var claims struct {
		Sub string `json:"sub"`
		JTI string `json:"jti"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", 0, false
	}
	if claims.Sub != "shield-clearance" || claims.JTI == "" {
		return "", 0, false
	}
	return claims.JTI, claims.Exp, true
}

func verifyMentaShieldClearance(token string) bool {
	if !captchaGateEnabled() {
		return true
	}
	if mentaAPIEndpoint == "" {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	_, exp, ok := parseShieldClearanceClaims(token)
	if !ok {
		return false
	}
	now := time.Now().Unix()
	if exp > 0 && now > exp {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	shieldCacheMu.Lock()
	if cached, hit := shieldClearanceCache[key]; hit && (cached == 0 || cached > now) {
		shieldCacheMu.Unlock()
		return true
	}
	shieldCacheMu.Unlock()

	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, mentaAPIEndpoint+"/shield/verify", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Menta shield verify failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	var result struct {
		Valid   bool `json:"valid"`
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	if !result.Valid && !result.Success {
		return false
	}
	ttl := exp
	if ttl <= 0 {
		ttl = now + int64(captchaGateCookieMaxAge)
	}
	shieldCacheMu.Lock()
	shieldClearanceCache[key] = ttl
	shieldCacheMu.Unlock()
	return true
}

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

// MentaCaptchaProxyHandler relays to Menta for browsers without direct access.
func MentaCaptchaProxyHandler(c *gin.Context) {
	if mentaAPIEndpoint == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "menta not configured"})
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	target := mentaAPIEndpoint + "/" + path
	if c.Request.URL.RawQuery != "" {
		target += "?" + c.Request.URL.RawQuery
	}
	// safety: ensure same host
	base, err := url.Parse(mentaAPIEndpoint)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "bad endpoint"})
		return
	}
	tgt, err := url.Parse(target)
	if err != nil || tgt.Host != base.Host || tgt.Scheme != base.Scheme {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target"})
		return
	}
	req, err := http.NewRequest(c.Request.Method, target, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request build failed"})
		return
	}
	for k, v := range c.Request.Header {
		req.Header[k] = v
	}
	if mentaAPIKey != "" {
		req.Header.Set("X-API-Key", mentaAPIKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Menta proxy failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "menta unreachable"})
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		c.Writer.Header()[k] = v
	}
	c.Status(resp.StatusCode)
	_, _ = c.Writer.Write(readAll(resp.Body))
}


func readAll(r io.Reader) []byte {
	buf := &bytes.Buffer{}
	_, _ = io.Copy(buf, r)
	return buf.Bytes()
}
