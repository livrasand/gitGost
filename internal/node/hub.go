// Package node implements the GitGost Node Protocol control plane: it keeps
// the registry of connected storage nodes, handles pairing codes, heartbeats
// and exposes synchronous RPC calls to a node over its WebSocket connection.
//
// Authority split:
//   - server: accounts, repository metadata, permissions, online/offline state
//   - node:   Git objects, refs, storage on the user's microSD
package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// IdentityKey is the gin context key holding the authenticated ZKP identity
// on /api/nodes requests gated by zkpAuthMiddleware.
const IdentityKey = "zkp_identity"

// Store persists provisioned nodes. If nil, node registry is in-memory only.
var NodeStore *Store

// RepoEntry describes a bare repository reported by a node.
type RepoEntry struct {
	Name string `json:"name"`
	Size uint64 `json:"size"`
}

// NodeInfo is the live status of a node as shown in the dashboard.
type NodeInfo struct {
	Name         string      `json:"name,omitempty"`
	Firmware     string      `json:"firmware,omitempty"`
	Online       bool        `json:"-"`
	UptimeSec    uint64      `json:"uptime_s"`
	StorageFree  uint64      `json:"storage_free"`
	StorageTotal uint64      `json:"storage_total"`
	RamTotal     uint64      `json:"ram_total"`
	HeapFree     uint64      `json:"heap_free"`
	WifiRSSI     int         `json:"wifi_rssi"`
	LastSeen     time.Time   `json:"last_seen"`
	Repos        []RepoEntry `json:"repositories"`
}

func (n *NodeInfo) OnlineSeen() {
	n.Online = true
	n.LastSeen = time.Now()
}

func (n *NodeInfo) MarkOffline() {
	n.Online = false
	n.LastSeen = time.Now()
}

// Pairing is a pending pairing attempt keyed by its short-lived code.
type Pairing struct {
	Code      string
	NodeID    string
	Name      string
	PubKeyHex string
	Conn      *Conn // live socket of the unprovisioned node, may be nil
	ExpiresAt time.Time
}

var (
	regMu       sync.RWMutex
	online      = map[string]*Conn{}  // node_id -> live connection
	tokens      = map[string]string{} // node_id -> token (provisioned)
	pubkeys     = map[string][]byte{} // node_id -> ed25519 public key
	nodeInfos   = map[string]*NodeInfo{}
	nodeAccount = map[string]string{}   // node_id -> owning identity
	pendingPair = map[string]*Pairing{} // code -> pairing attempt
)

const pairingCodeTTL = 10 * time.Minute

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func normalizeCode(code string) string {
	out := make([]rune, 0, len(code))
	for _, r := range code {
		if r == '-' || r == ' ' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

func getInfo(id string) *NodeInfo {
	info := nodeInfos[id]
	if info == nil {
		info = &NodeInfo{}
		nodeInfos[id] = info
	}
	return info
}

// RegisterPairing stores a pending pairing attempt from a pair_request.
func RegisterPairing(conn *Conn, code, nodeID, name, pubKeyHex string) {
	code = normalizeCode(code)
	regMu.Lock()
	defer regMu.Unlock()
	pendingPair[code] = &Pairing{
		Code:      code,
		NodeID:    nodeID,
		Name:      name,
		PubKeyHex: pubKeyHex,
		Conn:      conn,
		ExpiresAt: time.Now().Add(pairingCodeTTL),
	}
}

// ConfirmPairing completes pairing when a user enters the code on the web UI.
// It sends pair_ok through the node connection and provisions it with a token.
// The account is the authenticated ZKP identity that owns the resulting node.
func ConfirmPairing(code, account string) (string, error) {
	code = normalizeCode(code)

	regMu.Lock()
	p := pendingPair[code]
	if p == nil || time.Now().After(p.ExpiresAt) {
		delete(pendingPair, code)
		regMu.Unlock()
		return "", fmt.Errorf("invalid or expired pairing code")
	}
	delete(pendingPair, code)

	token := newToken()
	tokens[p.NodeID] = token
	nodeAccount[p.NodeID] = account
	getInfo(p.NodeID).Name = p.Name
	if key, err := hex.DecodeString(p.PubKeyHex); err == nil && len(key) == ed25519.PublicKeySize {
		pubkeys[p.NodeID] = key
	}
	// Prefer the current live connection over the one captured at
	// RegisterPairing time: the ESP32 reconnects frequently, so p.Conn is
	// often already dead and a pair_ok sent there would be lost silently.
	conn := online[p.NodeID]
	if conn == nil {
		conn = p.Conn
	}
	regMu.Unlock()

	if conn != nil {
		_ = conn.Send(map[string]interface{}{
			"type":    "pair_ok",
			"node_id": p.NodeID,
			"token":   token,
		})
	}

	if NodeStore != nil {
		_ = NodeStore.Put(&ProvisionedNode{
			ID:        p.NodeID,
			Token:     token,
			PubKey:    pubkeys[p.NodeID],
			Name:      p.Name,
			Firmware:  getInfo(p.NodeID).Firmware,
			Account:   account,
			CreatedAt: time.Now(),
		})
	}

	return p.NodeID, nil
}

// SetOnline registers an authenticated hello from a provisioned node.
func SetOnline(nodeID, token, name, fwVersion string, c *Conn) bool {
	regMu.Lock()
	defer regMu.Unlock()

	stored, ok := tokens[nodeID]
	if !ok || stored != token {
		return false
	}
	if name != "" {
		getInfo(nodeID).Name = name
	}
	if fwVersion != "" {
		getInfo(nodeID).Firmware = fwVersion
	}
	getInfo(nodeID).OnlineSeen()
	online[nodeID] = c
	return true
}

// IsOnline reports whether the node currently has a live connection.
func IsOnline(nodeID string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	return online[nodeID] != nil
}

// GetConn returns the live connection of a node, or nil when offline.
func GetConn(nodeID string) *Conn {
	regMu.RLock()
	defer regMu.RUnlock()
	return online[nodeID]
}

// SetOffline marks a node as gone (connection dropped).
func SetOffline(nodeID string) {
	regMu.Lock()
	delete(online, nodeID)
	getInfo(nodeID).MarkOffline()
	regMu.Unlock()
}

// Touch updates liveness telemetry from a heartbeat.
func Touch(nodeID string, uptime, free, total, ramTotal, heap uint64, rssi int) {
	regMu.Lock()
	defer regMu.Unlock()
	info := getInfo(nodeID)
	info.UptimeSec = uptime
	info.StorageFree = free
	info.StorageTotal = total
	info.RamTotal = ramTotal
	info.HeapFree = heap
	info.WifiRSSI = rssi
	info.LastSeen = time.Now()
}

// UpdateRepos stores the repository index reported by a node.
func UpdateRepos(nodeID string, repos []RepoEntry) {
	regMu.Lock()
	defer regMu.Unlock()
	getInfo(nodeID).Repos = repos
}

// AccountOf returns the owning identity of a node, or "" if unknown.
func AccountOf(nodeID string) string {
	regMu.RLock()
	defer regMu.RUnlock()
	return nodeAccount[nodeID]
}

// ListByAccount returns the dashboard view of nodes owned by the given account.
func ListByAccount(account string) []map[string]interface{} {
	regMu.RLock()
	defer regMu.RUnlock()

	out := make([]map[string]interface{}, 0, len(tokens))
	for id := range tokens {
		if nodeAccount[id] != account {
			continue
		}
		info := nodeInfos[id]
		if info == nil {
			info = &NodeInfo{LastSeen: time.Now()}
		}
		isOnline := online[id] != nil
		name := info.Name
		if name == "" {
			name = id
		}
		repos := info.Repos
		if repos == nil {
			repos = []RepoEntry{}
		}
		out = append(out, map[string]interface{}{
			"id":           id,
			"name":         name,
			"online":       isOnline,
			"last_seen":    info.LastSeen,
			"storage_free": info.StorageFree,
			"storage_max":  info.StorageTotal,
			"ram_total":    info.RamTotal,
			"uptime_s":     info.UptimeSec,
			"heap_free":    info.HeapFree,
			"wifi_rssi":    info.WifiRSSI,
			"firmware":     info.Firmware,
			"repositories": repos,
		})
	}
	return out
}

// List returns the dashboard view of every known node.
func List() []map[string]interface{} {
	regMu.RLock()
	defer regMu.RUnlock()

	out := make([]map[string]interface{}, 0, len(tokens))
	for id := range tokens {
		info := nodeInfos[id]
		if info == nil {
			info = &NodeInfo{LastSeen: time.Now()}
		}
		isOnline := online[id] != nil
		name := info.Name
		if name == "" {
			name = id
		}
		repos := info.Repos
		if repos == nil {
			repos = []RepoEntry{}
		}
		out = append(out, map[string]interface{}{
			"id":           id,
			"name":         name,
			"online":       isOnline,
			"last_seen":    info.LastSeen,
			"storage_free": info.StorageFree,
			"storage_max":  info.StorageTotal,
			"ram_total":    info.RamTotal,
			"uptime_s":     info.UptimeSec,
			"heap_free":    info.HeapFree,
			"wifi_rssi":    info.WifiRSSI,
			"firmware":     info.Firmware,
			"repositories": repos,
		})
	}
	return out
}

// IsProvisioned reports whether a node_id has been paired to an account.
func IsProvisioned(nodeID string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := tokens[nodeID]
	return ok
}

// LoadProvisioned restores all provisioned nodes from the store into memory.
// Called once at server startup.
func LoadProvisioned() error {
	if NodeStore == nil {
		return nil
	}
	nodes, err := NodeStore.LoadAll()
	if err != nil {
		return fmt.Errorf("cargar nodos persistidos: %w", err)
	}
	regMu.Lock()
	defer regMu.Unlock()
	for _, n := range nodes {
		tokens[n.ID] = n.Token
		if len(n.PubKey) == ed25519.PublicKeySize {
			pubkeys[n.ID] = n.PubKey
		}
		if n.Account != "" {
			nodeAccount[n.ID] = n.Account
		}
		info := getInfo(n.ID)
		info.Name = n.Name
		info.Firmware = n.Firmware
		if !n.CreatedAt.IsZero() {
			info.LastSeen = n.CreatedAt
		}
	}
	return nil
}
