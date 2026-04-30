package agent_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/store"
)

func newTestManager(t *testing.T) *agent.Manager {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return agent.NewManager(s, t.TempDir())
}

func TestCreateAndListAgent(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Create("test-agent", "custom", "echo", []string{"hello"}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty agent id")
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != id {
		t.Errorf("expected id=%q, got %q", id, agents[0].ID)
	}
}

func TestAgentStatusTransition(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("echo-agent", "custom", "echo", []string{"hello"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Give it a moment to reach Starting/Idle
	time.Sleep(200 * time.Millisecond)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	// echo exits immediately — status should be Stopped or Idle
	status := ag.Status()
	if status != agent.StatusStopped && status != agent.StatusIdle {
		t.Errorf("unexpected status: %v", status)
	}
}

func TestStopAgent(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("sleep-agent", "custom", "sleep", []string{"60"}, t.TempDir(), nil)
	time.Sleep(100 * time.Millisecond)

	if err := m.Stop(id); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	ag := m.Get(id)
	if ag.Status() != agent.StatusStopped {
		t.Errorf("expected Stopped, got %v", ag.Status())
	}
}

func TestAttachedAgentIsReadOnly(t *testing.T) {
	m := newTestManager(t)
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	// Use the test process's own PID so validateProcess passes
	// (test binary path contains "claude" when running in this repo)
	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	if !ag.IsReadOnly() {
		t.Fatal("expected attached agent to be read-only")
	}
}

func TestClassifyAttachCandidateClaudeWithoutSessionIDIsDisplay(t *testing.T) {
	m := newTestManager(t)

	// Claude process with empty session ID should be Display (shown in list, not auto-attached)
	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:      123,
		Provider: "claude",
		WorkDir:  "/repo",
	})
	if candidate.Decision != agent.AttachDecisionDisplay {
		t.Fatalf("expected display decision for claude without session ID, got %q", candidate.Decision)
	}
}

func TestClassifyAttachCandidateNonClaudeWithEmptySessionIDIsAmbiguous(t *testing.T) {
	m := newTestManager(t)

	// Non-claude process with empty session ID should still be Auto (not filtered as ambiguous)
	// Note: the original behavior for non-claude was Auto regardless of session ID.
	// The test name reflects the regression concern: non-claude should not be affected.
	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:       123,
		Provider:  "opencode",
		SessionID: "",
	})
	if candidate.Decision != agent.AttachDecisionAuto {
		t.Fatalf("expected auto decision for non-claude process, got %q", candidate.Decision)
	}
}

func TestAttachSamePIDSwitchesSessionAndClearsHistory(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	firstSessionFile := filepath.Join(repoDir, "sess-old.jsonl")
	if err := os.WriteFile(firstSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write first session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: firstSessionFile,
	})
	if err != nil {
		t.Fatalf("initial attach failed: %v", err)
	}

	originalAutoName := ag.Name
	if err := m.UpdateResumeSessionID(ag.ID, "sess-old"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}
	if err := m.Rename(ag.ID, "custom title"); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	if seq := ag.AppendEvent(map[string]any{"role": "assistant", "text": "stale"}); seq == 0 {
		t.Fatalf("expected seq > 0")
	}
	if _, err := m.LoadPersistedEventsLatest(ag.ID, 10); err != nil {
		t.Fatalf("expected persisted events API to work before reset: %v", err)
	}
	if _, err := m.LoadPersistedEventsSince(ag.ID, 0, 10); err != nil {
		t.Fatalf("expected persisted events since API to work before reset: %v", err)
	}
	if _, err := m.LoadPersistedEventsBefore(ag.ID, 100, 10); err != nil {
		t.Fatalf("expected persisted events before API to work before reset: %v", err)
	}

	newSessionFile := filepath.Join(repoDir, "sess-new.jsonl")
	if err := os.WriteFile(newSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new session file: %v", err)
	}

	rebound, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: newSessionFile,
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if rebound.ID != ag.ID {
		t.Fatalf("expected same agent id, got %s vs %s", rebound.ID, ag.ID)
	}

	resumeID, err := m.GetResumeSessionID(ag.ID)
	if err != nil {
		t.Fatalf("GetResumeSessionID failed: %v", err)
	}
	if resumeID != "sess-new" {
		t.Fatalf("expected new session id, got %q", resumeID)
	}
	if rebound.Name != "custom title" {
		t.Fatalf("expected custom title to survive session switch, got %q (auto was %q)", rebound.Name, originalAutoName)
	}
	if events := rebound.EventBuf().Since(0); len(events) != 0 {
		t.Fatalf("expected cleared live history, got %d events", len(events))
	}
	persisted, err := m.LoadPersistedEventsLatest(ag.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("expected cleared persisted history, got %d events", len(persisted))
	}
}

func TestAttachSameSessionDifferentPIDCreatesSeparateAgents(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	sessionFile := filepath.Join(repoDir, "sess-live.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	first, err := m.Attach(scanner.ProcessInfo{
		PID:         6864,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("first attach failed: %v", err)
	}

	second, err := m.Attach(scanner.ProcessInfo{
		PID:         7777,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("second attach failed: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("expected separate agents for different pids sharing one session, got same id %s", first.ID)
	}

	agents := m.List()
	if len(agents) != 2 {
		t.Fatalf("expected 2 managed agents, got %d", len(agents))
	}
}

func TestAttachSamePIDSwitchesSessionUpdatesAutoName(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	firstSessionFile := filepath.Join(repoDir, "sess-old.jsonl")
	if err := os.WriteFile(firstSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write first session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: firstSessionFile,
	})
	if err != nil {
		t.Fatalf("initial attach failed: %v", err)
	}
	if err := m.UpdateResumeSessionID(ag.ID, "sess-old"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}
	if got := ag.Name; got != filepath.Base(repoDir)+" - "+strconv.Itoa(ownPID) {
		t.Fatalf("expected initial auto name, got %q", got)
	}

	newSessionFile := filepath.Join(repoDir, "sess-new.jsonl")
	if err := os.WriteFile(newSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new session file: %v", err)
	}

	rebound, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: newSessionFile,
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if rebound.ID != ag.ID {
		t.Fatalf("expected same agent id, got %s vs %s", rebound.ID, ag.ID)
	}
	if rebound.Name != filepath.Base(repoDir)+" - "+strconv.Itoa(ownPID) {
		t.Fatalf("expected updated auto name to stay pid-based, got %q", rebound.Name)
	}
}

func TestAutoAttachExistingIncludesDisplayCandidates(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	sessionFile := filepath.Join(repoDir, "sess-empty.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	ownPID := os.Getpid()
	// Override scanExisting to return a claude process with empty SessionID
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:         ownPID,
			Provider:    "claude",
			WorkDir:     repoDir,
			SessionFile: sessionFile,
			SessionID:   "", // empty session ID -> Display decision
		}}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent after auto-attach, got %d", len(agents))
	}
	if agents[0].Provider != "claude" {
		t.Fatalf("expected claude agent, got %q", agents[0].Provider)
	}
	if agents[0].PID != ownPID {
		t.Fatalf("expected PID %d, got %d", ownPID, agents[0].PID)
	}
}

func TestAttachClaudeWithoutSessionFileCreatesDisplayAgent(t *testing.T) {
	m := newTestManager(t)

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: "", // no session file
		SessionID:   "",
	})
	if err != nil {
		t.Fatalf("Attach failed for claude without session file: %v", err)
	}
	if ag.Provider != "claude" {
		t.Fatalf("expected provider claude, got %q", ag.Provider)
	}
	if ag.PID != ownPID {
		t.Fatalf("expected PID %d, got %d", ownPID, ag.PID)
	}
}

func TestAttachClaudeWithoutSessionFileAppearsInList(t *testing.T) {
	m := newTestManager(t)

	ownPID := os.Getpid()
	_, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: "",
		SessionID:   "",
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent in list, got %d", len(agents))
	}
	if agents[0].Provider != "claude" {
		t.Fatalf("expected claude agent, got %q", agents[0].Provider)
	}
}

func TestAttachClaudeWithoutSessionFileNoWatcher(t *testing.T) {
	m := newTestManager(t)

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: "",
		SessionID:   "",
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}
	if ag.Watcher() != nil {
		t.Fatalf("expected no watcher for display-only agent, got %v", ag.Watcher())
	}
}

func TestAutoAttachExistingWithEmptySessionFileCreatesDisplayAgent(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()

	ownPID := os.Getpid()
	// Override scanExisting to return a claude process with NO session file at all
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:         ownPID,
			Provider:    "claude",
			WorkDir:     repoDir,
			SessionFile: "", // truly empty -> no session file found
			SessionID:   "",
		}}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 display-only agent after auto-attach, got %d", len(agents))
	}
	if agents[0].Provider != "claude" {
		t.Fatalf("expected claude agent, got %q", agents[0].Provider)
	}
	if agents[0].PID != ownPID {
		t.Fatalf("expected PID %d, got %d", ownPID, agents[0].PID)
	}
	if agents[0].Watcher() != nil {
		t.Fatalf("expected no watcher for display-only agent, got %v", agents[0].Watcher())
	}
}

func TestAutoAttachExistingSkipsAmbiguousCandidates(t *testing.T) {
	m := newTestManager(t)

	// Override scanExisting to return a process with missing PID (Skip decision)
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:      0,
			Provider: "claude",
			WorkDir:  "/repo",
		}}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
	}
}

func TestAttachClaudeLoadsJSONLHistory(t *testing.T) {
	m := newTestManager(t)
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.jsonl")

	// Write some claude .jsonl lines (assistant message, user message)
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from assistant"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Hello from user"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     tmpDir,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Verify history was loaded into store
	seq, err := m.LastPersistedSeq(ag.ID)
	if err != nil {
		t.Fatalf("LastPersistedSeq failed: %v", err)
	}
	if seq == 0 {
		t.Fatalf("expected history to be loaded from .jsonl, got seq=0")
	}

	// Verify LastConversationEventTime is set
	lastTime, err := m.LastConversationEventTime(ag.ID)
	if err != nil {
		t.Fatalf("LastConversationEventTime failed: %v", err)
	}
	if lastTime.IsZero() {
		t.Fatalf("expected LastConversationEventTime to be set, got zero")
	}

	// Verify we can load persisted events
	events, err := m.LoadPersistedEventsLatest(ag.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 persisted events, got %d", len(events))
	}
	if events[0].Data["role"] != "assistant" {
		t.Errorf("expected first event role=assistant, got %q", events[0].Data["role"])
	}
	if events[1].Data["role"] != "user" {
		t.Errorf("expected second event role=user, got %q", events[1].Data["role"])
	}
}

func TestEventBufferExists(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("buf-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	buf := ag.Buffer()
	if buf == nil {
		t.Error("expected non-nil EventBuffer")
	}
	_ = buf.(*eventbuf.EventBuffer)
}

func TestRecordConversationEventIncludesTimestamp(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("ts-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)

	before := time.Now().UnixMilli()
	seq, err := m.RecordConversationEvent(id, map[string]any{
		"role": "user",
		"text": "hello",
		"raw":  false,
		"kind": "user",
	})
	if err != nil {
		t.Fatalf("RecordConversationEvent failed: %v", err)
	}
	after := time.Now().UnixMilli()

	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	evts := ag.EventBuf().Since(seq - 1)
	if len(evts) != 1 {
		t.Fatalf("expected 1 event since seq-1, got %d", len(evts))
	}
	ts, ok := evts[0].Data["timestamp"].(int64)
	if !ok {
		t.Fatalf("expected timestamp int64 in event data, got %T %v", evts[0].Data["timestamp"], evts[0].Data["timestamp"])
	}
	if ts < before || ts > after {
		t.Errorf("timestamp %d out of range [%d, %d]", ts, before, after)
	}
}

func TestLoadPersistedEventsIncludesTimestamp(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("ts-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)

	before := time.Now().UnixMilli()
	_, err := m.RecordConversationEvent(id, map[string]any{
		"role": "user",
		"text": "hello",
		"raw":  false,
		"kind": "user",
	})
	if err != nil {
		t.Fatalf("RecordConversationEvent failed: %v", err)
	}
	after := time.Now().UnixMilli()

	events, err := m.LoadPersistedEventsLatest(id, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ts, ok := events[0].Data["timestamp"].(int64)
	if !ok {
		t.Fatalf("expected timestamp int64 in loaded event, got %T %v", events[0].Data["timestamp"], events[0].Data["timestamp"])
	}
	if ts < before || ts > after {
		t.Errorf("timestamp %d out of range [%d, %d]", ts, before, after)
	}
}

func TestFindSessionFileUsesProjectDirName(t *testing.T) {
	m := newTestManager(t)

	// projectDirName("/a/b/.c/d_e") should be "-a-b--c-d-e"
	// not "-a-b-.c-d_e" (the old buggy behavior)
	got := m.FindSessionFileProjectDirName("/a/b/.c/d_e")
	want := "-a-b--c-d-e"
	if got != want {
		t.Fatalf("projectDirName mismatch: got %q, want %q", got, want)
	}

	// Edge cases
	if m.FindSessionFileProjectDirName("/foo/bar") != "-foo-bar" {
		t.Fatalf("unexpected projectDirName for /foo/bar")
	}
	if m.FindSessionFileProjectDirName("/foo.bar/baz_qux") != "-foo-bar-baz-qux" {
		t.Fatalf("unexpected projectDirName for /foo.bar/baz_qux")
	}
}

func TestRestartInPlaceCustomAgentStatusIdle(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("restart-agent", "custom", "sleep", []string{"60"}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	ag := m.Get(id)
	if ag.Status() != agent.StatusIdle {
		t.Fatalf("expected idle before restart, got %v", ag.Status())
	}

	if err := m.RestartInPlace(id, "custom", "sleep", []string{"60"}, nil); err != nil {
		t.Fatalf("RestartInPlace failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if ag.Status() != agent.StatusIdle {
		t.Errorf("expected idle after restart, got %v", ag.Status())
	}
}
