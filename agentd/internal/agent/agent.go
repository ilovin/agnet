package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/phone-talk/agentd/internal/eventbuf"
	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/watcher"
)

type Status string

const (
	StatusCreated  Status = "created"
	StatusStarting Status = "starting"
	StatusIdle     Status = "idle"
	StatusWorking  Status = "working"
	StatusStopped  Status = "stopped"
	StatusCrashed  Status = "crashed"
)

// Agent represents a single managed AI agent process.
type Agent struct {
	ID       string
	Name     string
	Provider string
	WorkDir  string
	Cmd      string   // original command used to spawn this agent
	Args     []string // original args used to spawn this agent

	mu                       sync.RWMutex
	status                   Status
	process                  *agentpty.Process
	buf                      *eventbuf.EventBuffer
	w                        *watcher.ClaudeWatcher
	permissionPromptActive    bool
	permissionPromptResolved  bool      // once resolved, suppress re-detection
	permissionResolvedAt      time.Time // when the prompt was resolved
	permissionManager        *PermissionManager // manages permission requests
}

func newAgent(id, name, provider, workDir, cmd string, args []string) *Agent {
	return &Agent{
		ID:                id,
		Name:              name,
		Provider:          provider,
		WorkDir:           workDir,
		Cmd:               cmd,
		Args:              args,
		status:            StatusCreated,
		buf:               eventbuf.New(10000),
		permissionManager: NewPermissionManager(),
	}
}

func (a *Agent) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Agent) setStatus(s Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = s
}

// Buffer returns the EventBuffer for this agent (typed as interface{} to avoid import cycle in tests).
func (a *Agent) Buffer() interface{} {
	return a.buf
}

// EventBuf returns the typed EventBuffer.
func (a *Agent) EventBuf() *eventbuf.EventBuffer {
	return a.buf
}

// AppendEvent adds a conversation event to this agent's EventBuffer.
func (a *Agent) AppendEvent(data map[string]any) uint64 {
	return a.buf.Append(data)
}

func (a *Agent) setProcess(p *agentpty.Process) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.process = p
}

func (a *Agent) setWatcher(w *watcher.ClaudeWatcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.w = w
}

func (a *Agent) setPermissionPromptActive(active bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionPromptActive = active
}

func (a *Agent) SetPermissionPromptActive(active bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionPromptActive = active
	if !active {
		// Mark as resolved so we don't re-trigger on TUI footer text immediately.
		a.permissionPromptResolved = true
		a.permissionResolvedAt = time.Now()
	}
}

func (a *Agent) PermissionPromptActive() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.permissionPromptActive
}

// PermissionPromptResolved returns true if the permission prompt was recently
// resolved (within 10s), to suppress re-detection from stale TUI footer output.
func (a *Agent) PermissionPromptResolved() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.permissionPromptResolved {
		return false
	}
	// Auto-expire suppression window so future real prompts can still be detected.
	return time.Since(a.permissionResolvedAt) < 10*time.Second
}

func (a *Agent) kill() {
	a.mu.RLock()
	p := a.process
	w := a.w
	a.mu.RUnlock()
	if w != nil {
		w.Stop()
	}
	if p != nil {
		_ = p.Kill()
	}
}

// Process returns the underlying PTY process
func (a *Agent) Process() *agentpty.Process {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.process
}

// PermissionManager returns the permission manager for this agent
func (a *Agent) PermissionManager() *PermissionManager {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.permissionManager
}

// WriteInput sends text to the agent's PTY stdin (as if typed by the user).
func (a *Agent) WriteInput(text string) error {
	a.mu.RLock()
	p := a.process
	w := a.w
	a.mu.RUnlock()
	if p == nil {
		// Check if this is an attached agent (has watcher but no process)
		if w != nil {
			return fmt.Errorf("attached agents are read-only; use the original terminal to interact")
		}
		return fmt.Errorf("agent process not running")
	}
	_, err := p.Write([]byte(text))
	return err
}
