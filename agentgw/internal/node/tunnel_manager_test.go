package node

import (
	"testing"

	"github.com/phone-talk/agentgw/internal/tunnel"
)

// fakeTunnelOpener is a test double for tunnel opening.
type fakeTunnelOpener struct {
	callCount int
	lastCfg   tunnel.Config
	returnTun *tunnel.Tunnel
	returnErr error
}

func (f *fakeTunnelOpener) Open(cfg tunnel.Config) (*tunnel.Tunnel, error) {
	f.callCount++
	f.lastCfg = cfg
	return f.returnTun, f.returnErr
}

func TestTunnelManagerConnectLocal(t *testing.T) {
	tm := NewTunnelManager()
	n := &Node{Host: "127.0.0.1", AgentdPort: 7373}

	wsURL, err := tm.Connect(n)
	if err != nil {
		t.Fatalf("Connect local failed: %v", err)
	}
	expected := "ws://127.0.0.1:7373/ws"
	if wsURL != expected {
		t.Errorf("expected wsURL %q, got %q", expected, wsURL)
	}
	if n.GetTunnel() != nil {
		t.Error("expected no tunnel for local node")
	}
}

func TestTunnelManagerConnectLocalhost(t *testing.T) {
	tm := NewTunnelManager()
	n := &Node{Host: "localhost", AgentdPort: 8080}

	wsURL, err := tm.Connect(n)
	if err != nil {
		t.Fatalf("Connect localhost failed: %v", err)
	}
	expected := "ws://localhost:8080/ws"
	if wsURL != expected {
		t.Errorf("expected wsURL %q, got %q", expected, wsURL)
	}
}

func TestTunnelManagerConnectRemote(t *testing.T) {
	fakeTun := &tunnel.Tunnel{}
	opener := &fakeTunnelOpener{returnTun: fakeTun}
	tm := NewTunnelManagerWithOpener(opener.Open)
	n := &Node{Host: "192.168.1.10", SSHPort: 22, AgentdPort: 7373, Token: "tok", SSHKeyPath: "/key"}

	wsURL, err := tm.Connect(n)
	if err != nil {
		t.Fatalf("Connect remote failed: %v", err)
	}
	if opener.callCount != 1 {
		t.Fatalf("expected 1 tunnel open, got %d", opener.callCount)
	}
	if opener.lastCfg.SSHHost != "192.168.1.10" {
		t.Errorf("expected SSHHost 192.168.1.10, got %q", opener.lastCfg.SSHHost)
	}
	if opener.lastCfg.SSHPort != 22 {
		t.Errorf("expected SSHPort 22, got %d", opener.lastCfg.SSHPort)
	}
	if n.GetTunnel() != fakeTun {
		t.Error("expected tunnel stored on node")
	}
	if wsURL == "" {
		t.Error("expected non-empty wsURL")
	}
}

func TestTunnelManagerConnectRemoteWithAlias(t *testing.T) {
	fakeTun := &tunnel.Tunnel{}
	opener := &fakeTunnelOpener{returnTun: fakeTun}
	tm := NewTunnelManagerWithOpener(opener.Open)
	n := &Node{Host: "192.168.1.10", SSHAlias: "ws", SSHPort: 22, AgentdPort: 7373, Token: "tok"}

	_, err := tm.Connect(n)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if opener.lastCfg.SSHAlias != "ws" {
		t.Errorf("expected SSHAlias ws, got %q", opener.lastCfg.SSHAlias)
	}
}

func TestTunnelManagerConnectClearsExisting(t *testing.T) {
	oldTun := &tunnel.Tunnel{}
	opener := &fakeTunnelOpener{returnTun: &tunnel.Tunnel{}}
	tm := NewTunnelManagerWithOpener(opener.Open)
	n := &Node{Host: "192.168.1.10", AgentdPort: 7373, Token: "tok"}
	n.SetTunnel(oldTun)

	_, err := tm.Connect(n)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if n.GetTunnel() == oldTun {
		t.Error("expected old tunnel to be replaced")
	}
}

func TestTunnelManagerOpenError(t *testing.T) {
	opener := &fakeTunnelOpener{returnErr: errTest("tunnel fail")}
	tm := NewTunnelManagerWithOpener(opener.Open)
	n := &Node{Host: "192.168.1.10", AgentdPort: 7373, Token: "tok"}

	_, err := tm.Connect(n)
	if err == nil {
		t.Fatal("expected error")
	}
	if n.GetStatus() != StatusError {
		t.Errorf("expected status error, got %s", n.GetStatus())
	}
}

func TestTunnelManagerDisconnect(t *testing.T) {
	fakeTun := &tunnel.Tunnel{}
	tm := NewTunnelManager()
	n := &Node{Host: "192.168.1.10", AgentdPort: 7373, Token: "tok"}
	n.SetTunnel(fakeTun)

	tm.Disconnect(n)
	if n.GetTunnel() != nil {
		t.Error("expected tunnel cleared after disconnect")
	}
}

func TestTunnelManagerDisconnectNoTunnel(t *testing.T) {
	tm := NewTunnelManager()
	n := &Node{Host: "192.168.1.10", AgentdPort: 7373, Token: "tok"}

	// Should not panic
	tm.Disconnect(n)
}

func TestTunnelManagerHealthCheck(t *testing.T) {
	fakeTun := &tunnel.Tunnel{}
	opener := &fakeTunnelOpener{returnTun: fakeTun}
	tm := NewTunnelManagerWithOpener(opener.Open)
	n := &Node{Host: "192.168.1.10", AgentdPort: 7373, Token: "tok"}

	// First connect
	_, _ = tm.Connect(n)
	if opener.callCount != 1 {
		t.Fatalf("expected 1 open, got %d", opener.callCount)
	}

	// Health check on disconnected node should reconnect
	n.SetStatus(StatusDisconnected)
	_, _ = tm.HealthCheck(n)
	if opener.callCount != 2 {
		t.Fatalf("expected 2 opens after health check, got %d", opener.callCount)
	}
}

func TestTunnelManagerHealthCheckConnected(t *testing.T) {
	tm := NewTunnelManager()
	n := &Node{Host: "192.168.1.10", AgentdPort: 7373, Token: "tok"}
	n.SetStatus(StatusConnected)
	n.SetTunnel(&tunnel.Tunnel{})

	_, err := tm.HealthCheck(n)
	if err != nil {
		t.Fatalf("HealthCheck on connected node should not error: %v", err)
	}
}

func errTest(msg string) error {
	return &testError{msg}
}

type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }
