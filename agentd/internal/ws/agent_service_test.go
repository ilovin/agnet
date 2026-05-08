package ws

import (
	"testing"
)

func TestAgentServiceFindExecutableFallback(t *testing.T) {
	svc := NewAgentService()
	got := svc.FindExecutable("nonexistent-binary-xyz")
	if got != "nonexistent-binary-xyz" {
		t.Fatalf("FindExecutable fallback = %q, want nonexistent-binary-xyz", got)
	}
}

func TestAgentServiceResolveLaunchClaudePermissionModeOverride(t *testing.T) {
	svc := NewAgentService()
	provider, cmd, args, env := svc.ResolveLaunch("claude", "", nil, "", "", "plan")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if len(args) < 3 {
		t.Fatalf("args too short: %v", args)
	}
	if args[2] != "plan" {
		t.Fatalf("permission mode = %q, want plan (args=%v)", args[2], args)
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want empty", env)
	}
	_ = cmd
}

func TestAgentServiceResolveLaunchClaudeWithEnv(t *testing.T) {
	svc := NewAgentService()
	provider, _, _, env := svc.ResolveLaunch("claude-bedrock", "", nil, "", "", "")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	found := false
	for _, e := range env {
		if e == "CLAUDE_CODE_USE_BEDROCK=1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("env = %v, want CLAUDE_CODE_USE_BEDROCK=1", env)
	}
}

func TestAgentServiceResolveLaunchOpencodeNoSession(t *testing.T) {
	svc := NewAgentService()
	provider, _, args, _ := svc.ResolveLaunch("opencode", "", nil, "", "", "")
	if provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", provider)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty when no session/model", args)
	}
}

func TestAgentServiceCurrentPermissionModeEmpty(t *testing.T) {
	svc := NewAgentService()
	if got := svc.CurrentPermissionMode(nil); got != "" {
		t.Fatalf("CurrentPermissionMode(nil) = %q, want empty", got)
	}
	if got := svc.CurrentPermissionMode([]string{}); got != "" {
		t.Fatalf("CurrentPermissionMode([]) = %q, want empty", got)
	}
}

func TestAgentServiceCurrentPermissionModeDangerouslySkip(t *testing.T) {
	svc := NewAgentService()
	got := svc.CurrentPermissionMode([]string{"--dangerously-skip-permissions"})
	if got != "bypassPermissions" {
		t.Fatalf("got %q, want bypassPermissions", got)
	}
}

func TestAgentServiceCurrentOpenCodeModelEmpty(t *testing.T) {
	svc := NewAgentService()
	if got := svc.CurrentOpenCodeModel(nil); got != "" {
		t.Fatalf("CurrentOpenCodeModel(nil) = %q, want empty", got)
	}
	if got := svc.CurrentOpenCodeModel([]string{}); got != "" {
		t.Fatalf("CurrentOpenCodeModel([]) = %q, want empty", got)
	}
	if got := svc.CurrentOpenCodeModel([]string{"-s", "abc"}); got != "" {
		t.Fatalf("CurrentOpenCodeModel([-s abc]) = %q, want empty", got)
	}
}

func TestAgentServiceFindClaudeSettingsEmpty(t *testing.T) {
	svc := NewAgentService()
	got := svc.FindClaudeSettings()
	_ = got
}

func TestAgentServiceProviderIDFromConfigEmpty(t *testing.T) {
	svc := NewAgentService()
	if got := svc.ProviderIDFromConfig("", nil, ""); got != "" {
		t.Fatalf("ProviderIDFromConfig(empty) = %q, want empty", got)
	}
}

func TestAgentServiceProviderIDFromConfigInvalidJSON(t *testing.T) {
	svc := NewAgentService()
	if got := svc.ProviderIDFromConfig("not-json", nil, ""); got != "" {
		t.Fatalf("ProviderIDFromConfig(invalid) = %q, want empty", got)
	}
}

func TestAgentServiceProviderIDFromConfigMatch(t *testing.T) {
	svc := NewAgentService()
	config := `{"id":"prov-123","model":"claude-sonnet","env":{"ANTHROPIC_BASE_URL":"http://test"}}`
	runtimeEnv := map[string]any{
		"ANTHROPIC_BASE_URL": "http://test",
	}
	if got := svc.ProviderIDFromConfig(config, runtimeEnv, "claude-sonnet"); got != "prov-123" {
		t.Fatalf("ProviderIDFromConfig(match) = %q, want prov-123", got)
	}
}

func TestAgentServiceProviderIDFromConfigMismatchURL(t *testing.T) {
	svc := NewAgentService()
	config := `{"id":"prov-123","env":{"ANTHROPIC_BASE_URL":"http://test"}}`
	runtimeEnv := map[string]any{
		"ANTHROPIC_BASE_URL": "http://other",
	}
	if got := svc.ProviderIDFromConfig(config, runtimeEnv, ""); got != "" {
		t.Fatalf("ProviderIDFromConfig(mismatch) = %q, want empty", got)
	}
}

func TestAgentServiceProviderIDFromConfigMismatchModel(t *testing.T) {
	svc := NewAgentService()
	config := `{"id":"prov-123","model":"claude-sonnet"}`
	if got := svc.ProviderIDFromConfig(config, nil, "other-model"); got != "" {
		t.Fatalf("ProviderIDFromConfig(mismatch model) = %q, want empty", got)
	}
}
