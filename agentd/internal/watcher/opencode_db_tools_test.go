package watcher

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// opencodeToolsTestDB creates a fresh sqlite DB at tmpdir/opencode.db with the
// opencode schema (session + message + part tables) and returns its path.
//
// Uses a separate helper from opencodeTestDB (in opencode_db_status_test.go)
// because these tool tests rely on full part rows with session_id+time_updated
// columns to exercise the JSON-payload extraction path.
func opencodeToolsTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE session (
		id TEXT PRIMARY KEY,
		directory TEXT,
		parent_id TEXT,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL,
		data TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE message (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL,
		data TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE part (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL,
		data TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create part: %v", err)
	}
	return dbPath
}

func opencodeToolsInsertMessage(t *testing.T, dbPath, msgID, sessionID, role string, ts int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	data := `{"role":"` + role + `"}`
	if _, err := db.Exec(`INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES(?, ?, ?, ?, ?)`,
		msgID, sessionID, ts, ts, data); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

func opencodeToolsInsertPart(t *testing.T, dbPath, partID, msgID, sessionID, partData string, ts int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES(?, ?, ?, ?, ?, ?)`,
		partID, msgID, sessionID, ts, ts, partData); err != nil {
		t.Fatalf("insert part: %v", err)
	}
}

// newTestOpenCodeWatcher constructs an OpenCodeDBWatcher pointed at an
// arbitrary dbPath (bypasses FindOpenCodeDB).
func newTestOpenCodeWatcher(dbPath, sessionID string, callback func(ConversationEvent)) *OpenCodeDBWatcher {
	return &OpenCodeDBWatcher{
		dbPath:    dbPath,
		sessionID: sessionID,
		callback:  callback,
		stop:      make(chan struct{}),
	}
}

// pollOnce drives a single poll() call without spinning the goroutine loop.
// Used to keep tests deterministic.
func (w *OpenCodeDBWatcher) pollOnce() {
	w.poll()
}

// drainEvents collects callback events.
type opencodeEventCollector struct {
	mu     sync.Mutex
	events []ConversationEvent
}

func (c *opencodeEventCollector) callback(e ConversationEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *opencodeEventCollector) snapshot() []ConversationEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ConversationEvent, len(c.events))
	copy(out, c.events)
	return out
}

// findToolUseEvent returns the first emitted event whose ToolUseName is set.
func findToolUseEvent(events []ConversationEvent) (ConversationEvent, bool) {
	for _, e := range events {
		if e.ToolUseName != "" {
			return e, true
		}
	}
	return ConversationEvent{}, false
}

// ---- Test 1: AskUserQuestion (opencode `question` tool) ----

func TestOpenCodeDBWatcher_QuestionToolEmitsToolUseFields(t *testing.T) {
	dbPath := opencodeToolsTestDB(t)
	const sessionID = "ses_test_question"
	const msgID = "msg_q1"

	// First (older) message so the watcher seeds lastMsgID and treats msg_q1 as new.
	opencodeToolsInsertMessage(t, dbPath, "msg_seed", sessionID, "user", 1000)
	opencodeToolsInsertPart(t, dbPath, "part_seed", "msg_seed", sessionID,
		`{"type":"text","text":"hi"}`, 1000)

	// Assistant message with a question tool part.
	opencodeToolsInsertMessage(t, dbPath, msgID, sessionID, "assistant", 2000)
	questionPart := `{"type":"tool","tool":"question","callID":"tool_abc123","state":{"status":"running","input":{"questions":[{"question":"Pick a color","header":"Color","multi_select":false,"options":[{"label":"Red","description":"warm"},{"label":"Blue","description":"cool"}]}]}}}`
	opencodeToolsInsertPart(t, dbPath, "part_q1", msgID, sessionID, questionPart, 2001)

	col := &opencodeEventCollector{}
	w := newTestOpenCodeWatcher(dbPath, sessionID, col.callback)
	// Seed lastMsgID to the seed message, like Start() would.
	w.lastMsgID = "msg_seed"

	// Need a second message to push the assistant message out of the streaming slot
	// so it's processed in the "complete" phase. Insert an even newer message.
	opencodeToolsInsertMessage(t, dbPath, "msg_after", sessionID, "user", 3000)
	opencodeToolsInsertPart(t, dbPath, "part_after", "msg_after", sessionID,
		`{"type":"text","text":"after"}`, 3001)

	w.pollOnce()

	events := col.snapshot()
	tu, ok := findToolUseEvent(events)
	if !ok {
		t.Fatalf("expected at least one event with ToolUseName populated, got %d events", len(events))
	}
	if tu.ToolUseName != "AskUserQuestion" {
		t.Errorf("ToolUseName = %q, want AskUserQuestion", tu.ToolUseName)
	}
	if tu.ToolUseID != "tool_abc123" {
		t.Errorf("ToolUseID = %q, want tool_abc123", tu.ToolUseID)
	}
	if len(tu.ToolUseInput) == 0 {
		t.Fatalf("ToolUseInput is empty")
	}
	// Verify the input is a JSON object containing questions.
	if !containsAll(string(tu.ToolUseInput), []string{"questions", "Pick a color", "Red", "Blue"}) {
		t.Errorf("ToolUseInput missing expected fields: %s", string(tu.ToolUseInput))
	}
}

// ---- Test 2: ExitPlanMode (no native opencode equivalent — verify regression that other tools don't accidentally map) ----

func TestOpenCodeDBWatcher_RegularToolDoesNotPopulateInteractiveFields(t *testing.T) {
	dbPath := opencodeToolsTestDB(t)
	const sessionID = "ses_test_regular"

	opencodeToolsInsertMessage(t, dbPath, "msg_seed", sessionID, "user", 1000)
	opencodeToolsInsertPart(t, dbPath, "part_seed", "msg_seed", sessionID,
		`{"type":"text","text":"hi"}`, 1000)

	opencodeToolsInsertMessage(t, dbPath, "msg_bash", sessionID, "assistant", 2000)
	bashPart := `{"type":"tool","tool":"bash","callID":"tool_bash1","state":{"status":"completed","input":{"command":"ls -la"},"output":"file1\nfile2"}}`
	opencodeToolsInsertPart(t, dbPath, "part_bash", "msg_bash", sessionID, bashPart, 2001)

	col := &opencodeEventCollector{}
	w := newTestOpenCodeWatcher(dbPath, sessionID, col.callback)
	w.lastMsgID = "msg_seed"

	opencodeToolsInsertMessage(t, dbPath, "msg_after", sessionID, "user", 3000)
	opencodeToolsInsertPart(t, dbPath, "part_after", "msg_after", sessionID,
		`{"type":"text","text":"after"}`, 3001)

	w.pollOnce()

	events := col.snapshot()
	if _, ok := findToolUseEvent(events); ok {
		t.Errorf("regular bash tool should NOT populate ToolUseName (only interactive ones do): %+v", events)
	}
}

// ---- Test 3: Malformed tool input — graceful fallback (no panic, no crash) ----

func TestOpenCodeDBWatcher_MalformedQuestionInputDoesNotCrash(t *testing.T) {
	dbPath := opencodeToolsTestDB(t)
	const sessionID = "ses_test_malformed"

	opencodeToolsInsertMessage(t, dbPath, "msg_seed", sessionID, "user", 1000)
	opencodeToolsInsertPart(t, dbPath, "part_seed", "msg_seed", sessionID,
		`{"type":"text","text":"hi"}`, 1000)

	// Garbled JSON in the part data — the entire part should be skipped without panic.
	opencodeToolsInsertMessage(t, dbPath, "msg_bad", sessionID, "assistant", 2000)
	opencodeToolsInsertPart(t, dbPath, "part_bad", "msg_bad", sessionID,
		`{"type":"tool","tool":"question","callID":"x","state":{`, 2001)

	col := &opencodeEventCollector{}
	w := newTestOpenCodeWatcher(dbPath, sessionID, col.callback)
	w.lastMsgID = "msg_seed"

	opencodeToolsInsertMessage(t, dbPath, "msg_after", sessionID, "user", 3000)
	opencodeToolsInsertPart(t, dbPath, "part_after", "msg_after", sessionID,
		`{"type":"text","text":"after"}`, 3001)

	// must not panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("poll panicked on malformed part: %v", r)
		}
	}()
	w.pollOnce()
	// Allow the poll to run to completion; nothing else to assert beyond non-panic.
	_ = time.Now()
}

// ---- Test 4: question tool with status=completed still populates fields (replay path) ----

func TestOpenCodeDBWatcher_CompletedQuestionAlsoPopulates(t *testing.T) {
	dbPath := opencodeToolsTestDB(t)
	const sessionID = "ses_test_completed_q"

	opencodeToolsInsertMessage(t, dbPath, "msg_seed", sessionID, "user", 1000)
	opencodeToolsInsertPart(t, dbPath, "part_seed", "msg_seed", sessionID,
		`{"type":"text","text":"hi"}`, 1000)

	opencodeToolsInsertMessage(t, dbPath, "msg_q", sessionID, "assistant", 2000)
	completedQ := `{"type":"tool","tool":"question","callID":"tool_done","state":{"status":"completed","input":{"questions":[{"question":"Q?","options":[{"label":"A"},{"label":"B"}]}]},"output":"answered"}}`
	opencodeToolsInsertPart(t, dbPath, "part_q", "msg_q", sessionID, completedQ, 2001)

	col := &opencodeEventCollector{}
	w := newTestOpenCodeWatcher(dbPath, sessionID, col.callback)
	w.lastMsgID = "msg_seed"

	opencodeToolsInsertMessage(t, dbPath, "msg_after", sessionID, "user", 3000)
	opencodeToolsInsertPart(t, dbPath, "part_after", "msg_after", sessionID,
		`{"type":"text","text":"after"}`, 3001)

	w.pollOnce()

	events := col.snapshot()
	tu, ok := findToolUseEvent(events)
	if !ok {
		t.Fatalf("expected ToolUseName populated for completed question, got %d events", len(events))
	}
	if tu.ToolUseName != "AskUserQuestion" || tu.ToolUseID != "tool_done" {
		t.Errorf("unexpected fields: name=%q id=%q", tu.ToolUseName, tu.ToolUseID)
	}
}

// containsAll returns true if all substrings appear in s.
func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !stringContains(s, sub) {
			return false
		}
	}
	return true
}

func stringContains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
