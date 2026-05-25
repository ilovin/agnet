package agent_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/watcher"

	_ "modernc.org/sqlite"
)

// hermesSessionTestDB is a small helper that creates a fresh sqlite DB with
// the Hermes state.db schema and exposes insert helpers for the integration
// scenarios below.
type hermesSessionTestDB struct {
	t       *testing.T
	dbPath  string
	insertM sync.Mutex
}

func newHermesSessionTestDB(t *testing.T) *hermesSessionTestDB {
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
	return &hermesSessionTestDB{t: t, dbPath: dbPath}
}

func (h *hermesSessionTestDB) insertSession(id, startedAt string) {
	h.t.Helper()
	h.insertM.Lock()
	defer h.insertM.Unlock()
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		h.t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR IGNORE INTO sessions(id, started_at) VALUES(?, ?)`, id, startedAt); err != nil {
		h.t.Fatalf("insert session: %v", err)
	}
}

func (h *hermesSessionTestDB) insertMessage(sessionID, role, content, ts string) {
	h.t.Helper()
	h.insertM.Lock()
	defer h.insertM.Unlock()
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		h.t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO messages(session_id, role, content, timestamp) VALUES(?, ?, ?, ?)`, sessionID, role, content, ts); err != nil {
		h.t.Fatalf("insert msg: %v", err)
	}
}

// TestHermesSessionSwitch_EndToEnd covers the M3+M4 pipeline:
//  1. Attach a hermes agent with session A and rows pre-populated in state.db.
//  2. Wait for the watcher to start polling.
//  3. Insert a message into a brand-new session B with a strictly-later
//     timestamp; the watcher must observe the switch and:
//     - Update the agent's resume session id to B.
//     - Clear EventBuf and reload B's history.
//     - Emit a conversation.cleared output broadcast.
//     - Emit an agent.status_changed broadcast carrying sessionId=B.
func TestHermesSessionSwitch_EndToEnd(t *testing.T) {
	dbCtx := newHermesSessionTestDB(t)
	restorePath := watcher.SetFindHermesStateDBForTest(func() string { return dbCtx.dbPath })
	defer restorePath()
	restoreInterval := watcher.SetHermesDBWatcherIntervalForTest(50 * time.Millisecond)
	defer restoreInterval()

	// Pre-populate session A so HermesStateDBHistory returns initial events.
	dbCtx.insertSession("A", "2026-05-25T10:00:00Z")
	dbCtx.insertMessage("A", "user", "a-hello", "2026-05-25T10:00:01Z")
	dbCtx.insertMessage("A", "assistant", "a-hi", "2026-05-25T10:00:02Z")

	m := newTestManager(t)

	// Capture broadcasts.
	var outMu sync.Mutex
	type outEv struct {
		agentID string
		data    map[string]any
	}
	var outs []outEv
	m.SetOnOutput(func(agentID string, data map[string]any) {
		outMu.Lock()
		outs = append(outs, outEv{agentID: agentID, data: data})
		outMu.Unlock()
	})

	var statusMu sync.Mutex
	type statusEv struct {
		agentID string
		data    map[string]any
	}
	var statuses []statusEv
	m.SetOnStatusChange(func(agentID string, data map[string]any) {
		statusMu.Lock()
		statuses = append(statuses, statusEv{agentID: agentID, data: data})
		statusMu.Unlock()
	})

	// Attach a hermes agent with session A.
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:       os.Getpid(),
		Provider:  "hermes",
		WorkDir:   t.TempDir(),
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "A"},
		SessionID: "A",
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Sanity: history loaded into EventBuf at attach time.
	if got := len(ag.EventBuf().Since(0)); got < 2 {
		t.Fatalf("expected attach to load >=2 events for session A, got %d", got)
	}

	// Now Hermes flips to session B (latest timestamp wins).
	dbCtx.insertSession("B", "2026-05-25T11:00:00Z")
	dbCtx.insertMessage("B", "user", "b-fresh", "2026-05-25T11:00:00Z")

	// Wait for switch to be processed. Three signals must converge:
	//   * resume session id == "B"
	//   * a conversation.cleared output broadcast
	//   * an agent.status_changed status broadcast carrying sessionId=B
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resumeID, _ := m.GetResumeSessionID(ag.ID)

		var sawCleared bool
		outMu.Lock()
		for _, o := range outs {
			if t, _ := o.data["type"].(string); t == "conversation.cleared" && o.agentID == ag.ID {
				sawCleared = true
				break
			}
		}
		outMu.Unlock()

		var sawStatusWithSessionB bool
		statusMu.Lock()
		for _, s := range statuses {
			if s.agentID != ag.ID {
				continue
			}
			method, _ := s.data["method"].(string)
			if method != "agent.status_changed" {
				continue
			}
			params, _ := s.data["params"].(map[string]any)
			if params == nil {
				continue
			}
			if sid, _ := params["sessionId"].(string); sid == "B" {
				sawStatusWithSessionB = true
				break
			}
		}
		statusMu.Unlock()

		if resumeID == "B" && sawCleared && sawStatusWithSessionB {
			// Validate EventBuf reflects only B (cleared then re-hydrated).
			events := ag.EventBuf().Since(0)
			// The single B message has been loaded, plus any events emitted
			// by the live watcher tail. We assert at least the B message is
			// present and no A message remains.
			var foundB, foundA bool
			for _, e := range events {
				text, _ := e.Data["text"].(string)
				if text == "b-fresh" {
					foundB = true
				}
				if text == "a-hello" || text == "a-hi" {
					foundA = true
				}
			}
			if !foundB {
				t.Fatalf("expected B event to be loaded into EventBuf, got %+v", events)
			}
			if foundA {
				t.Fatalf("expected A events to be cleared from EventBuf, got %+v", events)
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}

	resumeID, _ := m.GetResumeSessionID(ag.ID)
	outMu.Lock()
	gotOuts := append([]outEv(nil), outs...)
	outMu.Unlock()
	statusMu.Lock()
	gotStatuses := append([]statusEv(nil), statuses...)
	statusMu.Unlock()
	t.Fatalf("session switch not observed in time. resumeID=%q outs=%+v statuses=%+v", resumeID, gotOuts, gotStatuses)
}

// TestHermesSessionSwitch_SuppressedWhileSending verifies M4 §4.3: when an
// agent has IsSending()==true, the watcher must not switch even if state.db
// indicates a different latest session. Once IsSending()==false, the next
// poll picks up the switch.
func TestHermesSessionSwitch_SuppressedWhileSending(t *testing.T) {
	dbCtx := newHermesSessionTestDB(t)
	restorePath := watcher.SetFindHermesStateDBForTest(func() string { return dbCtx.dbPath })
	defer restorePath()
	restoreInterval := watcher.SetHermesDBWatcherIntervalForTest(50 * time.Millisecond)
	defer restoreInterval()

	dbCtx.insertSession("A", "2026-05-25T10:00:00Z")
	dbCtx.insertMessage("A", "user", "a1", "2026-05-25T10:00:01Z")

	m := newTestManager(t)

	ag, err := m.Attach(scanner.ProcessInfo{
		PID:       os.Getpid(),
		Provider:  "hermes",
		WorkDir:   t.TempDir(),
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "A"},
		SessionID: "A",
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Mark as sending; subsequent state.db churn must not trigger a switch.
	ag.BeginSend()

	dbCtx.insertSession("B", "2026-05-25T11:00:00Z")
	dbCtx.insertMessage("B", "user", "b1", "2026-05-25T11:00:00Z")

	// Wait long enough for several poll cycles. Resume id must remain "A".
	time.Sleep(300 * time.Millisecond)
	if rid, _ := m.GetResumeSessionID(ag.ID); rid != "A" {
		ag.EndSend()
		t.Fatalf("expected resume session to remain A while sending, got %q", rid)
	}

	// Release the suppressor; the next poll should pick up the switch.
	ag.EndSend()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rid, _ := m.GetResumeSessionID(ag.ID); rid == "B" {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	rid, _ := m.GetResumeSessionID(ag.ID)
	t.Fatalf("expected resume session B after EndSend, got %q", rid)
}
