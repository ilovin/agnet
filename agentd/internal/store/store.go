package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// AgentRecord is a persisted agent entry.
type AgentRecord struct {
	ID              string
	Name            string
	Provider        string
	WorkDir         string
	ResumeSessionID string
	PID             int
}

// ConversationEventRecord is a persisted conversation event.
type ConversationEventRecord struct {
	AgentID   string
	Seq       uint64
	Role      string
	Text      string
	Raw       bool
	Kind      string
	CreatedAt string
}

// Store wraps a SQLite database for agent metadata.
type Store struct {
	db *sql.DB
	mu sync.Mutex // serialize writes to avoid SQLITE_BUSY
}

// Open opens (or creates) a SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
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
		resume_session_id TEXT NOT NULL DEFAULT '',
		pid INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Add pid column to existing tables that were created before it existed.
	// ALTER TABLE ADD COLUMN is a no-op error if the column already exists,
	// so we just ignore the error.
	_, _ = s.db.Exec(`ALTER TABLE agents ADD COLUMN pid INTEGER NOT NULL DEFAULT 0`)

	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS conversation_events (
		agent_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		data_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		PRIMARY KEY (agent_id, seq)
	)`)
	if err != nil {
		return fmt.Errorf("migrate conversation_events: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveAgent(r AgentRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO agents (id, name, provider, work_dir, resume_session_id, pid) VALUES (?,?,?,?,?,?)`,
		r.ID, r.Name, r.Provider, r.WorkDir, r.ResumeSessionID, r.PID,
	)
	if err != nil {
		return fmt.Errorf("save agent %s: %w", r.ID, err)
	}
	return nil
}

func (s *Store) ListAgents() ([]AgentRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, provider, work_dir, resume_session_id, pid FROM agents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRecord
	for rows.Next() {
		var r AgentRecord
		if err := rows.Scan(&r.ID, &r.Name, &r.Provider, &r.WorkDir, &r.ResumeSessionID, &r.PID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAgentName(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE agents SET name=? WHERE id=?`, name, id)
	if err != nil {
		return fmt.Errorf("update name for agent %s: %w", id, err)
	}
	return nil
}

func (s *Store) UpdateResumeSessionID(id, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE agents SET resume_session_id=? WHERE id=?`, sessionID, id)
	if err != nil {
		return fmt.Errorf("update resume session for agent %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent %s not found", id)
	}
	return nil
}

func (s *Store) UpdateAgentPID(id string, pid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE agents SET pid=? WHERE id=?`, pid, id)
	if err != nil {
		return fmt.Errorf("update pid for agent %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent %s not found", id)
	}
	return nil
}

func (s *Store) ClearConversationEvents(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM conversation_events WHERE agent_id=?`, agentID)
	if err != nil {
		return fmt.Errorf("clear conversation events for agent %s: %w", agentID, err)
	}
	return nil
}

func (s *Store) UpdateAgentProvider(id, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE agents SET provider=? WHERE id=?`, provider, id)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func (s *Store) DeleteAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM agents WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete agent %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent %s not found", id)
	}
	return nil
}

func (s *Store) SaveConversationEvent(agentID string, seq uint64, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agentID == "" {
		return fmt.Errorf("agent id is required")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO conversation_events (agent_id, seq, data_json, created_at) VALUES (?,?,?,?)`,
		agentID,
		seq,
		string(payload),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save conversation event agent=%s seq=%d: %w", agentID, seq, err)
	}
	return nil
}

func (s *Store) ListConversationEventsSince(agentID string, afterSeq uint64, limit int) ([]ConversationEventRecord, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.db.Query(
		`SELECT agent_id, seq, data_json, created_at
		 FROM conversation_events
		 WHERE agent_id=? AND seq>?
		 ORDER BY seq ASC
		 LIMIT ?`,
		agentID,
		afterSeq,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list conversation events: %w", err)
	}
	defer rows.Close()

	out := make([]ConversationEventRecord, 0)
	for rows.Next() {
		var r ConversationEventRecord
		var dataJSON string
		if err := rows.Scan(&r.AgentID, &r.Seq, &dataJSON, &r.CreatedAt); err != nil {
			return nil, err
		}
		var data map[string]any
		_ = json.Unmarshal([]byte(dataJSON), &data)
		r.Role, _ = data["role"].(string)
		r.Text, _ = data["text"].(string)
		r.Raw, _ = data["raw"].(bool)
		r.Kind, _ = data["kind"].(string)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListConversationEventsBefore(agentID string, beforeSeq uint64, limit int) ([]ConversationEventRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT agent_id, seq, data_json, created_at
		 FROM conversation_events
		 WHERE agent_id=? AND seq<?
		 ORDER BY seq DESC
		 LIMIT ?`,
		agentID,
		beforeSeq,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list conversation events before: %w", err)
	}
	defer rows.Close()

	tmp := make([]ConversationEventRecord, 0)
	for rows.Next() {
		var r ConversationEventRecord
		var dataJSON string
		if err := rows.Scan(&r.AgentID, &r.Seq, &dataJSON, &r.CreatedAt); err != nil {
			return nil, err
		}
		var data map[string]any
		_ = json.Unmarshal([]byte(dataJSON), &data)
		r.Role, _ = data["role"].(string)
		r.Text, _ = data["text"].(string)
		r.Raw, _ = data["raw"].(bool)
		r.Kind, _ = data["kind"].(string)
		tmp = append(tmp, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ConversationEventRecord, 0, len(tmp))
	for i := len(tmp) - 1; i >= 0; i-- {
		out = append(out, tmp[i])
	}
	return out, nil
}

func (s *Store) ListConversationEventsLatest(agentID string, limit int) ([]ConversationEventRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT agent_id, seq, data_json, created_at
		 FROM conversation_events
		 WHERE agent_id=?
		 ORDER BY seq DESC
		 LIMIT ?`,
		agentID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list latest conversation events: %w", err)
	}
	defer rows.Close()

	tmp := make([]ConversationEventRecord, 0)
	for rows.Next() {
		var r ConversationEventRecord
		var dataJSON string
		if err := rows.Scan(&r.AgentID, &r.Seq, &dataJSON, &r.CreatedAt); err != nil {
			return nil, err
		}
		var data map[string]any
		_ = json.Unmarshal([]byte(dataJSON), &data)
		r.Role, _ = data["role"].(string)
		r.Text, _ = data["text"].(string)
		r.Raw, _ = data["raw"].(bool)
		r.Kind, _ = data["kind"].(string)
		tmp = append(tmp, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ConversationEventRecord, 0, len(tmp))
	for i := len(tmp) - 1; i >= 0; i-- {
		out = append(out, tmp[i])
	}
	return out, nil
}

func (s *Store) LastConversationSeq(agentID string) (uint64, error) {
	var last sql.NullInt64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM conversation_events WHERE agent_id=?`, agentID).Scan(&last); err != nil {
		return 0, fmt.Errorf("last conversation seq: %w", err)
	}
	if !last.Valid || last.Int64 < 0 {
		return 0, nil
	}
	return uint64(last.Int64), nil
}
