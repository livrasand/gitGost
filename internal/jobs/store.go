package jobs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL DEFAULT '',
    args TEXT NOT NULL DEFAULT '[]',
    state TEXT NOT NULL DEFAULT 'queued',
    progress TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    pid INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);`

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("crear esquema de jobs: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Create(j *Job) (int64, error) {
	now := time.Now().UTC()
	j.State = StateQueued
	j.CreatedAt = now
	j.UpdatedAt = now
	argsJSON, err := json.Marshal(j.Args)
	if err != nil {
		return 0, fmt.Errorf("serializar args: %w", err)
	}
	res, err := s.db.Exec(
		`INSERT INTO jobs (operation, url, origin, target, cwd, args, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Operation, j.URL, j.Origin, j.Target, j.CWD, string(argsJSON), j.State,
		j.CreatedAt.Format(time.RFC3339Nano), j.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Get(id int64) (*Job, error) {
	row := s.db.QueryRow(
		`SELECT id, operation, url, origin, target, cwd, args, state, progress, error, pid, created_at, updated_at
		 FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

func (s *Store) List(limit int) ([]Job, error) {
	rows, err := s.db.Query(
		`SELECT id, operation, url, origin, target, cwd, args, state, progress, error, pid, created_at, updated_at
		 FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) SetState(id int64, state string) error {
	return s.touch(id, "state", state)
}

func (s *Store) SetProgress(id int64, progress string) error {
	return s.touch(id, "progress", progress)
}

func (s *Store) SetError(id int64, errMsg string) error {
	return s.touch(id, "error", errMsg)
}

func (s *Store) SetPID(id int64, pid int) error {
	if _, err := s.db.Exec(`UPDATE jobs SET pid = ?, updated_at = ? WHERE id = ?`,
		pid, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return err
	}
	return nil
}

func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	return err
}

func (s *Store) touch(id int64, column, value string) error {
	_, err := s.db.Exec(
		fmt.Sprintf(`UPDATE jobs SET %s = ?, updated_at = ? WHERE id = ?`, column),
		value, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*Job, error) {
	var (
		j        Job
		argsJSON string
		created  string
		updated  string
	)
	if err := row.Scan(&j.ID, &j.Operation, &j.URL, &j.Origin, &j.Target, &j.CWD,
		&argsJSON, &j.State, &j.Progress, &j.Error, &j.PID, &created, &updated); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(argsJSON), &j.Args); err != nil {
		return nil, fmt.Errorf("deserializar args: %w", err)
	}
	j.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	j.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &j, nil
}
