package agent

import (
	"fmt"
	"os/exec"
	"strings"
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

// StatusChangeCallback is called when an agent's status changes.
type StatusChangeCallback func(agentID string, oldStatus, newStatus Status)

// Agent represents a single managed AI agent process.
type Agent struct {
	ID       string
	Name     string
	Provider string
	WorkDir  string
	Cmd      string   // original command used to spawn this agent
	Args     []string // original args used to spawn this agent
	PID      int      // process ID, only set for attached agents

	mu                      sync.RWMutex
	status                  Status
	process                 *agentpty.Process
	buf                     *eventbuf.EventBuffer
	w                       watcher.SessionWatcher
	permissionPromptActive  bool
	permissionPromptResolved bool      // once resolved, suppress re-detection
	permissionResolvedAt     time.Time // when the prompt was resolved
	permissionManager       *PermissionManager // manages permission requests

	// Attached-session input routing metadata.
	attachMode           string
	attachReadOnly       bool
	attachReadOnlyReason string
	tmuxTarget           string

	// Status change callback (set by Manager)
	onStatusChange StatusChangeCallback
}

// SetOnStatusChange registers a callback for status changes.
func (a *Agent) SetOnStatusChange(cb StatusChangeCallback) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onStatusChange = cb
}

// InitSeq sets the buffer's sequence counter so subsequent appends continue from lastSeq+1.
func (a *Agent) InitSeq(lastSeq uint64) {
	a.buf.InitSeq(lastSeq)
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

// NewTestAgent creates an agent for testing purposes (exported for test packages).
func NewTestAgent(name, provider string) *Agent {
	return newAgent(newUUID(), name, provider, "/tmp", "echo", []string{"hello"})
}

func (a *Agent) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Agent) setStatus(s Status) {
	a.mu.Lock()
	oldStatus := a.status
	a.status = s
	onChange := a.onStatusChange
	a.mu.Unlock()
	if oldStatus != s && onChange != nil {
		onChange(a.ID, oldStatus, s)
	}
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

func (a *Agent) setWatcher(w watcher.SessionWatcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.w = w
}

func (a *Agent) Watcher() watcher.SessionWatcher {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.w
}

func (a *Agent) IsReadOnly() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.attachReadOnly
}

func (a *Agent) SetAttachInputRoute(mode string, readOnly bool, reason string, tmuxTarget string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attachMode = mode
	a.attachReadOnly = readOnly
	a.attachReadOnlyReason = reason
	a.tmuxTarget = tmuxTarget
}

func (a *Agent) AttachMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.attachMode
}

func (a *Agent) TmuxTarget() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tmuxTarget
}

func (a *Agent) AttachReadOnly() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.attachReadOnly
}

func (a *Agent) AttachReadOnlyReason() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.attachReadOnlyReason
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
	attachMode := a.attachMode
	attachReadOnly := a.attachReadOnly
	attachReadOnlyReason := a.attachReadOnlyReason
	tmuxTarget := a.tmuxTarget
	a.mu.RUnlock()

	if p != nil {
		_, err := p.Write([]byte(text))
		return err
	}

	if attachMode == "tmux" && tmuxTarget != "" {
		if attachReadOnly {
			if attachReadOnlyReason != "" {
				return fmt.Errorf("attached agent is read-only: %s", attachReadOnlyReason)
			}
			return fmt.Errorf("attached agent is read-only")
		}
		return sendTmuxInput(tmuxTarget, text)
	}

	if w != nil {
		if attachReadOnlyReason != "" {
			return fmt.Errorf("attached agent is read-only: %s", attachReadOnlyReason)
		}
		return fmt.Errorf("attached agents are read-only; use the original terminal to interact")
	}

	return fmt.Errorf("agent process not running")
}

func sendTmuxInput(target string, text string) error {
	remaining := text
	for len(remaining) > 0 {
		switch {
		case strings.HasPrefix(remaining, "\x1b[A"):
			if err := sendTmuxKey(target, "Up"); err != nil {
				return err
			}
			remaining = remaining[3:]
		case strings.HasPrefix(remaining, "\x1b[B"):
			if err := sendTmuxKey(target, "Down"); err != nil {
				return err
			}
			remaining = remaining[3:]
		case strings.HasPrefix(remaining, "\x1b[C"):
			if err := sendTmuxKey(target, "Right"); err != nil {
				return err
			}
			remaining = remaining[3:]
		case strings.HasPrefix(remaining, "\x1b[D"):
			if err := sendTmuxKey(target, "Left"); err != nil {
				return err
			}
			remaining = remaining[3:]
		case strings.HasPrefix(remaining, "\r"):
			if err := sendTmuxKey(target, "Enter"); err != nil {
				return err
			}
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "\n"):
			if err := sendTmuxKey(target, "Enter"); err != nil {
				return err
			}
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "\t"):
			if err := sendTmuxKey(target, "Tab"); err != nil {
				return err
			}
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "\x7f"):
			if err := sendTmuxKey(target, "BSpace"); err != nil {
				return err
			}
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "\x1b"):
			if err := sendTmuxKey(target, "Escape"); err != nil {
				return err
			}
			remaining = remaining[1:]
		default:
			next := len(remaining)
			for i := 0; i < len(remaining); i++ {
				if remaining[i] < 0x20 || remaining[i] == 0x7f || remaining[i] == 0x1b {
					next = i
					break
				}
			}
			chunk := remaining[:next]
			if chunk != "" {
				if err := sendTmuxLiteral(target, chunk); err != nil {
					return err
				}
			}
			remaining = remaining[next:]
		}
	}
	return nil
}

func sendTmuxLiteral(target string, text string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("tmux send-keys failed: %w", err)
		}
		return fmt.Errorf("tmux send-keys failed: %s", msg)
	}
	return nil
}

func sendTmuxKey(target string, key string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", target, key)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("tmux send-keys failed: %w", err)
		}
		return fmt.Errorf("tmux send-keys failed: %s", msg)
	}
	return nil
}
