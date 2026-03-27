package agent

import (
	"crypto/rand"
	"fmt"
	"sync"

	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// newUUID generates a random UUID v4 string without external dependencies.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Manager creates, tracks, and controls Agent instances.
type Manager struct {
	mu      sync.RWMutex
	agents  map[string]*Agent
	store   *store.Store
	dataDir string
}

func NewManager(s *store.Store, dataDir string) *Manager {
	return &Manager{
		agents:  make(map[string]*Agent),
		store:   s,
		dataDir: dataDir,
	}
}

// Create spawns a new agent process using the given command/args.
func (m *Manager) Create(name, cmd string, args []string, workDir string) (string, error) {
	id := newUUID()
	ag := newAgent(id, name, "custom", workDir, cmd, args)

	m.mu.Lock()
	m.agents[id] = ag
	m.mu.Unlock()

	_ = m.store.SaveAgent(store.AgentRecord{
		ID: id, Name: name, Provider: "custom", WorkDir: workDir,
	})

	ag.setStatus(StatusStarting)

	p, err := agentpty.Spawn(cmd, args, workDir, nil)
	if err != nil {
		ag.setStatus(StatusCrashed)
		return id, fmt.Errorf("spawn: %w", err)
	}
	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	go func() {
		_ = p.Wait()
		ag.setStatus(StatusStopped)
	}()

	return id, nil
}

// CreateWithWatcher spawns an agent and starts a ClaudeWatcher on sessionFile.
func (m *Manager) CreateWithWatcher(name, cmd string, args []string, workDir, sessionFile string) (string, error) {
	id, err := m.Create(name, cmd, args, workDir)
	if err != nil {
		return id, err
	}
	ag := m.Get(id)
	if ag == nil {
		return id, nil
	}

	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {
		data := map[string]any{
			"role": e.Role,
			"text": e.Text,
		}
		if e.StatusChange != nil {
			data["statusChange"] = string(*e.StatusChange)
			if *e.StatusChange == watcher.StatusWorking {
				ag.setStatus(StatusWorking)
			} else {
				ag.setStatus(StatusIdle)
			}
		}
		ag.AppendEvent(data)
	})

	if err := w.Start(); err != nil {
		return id, fmt.Errorf("watcher start: %w", err)
	}
	ag.setWatcher(w)
	return id, nil
}

// List returns a snapshot of all agents.
func (m *Manager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Agent, 0, len(m.agents))
	for _, ag := range m.agents {
		out = append(out, ag)
	}
	return out
}

// Get returns an agent by ID or nil.
func (m *Manager) Get(id string) *Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agents[id]
}

// Stop kills the agent process.
func (m *Manager) Stop(id string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}
	ag.kill()
	ag.setStatus(StatusStopped)
	return nil
}

// Remove stops and removes the agent from tracking.
func (m *Manager) Remove(id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.agents, id)
	m.mu.Unlock()
	return m.store.DeleteAgent(id)
}
