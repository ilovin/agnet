package agent

// Event kind constants for JSON-RPC conversation events (R-010 T1).
// These are the values that appear in the "kind" field of conversation
// events pushed over the WebSocket to Flutter clients.
const (
	// KindToolUse is emitted when Claude invokes a tool (Bash, Edit, Write, etc.).
	KindToolUse = "tool_use"
	// KindToolResult is emitted with the output of a completed tool call.
	KindToolResult = "tool_result"
	// KindPermissionRequest is emitted when agentd receives a control_request
	// asking the user to approve a tool invocation.
	KindPermissionRequest = "permission_request"
	// KindPermissionPrompt is a legacy PTY-detection fallback kind.
	KindPermissionPrompt = "permission_prompt"
	// KindAskUserQuestion is emitted when Claude wants to ask the user one or
	// more structured questions (AskUserQuestion tool_use event). The payload
	// is AskUserQuestionPayload serialised as permissionRequest-style data.
	KindAskUserQuestion = "ask_user_question"
	// KindExitPlanMode is emitted when Claude presents a plan to the user and
	// asks them to approve it before execution (ExitPlanMode tool_use event).
	KindExitPlanMode = "exit_plan_mode"
)

// AskUserQuestionPayload is the event payload for KindAskUserQuestion events.
// It corresponds to Claude Code's AskUserQuestion tool call.
type AskUserQuestionPayload struct {
	ToolUseID string            `json:"tool_use_id"`
	Questions []AskUserQuestion `json:"questions"`
}

// AskUserQuestion is a single question within an AskUserQuestionPayload.
type AskUserQuestion struct {
	Question    string                  `json:"question"`
	Header      string                  `json:"header,omitempty"`
	MultiSelect bool                    `json:"multi_select"`
	Options     []AskUserQuestionOption `json:"options"`
}

// AskUserQuestionOption is one selectable choice in an AskUserQuestion.
type AskUserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// ExitPlanModePayload is the event payload for KindExitPlanMode events.
// It corresponds to Claude Code's ExitPlanMode tool call.
type ExitPlanModePayload struct {
	ToolUseID string `json:"tool_use_id"`
	Plan      string `json:"plan"`
}
