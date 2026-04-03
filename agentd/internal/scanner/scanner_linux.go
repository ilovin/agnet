//go:build linux

package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// scanLinux scans for processes on Linux using /proc.
func (s *Scanner) scanLinux() ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
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

		info, ok := s.scanProcess(pid)
		if ok {
			found = append(found, info)
		}
	}

	return found, nil
}

// scanProcess checks if a process is a Claude or OpenCode agent.
func (s *Scanner) scanProcess(pid int) (ProcessInfo, bool) {
	// Read command line
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
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
	workDir, err := os.Readlink(cwdPath)
	if err != nil {
		workDir = "/"
	}

	// Filter out agentd's own children (they're already managed)
	ppid := s.getPPID(pid)
	if ppid > 0 && s.isAgentd(ppid) {
		return ProcessInfo{}, false // skip agentd's children
	}

	// Detect tmux/screen session
	session := s.detectTmuxSession(pid)
	if session == "" {
		session = s.detectScreenSession(pid)
	}

	// Get controlling terminal
	terminal := s.getTerminal(pid)

	return ProcessInfo{
		PID:      pid,
		Cmd:      cmd,
		Args:     parts[1:],
		WorkDir:  workDir,
		Provider: provider,
		Session:  session,
		Terminal: terminal,
	}, true
}

// getPPID returns the parent process ID.
func (s *Scanner) getPPID(pid int) int {
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(statusPath)
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
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(exePath)
	if err != nil {
		return false
	}
	return strings.Contains(filepath.Base(target), "agentd")
}

// detectTmuxSession checks if process is running in a tmux session.
func (s *Scanner) detectTmuxSession(pid int) string {
	environPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := os.ReadFile(environPath)
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
				return s.getTmuxSessionName(pid)
			}
		}
	}
	return ""
}

// getTmuxSessionName gets the tmux session name for a process.
func (s *Scanner) getTmuxSessionName(pid int) string {
	// Look for tmux client process in the process tree
	for ppid := pid; ppid > 1; {
		ppid = s.getPPID(ppid)
		if ppid <= 1 {
			break
		}
		cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", ppid)
		data, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}
		cmd := string(data)
		if strings.Contains(cmd, "tmux:") && strings.Contains(cmd, "client") {
			// This is a tmux client, try to find session from its parent
			return s.findTmuxSessionFromServer(ppid)
		}
	}
	return ""
}

// findTmuxSessionFromServer finds tmux session name from tmux server process.
func (s *Scanner) findTmuxSessionFromServer(tmuxPid int) string {
	// This is complex - would need to parse tmux socket or use tmux command
	// For now, return a placeholder that indicates tmux is being used
	return "tmux-attached"
}

// detectScreenSession checks if process is running in a screen session.
func (s *Scanner) detectScreenSession(pid int) string {
	environPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := os.ReadFile(environPath)
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
	fdPath := fmt.Sprintf("/proc/%d/fd/0", pid)
	tty, err := os.Readlink(fdPath)
	if err != nil {
		return ""
	}
	return tty
}

// stub for Darwin compatibility - never called on Linux
func (s *Scanner) scanDarwin() ([]ProcessInfo, error) {
	return nil, nil
}
