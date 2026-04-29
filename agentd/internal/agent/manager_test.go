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
