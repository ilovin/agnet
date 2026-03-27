package node

import (
	"sync"

	"github.com/phone-talk/agentgw/internal/proxy"
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
	Status     Status // readable directly; use SetStatus for thread-safe updates

	mu    sync.RWMutex
	proxy *proxy.Proxy
}

// SetStatus updates Status under the write lock.
func (n *Node) SetStatus(s Status) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Status = s
}

// GetStatus reads Status under the read lock.
func (n *Node) GetStatus() Status {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Status
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
