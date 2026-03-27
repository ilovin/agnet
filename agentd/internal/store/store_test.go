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
	s.SaveAgent(ag)

	if err := s.UpdateResumeSessionID("agent-2", "sess-abc"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}

	agents, _ := s.ListAgents()
	if agents[0].ResumeSessionID != "sess-abc" {
		t.Errorf("expected sess-abc, got %q", agents[0].ResumeSessionID)
	}
}

func TestDeleteAgent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.SaveAgent(store.AgentRecord{ID: "del-1", Name: "x", Provider: "claude-code", WorkDir: "/tmp"})
	s.DeleteAgent("del-1")

	agents, _ := s.ListAgents()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after delete, got %d", len(agents))
	}
}
