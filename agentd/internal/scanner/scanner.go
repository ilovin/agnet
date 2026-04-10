// Package scanner discovers existing Claude/OpenCode processes on the system.
package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProcessInfo holds information about a discovered agent process.
type ProcessInfo struct {
	PID         int
	PPID        int
	Cmd         string
	Args        []string
	WorkDir     string
	Provider    string // "claude" or "opencode"
	Session     string // tmux/screen session name if available
	Terminal    string // TTY device if available
	TmuxTarget  string // tmux pane target (session:window.pane) if available
	SessionID   string
	SessionFile string
}

const (
	AttachModeWatcher = "watcher"
	AttachModeTmux    = "tmux"
)

func normalizeTTY(tty string) string {
	tty = strings.TrimSpace(tty)
	if tty == "" || tty == "??" {
		return ""
	}
	if strings.HasPrefix(tty, "/dev/") {
		return tty
	}
	return "/dev/" + strings.TrimPrefix(tty, "/dev/")
}

func resolveTmuxTargetFromPaneList(output string, terminal string) (string, string) {
	wantTTY := normalizeTTY(terminal)
	if wantTTY == "" {
		return "", ""
	}

	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		paneTTY := normalizeTTY(parts[0])
		session := strings.TrimSpace(parts[1])
		target := strings.TrimSpace(parts[2])
		if paneTTY != wantTTY || target == "" {
			continue
		}
		if session == "" {
			if idx := strings.Index(target, ":"); idx > 0 {
				session = target[:idx]
			}
		}
		return target, session
	}

	return "", ""
}

// AttachMode reports how input could be routed for an attached process.
func (p *ProcessInfo) AttachMode() string {
	if p.TmuxTarget != "" {
		return AttachModeTmux
	}
	return AttachModeWatcher
}

// AttachReadOnly reports whether attaching this process will be observe-only.
func (p *ProcessInfo) AttachReadOnly() bool {
	return p.AttachReadOnlyReason() != ""
}

// AttachReadOnlyReason explains why attached input is blocked.
func (p *ProcessInfo) AttachReadOnlyReason() string {
	if p.TmuxTarget != "" {
		return ""
	}

	switch p.Provider {
	case "claude":
		if p.Terminal == "" {
			return "no controlling terminal found for Claude process; attach is observe-only"
		}
		return "no safe input route found (tmux pane not detected); use the original terminal to interact"
	default:
		return "attached input routing is not supported for this provider"
	}
}

// Scanner scans for existing AI agent processes.
type Scanner struct{}

// New creates a new process scanner.
func New() *Scanner {
	return &Scanner{}
}

// Scan discovers all running Claude and OpenCode processes.
func (s *Scanner) Scan() ([]ProcessInfo, error) {
	// Check if /proc exists (Linux)
	if _, err := os.Stat("/proc"); err == nil {
		return s.scanLinux()
	}
	// Fallback to Darwin (macOS) implementation
	return s.scanDarwin()
}

// FindSessionFile returns the exact JSONL session file for a live Claude process.
func (p *ProcessInfo) FindSessionFile() string {
	if p.Provider != "claude" {
		return ""
	}
	if p.SessionFile != "" {
		return p.SessionFile
	}
	_, sessionFile := findClaudeSessionInfo(p.PID, p.WorkDir)
	return sessionFile
}

// IsAttachable returns true if this process can be attached for interaction.
func (p *ProcessInfo) IsAttachable() bool {
	return p.Session != "" || p.Terminal != "" || p.TmuxTarget != ""
}

func finalizeProcessScan(processes []ProcessInfo) []ProcessInfo {
	parents := filterParentAgents(processes)
	out := make([]ProcessInfo, 0, len(parents))
	for _, proc := range parents {
		if proc.Provider == "claude" {
			sessionID, sessionFile := findClaudeSessionInfo(proc.PID, proc.WorkDir)
			if sessionID == "" {
				continue
			}
			proc.SessionID = sessionID
			proc.SessionFile = sessionFile
		}
		out = append(out, proc)
	}
	return out
}

func filterParentAgents(processes []ProcessInfo) []ProcessInfo {
	byPID := make(map[int]ProcessInfo, len(processes))
	for _, proc := range processes {
		byPID[proc.PID] = proc
	}

	out := make([]ProcessInfo, 0, len(processes))
	for _, proc := range processes {
		if hasAIAgentAncestor(proc, byPID) {
			continue
		}
		out = append(out, proc)
	}
	return out
}

func hasAIAgentAncestor(proc ProcessInfo, byPID map[int]ProcessInfo) bool {
	seen := map[int]bool{}
	for parentPID := proc.PPID; parentPID > 0; {
		if seen[parentPID] {
			break
		}
		seen[parentPID] = true
		parent, ok := byPID[parentPID]
		if !ok {
			break
		}
		if parent.Provider == "claude" || parent.Provider == "opencode" {
			return true
		}
		parentPID = parent.PPID
	}
	return false
}

func findClaudeSessionInfo(pid int, workDir string) (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}

	sessionID := readClaudeSessionID(home, pid)
	if sessionID == "" {
		return "", ""
	}

	return sessionID, findClaudeSessionFile(home, sessionID, workDir)
}

func readClaudeSessionID(home string, pid int) string {
	pidFile := filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", pid))
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return ""
	}
	var pidInfo struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &pidInfo); err != nil {
		return ""
	}
	return pidInfo.SessionID
}

func findClaudeSessionFile(home, sessionID, workDir string) string {
	if sessionID == "" {
		return ""
	}

	projectsBase := filepath.Join(home, ".claude", "projects")
	preferredDir := filepath.Join(projectsBase, projectDirName(workDir))
	if preferredDir != projectsBase {
		candidate := filepath.Join(preferredDir, sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	entries, err := os.ReadDir(projectsBase)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsBase, entry.Name(), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func projectDirName(workDir string) string {
	dirName := strings.ReplaceAll(workDir, "/", "-")
	if dirName == "" {
		return "-"
	}
	return dirName
}
