package pty_test

import (
	"strings"
	"testing"
	"time"

	agentpty "github.com/phone-talk/agentd/internal/pty"
)

func TestSpawnAndRead(t *testing.T) {
	p, err := agentpty.Spawn("echo", []string{"hello agentd"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	defer p.Kill()

	// Collect output for up to 2 seconds
	out := collectOutput(p, 2*time.Second)
	if !strings.Contains(out, "hello agentd") {
		t.Errorf("expected 'hello agentd' in output, got: %q", out)
	}
}

func TestKill(t *testing.T) {
	p, err := agentpty.Spawn("sleep", []string{"60"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Errorf("Kill failed: %v", err)
	}
	// Wait should return (process ended)
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Wait did not return after Kill")
	}
}

func collectOutput(p *agentpty.Process, timeout time.Duration) string {
	var sb strings.Builder
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		p.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := p.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}
