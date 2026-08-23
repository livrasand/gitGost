package http

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/zkp"
)

const zkpChallengeTTL = 2 * time.Minute

type zkpRegistration struct{ PublicKey zkp.PublicKey }
type zkpChallenge struct {
	Identity string
	Nonce    []byte
	Expires  time.Time
	Used     bool
}

var zkpState = struct {
	sync.Mutex
	registrations map[string]zkpRegistration
	challenges    map[string]*zkpChallenge
}{registrations: make(map[string]zkpRegistration), challenges: make(map[string]*zkpChallenge)}

type zkpRegisterRequest struct {
	Identity  string `json:"identity"`
	PublicKey string `json:"public_key"`
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

func ZKPRegisterHandler(c *gin.Context) {
	var req zkpRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Identity) == "" || req.PublicKey == "" {
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
	zkpState.Lock()
	defer zkpState.Unlock()
	if _, exists := zkpState.registrations[req.Identity]; exists {
		c.JSON(http.StatusConflict, gin.H{"error": "identity is already registered"})
		return
	}
	zkpState.registrations[req.Identity] = zkpRegistration{PublicKey: publicKey}
	c.JSON(http.StatusCreated, gin.H{"identity": req.Identity})
}

func ZKPChallengeHandler(c *gin.Context) {
	var req zkpChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Identity) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identity is required"})
		return
	}
	zkpState.Lock()
	defer zkpState.Unlock()
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
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}
