package node

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const nodesSchema = "CREATE TABLE IF NOT EXISTS nodes (" +
	"id TEXT PRIMARY KEY, " +
	"token TEXT NOT NULL, " +
	"pubkey TEXT NOT NULL, " +
	"name TEXT NOT NULL, " +
	"firmware TEXT NOT NULL, " +
	"account TEXT NOT NULL DEFAULT '', " +
	"created_at TEXT NOT NULL" +
	");"

// Store persists provisioned node credentials across restarts.
type Store struct {
	db *sql.DB
}

// OpenStore initializes the node registry database.
func OpenStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("crear directorio de datos: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(nodesSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("crear esquema de nodes: %w", err)
	}
	// Backfill migration: add the account column to databases created
	// before the identity-scoping change. New tables already include it.
	if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN account TEXT NOT NULL DEFAULT ''`); err != nil {
		// Column already exists (new schema): SQLite returns an error,
		// which we ignore since CREATE TABLE IF NOT EXISTS handled it.
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrar columna account: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// ProvisionedNode is a persisted node credential.
type ProvisionedNode struct {
	ID        string
	Token     string
	PubKey    []byte
	Name      string
	Firmware  string
	Account   string
	CreatedAt time.Time
}

// Put inserts or updates a provisioned node.
func (s *Store) Put(n *ProvisionedNode) error {
	pubkeyHex := hex.EncodeToString(n.PubKey)
	_, err := s.db.Exec(
		`INSERT INTO nodes (id, token, pubkey, name, firmware, account, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET token=excluded.token, pubkey=excluded.pubkey, name=excluded.name, firmware=excluded.firmware, account=excluded.account`,
		n.ID, n.Token, pubkeyHex, n.Name, n.Firmware, n.Account,
		n.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// Get returns a single provisioned node by ID.
func (s *Store) Get(id string) (*ProvisionedNode, error) {
	row := s.db.QueryRow(
		`SELECT id, token, pubkey, name, firmware, account, created_at FROM nodes WHERE id = ?`, id)
	return scanNode(row)
}

// Delete removes a provisioned node.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	return err
}

// LoadAll returns every provisioned node, ordered by creation time.
func (s *Store) LoadAll() ([]ProvisionedNode, error) {
	rows, err := s.db.Query(
		`SELECT id, token, pubkey, name, firmware, account, created_at FROM nodes ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProvisionedNode
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// LoadByAccount returns all provisioned nodes owned by the given account.
func (s *Store) LoadByAccount(account string) ([]ProvisionedNode, error) {
	rows, err := s.db.Query(
		`SELECT id, token, pubkey, name, firmware, account, created_at FROM nodes WHERE account = ? ORDER BY created_at ASC`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProvisionedNode
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(row rowScanner) (*ProvisionedNode, error) {
	var (
		n         ProvisionedNode
		pubkeyHex string
		created   string
	)
	if err := row.Scan(&n.ID, &n.Token, &pubkeyHex, &n.Name, &n.Firmware, &n.Account, &created); err != nil {
		return nil, err
	}
	if pubkeyHex != "" {
		key, err := hex.DecodeString(pubkeyHex)
		if err != nil {
			return nil, fmt.Errorf("decodificar pubkey de %s: %w", n.ID, err)
		}
		n.PubKey = key
	}
	n.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &n, nil
}
