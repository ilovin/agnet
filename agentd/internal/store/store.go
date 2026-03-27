package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// AgentRecord is a persisted agent entry.
type AgentRecord struct {
	ID              string
	Name            string
	Provider        string
	WorkDir         string
	ResumeSessionID string
}

// Store wraps a SQLite database for agent metadata.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agents (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		provider TEXT NOT NULL,
		work_dir TEXT NOT NULL,
		resume_session_id TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveAgent(r AgentRecord) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO agents (id, name, provider, work_dir, resume_session_id) VALUES (?,?,?,?,?)`,
		r.ID, r.Name, r.Provider, r.WorkDir, r.ResumeSessionID,
	)
	return err
}

func (s *Store) ListAgents() ([]AgentRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, provider, work_dir, resume_session_id FROM agents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRecord
	for rows.Next() {
		var r AgentRecord
		if err := rows.Scan(&r.ID, &r.Name, &r.Provider, &r.WorkDir, &r.ResumeSessionID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateResumeSessionID(id, sessionID string) error {
	_, err := s.db.Exec(`UPDATE agents SET resume_session_id=? WHERE id=?`, sessionID, id)
	return err
}

func (s *Store) DeleteAgent(id string) error {
	_, err := s.db.Exec(`DELETE FROM agents WHERE id=?`, id)
	return err
}
