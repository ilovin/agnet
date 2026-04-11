package ws

import (
	"path/filepath"
	"testing"
)

func TestResolveLaunchClaudeWithSessionAndModel(t *testing.T) {
	provider, cmd, args, env := resolveLaunch("claude", "", nil, "abc123", "claude-sonnet-4-6")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if filepath.Base(cmd) != "claude" {
		t.Fatalf("cmd = %q, want basename claude", cmd)
	}
	// Claude now uses -p mode with stream-json output format for structured events
	want := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--resume", "abc123",
		"--model", "claude-sonnet-4-6",
	}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want empty for default claude provider", env)
	}
}

func TestResolveLaunchOpencodeSession(t *testing.T) {
	provider, cmd, args, _ := resolveLaunch("opencode", "", []string{"ignored"}, "ses_123", "")
	if provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", provider)
	}
	if filepath.Base(cmd) != "opencode" {
		t.Fatalf("cmd = %q, want basename opencode", cmd)
	}
	want := []string{"-s", "ses_123"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestResolveLaunchDefaultProviderUsesClaude(t *testing.T) {
	provider, cmd, args, _ := resolveLaunch("", "", nil, "", "")
	if provider != "" {
		t.Fatalf("provider = %q, want empty", provider)
	}
	if cmd != "claude" {
		t.Fatalf("cmd = %q, want claude", cmd)
	}
	want := []string{"--permission-mode", "bypassPermissions"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestResolveLaunchBedrockSetsEnvAndNormalizesProvider(t *testing.T) {
	provider, cmd, _, env := resolveLaunch("claude-bedrock", "", nil, "", "")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if filepath.Base(cmd) != "claude" {
		t.Fatalf("cmd = %q, want basename claude", cmd)
	}
	found := false
	for _, e := range env {
		if e == "CLAUDE_CODE_USE_BEDROCK=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env = %v, want CLAUDE_CODE_USE_BEDROCK=1", env)
	}
}

func TestResolveLaunchVertexSetsEnvAndNormalizesProvider(t *testing.T) {
	provider, cmd, _, env := resolveLaunch("claude-vertex", "", nil, "", "")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if filepath.Base(cmd) != "claude" {
		t.Fatalf("cmd = %q, want basename claude", cmd)
	}
	found := false
	for _, e := range env {
		if e == "CLAUDE_CODE_USE_VERTEX=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env = %v, want CLAUDE_CODE_USE_VERTEX=1", env)
	}
}
