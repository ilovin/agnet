package watcher

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// hermesTestDB creates a fresh sqlite DB at tmpdir/state.db with the
// Hermes state.db schema (sessions + messages tables) and returns its path.
func hermesTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at TEXT)`); err != nil {
		t.Fatalf("create sessions: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE messages (session_id TEXT, role TEXT, content TEXT, timestamp TEXT)`); err != nil {
		t.Fatalf("create messages: %v", err)
	}
	return dbPath
}

func hermesInsertSession(t *testing.T, dbPath, sessionID, startedAt string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR IGNORE INTO sessions(id, started_at) VALUES(?, ?)`, sessionID, startedAt); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func hermesInsertMessage(t *testing.T, dbPath, sessionID, role, content, ts string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO messages(session_id, role, content, timestamp) VALUES(?, ?, ?, ?)`, sessionID, role, content, ts); err != nil {
		t.Fatalf("insert msg: %v", err)
	}
}

// withFindHermesStateDBFunc swaps the package-level finder for the duration of the test.
func withFindHermesStateDBFunc(t *testing.T, fn func() string) {
	t.Helper()
	orig := findHermesStateDBFunc
	findHermesStateDBFunc = fn
	t.Cleanup(func() { findHermesStateDBFunc = orig })
}

// withHermesDBWatcherInterval shortens the polling interval for tests.
func withHermesDBWatcherInterval(t *testing.T, d time.Duration) {
	t.Helper()
	orig := hermesDBWatcherInterval
	hermesDBWatcherInterval = d
	t.Cleanup(func() { hermesDBWatcherInterval = orig })
}

// ---- M1: HermesStateDBLoadSession ----

func TestHermesStateDBLoadSession_HappyPath(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })

	hermesInsertSession(t, dbPath, "S1", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "S1", "user", "hello", "2026-05-25T10:00:01Z")
	hermesInsertMessage(t, dbPath, "S1", "assistant", "world", "2026-05-25T10:00:02Z")
	hermesInsertMessage(t, dbPath, "S1", "user", "bye", "2026-05-25T10:00:03Z")
	// noise from another session
	hermesInsertSession(t, dbPath, "S2", "2026-05-25T09:00:00Z")
	hermesInsertMessage(t, dbPath, "S2", "user", "other", "2026-05-25T09:00:01Z")

	events, err := HermesStateDBLoadSession("S1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	expect := []struct {
		role string
		text string
	}{
		{"user", "hello"},
		{"assistant", "world"},
		{"user", "bye"},
	}
	for i, e := range expect {
		if events[i].Role != e.role || events[i].Text != e.text {
			t.Errorf("event %d: got role=%q text=%q, want role=%q text=%q",
				i, events[i].Role, events[i].Text, e.role, e.text)
		}
	}
}

func TestHermesStateDBLoadSession_NoSession(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })

	hermesInsertSession(t, dbPath, "S1", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "S1", "user", "hello", "2026-05-25T10:00:01Z")

	events, err := HermesStateDBLoadSession("does-not-exist")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestHermesStateDBLoadSession_NoDB(t *testing.T) {
	withFindHermesStateDBFunc(t, func() string { return "" })

	events, err := HermesStateDBLoadSession("anything")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestHermesStateDBHistory_RegressionUnchanged(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })

	hermesInsertSession(t, dbPath, "S_OLD", "2026-05-24T10:00:00Z")
	hermesInsertMessage(t, dbPath, "S_OLD", "user", "old1", "2026-05-24T10:00:01Z")
	hermesInsertMessage(t, dbPath, "S_OLD", "assistant", "old2", "2026-05-24T10:00:02Z")

	hermesInsertSession(t, dbPath, "S_NEW", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "S_NEW", "user", "new1", "2026-05-25T10:00:01Z")
	hermesInsertMessage(t, dbPath, "S_NEW", "assistant", "new2", "2026-05-25T10:00:02Z")
	hermesInsertMessage(t, dbPath, "S_NEW", "user", "new3", "2026-05-25T10:00:03Z")

	events, sessionID, err := HermesStateDBHistory()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if sessionID != "S_NEW" {
		t.Fatalf("expected most recent session S_NEW, got %q", sessionID)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Text != "new1" || events[2].Text != "new3" {
		t.Errorf("ordering wrong: %+v", events)
	}
}

// ---- M2: HermesDBWatcher ----

// drainEvents collects callback events; helper to keep tests readable.
type eventCollector struct {
	mu     sync.Mutex
	events []ConversationEvent
}

func (c *eventCollector) callback(e ConversationEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *eventCollector) snapshot() []ConversationEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ConversationEvent, len(c.events))
	copy(out, c.events)
	return out
}

func (c *eventCollector) waitFor(t *testing.T, n int, timeout time.Duration) []ConversationEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ev := c.snapshot()
		if len(ev) >= n {
			return ev
		}
		time.Sleep(20 * time.Millisecond)
	}
	return c.snapshot()
}

func TestHermesDBWatcher_DetectsSessionSwitch(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })
	withHermesDBWatcherInterval(t, 50*time.Millisecond)

	// session A is "current"
	hermesInsertSession(t, dbPath, "A", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "A", "user", "a1", "2026-05-25T10:00:01Z")
	hermesInsertMessage(t, dbPath, "A", "assistant", "a2", "2026-05-25T10:00:02Z")

	col := &eventCollector{}
	w := NewHermesDBWatcher("agent-1", "A", col.callback)

	var switchedTo string
	var switchMu sync.Mutex
	w.OnSessionSwitch(func(newID string) {
		switchMu.Lock()
		defer switchMu.Unlock()
		switchedTo = newID
	})
	w.SetSkipExisting(true) // don't replay history

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	// Now session B becomes the most recent (timestamp later than any A msg).
	hermesInsertSession(t, dbPath, "B", "2026-05-25T11:00:00Z")
	hermesInsertMessage(t, dbPath, "B", "user", "b1", "2026-05-25T11:00:01Z")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		switchMu.Lock()
		got := switchedTo
		switchMu.Unlock()
		if got == "B" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("OnSessionSwitch was not called with B (last seen=%q)", switchedTo)
}

func TestHermesDBWatcher_EmitsNewMessages(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })
	withHermesDBWatcherInterval(t, 50*time.Millisecond)

	hermesInsertSession(t, dbPath, "A", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "A", "user", "old", "2026-05-25T10:00:00Z")

	col := &eventCollector{}
	w := NewHermesDBWatcher("agent-1", "A", col.callback)
	w.SetSkipExisting(true)
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	// give the watcher one tick to seed lastTS
	time.Sleep(120 * time.Millisecond)

	// new message arrives
	hermesInsertMessage(t, dbPath, "A", "assistant", "fresh", "2026-05-25T10:01:00Z")

	events := col.waitFor(t, 1, 2*time.Second)
	if len(events) < 1 {
		t.Fatalf("expected >=1 event, got %d", len(events))
	}
	if events[0].Role != "assistant" || events[0].Text != "fresh" {
		t.Fatalf("got event %+v", events[0])
	}
}

func TestHermesDBWatcher_NoEmitOnUnchanged(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })
	withHermesDBWatcherInterval(t, 50*time.Millisecond)

	hermesInsertSession(t, dbPath, "A", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "A", "user", "only", "2026-05-25T10:00:00Z")

	col := &eventCollector{}
	w := NewHermesDBWatcher("agent-1", "A", col.callback)
	w.SetSkipExisting(true)
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	time.Sleep(300 * time.Millisecond)

	if got := col.snapshot(); len(got) != 0 {
		t.Fatalf("expected 0 events on unchanged DB, got %d: %+v", len(got), got)
	}
}

func TestHermesDBWatcher_StopIsIdempotent(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })
	withHermesDBWatcherInterval(t, 50*time.Millisecond)
	hermesInsertSession(t, dbPath, "A", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "A", "user", "x", "2026-05-25T10:00:00Z")

	col := &eventCollector{}
	w := NewHermesDBWatcher("agent-1", "A", col.callback)
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	w.Stop()
	w.Stop()
	w.Stop()
}

func TestHermesDBWatcher_SkipExistingSuppressesInitialEmits(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })
	withHermesDBWatcherInterval(t, 50*time.Millisecond)

	hermesInsertSession(t, dbPath, "A", "2026-05-25T10:00:00Z")
	for i := 0; i < 5; i++ {
		hermesInsertMessage(t, dbPath, "A", "user", "m", time.Date(2026, 5, 25, 10, 0, i, 0, time.UTC).Format(time.RFC3339))
	}

	col := &eventCollector{}
	w := NewHermesDBWatcher("agent-1", "A", col.callback)
	w.SetSkipExisting(true)
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	// allow one or two ticks; should not emit historical 5 messages
	time.Sleep(200 * time.Millisecond)
	if got := col.snapshot(); len(got) != 0 {
		t.Fatalf("expected 0 emits with skipExisting, got %d", len(got))
	}

	// new message after skip should be emitted
	hermesInsertMessage(t, dbPath, "A", "assistant", "after", "2026-05-25T10:05:00Z")
	events := col.waitFor(t, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Text != "after" {
		t.Fatalf("expected text=after, got %+v", events[0])
	}
}

func TestHermesDBWatcher_SetSessionIDResetsLastTS(t *testing.T) {
	dbPath := hermesTestDB(t)
	withFindHermesStateDBFunc(t, func() string { return dbPath })
	withHermesDBWatcherInterval(t, 50*time.Millisecond)

	hermesInsertSession(t, dbPath, "A", "2026-05-25T10:00:00Z")
	hermesInsertMessage(t, dbPath, "A", "user", "a1", "2026-05-25T10:00:01Z")
	hermesInsertMessage(t, dbPath, "A", "assistant", "a2", "2026-05-25T10:00:02Z")

	col := &eventCollector{}
	w := NewHermesDBWatcher("agent-1", "A", col.callback)
	w.SetSkipExisting(true)
	// disable session switch detection in this test to focus on emit behavior
	w.OnSessionSwitch(func(string) {})

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	time.Sleep(120 * time.Millisecond) // seed lastTS for A

	// switch to fresh session B; lastTS should reset (no B messages yet → empty seed)
	hermesInsertSession(t, dbPath, "B", "2026-05-25T11:00:00Z")
	w.SetSessionID("B")

	// emit B messages
	hermesInsertMessage(t, dbPath, "B", "user", "b1", "2026-05-25T11:00:01Z")
	hermesInsertMessage(t, dbPath, "B", "assistant", "b2", "2026-05-25T11:00:02Z")

	events := col.waitFor(t, 2, 2*time.Second)
	if len(events) < 2 {
		t.Fatalf("expected >=2 events for B, got %d: %+v", len(events), events)
	}
	if events[0].Text != "b1" || events[1].Text != "b2" {
		t.Fatalf("unexpected events: %+v", events)
	}
}
