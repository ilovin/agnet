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
	lastMsgID string  // track last fully processed message ID
	db        *sql.DB // persistent connection
	dbMu      sync.Mutex

	workDir         string
	onSwitch        func(newSessionID string)
	lastSessionCheck time.Time

	// Streaming message tracking: opencode writes parts progressively,
	// so the most recent message may receive new parts between polls.
	// We keep re-querying it until a newer message appears.
	streamingMsgID      string // msgID of the message still receiving parts
	streamingMsgText    string // last emitted text for the streaming message
	streamingUnchanged  int    // consecutive polls with unchanged text
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

func (w *OpenCodeDBWatcher) getDB() (*sql.DB, error) {
	w.dbMu.Lock()
	defer w.dbMu.Unlock()
	if w.db != nil {
		return w.db, nil
	}
	db, err := sql.Open("sqlite", w.dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	w.db = db
	return w.db, nil
}

func (w *OpenCodeDBWatcher) Start() error {
	if w.dbPath == "" {
		return nil
	}
	// Seed lastMsgID from the DB so we don't re-process historical messages
	// when the watcher is restarted for an existing agent.
	if db, err := w.getDB(); err == nil {
		var lastID string
		if err := db.QueryRow(
			`SELECT id FROM message WHERE session_id = ? ORDER BY time_created DESC LIMIT 1`,
			w.sessionID,
		).Scan(&lastID); err == nil && lastID != "" {
			w.lastMsgID = lastID
		}
	}
	go w.loop()
	return nil
}

func (w *OpenCodeDBWatcher) Stop() {
	w.once.Do(func() {
		close(w.stop)
		w.dbMu.Lock()
		if w.db != nil {
			w.db.Close()
			w.db = nil
		}
		w.dbMu.Unlock()
	})
}

// SetSkipExisting is a no-op for OpenCodeDBWatcher; it already seeds lastMsgID
// in Start() to avoid re-processing historical messages.
func (w *OpenCodeDBWatcher) SetSkipExisting(bool) {}

// ResetOffset resets the message tracking so historical messages are not re-read
// after a conversation.clear.
func (w *OpenCodeDBWatcher) ResetOffset() {
	w.streamingMsgID = ""
	w.streamingMsgText = ""
	// Seed lastMsgID to the latest message so we don't re-process history after clear
	if w.dbPath != "" {
		if db, err := w.getDB(); err == nil {
			var lastID string
			if err := db.QueryRow(
				`SELECT id FROM message WHERE session_id = ? ORDER BY time_created DESC LIMIT 1`,
				w.sessionID,
			).Scan(&lastID); err == nil && lastID != "" {
				w.lastMsgID = lastID
			}
		}
	}
}

func (w *OpenCodeDBWatcher) SetWorkDir(dir string) {
	w.workDir = dir
}

func (w *OpenCodeDBWatcher) OnSessionSwitch(fn func(newSessionID string)) {
	w.onSwitch = fn
}

func (w *OpenCodeDBWatcher) loop() {
	ticker := time.NewTicker(3 * time.Second)
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

// openCodePart is the structure of part.data JSON in opencode.db.
//
// OpenCode tool parts have shape:
//
//	{
//	  "type": "tool",
//	  "tool": "<tool name>",        // e.g. "bash", "question", "edit"
//	  "callID": "<tool call id>",
//	  "state": {
//	    "status": "running" | "completed" | "error",
//	    "input": { ... }              // tool-specific args
//	    "output": "...",              // when completed
//	    ...
//	  }
//	}
type openCodePart struct {
	Type   string             `json:"type"`
	Text   string             `json:"text"`
	Tool   string             `json:"tool"`
	CallID string             `json:"callID"`
	State  openCodePartState  `json:"state"`
}

type openCodePartState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
}

// opencodeToolNameToInteractiveKind maps an OpenCode tool name (lowercase) to
// the canonical interactive tool name understood by ParseInteractiveToolUse.
// Returns ("", false) for non-interactive tools — the caller then leaves the
// ConversationEvent.ToolUseName unset and the event renders as plain text.
//
// OpenCode's "question" tool is the equivalent of Claude Code's
// "AskUserQuestion" — same input shape (questions[].question/header/options).
// OpenCode does not currently have an "ExitPlanMode" equivalent.
func opencodeToolNameToInteractiveKind(toolName string) (string, bool) {
	switch toolName {
	case "question":
		return "AskUserQuestion", true
	default:
		return "", false
	}
}

func (w *OpenCodeDBWatcher) poll() {
	w.refreshSession()

	db, err := w.getDB()
	if err != nil {
		return
	}

	// Determine the earliest message to include in the query.
	// We must include the streaming message (if any) so we can re-check its parts.
	effectiveLastID := w.lastMsgID
	if w.streamingMsgID != "" {
		effectiveLastID = w.streamingMsgID
	}

	// Phase 1: Collect message IDs and metadata (close rows before querying parts
	// to avoid deadlocking with MaxOpenConns=1).
	type msgHeader struct {
		id   string
		data string
	}
	var headers []msgHeader

	var rows *sql.Rows
	if effectiveLastID == "" {
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
			w.sessionID, effectiveLastID)
	}
	if err != nil {
		return
	}
	for rows.Next() {
		var msgID, msgData string
		if err := rows.Scan(&msgID, &msgData); err != nil {
			continue
		}
		// Skip messages we've already fully processed (not the streaming one)
		if msgID == w.lastMsgID && msgID != w.streamingMsgID {
			continue
		}
		headers = append(headers, msgHeader{id: msgID, data: msgData})
	}
	rows.Close()

	// Phase 2: Query parts for each message.
	type msgInfo struct {
		id         string
		role       string
		text       string
		hasTool    bool
		toolName   string          // interactive tool name (e.g. "AskUserQuestion") if any
		toolUseID  string          // opencode callID for the captured tool
		toolInput  json.RawMessage // raw JSON of tool input
	}
	var msgs []msgInfo

	for _, h := range headers {
		var msg openCodeMsg
		if err := json.Unmarshal([]byte(h.data), &msg); err != nil {
			continue
		}

		partRows, err := db.Query(`
			SELECT data FROM part
			WHERE message_id = ?
			ORDER BY time_created ASC`,
			h.id)
		if err != nil {
			continue
		}

		var textParts []string
		var hasToolUse bool
		var capturedToolName, capturedToolID string
		var capturedToolInput json.RawMessage
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
			case "reasoning":
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
				hasToolUse = true
			case "step-start", "step-finish":
				hasToolUse = true
			case "tool-invocation", "tool-result", "tool":
				hasToolUse = true
				// Capture interactive tool metadata so the manager-side
				// callback (makeWatcherCallback) can dispatch via
				// ParseInteractiveToolUse and emit a structured event
				// (kind=ask_user_question / exit_plan_mode) instead of a
				// plain text bubble. Mirrors the Claude jsonl path.
				// Only the FIRST interactive tool in the message is captured.
				if capturedToolName == "" {
					if mapped, ok := opencodeToolNameToInteractiveKind(part.Tool); ok {
						capturedToolName = mapped
						capturedToolID = part.CallID
						if len(part.State.Input) > 0 {
							capturedToolInput = append(json.RawMessage(nil), part.State.Input...)
						}
					}
				}
			}
		}
		partRows.Close()

		text := ""
		for i, t := range textParts {
			if i > 0 {
				text += "\n"
			}
			text += t
		}
		msgs = append(msgs, msgInfo{
			id:        h.id,
			role:      msg.Role,
			text:      text,
			hasTool:   hasToolUse,
			toolName:  capturedToolName,
			toolUseID: capturedToolID,
			toolInput: capturedToolInput,
		})
	}

	if len(msgs) == 0 {
		return
	}

	// Process all but the last message normally (they are complete).
	// The last message might still be streaming, so we keep it for re-querying.
	for i := 0; i < len(msgs)-1; i++ {
		m := msgs[i]
		w.lastMsgID = m.id
		// Clear streaming tracking if this was the streaming message and a newer one exists
		if m.id == w.streamingMsgID {
			w.streamingMsgID = ""
			w.streamingMsgText = ""
		}
		// Emit when there is visible text OR an interactive tool payload to dispatch.
		if m.text != "" || m.toolName != "" {
			ev := ConversationEvent{
				Role:         m.role,
				Text:         m.text,
				MsgID:        m.id,
				ToolUseName:  m.toolName,
				ToolUseID:    m.toolUseID,
				ToolUseInput: m.toolInput,
			}
			if m.role == "assistant" {
				if m.hasTool {
					s := StatusWorking
					ev.StatusChange = &s
				} else {
					s := StatusStandby
					ev.StatusChange = &s
				}
			}
			w.callback(ev)
		}
	}

	// Handle the last message (potentially still streaming)
	last := msgs[len(msgs)-1]

	if last.id == w.streamingMsgID {
		// Same message as last poll — check if text changed
		if last.text == w.streamingMsgText {
			w.streamingUnchanged++
			if w.streamingUnchanged >= 10 && last.role == "assistant" {
				// No text change for ~30s — treat as complete
				s := StatusStandby
				w.callback(ConversationEvent{StatusChange: &s})
				w.lastMsgID = last.id
				w.streamingMsgID = ""
				w.streamingMsgText = ""
				w.streamingUnchanged = 0
			}
			return
		}
		// Text updated — emit update event
		w.streamingUnchanged = 0
		w.streamingMsgText = last.text
		if last.text != "" || last.toolName != "" {
			ev := ConversationEvent{
				Role:         last.role,
				Text:         last.text,
				MsgID:        last.id,
				ToolUseName:  last.toolName,
				ToolUseID:    last.toolUseID,
				ToolUseInput: last.toolInput,
			}
			if last.role == "assistant" {
				s := StatusWorking
				ev.StatusChange = &s
			}
			w.callback(ev)
		}
		return
	}

	// New streaming message
	w.streamingMsgID = last.id
	w.streamingMsgText = last.text
	if last.text != "" || last.toolName != "" {
		ev := ConversationEvent{
			Role:         last.role,
			Text:         last.text,
			MsgID:        last.id,
			ToolUseName:  last.toolName,
			ToolUseID:    last.toolUseID,
			ToolUseInput: last.toolInput,
		}
		if last.role == "assistant" {
			s := StatusWorking
			ev.StatusChange = &s
		}
		w.callback(ev)
	} else if last.role == "assistant" {
		s := StatusWorking
		w.callback(ConversationEvent{
			Role:         last.role,
			MsgID:        last.id,
			StatusChange: &s,
		})
	}
}

func (w *OpenCodeDBWatcher) refreshSession() {
	if w.workDir == "" || w.onSwitch == nil {
		return
	}
	if time.Since(w.lastSessionCheck) < 5*time.Second {
		return
	}
	w.lastSessionCheck = time.Now()

	db, err := w.getDB()
	if err != nil {
		return
	}

	var latestSessionID string
	err = db.QueryRow(`
		SELECT id FROM session
		WHERE directory = ? AND parent_id IS NULL
		ORDER BY time_updated DESC
		LIMIT 1`, w.workDir).Scan(&latestSessionID)
	if err != nil || latestSessionID == "" || latestSessionID == w.sessionID {
		return
	}

	log.Printf("[OpenCodeDB] Session switched from %s to %s for workDir %s", w.sessionID, latestSessionID, w.workDir)
	w.sessionID = latestSessionID
	w.lastMsgID = ""
	w.streamingMsgID = ""
	w.streamingMsgText = ""
	w.onSwitch(latestSessionID)
}

// OpenCodeDBHistory loads all conversation events from the opencode DB for a session.
func OpenCodeDBHistory(sessionID string) ([]ConversationEvent, error) {
	dbPath := FindOpenCodeDB()
	if dbPath == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Phase 1: Collect message headers (close rows before querying parts
	// to avoid deadlocking with nested queries on a single connection).
	type msgHeader struct {
		id   string
		data string
	}
	rows, err := db.Query(`
		SELECT m.id, m.data
		FROM message m
		WHERE m.session_id = ?
		ORDER BY m.time_created ASC`,
		sessionID)
	if err != nil {
		return nil, err
	}
	var headers []msgHeader
	for rows.Next() {
		var msgID, msgData string
		if err := rows.Scan(&msgID, &msgData); err != nil {
			continue
		}
		headers = append(headers, msgHeader{id: msgID, data: msgData})
	}
	rows.Close()

	// Phase 2: Query parts for each message.
	var events []ConversationEvent
	for _, h := range headers {
		var msg openCodeMsg
		if err := json.Unmarshal([]byte(h.data), &msg); err != nil {
			continue
		}

		partRows, err := db.Query(`
			SELECT data FROM part
			WHERE message_id = ?
			ORDER BY time_created ASC`,
			h.id)
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
				Role:  msg.Role,
				Text:  text,
				MsgID: h.id,
			})
		}
	}

	log.Printf("[OpenCodeDB] Loaded %d events for session %s", len(events), sessionID)
	return events, nil
}
