package node

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/phone-talk/agentgw/internal/nodecfg"
)

// Registry manages in-memory node metadata (Add, Remove, Rename, List, Get).
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{nodes: make(map[string]*Node)}
}

// Add creates a new Node from the entry, stores it, and returns its ID.
func (r *Registry) Add(entry nodecfg.NodeEntry) (string, error) {
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
		SSHAlias:   entry.SSHAlias,
		status:     StatusDisconnected,
	}
	r.mu.Lock()
	r.nodes[n.ID] = n
	r.mu.Unlock()
	return n.ID, nil
}

// LoadAll populates nodes from persisted config without writing back to disk.
func (r *Registry) LoadAll(entries []nodecfg.NodeEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if entry.ID == "" {
			entry.ID = uuid.New().String()
		}
		r.nodes[entry.ID] = &Node{
			ID:         entry.ID,
			Name:       entry.Name,
			Host:       entry.Host,
			SSHPort:    entry.SSHPort,
			AgentdPort: entry.AgentdPort,
			Token:      entry.Token,
			SSHKeyPath: entry.SSHKeyPath,
			SSHAlias:   entry.SSHAlias,
			status:     StatusDisconnected,
		}
	}
}

// Get returns a node by ID or nil if not found.
func (r *Registry) Get(id string) *Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodes[id]
}

// List returns a snapshot slice of all nodes.
func (r *Registry) List() []*Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	return out
}

// Rename updates the display name of a node.
func (r *Registry) Rename(id, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return fmt.Errorf("node %q not found", id)
	}
	n.Name = name
	return nil
}

// Remove deletes a node from the registry.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return fmt.Errorf("node %q not found", id)
	}
	delete(r.nodes, id)
	return nil
}

// ToEntries converts the in-memory node map to a slice for persistence.
func (r *Registry) ToEntries() []nodecfg.NodeEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]nodecfg.NodeEntry, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, nodecfg.NodeEntry{
			ID:         n.ID,
			Name:       n.Name,
			Host:       n.Host,
			SSHPort:    n.SSHPort,
			AgentdPort: n.AgentdPort,
			SSHKeyPath: n.SSHKeyPath,
			Token:      n.Token,
			SSHAlias:   n.SSHAlias,
		})
	}
	return out
}
