//go:build integration

package agent_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// Tests that when a JSONL file is written, the agent's EventBuffer gets populated.
func TestClaudeWatcherIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, tmpDir)

	sessionFile := filepath.Join(tmpDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	id, err := mgr.CreateWithWatcher("watcher-agent", "echo", []string{"x"}, tmpDir, sessionFile)
	if err != nil {
		t.Fatalf("CreateWithWatcher: %v", err)
	}

	// Write a message line to the session file
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done!"}]}}` + "\n"
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Wait for watcher to pick it up (polls every 300ms)
	time.Sleep(700 * time.Millisecond)

	ag := mgr.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	events := ag.EventBuf().Since(0)
	if len(events) == 0 {
		t.Error("expected events in buffer after watcher write")
	}
	_ = watcher.StatusStandby // verify import compiles
}
