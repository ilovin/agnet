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

// TestMakeWatcherCallback_InjectsSessionID verifies that the watcher callback
// stamps the agent's current resume session ID onto every onOutput payload.
//
// This is the agentd half of the "messages disappear / show stale content
// after a Hermes /clear-style session switch" fix: the WS layer relies on
// data["sessionId"] to populate conversation.message.params.sessionId, and
// the Flutter app keys its conversation cache on (nodeId, agentId, sessionId).
// Omitting sessionId at the watcher boundary causes pushes for the live
// session to land in the wrong cache bucket and never render — the symptom
// reported as "agent app stuck on seq 242 while agentd DB advanced to 250".
//
// We exercise the three onOutput-emitting branches inside makeWatcherCallback:
//
//  1. Interactive tool_use (AskUserQuestion / ExitPlanMode)
//  2. Plain assistant text without msg_id (claude JSONL append path)
//  3. Streaming update via msg_id (opencode update-or-append path) — both the
//     "first append" map and the "_update" map fired on subsequent text growth
func TestMakeWatcherCallback_InjectsSessionID(t *testing.T) {
	const wantSession = "session-broadcast-fix-001"

	newSetup := func(t *testing.T, name string) (*Manager, *Agent, *sync.Mutex, *[]map[string]any) {
		t.Helper()
		s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		m := NewManager(s, t.TempDir())
		ag := NewTestAgent(name, "claude")
		ag.SetResumeSessionID(wantSession)

		var mu sync.Mutex
		var captured []map[string]any
		m.SetOnOutput(func(_ string, data map[string]any) {
			mu.Lock()
			defer mu.Unlock()
			// Defensive copy so subsequent in-place mutations by makeWatcherCallback
			// (e.g. data["seq"] = seq) do not retroactively change earlier captures.
			snapshot := make(map[string]any, len(data))
			for k, v := range data {
				snapshot[k] = v
			}
			captured = append(captured, snapshot)
		})
		return m, ag, &mu, &captured
	}

	t.Run("interactive tool_use carries sessionId", func(t *testing.T) {
		m, ag, mu, captured := newSetup(t, "ask-agent")
		cb := m.MakeWatcherCallback(ag.ID, ag)
		cb(watcher.ConversationEvent{
			Role:         "assistant",
			Text:         "[AskUserQuestion]",
			ToolUseName:  "AskUserQuestion",
			ToolUseID:    "toolu_ask_002",
			ToolUseInput: []byte(`{"questions":[{"question":"q","header":"H","multi_select":false,"options":[{"label":"A","description":"d"}]}]}`),
		})
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 1 {
			t.Fatalf("expected 1 onOutput, got %d", len(*captured))
		}
		if got, _ := (*captured)[0]["sessionId"].(string); got != wantSession {
			t.Errorf("interactive tool_use sessionId = %q, want %q (full: %v)", got, wantSession, (*captured)[0])
		}
	})

	t.Run("plain assistant text carries sessionId", func(t *testing.T) {
		m, ag, mu, captured := newSetup(t, "plain-agent")
		cb := m.MakeWatcherCallback(ag.ID, ag)
		cb(watcher.ConversationEvent{
			Role: "assistant",
			Text: "Hello after /clear",
		})
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 1 {
			t.Fatalf("expected 1 onOutput, got %d", len(*captured))
		}
		if got, _ := (*captured)[0]["sessionId"].(string); got != wantSession {
			t.Errorf("plain assistant sessionId = %q, want %q (full: %v)", got, wantSession, (*captured)[0])
		}
	})

	t.Run("streaming update path carries sessionId", func(t *testing.T) {
		m, ag, mu, captured := newSetup(t, "stream-agent")
		cb := m.MakeWatcherCallback(ag.ID, ag)
		// First emit creates the message.
		cb(watcher.ConversationEvent{
			Role:  "assistant",
			Text:  "partial",
			MsgID: "msg-stream-1",
		})
		// Second emit with same MsgID + grown text takes the _update branch.
		cb(watcher.ConversationEvent{
			Role:  "assistant",
			Text:  "partial and more",
			MsgID: "msg-stream-1",
		})

		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 2 {
			t.Fatalf("expected 2 onOutput captures, got %d (full: %v)", len(*captured), *captured)
		}
		// Append path.
		if got, _ := (*captured)[0]["sessionId"].(string); got != wantSession {
			t.Errorf("streaming append sessionId = %q, want %q (full: %v)", got, wantSession, (*captured)[0])
		}
		// _update path: the helper map fed to cb only carries _update/agentId/msg_id/text/seq today,
		// which is exactly the failure mode we are fixing — sessionId must be added there too.
		if isUpdate, _ := (*captured)[1]["_update"].(bool); !isUpdate {
			t.Fatalf("expected second capture to be an _update map, got %v", (*captured)[1])
		}
		if got, _ := (*captured)[1]["sessionId"].(string); got != wantSession {
			t.Errorf("streaming _update sessionId = %q, want %q (full: %v)", got, wantSession, (*captured)[1])
		}
	})
}

// TestMakeWatcherCallback_PlainAssistantText covers the existing happy path:// plain text assistant messages should keep flowing through unchanged.
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

// TestMakeWatcherCallback_ReasoningKindPassesThrough verifies that an opencode
// reasoning sub-event (Kind="thinking") propagates data["kind"]="thinking" to
// the client so the Flutter app renders it as a thinking block (#77).
func TestMakeWatcherCallback_ReasoningKindPassesThrough(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	m := NewManager(s, t.TempDir())
	ag := NewTestAgent("reason-agent", "opencode")

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
		Role:  "assistant",
		Text:  "Let me reason about this.",
		Kind:  "thinking",
		MsgID: "msg_a:part_r1",
	})

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected onOutput to fire")
	}
	if k, _ := captured["kind"].(string); k != "thinking" {
		t.Errorf("expected kind=thinking, got %q (full: %v)", k, captured)
	}
	if txt, _ := captured["text"].(string); txt != "Let me reason about this." {
		t.Errorf("expected reasoning text preserved, got %q", txt)
	}
}

// TestMakeWatcherCallback_ToolUseKindPassesThrough verifies that an opencode
// non-interactive tool sub-event (Kind="tool_use", ToolName="Bash") propagates
// data["kind"]="tool_use" and data["toolName"]="Bash" to the client so the
// Flutter app renders it as an activity block (#77).
func TestMakeWatcherCallback_ToolUseKindPassesThrough(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	m := NewManager(s, t.TempDir())
	ag := NewTestAgent("tool-agent", "opencode")

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
		Role:     "assistant",
		Text:     "[Bash: ls -la]",
		Kind:     "tool_use",
		ToolName: "Bash",
		MsgID:    "msg_a:part_t1",
	})

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected onOutput to fire")
	}
	if k, _ := captured["kind"].(string); k != "tool_use" {
		t.Errorf("expected kind=tool_use, got %q (full: %v)", k, captured)
	}
	if tn, _ := captured["toolName"].(string); tn != "Bash" {
		t.Errorf("expected toolName=Bash, got %q (full: %v)", tn, captured)
	}
	if txt, _ := captured["text"].(string); txt != "[Bash: ls -la]" {
		t.Errorf("expected tool text preserved, got %q", txt)
	}
	// Must NOT be tagged as interactive.
	if k, _ := captured["kind"].(string); k == "ask_user_question" || k == "exit_plan_mode" {
		t.Errorf("tool_use must not be interactive kind, got %q", k)
	}
}
