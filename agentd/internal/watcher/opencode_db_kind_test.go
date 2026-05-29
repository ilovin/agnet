package watcher

import (
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

// Tests for #77/#75: opencode reasoning + non-interactive tool calls
// must surface as separate ConversationEvents with Kind populated, so
// the Flutter app can route them to the thinking / activity buckets
// (mirrors the Claude flow which already emits kind=thinking and
// kind=tool_use as distinct events).

// opencodeInsertPartRaw inserts a part with raw JSON data — needed for
// non-text/reasoning types where we need to specify tool/callID/state.
func opencodeInsertPartRaw(t *testing.T, dbPath, partID, msgID, data string, ts float64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO part(id, message_id, data, time_created) VALUES(?, ?, ?, ?)`,
		partID, msgID, data, ts,
	); err != nil {
		t.Fatalf("insert part raw: %v", err)
	}
}

func findKindEvent(events []ConversationEvent, kind string) (ConversationEvent, bool) {
	for _, e := range events {
		if e.Kind == kind {
			return e, true
		}
	}
	return ConversationEvent{}, false
}

func countKindEvents(events []ConversationEvent, kind string) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// ---- 1. reasoning-only part → kind=thinking event ----
func TestOpenCodeDB_ReasoningEmitsKindThinking(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "go", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "reasoning", "Let me think about this...", 2.1)

	// Newer message so a1 lands in the "complete" phase (so we get the full
	// final-state emit deterministically).
	opencodeInsertMessage(t, dbPath, "u2", "S", "user", 3.0)
	opencodeInsertPart(t, dbPath, "u2p1", "u2", "text", "next", 3.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()

	ev, ok := findKindEvent(events, "thinking")
	if !ok {
		t.Fatalf("expected a kind=thinking event, got %d events: %+v", len(events), events)
	}
	if ev.Text != "Let me think about this..." {
		t.Errorf("thinking event text = %q, want reasoning text", ev.Text)
	}
	if ev.Role != "assistant" {
		t.Errorf("thinking event role = %q, want assistant", ev.Role)
	}
}

// ---- 2. non-interactive tool (bash) → kind=tool_use with summary ----
func TestOpenCodeDB_BashToolEmitsKindToolUse(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "run ls", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	bashPart := `{"type":"tool","tool":"bash","callID":"call_1","state":{"status":"completed","input":{"command":"ls -la"},"output":"ok"}}`
	opencodeInsertPartRaw(t, dbPath, "a1p1", "a1", bashPart, 2.1)

	opencodeInsertMessage(t, dbPath, "u2", "S", "user", 3.0)
	opencodeInsertPart(t, dbPath, "u2p1", "u2", "text", "next", 3.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()

	ev, ok := findKindEvent(events, "tool_use")
	if !ok {
		t.Fatalf("expected a kind=tool_use event for bash, got events: %+v", events)
	}
	// Text should be "[Bash: ls -la]" (or similar tool summary). We match
	// loosely to allow for capitalisation policy decisions.
	if !stringContains(ev.Text, "ls -la") {
		t.Errorf("tool_use event text = %q, want it to contain command 'ls -la'", ev.Text)
	}
	if !stringContains(ev.Text, "Bash") && !stringContains(ev.Text, "bash") {
		t.Errorf("tool_use event text = %q, want it to contain 'Bash'", ev.Text)
	}
}

// ---- 3. interactive tool (question) → still emits ToolUseName + no Kind ----
// (regression for #74 — the interactive tool path must not now leak kind=tool_use)
func TestOpenCodeDB_QuestionToolKeepsInteractivePath(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "ask me", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	qPart := `{"type":"tool","tool":"question","callID":"call_q","state":{"status":"running","input":{"questions":[{"question":"Pick","options":[{"label":"A"},{"label":"B"}]}]}}}`
	opencodeInsertPartRaw(t, dbPath, "a1p1", "a1", qPart, 2.1)

	opencodeInsertMessage(t, dbPath, "u2", "S", "user", 3.0)
	opencodeInsertPart(t, dbPath, "u2p1", "u2", "text", "next", 3.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()

	// Must find an event with ToolUseName = AskUserQuestion
	var found bool
	for _, e := range events {
		if e.ToolUseName == "AskUserQuestion" {
			found = true
			if e.Kind == "tool_use" {
				t.Errorf("interactive question must NOT carry kind=tool_use (manager.go dispatches via ParseInteractiveToolUse), got: %+v", e)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected ToolUseName=AskUserQuestion event preserved (regression for #74), events: %+v", events)
	}
}

// ---- 4. mixed: reasoning + tool + text → 3 separate events ----
func TestOpenCodeDB_MixedReasoningToolText(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "go", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "reasoning", "First I think.", 2.1)
	bashPart := `{"type":"tool","tool":"bash","callID":"call_b","state":{"status":"completed","input":{"command":"echo hi"}}}`
	opencodeInsertPartRaw(t, dbPath, "a1p2", "a1", bashPart, 2.2)
	opencodeInsertPart(t, dbPath, "a1p3", "a1", "text", "Done.", 2.3)

	opencodeInsertMessage(t, dbPath, "u2", "S", "user", 3.0)
	opencodeInsertPart(t, dbPath, "u2p1", "u2", "text", "next", 3.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()

	if got := countKindEvents(events, "thinking"); got < 1 {
		t.Errorf("expected >=1 thinking event, got %d. events=%+v", got, events)
	}
	if got := countKindEvents(events, "tool_use"); got < 1 {
		t.Errorf("expected >=1 tool_use event, got %d. events=%+v", got, events)
	}
	// Plain text event = no Kind (existing path)
	var sawPlainText bool
	for _, e := range events {
		if e.MsgID == "a1" && e.Text == "Done." && e.Kind == "" {
			sawPlainText = true
		}
	}
	if !sawPlainText {
		t.Errorf("expected plain text event with text=Done. and empty kind for a1, events=%+v", events)
	}
}

// ---- 5. streaming: reasoning emitted ONCE across multiple polls ----
func TestOpenCodeDB_ReasoningEmittedOnceAcrossPolls(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "go", 1.1)

	// Streaming assistant message — reasoning part appears, then text appears later.
	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "reasoning", "Step 1 plan.", 2.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	// Poll 1: reasoning appears for the first time.
	w.poll()
	count1 := countKindEvents(col.snapshot(), "thinking")
	if count1 != 1 {
		t.Fatalf("poll 1: expected exactly 1 thinking event, got %d. events=%+v", count1, col.snapshot())
	}

	// Poll 2: nothing changed — reasoning must NOT be emitted again.
	w.poll()
	count2 := countKindEvents(col.snapshot(), "thinking")
	if count2 != 1 {
		t.Fatalf("poll 2: expected still 1 thinking event (idempotent), got %d. events=%+v", count2, col.snapshot())
	}

	// Poll 3: text emerges. Reasoning must STILL be 1.
	opencodeInsertPart(t, dbPath, "a1p2", "a1", "text", "Answer.", 2.2)
	w.poll()
	count3 := countKindEvents(col.snapshot(), "thinking")
	if count3 != 1 {
		t.Fatalf("poll 3: expected still 1 thinking event after text emerges, got %d. events=%+v", count3, col.snapshot())
	}
}

// ---- 6. Two reasoning parts → two separate thinking events ----
func TestOpenCodeDB_TwoReasoningPartsEmitTwoThinkingEvents(t *testing.T) {
	dbPath := opencodeTestDB(t)

	opencodeInsertMessage(t, dbPath, "u1", "S", "user", 1.0)
	opencodeInsertPart(t, dbPath, "u1p1", "u1", "text", "go", 1.1)

	opencodeInsertMessage(t, dbPath, "a1", "S", "assistant", 2.0)
	opencodeInsertPart(t, dbPath, "a1p1", "a1", "reasoning", "First thought.", 2.1)
	opencodeInsertPart(t, dbPath, "a1p2", "a1", "reasoning", "Second thought.", 2.2)

	opencodeInsertMessage(t, dbPath, "u2", "S", "user", 3.0)
	opencodeInsertPart(t, dbPath, "u2p1", "u2", "text", "next", 3.1)

	col := &eventCollector{}
	w := newOpenCodeWatcherForTest(dbPath, "S", col.callback)

	w.poll()
	events := col.snapshot()

	if got := countKindEvents(events, "thinking"); got != 2 {
		t.Fatalf("expected 2 thinking events for 2 reasoning parts, got %d. events=%+v", got, events)
	}

	// Verify their MsgIDs differ (so the app dedup-by-msgId doesn't collapse them).
	var thinkingMsgIDs []string
	for _, e := range events {
		if e.Kind == "thinking" {
			thinkingMsgIDs = append(thinkingMsgIDs, e.MsgID)
		}
	}
	if len(thinkingMsgIDs) == 2 && thinkingMsgIDs[0] == thinkingMsgIDs[1] {
		t.Errorf("two thinking events must have distinct MsgIDs, got both = %q", thinkingMsgIDs[0])
	}
}

// containsAll already exists in opencode_db_tools_test.go — reuse it.

// stringContains reuses helper from opencode_db_tools_test.go.

// noLint: silence unused on platforms where helpers aren't fully used.
var _ = fmt.Sprintf
