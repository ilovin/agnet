// Package scanner discovers existing Claude/OpenCode processes on the system.
package scanner

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	case "opencode":
		// OpenCode supports resume mode via `opencode run --session`
		// Input is routed through resume, not PTY
		if p.SessionID != "" {
			return "" // Allow input via resume mode
		}
		return "no session ID available for OpenCode resume"
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

// FindSessionFile returns the exact JSONL session file for a live Claude/OpenCode process.
func (p *ProcessInfo) FindSessionFile() string {
	if p.SessionFile != "" {
		return p.SessionFile
	}
	if p.Provider == "claude" {
		_, sessionFile := findClaudeSessionInfo(p.PID, p.WorkDir)
		return sessionFile
	}
	if p.Provider == "opencode" {
		_, sessionFile := findOpenCodeSessionInfo(p.PID, p.WorkDir)
		return sessionFile
	}
	return ""
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
			// Try to find session info, but don't skip if not found
			// Process may still be attachable even without session file
			sessionID, sessionFile := findClaudeSessionInfo(proc.PID, proc.WorkDir)
			proc.SessionID = sessionID
			proc.SessionFile = sessionFile
		} else if proc.Provider == "opencode" {
			// Try to find OpenCode session info
			sessionID, sessionFile := findOpenCodeSessionInfo(proc.PID, proc.WorkDir)
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

	// Try current user's home first
	sessionID := readClaudeSessionID(home, pid)
	if sessionID != "" {
		return sessionID, findClaudeSessionFile(home, sessionID, workDir)
	}

	// When running as root, also scan /home/*/.claude/sessions/ for other users
	if _, err := os.Stat("/home"); err == nil {
		entries, err := os.ReadDir("/home")
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				userHome := filepath.Join("/home", entry.Name())
				sessionID = readClaudeSessionID(userHome, pid)
				if sessionID != "" {
					return sessionID, findClaudeSessionFile(userHome, sessionID, workDir)
				}
			}
		}
	}

	return "", ""
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

func findOpenCodeSessionInfo(pid int, workDir string) (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}

	// Build list of storage base directories to scan.
	// OpenCode may store sessions under session_diff or session subdirectories.
	opencodeStorageDirs := []string{
		filepath.Join(home, ".local", "share", "opencode", "storage"),
		filepath.Join(home, "Library", "Application Support", "opencode", "storage"),
		filepath.Join(home, "Library", "Application Support", "OpenCode", "storage"),
	}

	// On Linux, also scan all user home directories under /home
	// This allows agentd running as root to find sessions from all users
	if _, err := os.Stat("/home"); err == nil {
		entries, err := os.ReadDir("/home")
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				userHome := filepath.Join("/home", entry.Name())
				opencodeStorageDirs = append(opencodeStorageDirs, filepath.Join(userHome, ".local", "share", "opencode", "storage"))
			}
		}
	}

	// Expand storage dirs into candidate session subdirectories
	var sessionDirs []string
	for _, storageDir := range opencodeStorageDirs {
		sessionDirs = append(sessionDirs,
			filepath.Join(storageDir, "session_diff"),
			filepath.Join(storageDir, "session"),
		)
	}

	// Find the most recently modified session file across all directories
	var latestFile string
	var latestTime time.Time

	for _, sessionDir := range sessionDirs {
		entries, err := os.ReadDir(sessionDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			// Accept any session file; ses_ prefix is common but not guaranteed
			sessionID := strings.TrimSuffix(name, ".json")
			if sessionID == "" {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			if info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latestFile = filepath.Join(sessionDir, name)
			}
		}
	}

	if latestFile != "" {
		// Extract session ID from filename (ses_XXX.json -> ses_XXX, or other.json -> other)
		base := filepath.Base(latestFile)
		sessionID := strings.TrimSuffix(base, ".json")
		log.Printf("[OpenCode] Found latest session: %s", sessionID)
		return sessionID, latestFile
	}
	log.Printf("[OpenCode] No session file found")

	return "", ""
}

func projectDirName(workDir string) string {
	dirName := strings.ReplaceAll(workDir, "/", "-")
	if dirName == "" {
		return "-"
	}
	return dirName
}
