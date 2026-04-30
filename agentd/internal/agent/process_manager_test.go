package agent_test

import (
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
)

func TestProcessManagerCreateAndStop(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("sleep-agent", "custom", "sleep", []string{"60"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}

	// Should be starting or idle
	status := ag.Status()
	if status != agent.StatusStarting && status != agent.StatusIdle {
		t.Fatalf("unexpected status: %v", status)
	}

	if err := m.Stop(id); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if ag.Status() != agent.StatusStopped {
		t.Fatalf("expected Stopped, got %v", ag.Status())
	}
}

func TestProcessManagerRestartInPlace(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("sleep-agent", "custom", "sleep", []string{"60"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := m.RestartInPlace(id, "custom", "sleep", []string{"30"}, nil); err != nil {
		t.Fatalf("RestartInPlace failed: %v", err)
	}

	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found after restart")
	}
	if ag.Status() != agent.StatusIdle && ag.Status() != agent.StatusStarting {
		t.Fatalf("unexpected status after restart: %v", ag.Status())
	}

	m.Stop(id)
}

func TestProcessManagerStartInPlaceWithMessage(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("echo-agent", "custom", "echo", []string{"hello"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Stop first so we can start in place with a message
	m.Stop(id)
	time.Sleep(100 * time.Millisecond)

	if err := m.StartInPlaceWithMessage(id, "custom", "echo", []string{"hello"}, nil, "world"); err != nil {
		t.Fatalf("StartInPlaceWithMessage failed: %v", err)
	}

	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	if ag.Status() != agent.StatusIdle && ag.Status() != agent.StatusStarting {
		t.Fatalf("unexpected status: %v", ag.Status())
	}

	m.Stop(id)
}

func TestProcessManagerRemove(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("echo-agent", "custom", "echo", []string{"hello"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := m.Remove(id); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	if m.Get(id) != nil {
		t.Fatal("expected agent to be removed")
	}
}

func TestProcessManagerClaudePrintModeIdle(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("claude-p", "claude", "claude", []string{"-p", "--dangerously-skip-permissions"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	if ag.Status() != agent.StatusIdle {
		t.Fatalf("expected idle for claude -p mode, got %v", ag.Status())
	}

	m.Stop(id)
}
