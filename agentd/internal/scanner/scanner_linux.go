//go:build linux

package scanner

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// scanLinux scans for processes on Linux using /proc.
func (s *Scanner) scanLinux() ([]ProcessInfo, error) {
	return s.scanLinuxWithFS(RealFileSystem{})
}

func (s *Scanner) scanLinuxWithFS(fs FileSystem) ([]ProcessInfo, error) {
	entries, err := fs.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var found []ProcessInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID directory
		}

		info, ok := s.scanProcessWithFS(fs, pid)
		if ok {
			found = append(found, info)
		}
	}

	return finalizeProcessScanWithFS(fs, found), nil
}

// scanProcess checks if a process is a Claude or OpenCode agent.
func (s *Scanner) scanProcess(pid int) (ProcessInfo, bool) {
	return s.scanProcessWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) scanProcessWithFS(fs FileSystem, pid int) (ProcessInfo, bool) {
	// Read command line
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := fs.ReadFile(cmdlinePath)
	if err != nil {
		return ProcessInfo{}, false
	}

	// cmdline is null-separated
	parts := strings.Split(string(data), "\x00")
	if len(parts) == 0 {
		return ProcessInfo{}, false
	}

	// Check if it's Claude or OpenCode
	cmd := parts[0]
	base := filepath.Base(cmd)

	var provider string
	switch {
	case strings.HasPrefix(base, "claude"):
		provider = "claude"
	case strings.HasPrefix(base, "opencode"):
		provider = "opencode"
	default:
		return ProcessInfo{}, false
	}

	// Get working directory
	cwdPath := fmt.Sprintf("/proc/%d/cwd", pid)
	workDir, err := fs.Readlink(cwdPath)
	if err != nil {
		workDir = "/"
	}

	// Filter out agentd's own children (they're already managed)
	ppid := s.getPPIDWithFS(fs, pid)
	if ppid > 0 && s.isAgentdWithFS(fs, ppid) {
		return ProcessInfo{}, false // skip agentd's children
	}

	// Get controlling terminal
	terminal := s.getTerminalWithFS(fs, pid)
	tmuxTarget, tmuxSession := s.detectLinuxTmuxFromTTYWithFS(fs, terminal)

	// Detect tmux/screen session
	session := tmuxSession
	if session == "" {
		session = s.detectTmuxSessionWithFS(fs, pid)
	}
	if session == "" {
		session = s.detectScreenSessionWithFS(fs, pid)
	}

	return ProcessInfo{
		PID:         pid,
		PPID:        ppid,
		Cmd:         cmd,
		Args:        parts[1:],
		WorkDir:     workDir,
		Provider:    provider,
		Session:     session,
		Terminal:    terminal,
		TmuxTarget:  tmuxTarget,
		SessionID:   "",
		SessionFile: "",
	}, true
}

// getPPID returns the parent process ID.
func (s *Scanner) getPPID(pid int) int {
	return s.getPPIDWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) getPPIDWithFS(fs FileSystem, pid int) int {
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	data, err := fs.ReadFile(statusPath)
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			ppidStr := strings.TrimSpace(strings.TrimPrefix(line, "PPid:"))
			ppid, _ := strconv.Atoi(ppidStr)
			return ppid
		}
	}
	return 0
}

// isAgentd checks if a process is agentd.
func (s *Scanner) isAgentd(pid int) bool {
	return s.isAgentdWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) isAgentdWithFS(fs FileSystem, pid int) bool {
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := fs.Readlink(exePath)
	if err != nil {
		return false
	}
	return strings.Contains(filepath.Base(target), "agentd")
}

// detectTmuxSession checks if process is running in a tmux session.
func (s *Scanner) detectTmuxSession(pid int) string {
	return s.detectTmuxSessionWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) detectTmuxSessionWithFS(fs FileSystem, pid int) string {
	environPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := fs.ReadFile(environPath)
	if err != nil {
		return ""
	}

	// Check for TMUX environment variable
	vars := strings.Split(string(data), "\x00")
	for _, v := range vars {
		if strings.HasPrefix(v, "TMUX=") {
			// TMUX format: /tmp/tmux-1000/default,12345,0
			tmuxVal := strings.TrimPrefix(v, "TMUX=")
			parts := strings.Split(tmuxVal, ",")
			if len(parts) >= 1 {
				// Try to get session name from tmux
				return s.getTmuxSessionNameWithFS(fs, pid)
			}
		}
	}
	return ""
}

// getTmuxSessionName gets the tmux session name for a process.
func (s *Scanner) getTmuxSessionName(pid int) string {
	return s.getTmuxSessionNameWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) getTmuxSessionNameWithFS(fs FileSystem, pid int) string {
	// Look for tmux client process in the process tree
	for ppid := pid; ppid > 1; {
		ppid = s.getPPIDWithFS(fs, ppid)
		if ppid <= 1 {
			break
		}
		cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", ppid)
		data, err := fs.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}
		cmd := string(data)
		if strings.Contains(cmd, "tmux:") && strings.Contains(cmd, "client") {
			// This is a tmux client, try to find session from its parent
			return s.findTmuxSessionFromServerWithFS(fs, ppid)
		}
	}
	return ""
}

// findTmuxSessionFromServer finds tmux session name from tmux server process.
func (s *Scanner) findTmuxSessionFromServer(tmuxPid int) string {
	return s.findTmuxSessionFromServerWithFS(RealFileSystem{}, tmuxPid)
}

func (s *Scanner) findTmuxSessionFromServerWithFS(fs FileSystem, tmuxPid int) string {
	// This is complex - would need to parse tmux socket or use tmux command
	// For now, return a placeholder that indicates tmux is being used
	return "tmux-attached"
}

// detectScreenSession checks if process is running in a screen session.
func (s *Scanner) detectScreenSession(pid int) string {
	return s.detectScreenSessionWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) detectScreenSessionWithFS(fs FileSystem, pid int) string {
	environPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := fs.ReadFile(environPath)
	if err != nil {
		return ""
	}

	vars := strings.Split(string(data), "\x00")
	for _, v := range vars {
		if strings.HasPrefix(v, "STY=") {
			// STY format: 12345.pts-0.hostname
			sty := strings.TrimPrefix(v, "STY=")
			parts := strings.Split(sty, ".")
			if len(parts) >= 2 {
				return parts[1] // session name
			}
			return sty
		}
	}
	return ""
}

// getTerminal returns the controlling terminal device.
func (s *Scanner) getTerminal(pid int) string {
	return s.getTerminalWithFS(RealFileSystem{}, pid)
}

func (s *Scanner) getTerminalWithFS(fs FileSystem, pid int) string {
	fdPath := fmt.Sprintf("/proc/%d/fd/0", pid)
	tty, err := fs.Readlink(fdPath)
	if err != nil {
		return ""
	}
	return normalizeTTY(tty)
}

func (s *Scanner) detectLinuxTmuxFromTTY(terminal string) (target string, session string) {
	return s.detectLinuxTmuxFromTTYWithFS(RealFileSystem{}, terminal)
}

func (s *Scanner) detectLinuxTmuxFromTTYWithFS(fs FileSystem, terminal string) (target string, session string) {
	wantTTY := normalizeTTY(terminal)
	if wantTTY == "" {
		return "", ""
	}

	format := "#{pane_tty}\t#{session_name}\t#{session_name}:#{window_index}.#{pane_index}"

	// Try default tmux first (works when agentd runs as the same user as tmux).
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", format)
	if out, err := cmd.Output(); err == nil {
		if tgt, sess := resolveTmuxTargetFromPaneList(string(out), wantTTY); tgt != "" {
			return tgt, sess
		}
	}

	// When agentd runs as root but tmux belongs to another user, the default
	// tmux command can't reach the user's socket.  Scan all /tmp/tmux-*
	// sockets and try each one explicitly.
	tmuxDirs, _ := filepath.Glob("/tmp/tmux-*")
	for _, dir := range tmuxDirs {
		sockets, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, sock := range sockets {
			cmd := exec.Command("tmux", "-S", sock, "list-panes", "-a", "-F", format)
			out, err := cmd.Output()
			if err != nil {
				continue
			}
			if tgt, sess := resolveTmuxTargetFromPaneList(string(out), wantTTY); tgt != "" {
				return tgt, sess
			}
		}
	}

	return "", ""
}

// stub for Darwin compatibility - never called on Linux
func (s *Scanner) scanDarwin() ([]ProcessInfo, error) {
	return nil, nil
}

func (s *Scanner) scanDarwinWithFS(fs FileSystem) ([]ProcessInfo, error) {
	return nil, nil
}
