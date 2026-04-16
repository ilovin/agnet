package watcher

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// OpenCodeDBWatcher polls the opencode SQLite database for new messages
// and emits ConversationEvents. OpenCode stores conversations in
// ~/.local/share/opencode/opencode.db (tables: message, part, session).
type OpenCodeDBWatcher struct {
	dbPath    string
	sessionID string
	callback  func(ConversationEvent)
	stop      chan struct{}
	once      sync.Once
	lastMsgID string // track last seen message ID to avoid duplicates
}

// NewOpenCodeDBWatcher creates a watcher that reads conversation data from
// the opencode SQLite database for the given session.
func NewOpenCodeDBWatcher(sessionID string, callback func(ConversationEvent)) *OpenCodeDBWatcher {
	dbPath := FindOpenCodeDB()
	return &OpenCodeDBWatcher{
		dbPath:    dbPath,
		sessionID: sessionID,
		callback:  callback,
		stop:      make(chan struct{}),
	}
}

func FindOpenCodeDB() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
		filepath.Join(home, "Library", "Application Support", "opencode", "opencode.db"),
	}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates,
					filepath.Join("/home", e.Name(), ".local", "share", "opencode", "opencode.db"))
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

func (w *OpenCodeDBWatcher) Start() error {
	if w.dbPath == "" {
		return nil // no DB found, nothing to watch
	}
	// Load existing messages first
	w.poll()
	go w.loop()
	return nil
}

func (w *OpenCodeDBWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

func (w *OpenCodeDBWatcher) loop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

// openCodeMsg is the structure of message.data JSON in opencode.db
type openCodeMsg struct {
	Role string `json:"role"`
}

// openCodePart is the structure of part.data JSON in opencode.db
type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (w *OpenCodeDBWatcher) poll() {
	// Open DB in read-only mode to avoid locking issues with opencode
	db, err := sql.Open("sqlite", w.dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return
	}
	defer db.Close()

	// Query messages newer than lastMsgID, ordered by time_created
	var rows *sql.Rows
	if w.lastMsgID == "" {
		rows, err = db.Query(`
			SELECT m.id, m.data
			FROM message m
			WHERE m.session_id = ?
			ORDER BY m.time_created ASC`,
			w.sessionID)
	} else {
		rows, err = db.Query(`
			SELECT m.id, m.data
			FROM message m
			WHERE m.session_id = ? AND m.time_created >= COALESCE(
				(SELECT time_created FROM message WHERE id = ?), 0
			)
			ORDER BY m.time_created ASC`,
			w.sessionID, w.lastMsgID)
	}
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var msgID, msgData string
		if err := rows.Scan(&msgID, &msgData); err != nil {
			continue
		}
		if msgID == w.lastMsgID {
			continue
		}

		var msg openCodeMsg
		if err := json.Unmarshal([]byte(msgData), &msg); err != nil {
			continue
		}

		// Get parts for this message
		partRows, err := db.Query(`
			SELECT data FROM part
			WHERE message_id = ?
			ORDER BY time_created ASC`,
			msgID)
		if err != nil {
			continue
		}

		var textParts []string
		var hasToolUse bool
		for partRows.Next() {
			var partData string
			if err := partRows.Scan(&partData); err != nil {
				continue
			}
			var part openCodePart
			if err := json.Unmarshal([]byte(partData), &part); err != nil {
				continue
			}
			switch part.Type {
			case "text":
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			case "tool-invocation", "tool-result":
				hasToolUse = true
			}
		}
		partRows.Close()

		w.lastMsgID = msgID

		// Emit event if there's text content
		if len(textParts) > 0 {
			text := ""
			for i, t := range textParts {
				if i > 0 {
					text += "\n"
				}
				text += t
			}

			ev := ConversationEvent{
				Role: msg.Role,
				Text: text,
			}
			if msg.Role == "assistant" && hasToolUse {
				s := StatusWorking
				ev.StatusChange = &s
			}
			w.callback(ev)
		}

		w.lastMsgID = msgID
	}
}

// OpenCodeDBHistory loads all conversation events from the opencode DB for a session.
func OpenCodeDBHistory(sessionID string) ([]ConversationEvent, error) {
	dbPath := FindOpenCodeDB()
	if dbPath == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT m.id, m.data
		FROM message m
		WHERE m.session_id = ?
		ORDER BY m.time_created ASC`,
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ConversationEvent
	for rows.Next() {
		var msgID, msgData string
		if err := rows.Scan(&msgID, &msgData); err != nil {
			continue
		}

		var msg openCodeMsg
		if err := json.Unmarshal([]byte(msgData), &msg); err != nil {
			continue
		}

		partRows, err := db.Query(`
			SELECT data FROM part
			WHERE message_id = ?
			ORDER BY time_created ASC`,
			msgID)
		if err != nil {
			continue
		}

		var textParts []string
		for partRows.Next() {
			var partData string
			if err := partRows.Scan(&partData); err != nil {
				continue
			}
			var part openCodePart
			if err := json.Unmarshal([]byte(partData), &part); err != nil {
				continue
			}
			if part.Type == "text" && part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		partRows.Close()

		if len(textParts) > 0 {
			text := ""
			for i, t := range textParts {
				if i > 0 {
					text += "\n"
				}
				text += t
			}
			events = append(events, ConversationEvent{
				Role: msg.Role,
				Text: text,
			})
		}
	}

	log.Printf("[OpenCodeDB] Loaded %d events for session %s", len(events), sessionID)
	return events, nil
}
