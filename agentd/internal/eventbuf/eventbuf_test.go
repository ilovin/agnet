package eventbuf_test

import (
	"testing"

	"github.com/phone-talk/agentd/internal/eventbuf"
)

func TestAppendAndSince(t *testing.T) {
	buf := eventbuf.New(100)

	buf.Append(map[string]any{"type": "a"})
	buf.Append(map[string]any{"type": "b"})
	buf.Append(map[string]any{"type": "c"})

	events := buf.Since(0)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Seq != 1 {
		t.Errorf("expected seq 1, got %d", events[0].Seq)
	}
	if events[2].Seq != 3 {
		t.Errorf("expected seq 3, got %d", events[2].Seq)
	}

	// Since(2) should return only event 3
	partial := buf.Since(2)
	if len(partial) != 1 {
		t.Fatalf("expected 1 event after seq 2, got %d", len(partial))
	}
	if partial[0].Seq != 3 {
		t.Errorf("expected seq 3, got %d", partial[0].Seq)
	}
}

func TestCapEviction(t *testing.T) {
	buf := eventbuf.New(3)
	for i := 0; i < 5; i++ {
		buf.Append(map[string]any{"i": i})
	}
	// Only last 3 should be retained
	events := buf.Since(0)
	if len(events) != 3 {
		t.Fatalf("expected 3 events after eviction, got %d", len(events))
	}
	if events[0].Seq != 3 {
		t.Errorf("expected oldest retained seq=3, got %d", events[0].Seq)
	}
}

func TestSinceReturnsEmpty(t *testing.T) {
	buf := eventbuf.New(100)
	buf.Append(map[string]any{"type": "x"})
	events := buf.Since(1) // already have seq 1, nothing newer
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestUpdateOrAppend(t *testing.T) {
	buf := eventbuf.New(100)

	// Append first event with msg_id
	seq1, updated := buf.UpdateOrAppend("msg1", map[string]any{
		"msg_id": "msg1",
		"role":   "assistant",
		"text":   "Hello",
	})
	if updated {
		t.Error("expected first append to not be an update")
	}
	if seq1 != 1 {
		t.Errorf("expected seq 1, got %d", seq1)
	}

	// Append second event
	seq2, _ := buf.UpdateOrAppend("msg2", map[string]any{
		"msg_id": "msg2",
		"role":   "user",
		"text":   "Hi",
	})
	if seq2 != 2 {
		t.Errorf("expected seq 2, got %d", seq2)
	}

	// Update msg1 with new text
	seq1Updated, wasUpdate := buf.UpdateOrAppend("msg1", map[string]any{
		"msg_id": "msg1",
		"role":   "assistant",
		"text":   "Hello, world!",
	})
	if !wasUpdate {
		t.Error("expected update to return true")
	}
	if seq1Updated != seq1 {
		t.Errorf("expected seq %d (unchanged), got %d", seq1, seq1Updated)
	}

	// Verify the update took effect
	events := buf.Since(0)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Find msg1
	for _, e := range events {
		if e.Data["msg_id"] == "msg1" {
			if e.Data["text"] != "Hello, world!" {
				t.Errorf("expected updated text, got %v", e.Data["text"])
			}
			if e.Seq != 1 {
				t.Errorf("expected seq 1 after update, got %d", e.Seq)
			}
		}
	}

	// No msg_id → normal append
	seq3, wasUpdate3 := buf.UpdateOrAppend("", map[string]any{
		"role": "assistant",
		"text": "No msg_id",
	})
	if wasUpdate3 {
		t.Error("expected no update for empty msg_id")
	}
	if seq3 != 3 {
		t.Errorf("expected seq 3, got %d", seq3)
	}
}
