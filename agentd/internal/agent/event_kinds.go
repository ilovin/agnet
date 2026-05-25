package agent

import "encoding/json"

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

// ParseInteractiveToolUse inspects a Claude tool_use block and returns
// (kind, payload, true) for AskUserQuestion / ExitPlanMode, otherwise
// returns ("", nil, false) so the caller can fall back to default tool_use.
//
// toolUseID is the Claude-assigned ID for the tool_use block; input is the
// already-decoded JSON input object (nil is treated as empty).
func ParseInteractiveToolUse(name string, toolUseID string, inputRaw json.RawMessage) (kind string, payload any, ok bool) {
	switch name {
	case "AskUserQuestion":
		p := parseAskUserQuestion(toolUseID, inputRaw)
		if p == nil {
			return "", nil, false
		}
		return KindAskUserQuestion, p, true

	case "ExitPlanMode":
		p := parseExitPlanMode(toolUseID, inputRaw)
		if p == nil {
			return "", nil, false
		}
		return KindExitPlanMode, p, true

	default:
		return "", nil, false
	}
}

// parseAskUserQuestion decodes the AskUserQuestion tool input into a payload.
// Returns nil on malformed input (no panic).
func parseAskUserQuestion(toolUseID string, inputRaw json.RawMessage) *AskUserQuestionPayload {
	if len(inputRaw) == 0 {
		return nil
	}
	var raw struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multi_select"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
				Preview     string `json:"preview"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(inputRaw, &raw); err != nil {
		return nil
	}
	if len(raw.Questions) == 0 {
		return nil
	}
	questions := make([]AskUserQuestion, 0, len(raw.Questions))
	for _, q := range raw.Questions {
		opts := make([]AskUserQuestionOption, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, AskUserQuestionOption{
				Label:       o.Label,
				Description: o.Description,
				Preview:     o.Preview,
			})
		}
		questions = append(questions, AskUserQuestion{
			Question:    q.Question,
			Header:      q.Header,
			MultiSelect: q.MultiSelect,
			Options:     opts,
		})
	}
	return &AskUserQuestionPayload{
		ToolUseID: toolUseID,
		Questions: questions,
	}
}

// parseExitPlanMode decodes the ExitPlanMode tool input into a payload.
// Returns nil on malformed input (no panic).
func parseExitPlanMode(toolUseID string, inputRaw json.RawMessage) *ExitPlanModePayload {
	if len(inputRaw) == 0 {
		return nil
	}
	var raw struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(inputRaw, &raw); err != nil {
		return nil
	}
	return &ExitPlanModePayload{
		ToolUseID: toolUseID,
		Plan:      raw.Plan,
	}
}
