package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/zkp"
)

func TestZKPAuthenticationRejectsReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/register", ZKPRegisterHandler)
	r.POST("/challenge", ZKPChallengeHandler)
	r.POST("/verify", ZKPVerifyHandler)

	private, public, err := zkp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	identity := "test-zkp-identity"
	registerBody := map[string]string{
		"identity":   identity,
		"public_key": base64.RawURLEncoding.EncodeToString(public.Bytes()),
	}
	response := postJSON(t, r, "/register", registerBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", response.Code, http.StatusCreated)
	}

	response = postJSON(t, r, "/challenge", map[string]string{"identity": identity})
	if response.Code != http.StatusOK {
		t.Fatalf("challenge status = %d, want %d", response.Code, http.StatusOK)
	}
	var challenge struct {
		ID    string `json:"challenge_id"`
		Value string `json:"challenge"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(challenge.Value)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := zkp.Prove(private, nonce)
	if err != nil {
		t.Fatal(err)
	}
	proofBody := map[string]string{
		"identity":     identity,
		"challenge_id": challenge.ID,
		"commitment_x": base64.RawURLEncoding.EncodeToString(proof.CommitmentX.Bytes()),
		"commitment_y": base64.RawURLEncoding.EncodeToString(proof.CommitmentY.Bytes()),
		"response":     base64.RawURLEncoding.EncodeToString(proof.Response.Bytes()),
	}
	response = postJSON(t, r, "/verify", proofBody)
	if response.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want %d", response.Code, http.StatusOK)
	}
	response = postJSON(t, r, "/verify", proofBody)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func postJSON(t *testing.T, handler http.Handler, path string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
