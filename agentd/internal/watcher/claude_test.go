package watcher_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/watcher"
)

func TestClaudeWatcherDetectsMessages(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "abc123.jsonl")

	// Pre-write a user message line
	line1 := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line1), 0644); err != nil {
		t.Fatal(err)
	}

	events := make(chan watcher.ConversationEvent, 10)
	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {
		events <- e
	})

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Append an assistant message
	line2 := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}` + "\n"
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(line2)
	f.Close()

	// Expect two events: one for existing line, one for new line
	got := collectEvents(events, 2, 3*time.Second)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(got))
	}
	if got[0].Role != "user" {
		t.Errorf("expected first event role=user, got %q", got[0].Role)
	}
	if got[1].Role != "assistant" {
		t.Errorf("expected second event role=assistant, got %q", got[1].Role)
	}
	if got[1].Text != "hi there" {
		t.Errorf("expected text 'hi there', got %q", got[1].Text)
	}
}

func TestClaudeWatcherDetectsWorking(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "xyz.jsonl")
	os.WriteFile(sessionFile, []byte{}, 0644)

	statuses := make(chan watcher.AgentStatus, 10)
	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {
		if e.StatusChange != nil {
			statuses <- *e.StatusChange
		}
	})
	w.Start()
	defer w.Stop()

	// tool_use line → Working
	toolLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}` + "\n"
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(toolLine)
	f.Close()

	got := collectStatuses(statuses, 1, 2*time.Second)
	if len(got) == 0 {
		t.Fatal("expected a status change event")
	}
	if got[0] != watcher.StatusWorking {
		t.Errorf("expected StatusWorking, got %v", got[0])
	}
}

func collectEvents(ch <-chan watcher.ConversationEvent, count int, timeout time.Duration) []watcher.ConversationEvent {
	var out []watcher.ConversationEvent
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			out = append(out, e)
			if len(out) >= count {
				return out
			}
		case <-deadline:
			return out
		}
	}
}

func collectStatuses(ch <-chan watcher.AgentStatus, count int, timeout time.Duration) []watcher.AgentStatus {
	var out []watcher.AgentStatus
	deadline := time.After(timeout)
	for {
		select {
		case s := <-ch:
			out = append(out, s)
			if len(out) >= count {
				return out
			}
		case <-deadline:
			return out
		}
	}
}

func TestClaudeWatcherStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "test.jsonl")
	os.WriteFile(sessionFile, []byte{}, 0644)

	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {})
	w.Start()

	// Calling Stop twice must not panic
	w.Stop()
	w.Stop() // should be a no-op, not a panic
}
