// Package scanner discovers existing Claude/OpenCode processes on the system.
package scanner

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	if !isClaudeProcess(pid) {
		return "", ""
	}

	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	tasksDir := filepath.Join(home, ".claude", "tasks")

	if sessionFile := findClaudeSessionFromTasks(pid, projectDir, tasksDir); sessionFile != "" {
		sessionID := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl")
		return sessionID, sessionFile
	}

	// When running as root, also scan /home/* for other users' Claude task dirs.
	if _, err := os.Stat("/home"); err == nil {
		entries, err := os.ReadDir("/home")
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				userHome := filepath.Join("/home", entry.Name())
				projectDir = filepath.Join(userHome, ".claude", "projects", projectDirName(workDir))
				tasksDir = filepath.Join(userHome, ".claude", "tasks")
				if sessionFile := findClaudeSessionFromTasks(pid, projectDir, tasksDir); sessionFile != "" {
					sessionID := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl")
					return sessionID, sessionFile
				}
			}
		}
	}

	return "", ""
}

func findClaudeSessionFromTasks(pid int, projectDir, tasksDir string) string {
	if pid <= 0 || projectDir == "" || tasksDir == "" {
		return ""
	}
	sessionIDs := make(map[string]struct{})

	procFdDir := fmt.Sprintf("/proc/%d/fd", pid)
	if fdEntries, err := os.ReadDir(procFdDir); err == nil {
		for _, fd := range fdEntries {
			link, err := os.Readlink(filepath.Join(procFdDir, fd.Name()))
			if err != nil {
				continue
			}
			if sessionID := claudeTaskSessionID(tasksDir, link); sessionID != "" {
				sessionIDs[sessionID] = struct{}{}
			}
		}
		return bestClaudeTaskSession(projectDir, tasksDir, sessionIDs)
	}

	cmd := exec.Command("lsof", "-p", strconv.Itoa(pid), "-Fn")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 || line[0] != 'n' {
			continue
		}
		if sessionID := claudeTaskSessionID(tasksDir, line[1:]); sessionID != "" {
			sessionIDs[sessionID] = struct{}{}
		}
	}
	return bestClaudeTaskSession(projectDir, tasksDir, sessionIDs)
}

func claudeTaskSessionID(tasksDir, path string) string {
	normalizedTasksDir := normalizeClaudeTaskPath(tasksDir)
	normalizedPath := normalizeClaudeTaskPath(path)
	if normalizedTasksDir == "" || normalizedPath == "" {
		return ""
	}
	rel, err := filepath.Rel(normalizedTasksDir, normalizedPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

func normalizeClaudeTaskPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, "/private/") {
		return strings.TrimPrefix(path, "/private")
	}
	return path
}

func bestClaudeTaskSession(projectDir, tasksDir string, sessionIDs map[string]struct{}) string {
	var best string
	var bestTaskTime time.Time
	var bestSessionTime time.Time
	for sessionID := range sessionIDs {
		candidate := filepath.Join(projectDir, sessionID+".jsonl")
		sessionInfo, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		taskTime := latestClaudeTaskActivity(filepath.Join(tasksDir, sessionID))
		if best == "" || taskTime.After(bestTaskTime) || (taskTime.Equal(bestTaskTime) && sessionInfo.ModTime().After(bestSessionTime)) {
			best = candidate
			bestTaskTime = taskTime
			bestSessionTime = sessionInfo.ModTime()
		}
	}
	return best
}

func latestClaudeTaskActivity(taskDir string) time.Time {
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
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

func isClaudeProcess(pid int) bool {
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		// On non-Linux systems (e.g., macOS), /proc doesn't exist.
		// Skip validation on these platforms as PID reuse is less of a concern.
		if os.IsNotExist(err) {
			return true
		}
		return false
	}
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.Contains(cmdline, "claude")
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

	// First: try to find session file by workDir matching
	// OpenCode stores sessions with workDir in the file content
	if workDir != "" {
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

				sessionFile := filepath.Join(sessionDir, name)
				sessionID := strings.TrimSuffix(name, ".json")

				// Read session file and check if workDir matches
				if match, err := matchSessionWorkDir(sessionFile, workDir); err == nil && match {
					// Verify the process is still running and is opencode
					if pid > 0 && !isOpenCodeProcess(pid) {
						continue
					}
					log.Printf("[OpenCode] Found session by workDir match: %s", sessionID)
					return sessionID, sessionFile
				}
			}
		}
	}

	// Second: if PID is provided, try to validate process and find its session
	if pid > 0 {
		if isOpenCodeProcess(pid) {
			// Find the most recently modified session file
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
				base := filepath.Base(latestFile)
				sessionID := strings.TrimSuffix(base, ".json")
				log.Printf("[OpenCode] Found latest session for PID %d: %s", pid, sessionID)
				return sessionID, latestFile
			}
		}
	}

	// Third: fallback to most recently modified session file (original behavior)
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
		base := filepath.Base(latestFile)
		sessionID := strings.TrimSuffix(base, ".json")
		log.Printf("[OpenCode] Found latest session (fallback): %s", sessionID)
		return sessionID, latestFile
	}
	log.Printf("[OpenCode] No session file found")

	return "", ""
}

// matchSessionWorkDir reads a session file and checks if its workDir matches
func matchSessionWorkDir(sessionFile, workDir string) (bool, error) {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return false, err
	}

	var session struct {
		WorkDir string `json:"workDir"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return false, err
	}

	return session.WorkDir == workDir, nil
}

func isOpenCodeProcess(pid int) bool {
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		// On non-Linux systems (e.g., macOS), /proc doesn't exist.
		// Skip validation on these platforms as PID reuse is less of a concern.
		if os.IsNotExist(err) {
			return true
		}
		return false
	}
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.Contains(cmdline, "opencode")
}

func projectDirName(workDir string) string {
	// Must match Claude's project directory naming: replace / . _ with -
	s := strings.ReplaceAll(strings.TrimRight(workDir, "/"), "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}
