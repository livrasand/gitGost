package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutAndGet(t *testing.T) {
	s := testStore(t)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	n := &ProvisionedNode{
		ID:        "E9CFC2D1",
		Token:     "test-token-abc123",
		PubKey:    pub,
		Name:      "genesis",
		Firmware:  "1.0.0",
		CreatedAt: time.Now(),
	}

	if err := s.Put(n); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get("E9CFC2D1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Token != n.Token {
		t.Errorf("token = %q, want %q", got.Token, n.Token)
	}
	if got.Name != n.Name {
		t.Errorf("name = %q, want %q", got.Name, n.Name)
	}
	if got.Firmware != n.Firmware {
		t.Errorf("firmware = %q, want %q", got.Firmware, n.Firmware)
	}
	if len(got.PubKey) != ed25519.PublicKeySize {
		t.Errorf("pubkey len = %d, want %d", len(got.PubKey), ed25519.PublicKeySize)
	}
}

func TestLoadAll(t *testing.T) {
	s := testStore(t)

	nodes := []ProvisionedNode{
		{ID: "NODE01", Token: "tok1", Name: "alpha", Firmware: "1.0.0"},
		{ID: "NODE02", Token: "tok2", Name: "beta", Firmware: "1.0.1"},
		{ID: "NODE03", Token: "tok3", Name: "gamma", Firmware: "1.0.2"},
	}

	for i := range nodes {
		if err := s.Put(&nodes[i]); err != nil {
			t.Fatalf("Put %s: %v", nodes[i].ID, err)
		}
	}

	loaded, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("LoadAll returned %d nodes, want 3", len(loaded))
	}

	// Verify order (by created_at ASC)
	if loaded[0].ID != "NODE01" || loaded[1].ID != "NODE02" || loaded[2].ID != "NODE03" {
		t.Errorf("order = %s, %s, %s", loaded[0].ID, loaded[1].ID, loaded[2].ID)
	}
}

func TestDelete(t *testing.T) {
	s := testStore(t)

	n := &ProvisionedNode{ID: "DEAD01", Token: "tok", Name: "doomed"}
	if err := s.Put(n); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := s.Delete("DEAD01"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := s.Get("DEAD01"); err == nil {
		t.Error("Get after Delete should fail")
	}
}

func TestUpdateExisting(t *testing.T) {
	s := testStore(t)

	n := &ProvisionedNode{ID: "UPD01", Token: "old-token", Name: "original", Account: "alice"}
	if err := s.Put(n); err != nil {
		t.Fatalf("Put: %v", err)
	}

	n.Token = "new-token"
	n.Name = "updated"
	if err := s.Put(n); err != nil {
		t.Fatalf("Put update: %v", err)
	}

	got, err := s.Get("UPD01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Token != "new-token" {
		t.Errorf("token = %q, want new-token", got.Token)
	}
	if got.Name != "updated" {
		t.Errorf("name = %q, want updated", got.Name)
	}
	if got.Account != "alice" {
		t.Errorf("account = %q, want alice", got.Account)
	}
}

func TestLoadByAccount(t *testing.T) {
	s := testStore(t)

	nodes := []ProvisionedNode{
		{ID: "NODE01", Token: "tok1", Name: "alpha", Account: "alice"},
		{ID: "NODE02", Token: "tok2", Name: "beta", Account: "bob"},
		{ID: "NODE03", Token: "tok3", Name: "gamma", Account: "alice"},
	}
	for i := range nodes {
		if err := s.Put(&nodes[i]); err != nil {
			t.Fatalf("Put %s: %v", nodes[i].ID, err)
		}
	}

	aliceNodes, err := s.LoadByAccount("alice")
	if err != nil {
		t.Fatalf("LoadByAccount(alice): %v", err)
	}
	if len(aliceNodes) != 2 {
		t.Fatalf("LoadByAccount(alice) returned %d nodes, want 2", len(aliceNodes))
	}
	ids := map[string]bool{}
	for _, n := range aliceNodes {
		ids[n.ID] = true
		if n.Account != "alice" {
			t.Errorf("node %s account = %q, want alice", n.ID, n.Account)
		}
	}
	if !ids["NODE01"] || !ids["NODE03"] {
		t.Errorf("LoadByAccount(alice) = %v, want NODE01 and NODE03", ids)
	}

	bobNodes, err := s.LoadByAccount("bob")
	if err != nil {
		t.Fatalf("LoadByAccount(bob): %v", err)
	}
	if len(bobNodes) != 1 || bobNodes[0].ID != "NODE02" {
		t.Errorf("LoadByAccount(bob) = %+v, want only NODE02", bobNodes)
	}

	nobody, err := s.LoadByAccount("nobody")
	if err != nil {
		t.Fatalf("LoadByAccount(nobody): %v", err)
	}
	if len(nobody) != 0 {
		t.Errorf("LoadByAccount(nobody) returned %d nodes, want 0", len(nobody))
	}
}
