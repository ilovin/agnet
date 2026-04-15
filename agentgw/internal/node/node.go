package node

import (
	"sync"

	"github.com/phone-talk/agentgw/internal/proxy"
	"github.com/phone-talk/agentgw/internal/tunnel"
)

type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusDeploying    Status = "deploying"
	StatusError        Status = "error"
)

// Node is the runtime representation of a managed remote node.
type Node struct {
	ID         string
	Name       string
	Host       string
	SSHPort    int
	AgentdPort int
	Token      string
	SSHKeyPath string
	SSHAlias   string // SSH config alias (e.g., "ws")

	mu     sync.RWMutex
	status Status // private — use GetStatus()/SetStatus()
	proxy  *proxy.Proxy
	tunnel *tunnel.Tunnel
}

// SetStatus updates status under the write lock.
func (n *Node) SetStatus(s Status) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status = s
}

// GetStatus reads status under the read lock.
func (n *Node) GetStatus() Status {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.status
}

// GetProxy returns the node's WS proxy client under the read lock.
func (n *Node) GetProxy() *proxy.Proxy {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.proxy
}

// SetProxy stores the node's WS proxy client under the write lock.
func (n *Node) SetProxy(p *proxy.Proxy) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.proxy = p
}

// GetTunnel returns the node's SSH tunnel under the read lock.
func (n *Node) GetTunnel() *tunnel.Tunnel {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.tunnel
}

// SetTunnel stores the node's SSH tunnel under the write lock.
func (n *Node) SetTunnel(t *tunnel.Tunnel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tunnel = t
}

// IsLocal returns true if the node is on localhost.
func (n *Node) IsLocal() bool {
	switch n.Host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// DisplayLocation returns a human-readable location string.
// For local nodes: "localhost"
// For remote nodes: "sshAlias (host)" or just "host"
func (n *Node) DisplayLocation() string {
	if n.IsLocal() {
		return "localhost"
	}
	if n.SSHAlias != "" {
		return n.SSHAlias + " (" + n.Host + ")"
	}
	return n.Host
}
