package store_test

import (
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentd/internal/store"
)

func TestSaveAndLoad(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ag := store.AgentRecord{
		ID:              "agent-1",
		Name:            "my claude",
		Provider:        "claude-code",
		WorkDir:         "/tmp/proj",
		ResumeSessionID: "",
	}
	if err := s.SaveAgent(ag); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != "agent-1" {
		t.Errorf("expected id=agent-1, got %q", agents[0].ID)
	}
}

func TestUpdateResumeSessionID(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ag := store.AgentRecord{ID: "agent-2", Name: "x", Provider: "claude-code", WorkDir: "/tmp"}
	if err := s.SaveAgent(ag); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}

	if err := s.UpdateResumeSessionID("agent-2", "sess-abc"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if agents[0].ResumeSessionID != "sess-abc" {
		t.Errorf("expected sess-abc, got %q", agents[0].ResumeSessionID)
	}
}

func TestClearConversationEvents(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveConversationEvent("agent-1", 1, map[string]any{"role": "assistant", "text": "old"}); err != nil {
		t.Fatalf("SaveConversationEvent failed: %v", err)
	}
	if err := s.ClearConversationEvents("agent-1"); err != nil {
		t.Fatalf("ClearConversationEvents failed: %v", err)
	}

	events, err := s.ListConversationEventsLatest("agent-1", 10)
	if err != nil {
		t.Fatalf("ListConversationEventsLatest failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events after clear, got %d", len(events))
	}
}

func TestDeleteAgent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveAgent(store.AgentRecord{ID: "del-1", Name: "x", Provider: "claude-code", WorkDir: "/tmp"}); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}
	if err := s.DeleteAgent("del-1"); err != nil {
		t.Fatalf("DeleteAgent failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after delete, got %d", len(agents))
	}
}

// TestConversationEventPayloadRoundtrip verifies that camelCase payload fields
// (askUserQuestion, exitPlanMode) survive a save → load cycle via all three
// List functions. This is a regression test for the Gap 2 fix (T2b).
func TestConversationEventPayloadRoundtrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	askPayload := map[string]any{
		"tool_use_id": "toolu_01",
		"questions": []any{
			map[string]any{"question": "Are you sure?", "multi_select": false, "options": []any{}},
		},
	}
	exitPayload := map[string]any{
		"tool_use_id": "toolu_02",
		"plan":        "Step 1: ...",
	}

	data1 := map[string]any{
		"role":            "assistant",
		"raw":             false,
		"kind":            "ask_user_question",
		"askUserQuestion": askPayload,
	}
	data2 := map[string]any{
		"role":         "assistant",
		"raw":          false,
		"kind":         "exit_plan_mode",
		"exitPlanMode": exitPayload,
	}

	if err := s.SaveConversationEvent("ag1", 1, data1); err != nil {
		t.Fatalf("save data1: %v", err)
	}
	if err := s.SaveConversationEvent("ag1", 2, data2); err != nil {
		t.Fatalf("save data2: %v", err)
	}

	// --- ListConversationEventsLatest ---
	latest, err := s.ListConversationEventsLatest("ag1", 10)
	if err != nil {
		t.Fatalf("ListConversationEventsLatest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("expected 2, got %d", len(latest))
	}
	if latest[0].Kind != "ask_user_question" {
		t.Errorf("kind: got %q, want ask_user_question", latest[0].Kind)
	}
	if latest[0].Payload == nil || latest[0].Payload["askUserQuestion"] == nil {
		t.Errorf("askUserQuestion payload missing from Latest; Payload=%v", latest[0].Payload)
	}
	if latest[1].Payload == nil || latest[1].Payload["exitPlanMode"] == nil {
		t.Errorf("exitPlanMode payload missing from Latest; Payload=%v", latest[1].Payload)
	}

	// --- ListConversationEventsSince ---
	since, err := s.ListConversationEventsSince("ag1", 0, 10)
	if err != nil {
		t.Fatalf("ListConversationEventsSince: %v", err)
	}
	if len(since) != 2 {
		t.Fatalf("expected 2, got %d", len(since))
	}
	if since[0].Payload == nil || since[0].Payload["askUserQuestion"] == nil {
		t.Errorf("askUserQuestion missing from Since; Payload=%v", since[0].Payload)
	}

	// --- ListConversationEventsBefore ---
	before, err := s.ListConversationEventsBefore("ag1", 3, 10)
	if err != nil {
		t.Fatalf("ListConversationEventsBefore: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("expected 2, got %d", len(before))
	}
	if before[1].Payload == nil || before[1].Payload["exitPlanMode"] == nil {
		t.Errorf("exitPlanMode missing from Before; Payload=%v", before[1].Payload)
	}
}
