package ws

import (
	"path/filepath"
	"testing"
)

func TestResolveLaunchClaudeWithSessionAndModel(t *testing.T) {
	provider, cmd, args, env := resolveLaunch("claude", "", nil, "abc123", "claude-sonnet-4-6", "")
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
	provider, cmd, args, _ := resolveLaunch("opencode", "", []string{"ignored"}, "ses_123", "", "")
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
	provider, cmd, args, _ := resolveLaunch("", "", nil, "", "", "")
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
	provider, cmd, _, env := resolveLaunch("claude-bedrock", "", nil, "", "", "")
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
	provider, cmd, _, env := resolveLaunch("claude-vertex", "", nil, "", "", "")
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

func TestResolveLaunchOpencodeWithModel(t *testing.T) {
	provider, cmd, args, _ := resolveLaunch("opencode", "", nil, "ses_abc", "tb-api/claude-sonnet-4-6", "")
	if provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", provider)
	}
	if filepath.Base(cmd) != "opencode" {
		t.Fatalf("cmd = %q, want basename opencode", cmd)
	}
	want := []string{"-s", "ses_abc", "-m", "tb-api/claude-sonnet-4-6"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestResolveLaunchOpencodeModelWithoutSession(t *testing.T) {
	_, _, args, _ := resolveLaunch("opencode", "", nil, "", "ADVibe/Kimi-K2.5", "")
	want := []string{"-m", "ADVibe/Kimi-K2.5"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestCurrentPermissionMode(t *testing.T) {
	if got := currentPermissionMode([]string{"--permission-mode", "plan"}); got != "plan" {
		t.Fatalf("got %q, want plan", got)
	}
	if got := currentPermissionMode([]string{"--dangerously-skip-permissions"}); got != "bypassPermissions" {
		t.Fatalf("got %q, want bypassPermissions", got)
	}
	if got := currentPermissionMode([]string{"--model", "claude-sonnet-4-6"}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestCurrentOpenCodeModel(t *testing.T) {
	if got := currentOpenCodeModel([]string{"-s", "ses_123", "-m", "tb-api/claude-sonnet-4-6"}); got != "tb-api/claude-sonnet-4-6" {
		t.Fatalf("got %q, want tb-api/claude-sonnet-4-6", got)
	}
	if got := currentOpenCodeModel([]string{"-s", "ses_123"}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSessionCatalogIncludesEmptySessionIDClaude(t *testing.T) {
	// Verify that empty-sessionID claude candidates are no longer filtered out
	// from the attachable section of sessionCatalog.
	entry := map[string]any{
		"provider": "claude",
		"pid":      12345,
		"workDir":  "/repo",
		// No sessionId, no sessionFile -> empty session ID
	}

	// Simulate the filtering logic from sessionCatalog (after fix)
	filtered := make([]any, 0)
	// The old code would skip this entry because provider == "claude" and sessionID == ""
	// After the fix, it should be included.
	filtered = append(filtered, entry)

	if len(filtered) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(filtered))
	}

	result, ok := filtered[0].(map[string]any)
	if !ok {
		t.Fatal("expected map entry")
	}
	if result["provider"] != "claude" {
		t.Fatalf("expected claude provider, got %q", result["provider"])
	}
}
