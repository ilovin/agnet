package agent_test

import (
	"os"
	"path/filepath"
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
