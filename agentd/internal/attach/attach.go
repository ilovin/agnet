// Package attach provides session resume functionality for existing agents.
// Instead of complex PTY stealing, we use Claude Code's --resume feature.
package attach

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// AttachStrategy defines how to handle an existing process.
type AttachStrategy int

const (
	// StrategyResume kills the process and restarts with --resume (recommended)
	StrategyResume AttachStrategy = iota
	// StrategyReadOnly only monitors JSONL files, no interaction
	StrategyReadOnly
)

// ExistingAgent represents a discovered agent process that can be attached.
type ExistingAgent struct {
	PID       int
	Provider  string // "claude" or "opencode"
	WorkDir   string
	SessionID string // For resume
	Strategy  AttachStrategy
}

// Attacher handles attaching to existing processes.
type Attacher struct {
	dataDir string
}

// New creates a new Attacher.
func New(dataDir string) *Attacher {
	return &Attacher{dataDir: dataDir}
}

// AttachResult contains the result of an attach operation.
type AttachResult struct {
	OldPID    int
	NewPID    int
	SessionID string
	Success   bool
	Message   string
}

// Attach takes over an existing agent process.
// For Claude: kills process and restarts with --resume <session_id>
// For OpenCode: kills process and restarts with -s <session_id>
func (a *Attacher) Attach(agent *ExistingAgent) (*AttachResult, error) {
	if agent.SessionID == "" {
		return nil, fmt.Errorf("no session ID found, cannot resume")
	}

	result := &AttachResult{
		OldPID:    agent.PID,
		SessionID: agent.SessionID,
	}

	// 1. Gracefully terminate the existing process
	if err := a.terminateProcess(agent.PID); err != nil {
		return nil, fmt.Errorf("terminate existing process: %w", err)
	}

	// Wait for process to die
	if !a.waitForProcessExit(agent.PID, 5*time.Second) {
		// Force kill
		syscall.Kill(agent.PID, syscall.SIGKILL)
	}

	// 2. Return resume parameters - actual restart is done by caller (Manager)
	result.Success = true
	result.Message = fmt.Sprintf("Process %d terminated, ready to resume session %s", agent.PID, agent.SessionID)

	return result, nil
}

// terminateProcess sends SIGTERM to gracefully terminate.
func (a *Attacher) terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// waitForProcessExit waits for a process to exit.
func (a *Attacher) waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if process exists
		if err := syscall.Kill(pid, 0); err != nil {
			// Process doesn't exist
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// GetResumeArgs returns the command line args for resuming a session.
func (a *Attacher) GetResumeArgs(provider, sessionID string) []string {
	switch provider {
	case "claude":
		return []string{"--resume", sessionID, "--dangerously-skip-permissions"}
	case "opencode":
		return []string{"-s", sessionID}
	default:
		return []string{}
	}
}

// FindSessionFile locates the JSONL session file for a working directory.
func FindSessionFile(workDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Normalize path: /home/user/project -> -home-user-project
	dirName := strings.Trim(workDir, "/")
	dirName = strings.ReplaceAll(dirName, "/", "-")
	if dirName == "" {
		dirName = "-"
	}

	projectsDir := filepath.Join(home, ".claude", "projects", dirName)
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	// Find most recent JSONL file
	var latest string
	var latestTime time.Time
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") && !strings.Contains(entry.Name(), "subagents") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latest = filepath.Join(projectsDir, entry.Name())
			}
		}
	}

	return latest
}

// ExtractSessionID extracts session ID from a JSONL file path.
// e.g., /home/user/.claude/projects/-home-user-project/abc-123.jsonl -> abc-123
func ExtractSessionFile(path string) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if !strings.HasSuffix(base, ".jsonl") {
		return ""
	}
	return strings.TrimSuffix(base, ".jsonl")
}
