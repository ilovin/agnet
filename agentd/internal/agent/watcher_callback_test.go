package agent

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// TestMakeWatcherCallback_AskUserQuestion verifies that when the JSONL watcher
// surfaces a tool_use event with name=AskUserQuestion, the watcher callback
// emits a structured conversation event with kind=ask_user_question and an
// askUserQuestion payload (questions[]) that the Flutter app can render as
// AskUserQuestionCard. This is the R-010 T2-bis fix: previously the watcher
// path only stored a "[AskUserQuestion]" text bubble.
func TestMakeWatcherCallback_AskUserQuestion(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	m := NewManager(s, t.TempDir())
	ag := NewTestAgent("ask-agent", "claude")

	var (
		mu       sync.Mutex
		captured map[string]any
	)
	m.SetOnOutput(func(agentID string, data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		captured = data
	})

	cb := m.MakeWatcherCallback(ag.ID, ag)

	cb(watcher.ConversationEvent{
		Role:         "assistant",
		Text:         "[AskUserQuestion]",
		ToolUseName:  "AskUserQuestion",
		ToolUseID:    "toolu_ask_001",
		ToolUseInput: []byte(`{"questions":[{"question":"Pick one","header":"H","multi_select":false,"options":[{"label":"A","description":"d"}]}]}`),
	})

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected onOutput to fire")
	}
	if got, _ := captured["kind"].(string); got != "ask_user_question" {
		t.Errorf("expected kind=ask_user_question, got %q (full: %v)", got, captured)
	}
	payload, ok := captured["askUserQuestion"].(map[string]any)
	if !ok {
		t.Fatalf("expected askUserQuestion payload map, got %T (full: %v)", captured["askUserQuestion"], captured)
	}
	if id, _ := payload["tool_use_id"].(string); id != "toolu_ask_001" {
		t.Errorf("expected tool_use_id=toolu_ask_001, got %q", id)
	}
	questions, ok := payload["questions"].([]any)
	if !ok || len(questions) == 0 {
		t.Fatalf("expected non-empty questions[], got %T %v", payload["questions"], payload["questions"])
	}
	q0, _ := questions[0].(map[string]any)
	if q, _ := q0["question"].(string); q != "Pick one" {
		t.Errorf("expected first question='Pick one', got %q", q)
	}

	// Verify the persisted event also carries the structured payload (so the
	// app can reconstruct the card on history reload, not just live broadcast).
	events := ag.EventBuf().Since(0)
	if len(events) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(events))
	}
	if k, _ := events[0].Data["kind"].(string); k != "ask_user_question" {
		t.Errorf("expected buffered event kind=ask_user_question, got %q", k)
	}
	if events[0].Data["askUserQuestion"] == nil {
		t.Errorf("expected buffered event to contain askUserQuestion payload, got %v", events[0].Data)
	}
	// And the regression scenario: it must NOT keep the "[AskUserQuestion]"
	// fallback text — that text bubble was the symptom we are fixing.
	if txt, _ := events[0].Data["text"].(string); strings.Contains(txt, "[AskUserQuestion]") {
		t.Errorf("expected interactive event to drop fallback text bubble, still got %q", txt)
	}
}

// TestMakeWatcherCallback_ExitPlanMode mirrors the AskUserQuestion test for
// ExitPlanMode interactive tool.
func TestMakeWatcherCallback_ExitPlanMode(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	m := NewManager(s, t.TempDir())
	ag := NewTestAgent("plan-agent", "claude")

	var (
		mu       sync.Mutex
		captured map[string]any
	)
	m.SetOnOutput(func(agentID string, data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		captured = data
	})

	cb := m.MakeWatcherCallback(ag.ID, ag)
	cb(watcher.ConversationEvent{
		Role:         "assistant",
		Text:         "[ExitPlanMode]",
		ToolUseName:  "ExitPlanMode",
		ToolUseID:    "toolu_plan_001",
		ToolUseInput: []byte(`{"plan":"Do A then B"}`),
	})

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected onOutput to fire")
	}
	if got, _ := captured["kind"].(string); got != "exit_plan_mode" {
		t.Errorf("expected kind=exit_plan_mode, got %q", got)
	}
	payload, ok := captured["exitPlanMode"].(map[string]any)
	if !ok {
		t.Fatalf("expected exitPlanMode payload, got %T", captured["exitPlanMode"])
	}
	if plan, _ := payload["plan"].(string); plan != "Do A then B" {
		t.Errorf("expected plan='Do A then B', got %q", plan)
	}
}

// TestMakeWatcherCallback_NonInteractiveToolFallsBack verifies ordinary tools
// (Bash, Edit, etc.) keep emitting the fallback text bubble — only
// AskUserQuestion / ExitPlanMode get the structured-payload treatment.
func TestMakeWatcherCallback_NonInteractiveToolFallsBack(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	m := NewManager(s, t.TempDir())
	ag := NewTestAgent("bash-agent", "claude")

	var (
		mu       sync.Mutex
		captured map[string]any
	)
	m.SetOnOutput(func(agentID string, data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		captured = data
	})

	cb := m.MakeWatcherCallback(ag.ID, ag)
	cb(watcher.ConversationEvent{
		Role:         "assistant",
		Text:         "[Bash: ls -la]",
		ToolUseName:  "Bash",
		ToolUseID:    "toolu_bash_001",
		ToolUseInput: []byte(`{"command":"ls -la"}`),
	})

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected onOutput to fire")
	}
	// Must NOT be tagged as an interactive event.
	if k, _ := captured["kind"].(string); k == "ask_user_question" || k == "exit_plan_mode" {
		t.Errorf("non-interactive tool should not emit interactive kind, got %q", k)
	}
	if txt, _ := captured["text"].(string); txt != "[Bash: ls -la]" {
		t.Errorf("expected fallback text preserved, got %q", txt)
	}
}

// TestMakeWatcherCallback_PlainAssistantText covers the existing happy path:
// plain text assistant messages should keep flowing through unchanged.
func TestMakeWatcherCallback_PlainAssistantText(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	m := NewManager(s, t.TempDir())
	ag := NewTestAgent("plain-agent", "claude")

	var (
		mu       sync.Mutex
		captured map[string]any
	)
	m.SetOnOutput(func(agentID string, data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		captured = data
	})

	cb := m.MakeWatcherCallback(ag.ID, ag)
	cb(watcher.ConversationEvent{
		Role: "assistant",
		Text: "Hello, world!",
	})

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected onOutput to fire")
	}
	if txt, _ := captured["text"].(string); txt != "Hello, world!" {
		t.Errorf("expected text='Hello, world!', got %q", txt)
	}
	// Plain text should not be tagged with an interactive kind.
	if k, _ := captured["kind"].(string); k == "ask_user_question" || k == "exit_plan_mode" {
		t.Errorf("plain text should not emit interactive kind, got %q", k)
	}
}
