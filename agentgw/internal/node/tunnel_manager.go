package node

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/phone-talk/agentgw/internal/tunnel"
)

// TunnelOpener opens an SSH tunnel for a given config.
type TunnelOpener func(cfg tunnel.Config) (*tunnel.Tunnel, error)

// TunnelManager handles SSH connection lifecycle.
type TunnelManager struct {
	open TunnelOpener
}

// NewTunnelManager creates a TunnelManager with the real tunnel.New opener.
func NewTunnelManager() *TunnelManager {
	return &TunnelManager{open: tunnel.New}
}

// NewTunnelManagerWithOpener creates a TunnelManager with a custom opener (for tests).
func NewTunnelManagerWithOpener(opener TunnelOpener) *TunnelManager {
	return &TunnelManager{open: opener}
}

// Connect establishes an SSH tunnel for the node and returns the WebSocket URL.
// For localhost/127.0.0.1, it returns a direct WS URL without creating a tunnel.
func (tm *TunnelManager) Connect(n *Node) (string, error) {
	// Clean up any existing tunnel
	if existing := n.GetTunnel(); existing != nil {
		_ = existing.Close()
		n.SetTunnel(nil)
	}

	// For localhost, connect directly without SSH tunnel
	if n.IsLocal() {
		return fmt.Sprintf("ws://%s:%d/ws", n.Host, n.AgentdPort), nil
	}

	tunCfg := tunnel.Config{
		SSHHost:    n.Host,
		SSHPort:    n.SSHPort,
		RemoteHost: "127.0.0.1",
		RemotePort: n.AgentdPort,
		SSHKeyPath: n.SSHKeyPath,
		SSHAlias:   n.SSHAlias,
	}

	// If SSHAlias is set, use it; otherwise use key-based auth
	if tunCfg.SSHAlias == "" {
		tunCfg.SSHKeyPath = resolveSSHKeyPath(tunCfg.SSHKeyPath)
	}

	tun, err := tm.open(tunCfg)
	if err != nil {
		n.SetStatus(StatusError)
		return "", fmt.Errorf("ssh tunnel: %w", err)
	}

	localPort, err := tun.LocalPort()
	if err != nil {
		tun.Close()
		n.SetStatus(StatusError)
		return "", fmt.Errorf("local port: %w", err)
	}
	n.SetTunnel(tun)

	return fmt.Sprintf("ws://127.0.0.1:%d/ws", localPort), nil
}

// Disconnect closes the node's SSH tunnel and clears it.
func (tm *TunnelManager) Disconnect(n *Node) {
	if tun := n.GetTunnel(); tun != nil {
		_ = tun.Close()
		n.SetTunnel(nil)
	}
}

// HealthCheck reconnects the tunnel if the node is disconnected or in error state.
func (tm *TunnelManager) HealthCheck(n *Node) (string, error) {
	st := n.GetStatus()
	if st == StatusError || st == StatusDisconnected {
		return tm.Connect(n)
	}
	return "", nil
}

// resolveSSHKeyPath returns the first existing SSH private key from common locations.
func resolveSSHKeyPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[0] // fallback, will fail later with a clear error
}
