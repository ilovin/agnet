package ws

import "github.com/phone-talk/agentd/internal/provider"

// sendPath is an internal strategy label for conversation.send dispatch.
type sendPath string

const (
	sendPathTmux       sendPath = "tmux"
	sendPathClaudePipe sendPath = "claude_pipe"
	sendPathClaudeInit sendPath = "claude_init"
	sendPathResumeCmd  sendPath = "resume_cmd"
	sendPathPTY        sendPath = "pty"
)

func decideSendPath(providerName, attachMode string, hasProcess bool, caps provider.Capabilities) sendPath {
	if attachMode == "tmux" {
		return sendPathTmux
	}
	if providerName == "claude" && hasProcess {
		return sendPathClaudePipe
	}
	if providerName == "claude" && !hasProcess {
		return sendPathClaudeInit
	}
	if caps.SendMode == provider.SendModeResumeCmd {
		return sendPathResumeCmd
	}
	return sendPathPTY
}
