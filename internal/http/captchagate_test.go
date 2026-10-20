package http

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// El estado de Menta vive en variables de paquete handlers.go; cada prueba
// guarda/restaura los valores para no ensuciar al resto del paquete.
func withMentaState(t *testing.T, endpoint string, enforce bool) {
	t.Helper()
	prevEndpoint := mentaAPIEndpoint
	prevEnforce := mentaEnforceCaptcha
	mentaAPIEndpoint = endpoint
	mentaEnforceCaptcha = enforce
	t.Cleanup(func() {
		mentaAPIEndpoint = prevEndpoint
		mentaEnforceCaptcha = prevEnforce
	})
	gin.SetMode(gin.TestMode)
}

func buildGateRouter() *gin.Engine {
	r := gin.New()
	r.POST("/api/captcha-gate", CaptchaGateTokenHandler)
	r.GET("/gated", captchaGate(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func TestCaptchaGateDisabledPassesThrough(t *testing.T) {
	withMentaState(t, "", false)
	r := buildGateRouter()
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/gated", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("gate debe dejar pasar cuando el CAPTCHA está deshabilitado; got %d", res.Code)
	}
}

func TestCaptchaGateRejectsWithoutCookie(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, mentaTestEndpoint, true)
	r := buildGateRouter()
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/gated", nil))
	if res.Code != http.StatusForbidden {
		t.Fatalf("sin cookie debe dar 403; got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "captcha_required") {
		t.Fatalf("respuesta esperada con captcha_required; got %q", res.Body.String())
	}
}

func TestCaptchaGateCookieRoundTrip(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	value := newGateCookieValue()
	if !validGateCookie(value) {
		t.Fatal("cookie recién emitida debe validar")
	}
	if validGateCookie(value + "x") {
		t.Fatal("cookie alterada no debe validar")
	}
	exp := time.Now().Add(-time.Hour).Unix()
	expired := "g1|" + strconv.FormatInt(exp, 10) + "|" + gateCookieMAC(exp)
	if validGateCookie(expired) {
		t.Fatal("cookie expirada no debe validar")
	}

	// Un secret distinto invalida cookies firmadas con otro.
	InitCaptchaGate([]byte("other-secret"))
	if validGateCookie(value) {
		t.Fatal("cookie firmada con otro secret no debe validar")
	}
}

func TestCaptchaGateAllowsValidCookie(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, mentaTestEndpoint, true)
	r := buildGateRouter()
	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	req.AddCookie(&http.Cookie{Name: captchaGateCookieName, Value: newGateCookieValue()})
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("con cookie válida debe pasar; got %d", res.Code)
	}
}

func TestCaptchaGateTokenIssuesCookie(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, "", false) // verificación deshabilitada: acepta cualquier token
	r := buildGateRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/captcha-gate", strings.NewReader(`{"captcha_token":"whatever"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("token issuance debe responder 200; got %d %s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != captchaGateCookieName {
		t.Fatalf("esperada cookie %s; got %+v", captchaGateCookieName, cookies)
	}
	if cookies[0].MaxAge != captchaGateCookieMaxAge {
		t.Fatalf("esperado MaxAge %d; got %d", captchaGateCookieMaxAge, cookies[0].MaxAge)
	}
	if !validGateCookie(cookies[0].Value) {
		t.Fatal("cookie emitida debe validar")
	}
}

func TestCaptchaGateTokenRejectsInvalidToken(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, mentaTestEndpoint, true)
	r := buildGateRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/captcha-gate", strings.NewReader(`{"captcha_token":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("token inválido debe dar 403 (fail-closed); got %d", res.Code)
	}
}

// buildTestJWT crea un token con forma JWT (3 partes) y payload verificable.
// La firma no se valida localmente: la validación real la hace Menta.
func buildTestJWT(t *testing.T, sub, jti string, exp int64) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"sub": sub, "jti": jti, "exp": exp, "ip": "203.0.113.1"})
	if err != nil {
		t.Fatalf("no se pudo serializar el payload: %v", err)
	}
	return header + "." + enc.EncodeToString(claims) + ".c2lnbmF0dXJl"
}

// TestCaptchaGateAcceptsShieldClearance valida el flujo universal de Menta:
// el clearance se verifica contra {MENTA}/shield/verify y emite la cookie.
func TestCaptchaGateAcceptsShieldClearance(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	var calls int32
	menta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shield/verify" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&calls, 1)
		var body struct {
			Token string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]bool{"success": true, "valid": body.Token != ""})
	}))
	defer menta.Close()
	withMentaState(t, menta.URL, true)

	r := buildGateRouter()
	token := buildTestJWT(t, "shield-clearance", "jti-shield-ok", time.Now().Add(time.Hour).Unix())
	req := httptest.NewRequest(http.MethodPost, "/api/captcha-gate",
		strings.NewReader(`{"shield_clearance":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("clearance válido debe dar 200; got %d %s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != captchaGateCookieName {
		t.Fatalf("esperada cookie %s; got %+v", captchaGateCookieName, cookies)
	}
	if !validGateCookie(cookies[0].Value) {
		t.Fatal("cookie emitida debe validar")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("esperada 1 llamada a /shield/verify; got %d", calls)
	}

	// Segundo uso del mismo clearance: se sirve desde la caché (el clearance
	// es reutilizable durante su TTL) sin volver a preguntar a Menta.
	req2 := httptest.NewRequest(http.MethodPost, "/api/captcha-gate",
		strings.NewReader(`{"shield_clearance":"`+token+`"}`))
	req2.Header.Set("Content-Type", "application/json")
	res2 := httptest.NewRecorder()
	r.ServeHTTP(res2, req2)
	if res2.Code != http.StatusOK {
		t.Fatalf("el clearance cacheado debe dar 200; got %d", res2.Code)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("el clearance cacheado no debe volver a llamar a Menta; got %d llamadas", calls)
	}
}

// TestCaptchaGateRejectsInvalidShieldClearance: un clearance que Menta
// rechaza (o con subject incorrecto) no emite cookie.
func TestCaptchaGateRejectsInvalidShieldClearance(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	menta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"success": true, "valid": false})
	}))
	defer menta.Close()
	withMentaState(t, menta.URL, true)

	r := buildGateRouter()
	token := buildTestJWT(t, "shield-clearance", "jti-shield-bad", time.Now().Add(time.Hour).Unix())
	req := httptest.NewRequest(http.MethodPost, "/api/captcha-gate",
		strings.NewReader(`{"shield_clearance":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("clearance inválido debe dar 403 (fail-closed); got %d", res.Code)
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("no debe emitirse cookie con un clearance inválido")
	}
}

// TestCaptchaGateRejectsExpiredShieldClearance: un clearance expirado no se
// consulta siquiera (falla antes de hablar con Menta).
func TestCaptchaGateRejectsExpiredShieldClearance(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	var calls int32
	menta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode(map[string]bool{"success": true, "valid": true})
	}))
	defer menta.Close()
	withMentaState(t, menta.URL, true)

	r := buildGateRouter()
	token := buildTestJWT(t, "shield-clearance", "jti-shield-expired", time.Now().Add(-time.Hour).Unix())
	req := httptest.NewRequest(http.MethodPost, "/api/captcha-gate",
		strings.NewReader(`{"shield_clearance":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("clearance expirado debe dar 403; got %d", res.Code)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("un clearance expirado no debe llamar a Menta; got %d llamadas", calls)
	}
}

// TestConfigHandlerExposesMentaAPI: el frontend necesita saber contra qué
// host resolver el proof-of-work (si no, el navegador no puede ir directo a
// Menta sin una URL hardcodeada en el HTML).
func TestConfigHandlerExposesMentaAPI(t *testing.T) {
	withMentaState(t, "https://menta.example.com", true)
	r := gin.New()
	r.GET("/api/config", ConfigHandler)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var body struct {
		CaptchaGate bool   `json:"captcha_gate"`
		MentaAPI    string `json:"menta_api"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v (%s)", err, res.Body.String())
	}
	if !body.CaptchaGate {
		t.Fatal("captcha_gate debe ser true con el gate activo")
	}
	if body.MentaAPI != "https://menta.example.com" {
		t.Fatalf("menta_api debe anunciar el endpoint de Menta; got %q", body.MentaAPI)
	}
}

func buildDocumentRouter() *gin.Engine {
	r := gin.New()
	// Documento ficticio: con cookie válida se sirve (200); sin cookie se
	// redirige al challenge page.
	r.GET("/", func(c *gin.Context) { serveDocumentWithGate(c, "./captchagate.go") })
	r.GET("/gh/owner/repo", func(c *gin.Context) { serveDocumentWithGate(c, "./captchagate.go") })
	return r
}

func TestCaptchaGateDocumentRedirectsToChallenge(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, mentaTestEndpoint, true)
	r := buildDocumentRouter()
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/?branch=main", nil))
	if res.Code != http.StatusFound {
		t.Fatalf("documento sin gate cookie debe redirigir 302; got %d", res.Code)
	}
	if loc := res.Header().Get("Location"); !strings.HasPrefix(loc, "/challenge.html?next=") {
		t.Fatalf("esperada redirección a /challenge.html?next=...; got %q", loc)
	}
	if !strings.Contains(res.Header().Get("Location"), "branch%3Dmain") {
		t.Fatalf("?next= debe preservar los query params destino; got %q", res.Header().Get("Location"))
	}
}

func TestCaptchaGateDocumentRedirectPreservesRoute(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, mentaTestEndpoint, true)
	r := buildDocumentRouter()
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/gh/owner/repo", nil))
	if res.Code != http.StatusFound {
		t.Fatalf("documento sin gate cookie debe redirigir 302; got %d", res.Code)
	}
	if !strings.Contains(res.Header().Get("Location"), "next=%2Fgh%2Fowner%2Frepo") {
		t.Fatalf("?next= debe preservar la ruta destino; got %q", res.Header().Get("Location"))
	}
}

func TestCaptchaGateDocumentServesWithCookie(t *testing.T) {
	InitCaptchaGate([]byte("test-secret"))
	withMentaState(t, mentaTestEndpoint, true)
	r := buildDocumentRouter()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: captchaGateCookieName, Value: newGateCookieValue()})
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("documento con cookie válida debe servirse; got %d", res.Code)
	}
}

func TestCaptchaGateDocumentDisabledPassesThrough(t *testing.T) {
	withMentaState(t, "", false)
	r := buildDocumentRouter()
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("documentos sin gate activo deben servirse sin redirección; got %d", res.Code)
	}
}
