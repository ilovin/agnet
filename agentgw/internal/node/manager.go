package node

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/proxy"
	"github.com/phone-talk/agentgw/internal/tunnel"
	gossh "golang.org/x/crypto/ssh"
)

// EventCallback is called when a node's agentd pushes an event.
type EventCallback func(nodeID string, event map[string]any)

// Manager tracks all configured nodes and their runtime state.
type Manager struct {
	mu        sync.RWMutex
	nodes     map[string]*Node
	store     *nodecfg.Store
	agentdBin []byte
	onEvent   EventCallback
}

// NewManager creates a Manager backed by the given store.
// agentdBin is the embedded agentd binary (may be nil in tests).
func NewManager(store *nodecfg.Store, agentdBin []byte) *Manager {
	return &Manager{
		nodes:     make(map[string]*Node),
		store:     store,
		agentdBin: agentdBin,
	}
}

// OnEvent registers a callback for agentd push events (nodeId is injected into params).
func (m *Manager) OnEvent(cb EventCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = cb
}

// LoadAll populates nodes from persisted config without writing back to disk.
func (m *Manager) LoadAll(entries []nodecfg.NodeEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range entries {
		if entry.ID == "" {
			entry.ID = uuid.New().String()
		}
		m.nodes[entry.ID] = &Node{
			ID:         entry.ID,
			Name:       entry.Name,
			Host:       entry.Host,
			SSHPort:    entry.SSHPort,
			AgentdPort: entry.AgentdPort,
			Token:      entry.Token,
			SSHKeyPath: entry.SSHKeyPath,
			status:     StatusDisconnected,
		}
	}
}

// Add creates a new node entry (not yet connected) and persists it.
func (m *Manager) Add(entry nodecfg.NodeEntry) (string, error) {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	n := &Node{
		ID:         entry.ID,
		Name:       entry.Name,
		Host:       entry.Host,
		SSHPort:    entry.SSHPort,
		AgentdPort: entry.AgentdPort,
		Token:      entry.Token,
		SSHKeyPath: entry.SSHKeyPath,
		status:     StatusDisconnected,
	}
	m.mu.Lock()
	m.nodes[n.ID] = n
	m.mu.Unlock()

	entries := m.toEntries()
	if err := m.store.Save(entries); err != nil {
		return n.ID, fmt.Errorf("save nodes: %w", err)
	}
	return n.ID, nil
}

// Connect establishes an SSH tunnel + WS proxy to a node's agentd.
func (m *Manager) Connect(id string) error {
	n := m.Get(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	n.SetStatus(StatusConnecting)

	keyPath := n.SSHKeyPath
	if keyPath == "" {
		keyPath = os.ExpandEnv("$HOME/.ssh/id_rsa")
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("read ssh key %s: %w", keyPath, err)
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("parse ssh key: %w", err)
	}

	tun, err := tunnel.New(tunnel.Config{
		SSHHost:    n.Host,
		SSHPort:    n.SSHPort,
		RemoteHost: "127.0.0.1",
		RemotePort: n.AgentdPort,
		AuthMethod: gossh.PublicKeys(signer),
	})
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("ssh tunnel: %w", err)
	}

	localPort, err := tun.LocalPort()
	if err != nil {
		tun.Close()
		n.SetStatus(StatusError)
		return fmt.Errorf("local port: %w", err)
	}

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", localPort)
	p, err := proxy.New(wsURL, n.Token)
	if err != nil {
		tun.Close()
		n.SetStatus(StatusError)
		return fmt.Errorf("ws proxy: %w", err)
	}

	m.mu.RLock()
	cb := m.onEvent
	m.mu.RUnlock()
	if cb != nil {
		p.OnEvent(func(ev map[string]any) {
			if params, ok := ev["params"].(map[string]any); ok {
				params["nodeId"] = n.ID
			}
			cb(n.ID, ev)
		})
	}

	n.SetProxy(p)
	n.SetStatus(StatusConnected)
	return nil
}

// ConnectAll attempts to connect all disconnected nodes in parallel.
func (m *Manager) ConnectAll() {
	for _, n := range m.List() {
		if n.GetStatus() == StatusDisconnected {
			go func(id string) {
				if err := m.Connect(id); err != nil {
					log.Printf("connect node %s: %v", id, err)
				}
			}(n.ID)
		}
	}
}

// Remove disconnects and deletes a node, then persists the updated list.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	n, ok := m.nodes[id]
	if ok {
		if p := n.GetProxy(); p != nil {
			p.Close()
		}
		delete(m.nodes, id)
	}
	m.mu.Unlock()

	return m.store.Save(m.toEntries())
}

// Get returns a node by ID or nil if not found.
func (m *Manager) Get(id string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes[id]
}

// List returns a snapshot slice of all nodes.
func (m *Manager) List() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out
}

// ForwardCall sends a JSON-RPC call to a specific node's agentd via its proxy.
func (m *Manager) ForwardCall(nodeID, method string, params map[string]any, timeout time.Duration) (any, error) {
	n := m.Get(nodeID)
	if n == nil {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}
	p := n.GetProxy()
	if p == nil {
		return nil, fmt.Errorf("node %q not connected", nodeID)
	}
	return p.Call(method, params, timeout)
}

// toEntries converts the in-memory node map to a slice for persistence.
func (m *Manager) toEntries() []nodecfg.NodeEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]nodecfg.NodeEntry, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, nodecfg.NodeEntry{
			ID:         n.ID,
			Name:       n.Name,
			Host:       n.Host,
			SSHPort:    n.SSHPort,
			AgentdPort: n.AgentdPort,
			SSHKeyPath: n.SSHKeyPath,
			Token:      n.Token,
		})
	}
	return out
}
