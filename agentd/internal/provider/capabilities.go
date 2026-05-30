package provider

// SendMode describes the primary send path for a provider.
type SendMode string

const (
	SendModePTY       SendMode = "pty"
	SendModeTMUX      SendMode = "tmux"
	SendModeResumeCmd SendMode = "resume_cmd"
	SendModeHTTP      SendMode = "http"
)

// Capabilities defines behavior flags for a provider.
type Capabilities struct {
	Name string

	SupportsThinking            bool
	SupportsToolUse             bool
	SupportsInteractiveQuestion bool
	SupportsPermissionPrompt    bool
	SupportsSessionSwitch       bool
	SupportsImageAttachment     bool

	// For tmux-attached providers, validates pane foreground command before send.
	RequiresTmuxForegroundValidation bool

	SendMode SendMode
}

var defaultCapabilities = map[string]Capabilities{
	"claude": {
		Name:                        "claude",
		SupportsThinking:            true,
		SupportsToolUse:             true,
		SupportsInteractiveQuestion: true,
		SupportsPermissionPrompt:    true,
		SupportsSessionSwitch:       true,
		SupportsImageAttachment:     true,
		SendMode:                    SendModePTY,
	},
	"codex": {
		Name:                             "codex",
		SupportsThinking:                 true,
		SupportsToolUse:                  true,
		SupportsInteractiveQuestion:      false,
		SupportsPermissionPrompt:         false,
		SupportsSessionSwitch:            true,
		SupportsImageAttachment:          true,
		RequiresTmuxForegroundValidation: true,
		SendMode:                         SendModeTMUX,
	},
	"opencode": {
		Name:                        "opencode",
		SupportsThinking:            true,
		SupportsToolUse:             true,
		SupportsInteractiveQuestion: true,
		SupportsPermissionPrompt:    false,
		SupportsSessionSwitch:       true,
		SupportsImageAttachment:     false,
		SendMode:                    SendModeResumeCmd,
	},
	"hermes": {
		Name:                             "hermes",
		SupportsThinking:                 true,
		SupportsToolUse:                  false,
		SupportsInteractiveQuestion:      false,
		SupportsPermissionPrompt:         false,
		SupportsSessionSwitch:            true,
		SupportsImageAttachment:          false,
		RequiresTmuxForegroundValidation: true,
		SendMode:                         SendModeTMUX,
	},
}

// CapabilitiesFor returns known capabilities for provider, with a safe default.
func CapabilitiesFor(name string) Capabilities {
	if c, ok := defaultCapabilities[name]; ok {
		return c
	}
	return Capabilities{
		Name:                    name,
		SupportsImageAttachment: true,
		SendMode:                SendModePTY,
	}
}
