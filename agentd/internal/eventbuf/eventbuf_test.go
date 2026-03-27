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
