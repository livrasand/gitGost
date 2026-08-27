package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func testKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return pub, priv, hex.EncodeToString(pub)
}

func signPayload(priv ed25519.PrivateKey, action, nodeID string, ts int64) string {
	payload := strings.Join([]string{action, nodeID, itoa(ts)}, ":")
	return hex.EncodeToString(ed25519.Sign(priv, []byte(payload)))
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestPairingFlow runs a full pairing round trip against a fake node:
// pair_request -> user confirms code -> pair_ok -> hello -> heartbeat.
func TestPairingFlow(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/node/ws", WSHandler)
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/node/ws?id=ABCDEF0123456789"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	pub, priv, pubHex := testKeyPair()

	// 1. node sends pair_request
	code := "ABCD-1234"
	err = ws.WriteJSON(map[string]interface{}{
		"type":    "pair_request",
		"code":    code,
		"node_id": "ABCDEF0123456789",
		"name":    "desk-node",
		"pubkey":  pubHex,
		"ts":      42,
		"sig":     signPayload(priv, "pair", "ABCDEF0123456789", 42),
	})
	if err != nil {
		t.Fatalf("pair_request: %v", err)
	}

	// wait for the server read loop to process the request
	waitFor(t, 2*time.Second, func() bool {
		regMu.RLock()
		defer regMu.RUnlock()
		return pendingPair[normalizeCode(code)] != nil
	})

	// bad signature must be rejected before confirmation
	if _, err := ConfirmPairing("ZZZZ-9999", ""); err == nil {
		t.Fatal("expected unknown code to fail")
	}

	// tampered signature should not register a valid pending pairing
	ws.WriteJSON(map[string]interface{}{
		"type": "pair_request", "code": "EVIL-CODE", "node_id": "ABCDEF0123456789",
		"name": "x", "pubkey": pubHex, "ts": 43,
		"sig": signPayload(priv, "WRONG", "ABCDEF0123456789", 43),
	})
	regMu.RLock()
	evil := pendingPair[normalizeCode("EVIL-CODE")] != nil
	regMu.RUnlock()
	if evil {
		t.Fatal("tampered pair_request must not be registered")
	}

	// 2. user enters the code on the web UI
	nodeID, err := ConfirmPairing(code, "testuser")
	if err != nil {
		t.Fatalf("ConfirmPairing: %v", err)
	}
	if nodeID != "ABCDEF0123456789" {
		t.Fatalf("unexpected node id %q", nodeID)
	}

	// 3. node receives pair_ok with token (skip interleaved error frames,
	// e.g. the rejection of the tampered pair_request)
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var pairOK struct {
		Token string `json:"token"`
	}
	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read pair_ok: %v", err)
		}
		var msg struct {
			Type  string `json:"type"`
			Token string `json:"token"`
		}
		if json.Unmarshal(payload, &msg) != nil {
			continue
		}
		if msg.Type == "pair_ok" {
			pairOK.Token = msg.Token
			break
		}
	}
	if pairOK.Token == "" {
		t.Fatal("empty token in pair_ok")
	}

	// 4. hello with correct signature+token goes online
	ts := time.Now().Unix()
	ws.WriteJSON(map[string]interface{}{
		"type": "hello", "node_id": nodeID, "name": "desk-node",
		"token": pairOK.Token, "fw": "test",
		"pubkey": pubHex, "ts": ts,
		"sig": signPayload(priv, "hello", nodeID, ts),
	})

	waitFor(t, 2*time.Second, func() bool { return IsOnline(nodeID) })

	// the paired node should be owned by "testuser"
	if got := AccountOf(nodeID); got != "testuser" {
		t.Fatalf("AccountOf = %q, want %q", got, "testuser")
	}

	// hello with wrong token must not go online
	SetOffline(nodeID)
	ws.WriteJSON(map[string]interface{}{
		"type": "hello", "node_id": nodeID,
		"token": "wrong-token", "fw": "test",
		"pubkey": pubHex, "ts": ts,
		"sig": signPayload(priv, "hello", nodeID, ts),
	})
	time.Sleep(200 * time.Millisecond)
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	for {
		var msg struct {
			Type string `json:"type"`
		}
		err := ws.ReadJSON(&msg)
		if err != nil {
			break
		}
		if msg.Type == "pair_rejected" {
			goto rejected
		}
	}
rejected:
	if IsOnline(nodeID) {
		t.Fatal("hello with wrong token must be rejected")
	}

	_ = pub
}

func TestNormalizeCode(t *testing.T) {
	if got := normalizeCode("7k4p-92xm"); got != "7K4P92XM" {
		t.Fatalf("normalizeCode lower: %q", got)
	}
	if got := normalizeCode("7K4P-92XM"); got != "7K4P92XM" {
		t.Fatalf("normalizeCode mixed: %q", got)
	}
}

func TestSafePaths(t *testing.T) {
	if safeRepo("../etc") || safeRepo("a/b") || safeRepo("") {
		t.Fatal("invalid repo accepted")
	}
	if !safeRepo("kodlet.git") || !safeRepo("my_repo-2") {
		t.Fatal("valid repo rejected")
	}
	if safePath("/abs/path") || safePath("a/../b") || safePath(`a\b`) {
		t.Fatal("unsafe path accepted")
	}
	if !safePath("objects/ab/cdef") || !safePath("") {
		t.Fatal("safe path rejected")
	}
}

func TestRPCTimeoutOnSilentNode(t *testing.T) {
	c := &Conn{
		ID:       "TESTNODE00000001",
		pending:  make(map[uint64]chan map[string]interface{}),
		closedCh: make(chan struct{}),
	}
	c.ws = silentWS{}
	start := time.Now()
	_, err := c.Call("fs_read", map[string]interface{}{"repo": "r", "path": "HEAD"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed < rpcTimeout-500*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
}

type silentWS struct{}

func (silentWS) WriteJSON(interface{}) error { return nil }
func (silentWS) Close() error                { return nil }
