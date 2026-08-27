package node

import (
	"crypto/ed25519"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true }, // nodes are not browsers
}

// validNodeName guards against control characters in device-reported names.
func validNodeName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// verifySignature checks an Ed25519 detached signature over
// "<action>:<nodeID>:<ts>" using the hex public key sent by the node.
func verifySignature(pubKeyHex, sigHex, payload string) bool {
	key, err1 := hex.DecodeString(pubKeyHex)
	sig, err2 := hex.DecodeString(sigHex)
	if err1 != nil || err2 != nil || len(key) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(key), []byte(payload), sig)
}

func signedPayload(nodeID string, ts float64, action string) string {
	return strings.Join([]string{action, nodeID, formatMillis(ts)}, ":")
}

func formatMillis(ts float64) string {
	return strconv.FormatUint(uint64(ts), 10)
}

// WSHandler upgrades /node/ws and runs the node read loop.
func WSHandler(c *gin.Context) {
	nodeID := strings.TrimSpace(strings.ToUpper(c.Query("id")))
	if nodeID == "" || len(nodeID) > 32 || !isSafeNodeID(nodeID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing or invalid node id"})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[node] upgrade failed for %s: %v", nodeID, err)
		return
	}

	conn := &Conn{
		ID:       nodeID,
		ws:       ws,
		pending:  make(map[uint64]chan map[string]interface{}),
		closedCh: make(chan struct{}),
	}

	defer func() {
		close(conn.closedCh)
		SetOffline(nodeID)
		_ = ws.Close()
	}()

	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			break // connection closed by node
		}

		msg, derr := decodeMessage(payload)
		if derr != nil {
			continue
		}
		mtype, _ := msg["type"].(string)

		switch mtype {
		case "pair_request":
			handlePairRequest(conn, msg)
		case "hello":
			handleHello(conn, msg)
		case "heartbeat":
			handleHeartbeat(conn, msg)
		case "repo_list":
			handleRepoList(conn, msg)
		case "resp", "ack", "fs_data", "error":
			if rid, ok := msg["req_id"].(float64); ok {
				conn.dispatch(rid, msg)
			}
		default:
			log.Printf("[node] unknown message type %q from %s", mtype, nodeID)
		}
	}
}

func isSafeNodeID(id string) bool {
	for _, r := range id {
		ok := (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F')
		if !ok {
			return false
		}
	}
	return len(id) >= 8 && len(id) <= 32
}

func handlePairRequest(conn *Conn, msg map[string]interface{}) {
	code, _ := msg["code"].(string)
	name, _ := msg["name"].(string)
	pubKeyHex, _ := msg["pubkey"].(string)
	sigHex, _ := msg["sig"].(string)
	ts, _ := msg["ts"].(float64)

	payload := signedPayload(conn.ID, ts, "pair")
	if !verifySignature(pubKeyHex, sigHex, payload) {
		log.Printf("[node] invalid pair_request signature from %s", conn.ID)
		_ = conn.Send(map[string]interface{}{
			"type":  "error",
			"error": "invalid_signature",
		})
		return
	}

	RegisterPairing(conn, code, conn.ID, name, pubKeyHex)
	log.Printf("[node] pairing code registered for %s (%s)", conn.ID, code)
}

func handleHello(conn *Conn, msg map[string]interface{}) {
	token, _ := msg["token"].(string)
	name, _ := msg["name"].(string)
	fw, _ := msg["fw"].(string)
	pubKeyHex, _ := msg["pubkey"].(string)
	sigHex, _ := msg["sig"].(string)
	ts, _ := msg["ts"].(float64)

	payload := signedPayload(conn.ID, ts, "hello")
	if !verifySignature(pubKeyHex, sigHex, payload) {
		log.Printf("[node] invalid hello signature from %s", conn.ID)
		_ = conn.Send(map[string]interface{}{"type": "error", "error": "invalid_signature"})
		_ = conn.ws.Close()
		return
	}

	if !SetOnline(conn.ID, token, name, fw, conn) {
		log.Printf("[node] hello rejected (bad token) from %s", conn.ID)
		_ = conn.Send(map[string]interface{}{"type": "pair_rejected"})
		_ = conn.ws.Close()
		return
	}

	regMu.RLock()
	key := pubkeys[conn.ID]
	regMu.RUnlock()
	if key != nil {
		conn.PubKey = key
	} else if k, err := hex.DecodeString(pubKeyHex); err == nil {
		conn.PubKey = k
	}

	conn.Name = name
	log.Printf("[node] ONLINE: %s (%s) fw=%s", conn.ID, name, fw)

	go func() {
		if _, err := conn.Call("repo_list", nil); err != nil {
			log.Printf("[node] repo_list refresh failed for %s: %v", conn.ID, err)
		}
	}()
}

func handleHeartbeat(conn *Conn, msg map[string]interface{}) {
	if !IsOnline(conn.ID) {
		return
	}
	Touch(conn.ID,
		uint64(num(msg["uptime_s"])),
		uint64(num(msg["storage_free"])),
		uint64(num(msg["storage_total"])),
		uint64(num(msg["ram_total"])),
		uint64(num(msg["heap_free"])),
		int(num(msg["wifi_rssi"])),
	)
}

func handleRepoList(conn *Conn, msg map[string]interface{}) {
	rawRepos, _ := msg["repositories"].([]interface{})
	repos := make([]RepoEntry, 0, len(rawRepos))
	for _, r := range rawRepos {
		obj, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := obj["name"].(string)
		repos = append(repos, RepoEntry{Name: name, Size: uint64(num(obj["size"]))})
	}
	UpdateRepos(conn.ID, repos)
	log.Printf("[node] repo index updated for %s: %d repos", conn.ID, len(repos))
}

func num(v interface{}) float64 {
	f, _ := v.(float64)
	return f
}

// PairHandler completes pairing from the web UI: POST /api/nodes/pair {"code"}.
// The authenticated identity is taken from the request context (set by
// zkpAuthMiddleware) or from the X-Gitgost-Key header for gitGost Account mode.
// This identity becomes the owner of the newly paired node.
func PairHandler(c *gin.Context) {
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}

	// Try to get owner from ZKP identity first
	identity, exists := c.Get(IdentityKey)
	var owner string
	if exists {
		owner, _ = identity.(string)
	}
	
	// If no ZKP identity, try to get from API key (gitGost Account mode)
	if owner == "" {
		apiKey := c.GetHeader("X-Gitgost-Key")
		if apiKey != "" {
			owner = apiKey
		}
	}
	
	if owner == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authenticated identity required"})
		return
	}

	nodeID, err := ConfirmPairing(body.Code, owner)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[node] PAIRED: %s at %s by %s", nodeID, time.Now().Format(time.RFC3339), owner)
	c.JSON(http.StatusOK, gin.H{"node_id": nodeID})
}

// ListHandler returns the nodes owned by the authenticated identity.
// It accepts both ZKP authentication (IdentityKey set by zkpAuthMiddlewareOptional)
// and API key authentication (X-Gitgost-Key header). For gitGost Account mode, the API key
// is used as the account identifier.
func ListHandler(c *gin.Context) {
	// Try to get identity from IdentityKey first (set by zkpAuthMiddlewareOptional if present)
	identity, exists := c.Get(IdentityKey)
	if exists {
		owner, _ := identity.(string)
		if owner != "" {
			c.JSON(http.StatusOK, gin.H{"nodes": ListByAccount(owner)})
			return
		}
	}
	
	// If no ZKP identity, try to get the API key owner from the X-Gitgost-Key header
	// The anonymousAuthMiddleware has already validated the key
	apiKey := c.GetHeader("X-Gitgost-Key")
	if apiKey != "" {
		// For gitGost Account mode: the API key is used as the account identifier
		// In the node registry, nodes are stored by account (owner)
		// We look up nodes where the owner matches this API key
		owner := apiKey
		c.JSON(http.StatusOK, gin.H{"nodes": ListByAccount(owner)})
		return
	}
	
	// Fallback: return empty list if no valid authentication
	c.JSON(http.StatusOK, gin.H{"nodes": []interface{}{}})
}
