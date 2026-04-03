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
	PID       int
	Cmd       string
	Args      []string
	WorkDir   string
	Provider  string // "claude" or "opencode"
	Session   string // tmux/screen session name if available
	Terminal  string // TTY device if available
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

// FindSessionFile attempts to find the JSONL session file for a Claude process.
// It reads the PID mapping file (~/.claude/sessions/<PID>.json) to get the sessionId,
// then looks for the corresponding JSONL file in the projects directories.
func (p *ProcessInfo) FindSessionFile() string {
	if p.Provider != "claude" {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Step 1: Read ~/.claude/sessions/<PID>.json to get sessionId
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	pidFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", p.PID))

	if _, err := os.Stat(pidFile); err == nil {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			var pidInfo struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(data, &pidInfo); err == nil && pidInfo.SessionID != "" {
				// Look for the JSONL file in projects directories
				projectsBase := filepath.Join(home, ".claude", "projects")
				entries, _ := os.ReadDir(projectsBase)
				for _, entry := range entries {
					if entry.IsDir() {
						jsonlPath := filepath.Join(projectsBase, entry.Name(), pidInfo.SessionID+".jsonl")
						if _, err := os.Stat(jsonlPath); err == nil {
							return jsonlPath
						}
					}
				}
			}
		}
	}

	// Step 2: Fallback - look for most recent JSONL in workDir's project directory
	dirName := strings.ReplaceAll(p.WorkDir, "/", "-")
	if dirName == "" || dirName == "-" {
		dirName = "-"
	}

	projectsDir := filepath.Join(home, ".claude", "projects", dirName)
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	// Find most recent JSONL file
	var latest string
	var latestTime int64
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Unix() > latestTime {
				latestTime = info.ModTime().Unix()
				latest = filepath.Join(projectsDir, entry.Name())
			}
		}
	}

	return latest
}

// IsAttachable returns true if this process can be attached for interaction.
func (p *ProcessInfo) IsAttachable() bool {
	return p.Session != "" || p.Terminal != ""
}
