package http

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	nodepkg "github.com/livrasand/gitGost/internal/node"
	"github.com/livrasand/gitGost/internal/zkp"
)

const (
	zkpChallengeTTL      = 2 * time.Minute
	zkpMaxBodySize       = 16 * 1024
	zkpMaxIdentityLength = 128
	zkpMaxRegistrations  = 10000
)

// zkpSessionTTL is how long an issued session token remains valid.
const zkpSessionTTL = 24 * time.Hour

// issueSessionToken creates an HMAC-signed bearer token binding the identity
// to a short expiry, using the server's long-lived secret key.
func issueSessionToken(identity string) string {
	expiry := strconv.FormatInt(time.Now().Add(zkpSessionTTL).Unix(), 10)
	payload := identity + "." + expiry
	sig := hmac.New(sha256.New, getSecretKey())
	sig.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
}

// ValidateSessionToken checks the HMAC and expiry, returning the identity.
func ValidateSessionToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", false
	}
	payload := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", false
	}
	expected := hmac.New(sha256.New, getSecretKey())
	expected.Write([]byte(payload))
	if !hmac.Equal(sig, expected.Sum(nil)) {
		return "", false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", false
	}
	if time.Now().Unix() > expiry {
		return "", false
	}
	return parts[0], true
}

// zkpAuthMiddleware requires a valid X-ZKP-Token header on the /api/nodes
// routes, binding the authenticated identity into the request context.
func zkpAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-ZKP-Token")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "ZKP session token required"})
			return
		}
		identity, ok := ValidateSessionToken(token)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired session token"})
			return
		}
		c.Set(nodepkg.IdentityKey, identity)
		c.Next()
	}
}

// zkpAuthMiddlewareOptional is like zkpAuthMiddleware but does not require the token.
// If a valid X-ZKP-Token header is present, it sets IdentityKey in the context.
// If not, it simply continues to the next middleware without error.
func zkpAuthMiddlewareOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-ZKP-Token")
		if token != "" {
			identity, ok := ValidateSessionToken(token)
			if ok {
				c.Set(nodepkg.IdentityKey, identity)
			}
			// If token is invalid, we just continue without setting IdentityKey
			// This allows fallback to other authentication methods
		}
		c.Next()
	}
}

type zkpRegistration struct {
	PublicKey zkp.PublicKey
	CreatedAt time.Time
}
type zkpChallenge struct {
	Identity string
	Nonce    []byte
	Expires  time.Time
	Used     bool
}

var zkpState = struct {
	sync.Mutex
	registrations map[string]zkpRegistration
	order         []string
	challenges    map[string]*zkpChallenge
}{registrations: make(map[string]zkpRegistration), challenges: make(map[string]*zkpChallenge)}

type zkpRegisterRequest struct {
	Identity     string `json:"identity"`
	PublicKey    string `json:"public_key"`
	CaptchaToken string `json:"captcha_token"`
	Website      string `json:"website"`
}
type zkpChallengeRequest struct {
	Identity string `json:"identity"`
}
type zkpVerifyRequest struct {
	Identity    string `json:"identity"`
	ChallengeID string `json:"challenge_id"`
	CommitmentX string `json:"commitment_x"`
	CommitmentY string `json:"commitment_y"`
	Response    string `json:"response"`
}

func zkpSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		if c.Request.ContentLength > zkpMaxBodySize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "ZKP request too large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, zkpMaxBodySize)
		c.Next()
	}
}

func ZKPRegisterHandler(c *gin.Context) {
	var req zkpRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identity and public_key are required"})
		return
	}
	req.Identity = strings.TrimSpace(req.Identity)
	if req.Identity == "" || len(req.Identity) > zkpMaxIdentityLength || req.PublicKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identity and public_key are required"})
		return
	}
	keyBytes, err := base64.RawURLEncoding.DecodeString(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public_key"})
		return
	}
	publicKey, err := zkp.ParsePublicKey(keyBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public_key"})
		return
	}
	if !verifyMentaCaptcha(req.CaptchaToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "captcha verification failed"})
		return
	}
	// Honeypot: silently accept bots/crawlers that filled the hidden field
	// without ever storing a registration. The 201 reply is identical to a
	// real success so the trap is never revealed.
	if strings.TrimSpace(req.Website) != "" {
		c.JSON(http.StatusCreated, gin.H{"identity": req.Identity})
		return
	}
	zkpState.Lock()
	defer zkpState.Unlock()
	if _, exists := zkpState.registrations[req.Identity]; exists {
		c.JSON(http.StatusConflict, gin.H{"error": "identity is already registered"})
		return
	}
	if len(zkpState.registrations) >= zkpMaxRegistrations {
		oldest := zkpState.order[0]
		zkpState.order = zkpState.order[1:]
		delete(zkpState.registrations, oldest)
	}
	zkpState.registrations[req.Identity] = zkpRegistration{PublicKey: publicKey, CreatedAt: time.Now()}
	zkpState.order = append(zkpState.order, req.Identity)
	c.JSON(http.StatusCreated, gin.H{"identity": req.Identity})
}

// zkpRegistrationGracePeriod is how long a ZKP registration may exist without
// a provisioned gitGost Forge node before it is automatically removed.
const zkpRegistrationGracePeriod = 7 * 24 * time.Hour

var zkpSweepOnce sync.Once

// StartZKPRegistrationSweeper launches a background task that deletes ZKP
// registrations older than zkpRegistrationGracePeriod whose identity has no
// provisioned gitGost Forge node. Such an identity cannot prove ownership of a
// node, so it is treated as a stale registration. NodeStore must be
// initialized (and LoadProvisioned called) before the sweeper takes effect.
// The task runs for the lifetime of the process.
func StartZKPRegistrationSweeper(interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	zkpSweepOnce.Do(func() {
		go func() {
			sweepZKPRegistrations()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				sweepZKPRegistrations()
			}
		}()
	})
}

func sweepZKPRegistrations() {
	zkpState.Lock()
	now := time.Now()
	var candidates []string
	for identity, reg := range zkpState.registrations {
		if now.Sub(reg.CreatedAt) > zkpRegistrationGracePeriod {
			candidates = append(candidates, identity)
		}
	}
	zkpState.Unlock()

	for _, identity := range candidates {
		if zkpIdentityHasNode(identity) {
			continue
		}
		zkpState.Lock()
		if reg, ok := zkpState.registrations[identity]; ok && time.Since(reg.CreatedAt) > zkpRegistrationGracePeriod {
			delete(zkpState.registrations, identity)
			for i, id := range zkpState.order {
				if id == identity {
					zkpState.order = append(zkpState.order[:i], zkpState.order[i+1:]...)
					break
				}
			}
		}
		zkpState.Unlock()
	}
}

func zkpIdentityHasNode(identity string) bool {
	if nodepkg.NodeStore == nil {
		return false
	}
	nodes, err := nodepkg.NodeStore.LoadByAccount(identity)
	if err != nil {
		return false
	}
	return len(nodes) > 0
}

func ZKPChallengeHandler(c *gin.Context) {
	var req zkpChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Identity) == "" || len(strings.TrimSpace(req.Identity)) > zkpMaxIdentityLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identity is required"})
		return
	}
	zkpState.Lock()
	defer zkpState.Unlock()
	for id, challenge := range zkpState.challenges {
		if time.Now().After(challenge.Expires) || challenge.Used {
			delete(zkpState.challenges, id)
		}
	}
	if _, exists := zkpState.registrations[req.Identity]; !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "identity is not registered"})
		return
	}
	nonce := make([]byte, 32)
	challengeIDBytes := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "challenge generation failed"})
		return
	}
	if _, err := rand.Read(challengeIDBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "challenge generation failed"})
		return
	}
	id := base64.RawURLEncoding.EncodeToString(challengeIDBytes)
	zkpState.challenges[id] = &zkpChallenge{Identity: req.Identity, Nonce: nonce, Expires: time.Now().Add(zkpChallengeTTL)}
	c.JSON(http.StatusOK, gin.H{"challenge_id": id, "challenge": base64.RawURLEncoding.EncodeToString(nonce)})
}

func ZKPVerifyHandler(c *gin.Context) {
	var req zkpVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Identity == "" || req.ChallengeID == "" || req.CommitmentX == "" || req.CommitmentY == "" || req.Response == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identity, challenge_id and proof are required"})
		return
	}
	zkpState.Lock()
	defer zkpState.Unlock()
	for id, storedChallenge := range zkpState.challenges {
		if time.Now().After(storedChallenge.Expires) || storedChallenge.Used {
			delete(zkpState.challenges, id)
		}
	}
	challenge, exists := zkpState.challenges[req.ChallengeID]
	registration, registered := zkpState.registrations[req.Identity]
	if !exists || !registered || challenge.Identity != req.Identity || challenge.Used || time.Now().After(challenge.Expires) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired challenge"})
		return
	}
	commitmentX, errX := base64.RawURLEncoding.DecodeString(req.CommitmentX)
	commitmentY, errY := base64.RawURLEncoding.DecodeString(req.CommitmentY)
	response, errResponse := base64.RawURLEncoding.DecodeString(req.Response)
	proof, proofErr := zkp.ParseProof(commitmentX, commitmentY, response)
	if errX != nil || errY != nil || errResponse != nil || proofErr != nil || !zkp.Verify(registration.PublicKey, challenge.Nonce, proof) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid proof"})
		return
	}
	challenge.Used = true
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"identity":      req.Identity,
		"session_token": issueSessionToken(req.Identity),
	})
}
