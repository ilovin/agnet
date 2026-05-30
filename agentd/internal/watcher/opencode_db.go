package watcher

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	workDir          string
	onSwitch         func(newSessionID string)
	lastSessionCheck time.Time

	// Streaming message tracking: opencode writes parts progressively,
	// so the most recent message may receive new parts between polls.
	// We keep re-querying it until a newer message appears.
	streamingMsgID      string // msgID of the message still receiving parts
	streamingMsgText    string // last emitted text for the streaming message
	streamingMsgHasTool bool   // last observed hasTool state for the streaming message
	streamingUnchanged  int    // consecutive polls with unchanged text

	// emittedParts tracks per-part sub-events (reasoning → kind=thinking,
	// non-interactive tool → kind=tool_use) that have already been emitted,
	// keyed by the part row id. Because the streaming message is re-polled
	// every tick, this set prevents the same reasoning/tool part from being
	// re-emitted as a duplicate event on each poll. Reset on session switch
	// and conversation.clear.
	emittedParts map[string]bool
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
	w.streamingMsgHasTool = false
	w.emittedParts = nil
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
	Type   string            `json:"type"`
	Text   string            `json:"text"`
	Tool   string            `json:"tool"`
	CallID string            `json:"callID"`
	State  openCodePartState `json:"state"`
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

// opencodeToolDisplayName converts an OpenCode tool name (lowercase, e.g.
// "bash", "todowrite", "webfetch") into the PascalCase display name used in
// the "[Tool: detail]" activity text the Flutter app expects (e.g. "Bash",
// "TodoWrite", "WebFetch"). Names that already match a Claude tool keep that
// casing so the app's existing tool-icon mapping applies.
func opencodeToolDisplayName(toolName string) string {
	switch toolName {
	case "bash":
		return "Bash"
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "edit":
		return "Edit"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	case "task":
		return "Task"
	case "todowrite":
		return "TodoWrite"
	case "webfetch":
		return "WebFetch"
	case "websearch":
		return "WebSearch"
	case "background_output":
		return "BackgroundOutput"
	}
	if toolName == "" {
		return "Tool"
	}
	// Fallback: capitalise the first rune so unknown tools still render
	// with a recognisable label (e.g. "skill" → "Skill").
	return strings.ToUpper(toolName[:1]) + toolName[1:]
}

// buildOpenCodeToolSummary produces a concise human-readable detail for a
// non-interactive OpenCode tool call, mirroring the Claude-side
// BuildToolInputSummary contract ("[Tool: detail]"). OpenCode input keys
// differ from Claude's (e.g. filePath vs file_path), so this is a dedicated
// extractor. Returns "" when no useful detail can be derived; the caller then
// renders "[Tool]" without a colon.
func buildOpenCodeToolSummary(toolName string, input json.RawMessage) string {
	var params map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &params)
	}
	if params == nil {
		params = map[string]any{}
	}
	str := func(key string) string {
		if v, ok := params[key].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	clip := func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "..."
		}
		return s
	}
	switch toolName {
	case "bash":
		if cmd := str("command"); cmd != "" {
			return clip(cmd, 100)
		}
	case "read", "write", "edit":
		if p := str("filePath"); p != "" {
			return filepath.Base(p)
		}
		if p := str("file_path"); p != "" {
			return filepath.Base(p)
		}
	case "grep":
		if pat := str("pattern"); pat != "" {
			return "pattern: " + clip(pat, 60)
		}
	case "glob":
		if pat := str("pattern"); pat != "" {
			return clip(pat, 60)
		}
	case "task":
		if d := str("description"); d != "" {
			return clip(d, 80)
		}
		if p := str("prompt"); p != "" {
			return clip(p, 80)
		}
	case "webfetch", "websearch":
		if u := str("url"); u != "" {
			return clip(u, 80)
		}
		if q := str("query"); q != "" {
			return clip(q, 80)
		}
	}
	// Generic fallback: surface common single-string fields.
	for _, key := range []string{"command", "filePath", "file_path", "pattern", "url", "query", "description", "prompt"} {
		if v := str(key); v != "" {
			return clip(v, 80)
		}
	}
	return ""
}

// opencodeToolText renders the "[Tool: detail]" activity text for a
// non-interactive tool call.
func opencodeToolText(toolName string, input json.RawMessage) string {
	display := opencodeToolDisplayName(toolName)
	summary := buildOpenCodeToolSummary(toolName, input)
	if summary != "" {
		return fmt.Sprintf("[%s: %s]", display, summary)
	}
	return fmt.Sprintf("[%s]", display)
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
	//
	// subEvent is a per-part event that is surfaced to the app as its OWN
	// ConversationEvent (separate from the concatenated text event), so the
	// Flutter side can group reasoning into thinking blocks and tool calls
	// into activity blocks (mirrors the Claude flow). Plain text parts are
	// still concatenated into msgInfo.text and emitted with the message's
	// own MsgID (preserving the streaming-text update path).
	type subEvent struct {
		partID    string          // part row id — used as the dedup key + sub-event MsgID base
		kind      string          // "thinking" or "tool_use"
		text      string          // event text (reasoning text, or "[Tool: detail]")
		toolName  string          // display tool name (for kind=tool_use, e.g. "Bash")
		toolUseID string          // opencode callID
		toolInput json.RawMessage // raw tool input JSON
	}
	type msgInfo struct {
		id        string
		role      string
		text      string
		hasTool   bool
		toolName  string          // interactive tool name (e.g. "AskUserQuestion") if any
		toolUseID string          // opencode callID for the captured tool
		toolInput json.RawMessage // raw JSON of tool input
		subs      []subEvent      // reasoning / non-interactive tool sub-events
	}
	var msgs []msgInfo

	for _, h := range headers {
		var msg openCodeMsg
		if err := json.Unmarshal([]byte(h.data), &msg); err != nil {
			continue
		}

		partRows, err := db.Query(`
			SELECT id, data FROM part
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
		var subs []subEvent
		for partRows.Next() {
			var partID, partData string
			if err := partRows.Scan(&partID, &partData); err != nil {
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
				hasToolUse = true
				// Emit reasoning as its own kind=thinking event so the app
				// renders it as a thinking block rather than inlining it with
				// the assistant's chat text.
				if part.Text != "" {
					subs = append(subs, subEvent{
						partID: partID,
						kind:   "thinking",
						text:   part.Text,
					})
				}
			case "step-start", "step-finish":
				hasToolUse = true
			case "tool-invocation", "tool-result", "tool":
				hasToolUse = true
				// Interactive tool (e.g. question → AskUserQuestion): capture
				// metadata so the manager-side callback dispatches via
				// ParseInteractiveToolUse and emits a structured event
				// (kind=ask_user_question). Only the FIRST interactive tool is
				// captured. Non-interactive tools fall through to emit a
				// kind=tool_use sub-event with a "[Tool: detail]" summary.
				if mapped, ok := opencodeToolNameToInteractiveKind(part.Tool); ok {
					if capturedToolName == "" {
						capturedToolName = mapped
						capturedToolID = part.CallID
						if len(part.State.Input) > 0 {
							capturedToolInput = append(json.RawMessage(nil), part.State.Input...)
						}
					}
				} else if part.Tool != "" {
					var input json.RawMessage
					if len(part.State.Input) > 0 {
						input = append(json.RawMessage(nil), part.State.Input...)
					}
					subs = append(subs, subEvent{
						partID:    partID,
						kind:      "tool_use",
						text:      opencodeToolText(part.Tool, input),
						toolName:  opencodeToolDisplayName(part.Tool),
						toolUseID: part.CallID,
						toolInput: input,
					})
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
			subs:      subs,
		})
	}

	if len(msgs) == 0 {
		return
	}

	// emitSubEvents emits each not-yet-emitted reasoning/tool sub-event for a
	// message as its own ConversationEvent (kind=thinking / kind=tool_use),
	// then marks it emitted so re-polls of the streaming message don't
	// duplicate it. Sub-event MsgIDs are derived from the part row id so the
	// app's update-by-msgId logic keeps them distinct from the message's text
	// event and from each other. `working` controls the StatusChange attached
	// (StatusWorking while streaming, StatusStandby for completed messages —
	// matching the text-event status policy below).
	emitSubEvents := func(m msgInfo, working bool) {
		for _, s := range m.subs {
			if w.emittedParts == nil {
				w.emittedParts = map[string]bool{}
			}
			key := m.id + ":" + s.partID
			if w.emittedParts[key] {
				continue
			}
			w.emittedParts[key] = true
			ev := ConversationEvent{
				Role:         m.role,
				Text:         s.text,
				Kind:         s.kind,
				MsgID:        key,
				ToolName:     s.toolName,
				ToolUseID:    s.toolUseID,
				ToolUseInput: s.toolInput,
			}
			if m.role == "assistant" {
				st := StatusStandby
				if working {
					st = StatusWorking
				}
				ev.StatusChange = &st
			}
			w.callback(ev)
		}
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
			w.streamingMsgHasTool = false
		}
		// Emit reasoning / non-interactive tool sub-events first (these
		// precede the final assistant text in the stream). The message is
		// complete here, so attach StatusStandby for assistant sub-events.
		emitSubEvents(m, false)
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

	// hasPendingSubs reports whether a message has reasoning/tool sub-events
	// that have not yet been emitted. Used so the streaming re-poll path does
	// not early-return (treating the message as "unchanged") when a new
	// reasoning or tool part arrived without changing the concatenated text.
	hasPendingSubs := func(m msgInfo) bool {
		for _, s := range m.subs {
			key := m.id + ":" + s.partID
			if w.emittedParts == nil || !w.emittedParts[key] {
				return true
			}
		}
		return false
	}

	// Handle the last message (potentially still streaming)
	last := msgs[len(msgs)-1]

	if last.id == w.streamingMsgID {
		// Same message as last poll — check if text or tool state changed.
		// The original implementation only compared text, so a brand-new
		// tool-invocation/step-start that arrived without any text emerging
		// produced no status event and the UI stayed idle. We now also
		// detect the hasTool transition so reasoning/tool-only updates emit
		// StatusWorking even while text remains "".
		textUnchanged := last.text == w.streamingMsgText
		toolStateChanged := last.hasTool != w.streamingMsgHasTool
		pendingSubs := hasPendingSubs(last)

		if textUnchanged && !toolStateChanged && !pendingSubs {
			w.streamingUnchanged++
			if w.streamingUnchanged >= 10 && last.role == "assistant" {
				// No change for ~30s — treat as complete
				s := StatusStandby
				w.callback(ConversationEvent{StatusChange: &s})
				// Don't update lastMsgID here — the message may still receive new
				// parts later (OpenCode writes parts progressively, sometimes with
				// a large gap between message creation and text part arrival).
				// If we set lastMsgID now, subsequent polls skip this message and
				// never see the new parts.  Keep lastMsgID pointing at the last
				// fully-processed message so the next poll still includes this one.
				// w.lastMsgID = last.id
				w.streamingMsgID = ""
				w.streamingMsgText = ""
				w.streamingMsgHasTool = false
				w.streamingUnchanged = 0
			}
			return
		}

		// Either text, tool state, or sub-events changed — reset counter.
		w.streamingUnchanged = 0

		// Emit any newly-arrived reasoning / non-interactive tool sub-events
		// (still streaming → StatusWorking). emittedParts dedups re-polls.
		emitSubEvents(last, true)

		// hasTool transitioned false → true: emit StatusWorking. This is the
		// regression fix — previously suppressed when text was unchanged.
		if last.hasTool && !w.streamingMsgHasTool && last.role == "assistant" {
			s := StatusWorking
			ev := ConversationEvent{
				Role:         last.role,
				MsgID:        last.id,
				StatusChange: &s,
			}
			if last.text != "" {
				ev.Text = last.text
			}
			w.callback(ev)
		}

		// Update the cached tool state regardless of direction.
		w.streamingMsgHasTool = last.hasTool

		// Text or interactive-tool update path (separate from the
		// hasTool-only transition above). Emit when text changed OR an
		// interactive tool payload (e.g. AskUserQuestion) is now present.
		if !textUnchanged || last.toolName != "" {
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
		}
		return
	}

	// New streaming message
	w.streamingMsgID = last.id
	w.streamingMsgText = last.text
	w.streamingMsgHasTool = last.hasTool
	// Emit reasoning / non-interactive tool sub-events first (still streaming).
	emitSubEvents(last, true)
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
	w.streamingMsgHasTool = false
	w.emittedParts = nil
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
			SELECT id, data FROM part
			WHERE message_id = ?
			ORDER BY time_created ASC`,
			h.id)
		if err != nil {
			continue
		}

		var textParts []string
		// Emit reasoning + non-interactive tool parts as their own ordered
		// events (kind=thinking / kind=tool_use) so a re-attach reproduces the
		// same thinking/activity blocks the live poll path emits. Interactive
		// tools (question → AskUserQuestion) keep their ToolUseName so the
		// manager dispatches the structured interactive event.
		var capturedToolName, capturedToolID string
		var capturedToolInput json.RawMessage
		for partRows.Next() {
			var partID, partData string
			if err := partRows.Scan(&partID, &partData); err != nil {
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
					events = append(events, ConversationEvent{
						Role:  msg.Role,
						Text:  part.Text,
						Kind:  "thinking",
						MsgID: h.id + ":" + partID,
					})
				}
			case "tool-invocation", "tool-result", "tool":
				if mapped, ok := opencodeToolNameToInteractiveKind(part.Tool); ok {
					if capturedToolName == "" {
						capturedToolName = mapped
						capturedToolID = part.CallID
						if len(part.State.Input) > 0 {
							capturedToolInput = append(json.RawMessage(nil), part.State.Input...)
						}
					}
				} else if part.Tool != "" {
					var input json.RawMessage
					if len(part.State.Input) > 0 {
						input = append(json.RawMessage(nil), part.State.Input...)
					}
					events = append(events, ConversationEvent{
						Role:         msg.Role,
						Text:         opencodeToolText(part.Tool, input),
						Kind:         "tool_use",
						MsgID:        h.id + ":" + partID,
						ToolName:     opencodeToolDisplayName(part.Tool),
						ToolUseID:    part.CallID,
						ToolUseInput: input,
					})
				}
			}
		}
		partRows.Close()

		if len(textParts) > 0 || capturedToolName != "" {
			text := ""
			for i, t := range textParts {
				if i > 0 {
					text += "\n"
				}
				text += t
			}
			events = append(events, ConversationEvent{
				Role:         msg.Role,
				Text:         text,
				MsgID:        h.id,
				ToolUseName:  capturedToolName,
				ToolUseID:    capturedToolID,
				ToolUseInput: capturedToolInput,
			})
		}
	}

	log.Printf("[OpenCodeDB] Loaded %d events for session %s", len(events), sessionID)
	return events, nil
}
