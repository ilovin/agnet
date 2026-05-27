package store_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/phone-talk/agentd/internal/store"
)

func TestSaveAndLoad(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ag := store.AgentRecord{
		ID:              "agent-1",
		Name:            "my claude",
		Provider:        "claude-code",
		WorkDir:         "/tmp/proj",
		ResumeSessionID: "",
	}
	if err := s.SaveAgent(ag); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != "agent-1" {
		t.Errorf("expected id=agent-1, got %q", agents[0].ID)
	}
}

func TestUpdateResumeSessionID(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ag := store.AgentRecord{ID: "agent-2", Name: "x", Provider: "claude-code", WorkDir: "/tmp"}
	if err := s.SaveAgent(ag); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}

	if err := s.UpdateResumeSessionID("agent-2", "sess-abc"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if agents[0].ResumeSessionID != "sess-abc" {
		t.Errorf("expected sess-abc, got %q", agents[0].ResumeSessionID)
	}
}

func TestClearConversationEvents(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveConversationEvent("agent-1", 1, map[string]any{"role": "assistant", "text": "old"}); err != nil {
		t.Fatalf("SaveConversationEvent failed: %v", err)
	}
	if err := s.ClearConversationEvents("agent-1"); err != nil {
		t.Fatalf("ClearConversationEvents failed: %v", err)
	}

	events, err := s.ListConversationEventsLatest("agent-1", 10)
	if err != nil {
		t.Fatalf("ListConversationEventsLatest failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events after clear, got %d", len(events))
	}
}

func TestDeleteAgent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveAgent(store.AgentRecord{ID: "del-1", Name: "x", Provider: "claude-code", WorkDir: "/tmp"}); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}
	if err := s.DeleteAgent("del-1"); err != nil {
		t.Fatalf("DeleteAgent failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after delete, got %d", len(agents))
	}
}

// TestListConversationEventsLatest_DoesNotReturnStaleSeq verifies that after an
// EventBuf.Reset() cycle — where new events are written with low seq numbers
// (overwriting old rows via INSERT OR REPLACE) while higher seq numbers from the
// previous run remain in the DB — ListConversationEventsLatest returns the
// *recently written* events, not the stale high-seq rows.
func TestListConversationEventsLatest_DoesNotReturnStaleSeq(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const agentID = "agent-stale-seq"

	// Phase 1: write 10 "old" events with seq 1-10 (simulating the previous run).
	// Use a fixed past timestamp so they are clearly older.
	for i := 1; i <= 10; i++ {
		data := map[string]any{
			"role": "assistant",
			"text": fmt.Sprintf("old-msg-%d", i),
		}
		if err := s.SaveConversationEvent(agentID, uint64(i), data); err != nil {
			t.Fatalf("save old event seq=%d: %v", i, err)
		}
	}

	// Small sleep so that nanosecond timestamps are strictly after the old batch.
	time.Sleep(2 * time.Millisecond)

	// Phase 2: simulate EventBuf.Reset() — new run starts seq from 1 again.
	// We write 5 *new* events with seq 1-5. INSERT OR REPLACE overwrites old
	// seq 1-5, but old seq 6-10 survive in the DB.
	for i := 1; i <= 5; i++ {
		data := map[string]any{
			"role": "assistant",
			"text": fmt.Sprintf("new-msg-%d", i),
		}
		if err := s.SaveConversationEvent(agentID, uint64(i), data); err != nil {
			t.Fatalf("save new event seq=%d: %v", i, err)
		}
	}

	// ListConversationEventsLatest with limit=5 should return the 5 newest rows
	// (the freshly written ones, text="new-msg-*"), NOT the stale seq 6-10
	// (text="old-msg-*").
	events, err := s.ListConversationEventsLatest(agentID, 5)
	if err != nil {
		t.Fatalf("ListConversationEventsLatest: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	for _, ev := range events {
		if ev.Text[:4] != "new-" {
			t.Errorf("got stale event text=%q (seq=%d); expected new-msg-*", ev.Text, ev.Seq)
		}
	}
}

// TestConversationEventPayloadRoundtrip verifies that camelCase payload fields
// (askUserQuestion, exitPlanMode) survive a save → load cycle via all three
// List functions. This is a regression test for the Gap 2 fix (T2b).
func TestConversationEventPayloadRoundtrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	askPayload := map[string]any{
		"tool_use_id": "toolu_01",
		"questions": []any{
			map[string]any{"question": "Are you sure?", "multi_select": false, "options": []any{}},
		},
	}
	exitPayload := map[string]any{
		"tool_use_id": "toolu_02",
		"plan":        "Step 1: ...",
	}

	data1 := map[string]any{
		"role":            "assistant",
		"raw":             false,
		"kind":            "ask_user_question",
		"askUserQuestion": askPayload,
	}
	data2 := map[string]any{
		"role":         "assistant",
		"raw":          false,
		"kind":         "exit_plan_mode",
		"exitPlanMode": exitPayload,
	}

	if err := s.SaveConversationEvent("ag1", 1, data1); err != nil {
		t.Fatalf("save data1: %v", err)
	}
	if err := s.SaveConversationEvent("ag1", 2, data2); err != nil {
		t.Fatalf("save data2: %v", err)
	}

	// --- ListConversationEventsLatest ---
	latest, err := s.ListConversationEventsLatest("ag1", 10)
	if err != nil {
		t.Fatalf("ListConversationEventsLatest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("expected 2, got %d", len(latest))
	}
	if latest[0].Kind != "ask_user_question" {
		t.Errorf("kind: got %q, want ask_user_question", latest[0].Kind)
	}
	if latest[0].Payload == nil || latest[0].Payload["askUserQuestion"] == nil {
		t.Errorf("askUserQuestion payload missing from Latest; Payload=%v", latest[0].Payload)
	}
	if latest[1].Payload == nil || latest[1].Payload["exitPlanMode"] == nil {
		t.Errorf("exitPlanMode payload missing from Latest; Payload=%v", latest[1].Payload)
	}

	// --- ListConversationEventsSince ---
	since, err := s.ListConversationEventsSince("ag1", 0, 10)
	if err != nil {
		t.Fatalf("ListConversationEventsSince: %v", err)
	}
	if len(since) != 2 {
		t.Fatalf("expected 2, got %d", len(since))
	}
	if since[0].Payload == nil || since[0].Payload["askUserQuestion"] == nil {
		t.Errorf("askUserQuestion missing from Since; Payload=%v", since[0].Payload)
	}

	// --- ListConversationEventsBefore ---
	before, err := s.ListConversationEventsBefore("ag1", 3, 10)
	if err != nil {
		t.Fatalf("ListConversationEventsBefore: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("expected 2, got %d", len(before))
	}
	if before[1].Payload == nil || before[1].Payload["exitPlanMode"] == nil {
		t.Errorf("exitPlanMode missing from Before; Payload=%v", before[1].Payload)
	}
}

// TestConversationEventsHasSessionIDColumn verifies that the conversation_events
// table has a session_id column after migration.
func TestConversationEventsHasSessionIDColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Open a separate connection to run a PRAGMA table_info query.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`PRAGMA table_info(conversation_events)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	hasSessionID := false
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "session_id" {
			hasSessionID = true
		}
	}
	if !hasSessionID {
		t.Fatalf("conversation_events missing session_id column")
	}
}

// TestSaveConversationEventStoresSessionID writes an event with a sessionID and
// verifies the underlying row carries that sessionID via a raw SQL probe.
func TestSaveConversationEventStoresSessionID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const agentID = "agent-with-session"
	const sessID = "session-abc"
	if err := s.SaveConversationEventWithSession(agentID, sessID, 1, map[string]any{"role": "user", "text": "hi"}); err != nil {
		t.Fatalf("SaveConversationEventWithSession: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var got string
	if err := db.QueryRow(`SELECT session_id FROM conversation_events WHERE agent_id=? AND seq=?`, agentID, 1).Scan(&got); err != nil {
		t.Fatalf("query session_id: %v", err)
	}
	if got != sessID {
		t.Fatalf("session_id: got %q want %q", got, sessID)
	}
}

// TestClearConversationEventsBeforePrunesOtherSessions verifies that events
// belonging to other sessions (including legacy empty-string session) are
// pruned, while current-session events are retained.
func TestClearConversationEventsBeforePrunesOtherSessions(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const agentID = "agent-prune"

	// Stale events from a previous session.
	if err := s.SaveConversationEventWithSession(agentID, "old-session", 1, map[string]any{"role": "user", "text": "stale-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConversationEventWithSession(agentID, "old-session", 2, map[string]any{"role": "assistant", "text": "stale-2"}); err != nil {
		t.Fatal(err)
	}

	// Legacy event with empty session (e.g. pre-migration row).
	if err := s.SaveConversationEventWithSession(agentID, "", 3, map[string]any{"role": "user", "text": "legacy"}); err != nil {
		t.Fatal(err)
	}

	// Current session.
	if err := s.SaveConversationEventWithSession(agentID, "current-session", 4, map[string]any{"role": "user", "text": "keep-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConversationEventWithSession(agentID, "current-session", 5, map[string]any{"role": "assistant", "text": "keep-2"}); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearConversationEventsBefore(agentID, "current-session"); err != nil {
		t.Fatalf("ClearConversationEventsBefore: %v", err)
	}

	events, err := s.ListConversationEventsLatest(agentID, 100)
	if err != nil {
		t.Fatalf("ListConversationEventsLatest: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events after prune, got %d (events=%+v)", len(events), events)
	}
	for _, ev := range events {
		if ev.Text != "keep-1" && ev.Text != "keep-2" {
			t.Errorf("unexpected surviving event text=%q seq=%d", ev.Text, ev.Seq)
		}
	}
}

// TestClearConversationEventsBeforeRequiresSession ensures that calling with
// an empty currentSessionID is a no-op — we never want to nuke all rows just
// because a sessionID isn't known yet.
func TestClearConversationEventsBeforeRequiresSession(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const agentID = "agent-empty-session-guard"
	if err := s.SaveConversationEventWithSession(agentID, "sess-1", 1, map[string]any{"role": "user", "text": "x"}); err != nil {
		t.Fatal(err)
	}
	// Empty currentSessionID must not delete anything.
	if err := s.ClearConversationEventsBefore(agentID, ""); err != nil {
		t.Fatalf("ClearConversationEventsBefore with empty sessionID returned error: %v", err)
	}
	events, err := s.ListConversationEventsLatest(agentID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event preserved, got %d", len(events))
	}
}

// TestMigrationAddsSessionIDColumnToLegacyDB simulates an old database that
// pre-dates the session_id column and verifies the migration adds the column
// (defaulting to '' for legacy rows) without dropping data.
func TestMigrationAddsSessionIDColumnToLegacyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// 1) Build a legacy schema by hand: conversation_events without session_id.
	{
		db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`CREATE TABLE conversation_events (
			agent_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			data_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (agent_id, seq)
		)`)
		if err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
		_, err = db.Exec(
			`INSERT INTO conversation_events (agent_id, seq, data_json, created_at) VALUES (?,?,?,?)`,
			"legacy-agent", 1, `{"role":"user","text":"legacy"}`, time.Now().UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
		_ = db.Close()
	}

	// 2) Open via the production migration path; should ALTER and add session_id.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open after legacy schema: %v", err)
	}
	defer s.Close()

	// Verify the legacy row survived and session_id defaulted to ''.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var sess string
	if err := db.QueryRow(`SELECT session_id FROM conversation_events WHERE agent_id=? AND seq=?`, "legacy-agent", 1).Scan(&sess); err != nil {
		t.Fatalf("query legacy row session_id: %v", err)
	}
	if sess != "" {
		t.Fatalf("legacy row session_id: got %q, want empty", sess)
	}

	// 3) After ClearConversationEventsBefore("current"), the legacy row (sess='')
	//    should be removed (it's not from the current session).
	if err := s.ClearConversationEventsBefore("legacy-agent", "current"); err != nil {
		t.Fatalf("ClearConversationEventsBefore: %v", err)
	}
	events, err := s.ListConversationEventsLatest("legacy-agent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected legacy rows pruned, got %d remaining", len(events))
	}
}
