package tunnel

import (
	"testing"
	"time"
)

func TestClientStatusSnapshot(t *testing.T) {
	c := NewClient("https://hub.example", "tok", "127.0.0.1:7374", "ltok")

	s0 := c.Status()
	if s0.Connected {
		t.Fatalf("expected disconnected initially")
	}
	if !s0.ConnectedAt.IsZero() {
		t.Fatalf("expected zero connectedAt initially")
	}

	c.updateStatusConnected(250 * time.Millisecond)
	s1 := c.Status()
	if !s1.Connected {
		t.Fatalf("expected connected")
	}
	if s1.LastHandshakeDuration != 250*time.Millisecond {
		t.Fatalf("expected handshake duration 250ms, got %v", s1.LastHandshakeDuration)
	}
	if s1.ConnectedAt.IsZero() || s1.LastHandshakeAt.IsZero() {
		t.Fatalf("expected connected/handshake timestamps set")
	}

	c.updateLastCommunication()
	s2 := c.Status()
	if s2.LastCommunicationAt.IsZero() {
		t.Fatalf("expected last communication timestamp set")
	}

	c.updateStatusDisconnected(assertErr("boom"))
	s3 := c.Status()
	if s3.Connected {
		t.Fatalf("expected disconnected")
	}
	if s3.LastDisconnectedAt.IsZero() {
		t.Fatalf("expected lastDisconnectedAt set")
	}
	if s3.LastError != "boom" {
		t.Fatalf("expected lastError boom, got %q", s3.LastError)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

func assertErr(s string) error { return testErr(s) }
