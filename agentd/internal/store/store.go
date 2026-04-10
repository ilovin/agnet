package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
}

// ConversationEventRecord is a persisted conversation event.
type ConversationEventRecord struct {
	AgentID   string
	Seq       uint64
	Role      string
	Text      string
	Raw       bool
	CreatedAt string
}

// Store wraps a SQLite database for agent metadata.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
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
		resume_session_id TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

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
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO agents (id, name, provider, work_dir, resume_session_id) VALUES (?,?,?,?,?)`,
		r.ID, r.Name, r.Provider, r.WorkDir, r.ResumeSessionID,
	)
	if err != nil {
		return fmt.Errorf("save agent %s: %w", r.ID, err)
	}
	return nil
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

func (s *Store) DeleteAgent(id string) error {
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
