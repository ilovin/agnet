package watcher

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// HermesStateDBHistory loads all conversation events from the Hermes state.db
// for the most recently active session. Returns events, the session ID, and any error.
func HermesStateDBHistory() ([]ConversationEvent, string, error) {
	dbPath := findHermesStateDB()
	if dbPath == "" {
		return nil, "", nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return nil, "", err
	}
	defer db.Close()

	// Find the session with the most recent message (Hermes reuses the same
	// session ID across daily resets, so ORDER BY started_at is wrong).
	var sessionID string
	err = db.QueryRow(`SELECT s.id FROM sessions s JOIN messages m ON s.id=m.session_id GROUP BY s.id ORDER BY MAX(m.timestamp) DESC LIMIT 1`).Scan(&sessionID)
	if err != nil {
		return nil, "", nil
	}

	// Load all messages for that session
	rows, err := db.Query(`SELECT role, content, timestamp FROM messages WHERE session_id=? ORDER BY timestamp ASC`, sessionID)
	if err != nil {
		return nil, sessionID, err
	}
	defer rows.Close()

	var events []ConversationEvent
	for rows.Next() {
		var role, content string
		var timestamp string
		if err := rows.Scan(&role, &content, &timestamp); err != nil {
			continue
		}
		events = append(events, ConversationEvent{
			Role: role,
			Text: content,
		})
	}

	log.Printf("[HermesDB] Loaded %d events for session %s", len(events), sessionID)
	return events, sessionID, nil
}

func findHermesStateDB() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".hermes", "state.db"),
	}
	// Also check common home directories for remote users
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates,
					filepath.Join("/home", e.Name(), ".hermes", "state.db"))
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
