package ws

import (
	"testing"

	"github.com/phone-talk/agentd/internal/provider"
)

func TestDecideSendPath(t *testing.T) {
	if got := decideSendPath("codex", "tmux", true, provider.CapabilitiesFor("codex")); got != sendPathTmux {
		t.Fatalf("tmux attach should choose tmux path, got %q", got)
	}
	if got := decideSendPath("claude", "", true, provider.CapabilitiesFor("claude")); got != sendPathClaudePipe {
		t.Fatalf("live claude should choose pipe path, got %q", got)
	}
	if got := decideSendPath("claude", "", false, provider.CapabilitiesFor("claude")); got != sendPathClaudeInit {
		t.Fatalf("fresh claude should choose init path, got %q", got)
	}
	if got := decideSendPath("opencode", "", false, provider.CapabilitiesFor("opencode")); got != sendPathResumeCmd {
		t.Fatalf("opencode should choose resume path, got %q", got)
	}
	if got := decideSendPath("custom", "", true, provider.CapabilitiesFor("custom")); got != sendPathPTY {
		t.Fatalf("custom should default to pty path, got %q", got)
	}
}
