package agent_test

import (
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/store"
)

func newTestEventManager(t *testing.T) (*agent.EventManager, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return agent.NewEventManager(s), s
}

func TestAppendAndPersistEvent(t *testing.T) {
	em, _ := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	data := map[string]any{"role": "user", "text": "hello"}
	seq := em.AppendAndPersistEvent(ag.ID, ag, data)
	if seq == 0 {
		t.Fatalf("expected seq > 0, got %d", seq)
	}

	// Verify event is in buffer
	events := ag.EventBuf().Since(0)
	if len(events) != 1 {
		t.Fatalf("expected 1 event in buffer, got %d", len(events))
	}
	if events[0].Data["text"] != "hello" {
		t.Errorf("expected text=hello, got %v", events[0].Data["text"])
	}
}

func TestUpdateOrAppendEvent_NewEvent(t *testing.T) {
	em, _ := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	data := map[string]any{"role": "assistant", "text": "hi", "msg_id": "msg-1"}
	seq, updated := em.UpdateOrAppendEvent(ag.ID, ag, data)
	if seq == 0 {
		t.Fatalf("expected seq > 0, got %d", seq)
	}
	if updated {
		t.Fatal("expected updated=false for new event")
	}

	events := ag.EventBuf().Since(0)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestUpdateOrAppendEvent_UpdatesExisting(t *testing.T) {
	em, _ := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	// First append
	data1 := map[string]any{"role": "assistant", "text": "partial", "msg_id": "msg-1"}
	seq1, _ := em.UpdateOrAppendEvent(ag.ID, ag, data1)

	// Update same msg_id
	data2 := map[string]any{"role": "assistant", "text": "completed", "msg_id": "msg-1"}
	seq2, updated := em.UpdateOrAppendEvent(ag.ID, ag, data2)

	if !updated {
		t.Fatal("expected updated=true")
	}
	if seq2 != seq1 {
		t.Fatalf("expected same seq, got %d vs %d", seq2, seq1)
	}

	events := ag.EventBuf().Since(0)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (updated in place), got %d", len(events))
	}
	if events[0].Data["text"] != "completed" {
		t.Errorf("expected updated text=completed, got %v", events[0].Data["text"])
	}
}

func TestRecordConversationEvent(t *testing.T) {
	em, _ := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	data := map[string]any{"role": "user", "text": "hello"}
	seq, err := em.RecordConversationEvent(ag.ID, ag, data)
	if err != nil {
		t.Fatalf("RecordConversationEvent failed: %v", err)
	}
	if seq == 0 {
		t.Fatalf("expected seq > 0, got %d", seq)
	}
}

func TestLoadPersistedEventsLatest(t *testing.T) {
	em, s := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	// Seed store directly
	_ = s.SaveConversationEvent(ag.ID, 1, map[string]any{"role": "user", "text": "a"})
	_ = s.SaveConversationEvent(ag.ID, 2, map[string]any{"role": "assistant", "text": "b"})

	events, err := em.LoadPersistedEventsLatest(ag.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Data["text"] != "a" {
		t.Errorf("expected first event text=a, got %v", events[0].Data["text"])
	}
}

func TestLoadPersistedEventsSince(t *testing.T) {
	em, s := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	_ = s.SaveConversationEvent(ag.ID, 1, map[string]any{"role": "user", "text": "a"})
	_ = s.SaveConversationEvent(ag.ID, 2, map[string]any{"role": "assistant", "text": "b"})
	_ = s.SaveConversationEvent(ag.ID, 3, map[string]any{"role": "user", "text": "c"})

	events, err := em.LoadPersistedEventsSince(ag.ID, 1, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsSince failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (after seq 1), got %d", len(events))
	}
}

func TestLoadPersistedEventsBefore(t *testing.T) {
	em, s := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	_ = s.SaveConversationEvent(ag.ID, 1, map[string]any{"role": "user", "text": "a"})
	_ = s.SaveConversationEvent(ag.ID, 2, map[string]any{"role": "assistant", "text": "b"})
	_ = s.SaveConversationEvent(ag.ID, 3, map[string]any{"role": "user", "text": "c"})

	events, err := em.LoadPersistedEventsBefore(ag.ID, 3, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsBefore failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (before seq 3), got %d", len(events))
	}
}

func TestLastPersistedSeq(t *testing.T) {
	em, s := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	seq, err := em.LastPersistedSeq(ag.ID)
	if err != nil {
		t.Fatalf("LastPersistedSeq failed: %v", err)
	}
	if seq != 0 {
		t.Fatalf("expected seq=0 for empty agent, got %d", seq)
	}

	_ = s.SaveConversationEvent(ag.ID, 5, map[string]any{"role": "user", "text": "x"})

	seq, err = em.LastPersistedSeq(ag.ID)
	if err != nil {
		t.Fatalf("LastPersistedSeq failed: %v", err)
	}
	if seq != 5 {
		t.Fatalf("expected seq=5, got %d", seq)
	}
}

func TestLastConversationEventTime(t *testing.T) {
	em, s := newTestEventManager(t)
	ag := agent.NewTestAgent("test-agent", "custom")

	tm, err := em.LastConversationEventTime(ag.ID)
	if err != nil {
		t.Fatalf("LastConversationEventTime failed: %v", err)
	}
	if !tm.IsZero() {
		t.Fatalf("expected zero time for empty agent, got %v", tm)
	}

	_ = s.SaveConversationEvent(ag.ID, 1, map[string]any{"role": "user", "text": "x"})

	tm, err = em.LastConversationEventTime(ag.ID)
	if err != nil {
		t.Fatalf("LastConversationEventTime failed: %v", err)
	}
	if tm.IsZero() {
		t.Fatal("expected non-zero time after saving event")
	}
}
