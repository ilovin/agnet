//go:build darwin

package scanner

import (
	"os/exec"
	"path/filepath"
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

	// Build a PID->(ppid,comm) map from a single ps call for ancestor lookups,
	// instead of spawning one ps per ancestor level in isTrackedAgent.
	allProcs := s.buildDarwinProcessMap()

	var found []ProcessInfo
	lines := strings.Split(string(output), "\n")

	// Skip header line
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}

		info, ok := s.parseDarwinProcess(line, allProcs)
		if ok {
			found = append(found, info)
		}
	}

	return finalizeProcessScan(found), nil
}

// darwinProcInfo holds minimal info about a process for ancestor lookups.
type darwinProcInfo struct {
	ppid int
	comm string
}

// buildDarwinProcessMap builds a PID→darwinProcInfo map from a single ps call.
func (s *Scanner) buildDarwinProcessMap() map[int]darwinProcInfo {
	cmd := exec.Command("ps", "-eo", "pid,ppid,comm=")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	m := make(map[int]darwinProcInfo)
	for i, line := range strings.Split(string(output), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		m[pid] = darwinProcInfo{ppid: ppid, comm: fields[2]}
	}
	return m
}

// parseDarwinProcess parses a ps output line for Darwin.
// allProcs is the pre-built PID map used for ancestor lookups (avoids per-ancestor exec.Command).
func (s *Scanner) parseDarwinProcess(line string, allProcs map[int]darwinProcInfo) (ProcessInfo, bool) {
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

	// Check if it's Claude or OpenCode.
	// Only match the executable name (comm or args[0]), never match
	// arbitrary arguments — paths like ~/.claude/shell-snapshots/... would
	// otherwise cause false positives (e.g. zsh sub-processes).
	var provider string
	switch {
	case strings.HasPrefix(comm, "claude"):
		provider = "claude"
	case len(args) > 0 && strings.HasPrefix(filepath.Base(args[0]), "claude"):
		provider = "claude"
		comm = "claude"
	case strings.HasPrefix(comm, "opencode"):
		provider = "opencode"
	case len(args) > 0 && strings.HasPrefix(filepath.Base(args[0]), "opencode"):
		provider = "opencode"
		comm = "opencode"
	default:
		return ProcessInfo{}, false
	}

		// Filter out agentd's own children (use allProcs map)
		if ppid > 0 {
			if info, ok := allProcs[ppid]; ok && strings.Contains(info.comm, "agentd") {
				return ProcessInfo{}, false
			}
		}

		// Filter out claude -p sub-agents (child processes spawned by Claude Code Agent tool).
		if provider == "claude" && isClaudeSubagentArgs(args) {
			return ProcessInfo{}, false
		}

		// Filter out processes whose ancestor is a claude/opencode agent (uses pre-built map).
		if ppid > 0 && s.isTrackedAgentCached(ppid, allProcs) {
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

// isTrackedAgentCached walks up the parent process tree using the pre-built
// allProcs map to check if any ancestor is a claude/opencode process.
// This replaces the old isTrackedAgent that spawned one exec.Command per ancestor.
func (s *Scanner) isTrackedAgentCached(pid int, allProcs map[int]darwinProcInfo) bool {
	visited := map[int]bool{}
	for p := pid; p > 1; {
		if visited[p] {
			return false
		}
		visited[p] = true
		info, ok := allProcs[p]
		if !ok {
			return false
		}
		if strings.HasPrefix(info.comm, "claude") || strings.HasPrefix(info.comm, "opencode") {
			return true
		}
		p = info.ppid
	}
	return false
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
	format := "#{pane_tty}\t#{session_name}\t#{session_name}:#{window_index}.#{pane_index}"

	// Try default tmux first
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", format)
	if out, err := cmd.Output(); err == nil {
		if tgt, sess := resolveTmuxTargetFromPaneList(string(out), wantTTY); tgt != "" {
			return tgt, sess
		}
	}

	// On macOS, tmux sockets may be in /private/tmp/tmux-* or /tmp/tmux-*
	for _, baseDir := range []string{"/tmp", "/private/tmp"} {
		tmuxDirs, _ := filepath.Glob(filepath.Join(baseDir, "tmux-*"))
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
	}

	return "", ""
}

// stub for Linux compatibility - never called on macOS
func (s *Scanner) scanLinux() ([]ProcessInfo, error) {
	return nil, nil
}

