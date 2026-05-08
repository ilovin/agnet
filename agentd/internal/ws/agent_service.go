package ws

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// AgentService contains business logic extracted from the WebSocket handler.
// It provides pure functions for launch resolution, provider discovery, and
// configuration parsing without depending on handler state.
type AgentService interface {
	// Launch utilities
	FindExecutable(name string) string
	ResolveLaunch(provider, cmd string, args []string, sessionID, model, permissionMode string) (resolvedProvider, resolvedCmd string, resolvedArgs, env []string)
	CurrentPermissionMode(args []string) string
	CurrentOpenCodeModel(args []string) string

	// Provider configuration
	FindClaudeSettings() string
	ProviderIDFromConfig(configJSON string, runtimeEnv map[string]any, runtimeModel string) string
}

// agentService is the default implementation of AgentService.
type agentService struct{}

// NewAgentService creates a new AgentService instance.
func NewAgentService() AgentService {
	return &agentService{}
}

// FindExecutable searches for a binary in PATH and common user-local locations.
// When agentd runs as root, user-installed binaries (e.g. ~/.opencode/bin/opencode)
// are not in root's PATH, so we check /home/*/ directories as well.
func (s *agentService) FindExecutable(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	// Scan /home/*/ for common install locations
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			candidates := []string{
				filepath.Join("/home", e.Name(), ".local", "bin", name),
				filepath.Join("/home", e.Name(), "."+name, "bin", name),
				filepath.Join("/home", e.Name(), "bin", name),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					return c
				}
			}
		}
	}
	return name // fallback to bare name
}

// ResolveLaunch resolves the launch command and arguments for a given provider.
func (s *agentService) ResolveLaunch(provider, cmd string, args []string, sessionID, model, permissionMode string) (string, string, []string, []string) {
	resolvedProvider := provider
	resolvedCmd := cmd
	resolvedArgs := append([]string{}, args...)
	var env []string

	switch provider {
	case "opencode":
		resolvedCmd = s.FindExecutable("opencode")
		resolvedArgs = []string{}
		if sessionID != "" {
			resolvedArgs = []string{"-s", sessionID}
		}
		if model != "" {
			resolvedArgs = append(resolvedArgs, "-m", model)
		}
	case "claude", "claude-bedrock", "claude-vertex":
		resolvedCmd = s.FindExecutable("claude")
		resolvedArgs = []string{
			"-p",
			"--permission-mode", "bypassPermissions",
			"--output-format", "stream-json",
			"--include-partial-messages",
			"--verbose",
		}
		if permissionMode != "" {
			resolvedArgs[2] = permissionMode
		}
		if sessionID != "" {
			resolvedArgs = append(resolvedArgs, "--resume", sessionID)
		}
		if model != "" {
			resolvedArgs = append(resolvedArgs, "--model", model)
		}
		// Provider-specific environment variables
		switch provider {
		case "claude-bedrock":
			env = append(env, "CLAUDE_CODE_USE_BEDROCK=1")
			resolvedProvider = "claude"
		case "claude-vertex":
			env = append(env, "CLAUDE_CODE_USE_VERTEX=1")
			resolvedProvider = "claude"
		}
	default:
		if resolvedCmd == "" {
			resolvedCmd = "claude"
			resolvedArgs = []string{"--permission-mode", "bypassPermissions"}
			if sessionID != "" {
				resolvedArgs = append(resolvedArgs, "--resume", sessionID)
			}
			if model != "" {
				resolvedArgs = append(resolvedArgs, "--model", model)
			}
		}
	}

	return resolvedProvider, resolvedCmd, resolvedArgs, env
}

// CurrentPermissionMode extracts the --permission-mode value from args.
func (s *agentService) CurrentPermissionMode(args []string) string {
	for i, a := range args {
		if a == "--permission-mode" && i+1 < len(args) {
			return args[i+1]
		}
		if a == "--dangerously-skip-permissions" {
			return "bypassPermissions"
		}
	}
	return ""
}

// CurrentOpenCodeModel extracts the -m value from opencode args.
func (s *agentService) CurrentOpenCodeModel(args []string) string {
	for i, a := range args {
		if a == "-m" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// FindClaudeSettings searches for Claude's settings.json in common locations.
func (s *agentService) FindClaudeSettings() string {
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".claude", "settings.json")}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, filepath.Join("/home", e.Name(), ".claude", "settings.json"))
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ProviderIDFromConfig matches a provider config against runtime settings.
func (s *agentService) ProviderIDFromConfig(configJSON string, runtimeEnv map[string]any, runtimeModel string) string {
	if configJSON == "" {
		return ""
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return ""
	}
	cfgEnv, _ := cfg["env"].(map[string]any)
	providerURL, _ := cfgEnv["ANTHROPIC_BASE_URL"].(string)
	providerToken, _ := cfgEnv["ANTHROPIC_AUTH_TOKEN"].(string)
	providerModel, _ := cfg["model"].(string)
	if providerModel == "" {
		providerModel, _ = cfgEnv["ANTHROPIC_MODEL"].(string)
	}

	actualURL, _ := runtimeEnv["ANTHROPIC_BASE_URL"].(string)
	actualToken, _ := runtimeEnv["ANTHROPIC_AUTH_TOKEN"].(string)
	actualModel := runtimeModel
	if actualModel == "" {
		actualModel, _ = runtimeEnv["ANTHROPIC_MODEL"].(string)
	}

	if providerURL != "" && providerURL != actualURL {
		return ""
	}
	if providerToken != "" && providerToken != actualToken {
		return ""
	}
	if providerModel != "" && providerModel != actualModel {
		return ""
	}
	id, _ := cfg["id"].(string)
	return id
}
