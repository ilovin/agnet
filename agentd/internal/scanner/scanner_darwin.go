//go:build darwin

package scanner

import (
	"os/exec"
	"strconv"
	"strings"
)

// scanDarwin scans for processes on macOS using ps and lsof commands.
func (s *Scanner) scanDarwin() ([]ProcessInfo, error) {
	// Get all processes with their command lines and controlling terminal.
	// ps -eo pid,ppid,tty,comm,args
	cmd := exec.Command("ps", "-eo", "pid,ppid,tty,comm,args")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var found []ProcessInfo
	lines := strings.Split(string(output), "\n")

	// Skip header line
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}

		info, ok := s.parseDarwinProcess(line)
		if ok {
			found = append(found, info)
		}
	}

	return finalizeProcessScan(found), nil
}

// parseDarwinProcess parses a ps output line for Darwin.
func (s *Scanner) parseDarwinProcess(line string) (ProcessInfo, bool) {
	// Parse: PID  PPID  TTY  COMM  ARGS...
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return ProcessInfo{}, false
	}

	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return ProcessInfo{}, false
	}

	ppid, _ := strconv.Atoi(fields[1])
	terminal := fields[2]
	comm := fields[3]
	args := fields[4:]

	// Check if it's Claude or OpenCode
	var provider string
	switch {
	case strings.HasPrefix(comm, "claude"):
		provider = "claude"
	case strings.Contains(strings.Join(args, " "), "claude"):
		provider = "claude"
		comm = "claude"
	case strings.HasPrefix(comm, "opencode"):
		provider = "opencode"
	case strings.Contains(strings.Join(args, " "), "opencode"):
		provider = "opencode"
		comm = "opencode"
	default:
		return ProcessInfo{}, false
	}

	// Filter out agentd's own children
	if ppid > 0 && s.isDarwinAgentd(ppid) {
		return ProcessInfo{}, false
	}

	// Get working directory using lsof
	workDir := s.getDarwinCWD(pid)
	if workDir == "" {
		workDir = "/"
	}

	if terminal == "??" {
		terminal = ""
	}
	terminal = normalizeTTY(terminal)
	tmuxTarget, tmuxSession := s.detectDarwinTmuxFromTTY(terminal)

	return ProcessInfo{
		PID:         pid,
		PPID:        ppid,
		Cmd:         comm,
		Args:        args,
		WorkDir:     workDir,
		Provider:    provider,
		Session:     tmuxSession,
		Terminal:    terminal,
		TmuxTarget:  tmuxTarget,
		SessionID:   "",
		SessionFile: "",
	}, true
}

// isDarwinAgentd checks if a process is agentd on macOS.
func (s *Scanner) isDarwinAgentd(pid int) bool {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "agentd")
}

// getDarwinCWD gets the current working directory of a process on macOS.
func (s *Scanner) getDarwinCWD(pid int) string {
	cmd := exec.Command("lsof", "-p", strconv.Itoa(pid), "-a", "-d", "cwd", "-Fn")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse lsof output: n/path/to/cwd
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}

// getDarwinTerminal gets the controlling terminal of a process on macOS.
func (s *Scanner) getDarwinTerminal(pid int) string {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "tty=")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(output))
	if tty == "??" {
		return ""
	}
	return tty
}

func (s *Scanner) detectDarwinTmuxFromTTY(terminal string) (target string, session string) {
	wantTTY := normalizeTTY(terminal)
	if wantTTY == "" {
		return "", ""
	}

	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_tty}\t#{session_name}\t#{session_name}:#{window_index}.#{pane_index}")
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}

	return resolveTmuxTargetFromPaneList(string(out), wantTTY)
}

// stub for Linux compatibility - never called on macOS
func (s *Scanner) scanLinux() ([]ProcessInfo, error) {
	return nil, nil
}

