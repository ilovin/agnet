package agent

// Provider describes how to launch and interact with a specific AI agent CLI.
type Provider interface {
	// Command returns the executable and args to launch the agent.
	Command(workDir string, resumeSessionID string) (cmd string, args []string)
	// SessionFilePath returns the path to the JSONL session file for the given workDir,
	// or "" if this provider doesn't use JSONL files.
	SessionFilePath(workDir string, sessionID string) string
}

// ClaudeCodeProvider launches `claude` and watches JSONL files.
type ClaudeCodeProvider struct{}

func (c *ClaudeCodeProvider) Command(workDir string, resumeSessionID string) (string, []string) {
	args := []string{"--dangerously-skip-permissions"}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	return "claude", args
}

func (c *ClaudeCodeProvider) SessionFilePath(workDir string, sessionID string) string {
	// Claude stores sessions at ~/.claude/projects/<escaped-cwd>/<sessionID>.jsonl
	// Session file discovery happens after launch; return "" until known.
	return ""
}
