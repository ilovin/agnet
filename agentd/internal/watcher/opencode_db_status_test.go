package watcher

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// opencodeTestDB creates a fresh sqlite DB at tmpdir/opencode.db with the
// opencode schema (session, message, part tables) and returns its path.
//
// The opencode DB stores messages with a `data` JSON column (containing role
// and other metadata) and stores parts of each message in a separate `part`
// table — also with a JSON `data` column. This mirrors the real opencode
// schema used by OpenCodeDBWatcher.poll().
func opencodeTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, parent_id TEXT, time_updated REAL)`); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created REAL)`); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT, time_created REAL)`); err != nil {
		t.Fatalf("create part: %v", err)
	}
	return dbPath
}

func opencodeInsertMessage(t *testing.T, dbPath, msgID, sessionID, role string, timeCreated float64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	data := fmt.Sprintf(`{"role":%q}`, role)
	if _, err := db.Exec(
		`INSERT INTO message(id, session_id, data, time_created) VALUES(?, ?, ?, ?)`,
		msgID, sessionID, data, timeCreated,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

// opencodeInsertPart inserts a part row. text is used only for "text" and
// "reasoning" types; for tool-related types it's ignored.
func opencodeInsertPart(t *testing.T, dbPath, partID, messageID, partType, text string, timeCreated float64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var data string
	switch partType {
	case "text", "reasoning":
		data = fmt.Sprintf(`{"type":%q,"text":%q}`, partType, text)
	default:
		data = fmt.Sprintf(`{"type":%q}`, partType)
	}
	if _, err := db.Exec(
		`INSERT INTO part(id, message_id, data, time_created) VALUES(?, ?, ?, ?)`,
		partID, messageID, data, timeCreated,
	); err != nil {
		t.Fatalf("insert part: %v", err)
	}
}

// opencodeDeletePart removes a part by ID — used to simulate a tool-invocation
// being replaced or removed (we don't actually expect this in production but
// it's useful to exercise the false→true→false hasTool transition without
// having to build a complex DB scenario).
func opencodeDeletePart(t *testing.T, dbPath, partID string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM part WHERE id = ?`, partID); err != nil {
		t.Fatalf("delete part: %v", err)
	}
}

// newOpenCodeWatcherForTest builds a watcher directly bound to dbPath, bypassing
// the FindOpenCodeDB() filesystem probe (which would point at the user's home
// directory and is unsuitable for unit tests).
func newOpenCodeWatcherForTest(dbPath, sessionID string, cb func(ConversationEvent)) *OpenCodeDBWatcher {
	return &OpenCodeDBWatcher{
		dbPath:    dbPath,
		sessionID: sessionID,
		callback:  cb,
		stop:      make(chan struct{}),
	}
}

// ---- The streaming-status bug regression tests ----
//
// These tests exercise OpenCodeDBWatcher.poll() directly (synchronously)
// rather than going through the goroutine-driven loop, so we can assert on
// the exact event sequence produced by each poll.

// TestOpenCodeDB_StepStartOnlyEmitsWorking covers the original bug:
// when an assistant message has only a step-start part (text="") and that
// state persists across polls, the watcher must emit StatusWorking on the
// FIRST poll where the part appears (so the UI leaves idle), and must NOT
// re-emit on every subsequent unchanged poll.
//
// step-start contributes to hasTool but does NOT contribute any text, so
// last.text stays "" — the failure mode where the "text unchanged" early
// return previously hid the status change.
func TestOpenCodeDB_StepStartOnlyEmitsWorking(t *testing.T) {
	dbPath := opencodeTestDB(t)

	// User message first (so the assistant message becomes the streaming "last").
	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "hello", 1.1)

	// Assistant message with ONLY a step-start part — text remains empty.
	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "step-start", "", 2.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	// First poll: must emit StatusWorking for the assistant message
	// because hasTool transitioned false → true on a brand-new message.
	w.poll()
	events := col.snapshot()
	var sawWorking bool
	for _, e := range events {
		if e.MsgID == "a1" && e.StatusChange != nil && *e.StatusChange == StatusWorking {
			sawWorking = true
		}
	}
	if !sawWorking {
		t.Fatalf("expected StatusWorking for a1 after first poll (step-start-only message), got events=%+v", events)
	}

	// Second poll: nothing changed — text is still "" and hasTool is still true.
	// The watcher MUST NOT spam StatusWorking on every unchanged poll.
	priorLen := len(col.snapshot())
	w.poll()
	after := col.snapshot()
	if len(after) != priorLen {
		t.Fatalf("expected no new events on unchanged poll, got %d new: %+v",
			len(after)-priorLen, after[priorLen:])
	}
}

// TestOpenCodeDB_ToolInvocationAppearsMidMessage covers the case where an
// assistant message starts with text="" and no parts at all, then a
// tool-invocation part appears on the next poll. The watcher must emit
// StatusWorking when the tool-invocation appears.
func TestOpenCodeDB_ToolInvocationAppearsMidMessage(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "do work", 1.1)

	// Assistant message exists but has no parts yet (rare but possible
	// during the very first poll of a brand-new streaming message).
	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	// First poll: text="" and hasTool=false. The "no parts" first poll branch
	// (handled by the `else if last.role == "assistant"` block at end of poll)
	// will fire StatusWorking once.
	w.poll()
	firstLen := len(col.snapshot())

	// Now a tool-invocation part appears.
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "tool-invocation", "", 2.1)

	// Second poll: text still "" but hasTool transitioned false → true.
	// MUST emit a StatusWorking event for a1.
	w.poll()
	events := col.snapshot()
	if len(events) <= firstLen {
		t.Fatalf("expected new event when tool-invocation appears, got none new (total=%d)", len(events))
	}
	var sawWorkingForTool bool
	for _, e := range events[firstLen:] {
		if e.MsgID == "a1" && e.StatusChange != nil && *e.StatusChange == StatusWorking {
			sawWorkingForTool = true
		}
	}
	if !sawWorkingForTool {
		t.Fatalf("expected StatusWorking after tool-invocation appears, got new events=%+v", events[firstLen:])
	}
}

// TestOpenCodeDB_ToolFinishesThenTextAppears covers the typical streaming
// flow: tool-invocation → text emerges. The watcher should emit StatusWorking
// when tool first appears, and emit a Working event with text once text
// appears.
func TestOpenCodeDB_ToolFinishesThenTextAppears(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "go", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "tool-invocation", "", 2.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	// First poll: tool present, text="" → StatusWorking with no text.
	w.poll()
	priorLen := len(col.snapshot())

	// Text emerges.
	opencodeInsertPart(t, dbPath, "a1p2", "a1", "text", "answer", 2.2)

	w.poll()
	events := col.snapshot()
	if len(events) <= priorLen {
		t.Fatalf("expected an event when text emerges, got none new")
	}
	// Expect the latest event for a1 to carry text="answer" and StatusWorking.
	var found bool
	for _, e := range events[priorLen:] {
		if e.MsgID == "a1" && e.Text == "answer" {
			if e.StatusChange == nil || *e.StatusChange != StatusWorking {
				t.Fatalf("expected StatusWorking with text emergence, got %+v", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an event with text=answer for a1, got %+v", events[priorLen:])
	}
}

// TestOpenCodeDB_PlainTextOnly covers the baseline case: an assistant
// message with a single text part and no tool/reasoning. Must emit
// StatusWorking with text on the first poll (this is the existing
// behaviour and must not regress).
func TestOpenCodeDB_PlainTextOnly(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "hi", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "text", "hello back", 2.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()
	var ok bool
	for _, e := range events {
		if e.MsgID == "a1" && e.Text == "hello back" &&
			e.StatusChange != nil && *e.StatusChange == StatusWorking {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected StatusWorking + text for a1, got %+v", events)
	}
}

// TestOpenCodeDB_NewerMessageEmitsStandbyForCompleted covers the case where
// a newer message arrives (so the previous assistant message is now the
// "complete" message). If that completed assistant message has hasTool=false
// and text!="", the existing code emits StatusStandby for it. We keep that
// behaviour intact (regression guard).
func TestOpenCodeDB_NewerMessageEmitsStandbyForCompleted(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "hi", 1.1)

	// Completed assistant message: pure text, no tools.
	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "text", "answer", 2.1)

	// Then a newer user message arrives (so a1 becomes "non-last").
	opencodeInsertMessage(t, dbPath, "u2", "S", "user", 3.0)
	opencodeInsertPart(t, dbPath, "u2p1", "u2", "text", "next", 3.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()

	// We expect at least one event for a1 with StatusStandby (since it's
	// completed and has no tool).
	var sawStandbyForA1 bool
	for _, e := range events {
		if e.MsgID == "a1" && e.StatusChange != nil && *e.StatusChange == StatusStandby {
			sawStandbyForA1 = true
		}
	}
	if !sawStandbyForA1 {
		t.Fatalf("expected StatusStandby for completed a1, got %+v", events)
	}
}

// TestOpenCodeDB_ToolPersistsThenTextAppears mirrors the production
// scenario seen in the bug report:
//   poll 1: tool-invocation only, text=""    → emit Working
//   poll 2: same tool-invocation, text=""    → no new event (idempotent)
//   poll 3: text emerges alongside tool      → emit Working+text
func TestOpenCodeDB_ToolPersistsThenTextAppears(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "go", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "tool-invocation", "", 2.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	// poll 1: emits Working (text="" + hasTool=true on brand-new streaming msg)
	w.poll()
	beforeLen := len(col.snapshot())
	if beforeLen == 0 {
		t.Fatalf("expected at least one event after poll 1")
	}
	var sawWorking1 bool
	for _, e := range col.snapshot() {
		if e.MsgID == "a1" && e.StatusChange != nil && *e.StatusChange == StatusWorking {
			sawWorking1 = true
		}
	}
	if !sawWorking1 {
		t.Fatalf("expected StatusWorking on poll 1, got %+v", col.snapshot())
	}

	// poll 2: nothing changed → no new events
	w.poll()
	if got := len(col.snapshot()); got != beforeLen {
		t.Fatalf("poll 2 (unchanged) emitted %d new events; expected 0",
			got-beforeLen)
	}

	// poll 3: text emerges
	opencodeInsertPart(t, dbPath, "a1p2", "a1", "text", "the answer", 2.2)
	w.poll()
	after := col.snapshot()
	if len(after) <= beforeLen {
		t.Fatalf("poll 3 (text emerges) should have emitted a new event")
	}
	var sawTextEvent bool
	for _, e := range after[beforeLen:] {
		if e.MsgID == "a1" && e.Text == "the answer" &&
			e.StatusChange != nil && *e.StatusChange == StatusWorking {
			sawTextEvent = true
		}
	}
	if !sawTextEvent {
		t.Fatalf("poll 3 should emit Working+text=the answer, got new=%+v", after[beforeLen:])
	}
}
