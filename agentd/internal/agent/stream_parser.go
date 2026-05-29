package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StreamJSONEvent represents a parsed stream-json event.
type StreamJSONEvent struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Raw       map[string]any  `json:"-"` // Original raw data for accessing extra fields
}

// StreamParser handles parsing of stream-json output from agent processes.
type StreamParser struct{}

// NewStreamParser creates a new StreamParser.
func NewStreamParser() *StreamParser {
	return &StreamParser{}
}

// TryParseStreamJSON attempts to parse a line as stream-json format.
func (sp *StreamParser) TryParseStreamJSON(text string) *StreamJSONEvent {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
		return nil
	}

	var ev StreamJSONEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return nil
	}
	ev.Raw = rawMap

	// Validate it's a known stream-json event type
	switch ev.Type {
	case "init", "message", "user", "assistant", "tool_use", "tool_result",
		"result", "permission_prompt", "control_request", "stream_event", "system",
		"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_stop":
		return &ev
	default:
		return nil
	}
}

// BuildToolResultSummary extracts a concise summary from a tool result output.
// toolName is optional (may be empty if not available in the event).
func (sp *StreamParser) BuildToolResultSummary(toolName string, output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "(no output)"
	}
	// Strip surrounding JSON string quotes if present
	if len(text) >= 2 && text[0] == '"' {
		var s string
		if err := json.Unmarshal(output, &s); err == nil {
			text = strings.TrimSpace(s)
		}
	}

	lines := strings.Split(text, "\n")
	nonEmpty := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}

	switch toolName {
	case "Bash":
		// Show first 5 non-empty lines
		preview := nonEmpty
		if len(preview) > 5 {
			preview = preview[:5]
			return strings.Join(preview, "\n") + fmt.Sprintf("\n... (%d lines total)", len(nonEmpty))
		}
		return strings.Join(preview, "\n")
	case "Grep":
		return fmt.Sprintf("%d matches", len(nonEmpty))
	case "Read":
		return fmt.Sprintf("%d lines", len(nonEmpty))
	case "Write", "Edit":
		return "done"
	}

	// Generic: first 3 lines, max 300 chars
	preview := nonEmpty
	if len(preview) > 3 {
		preview = preview[:3]
	}
	result := strings.Join(preview, "\n")
	if len(result) > 300 {
		result = result[:300] + "..."
	}
	if len(nonEmpty) > 3 {
		result += fmt.Sprintf("\n... (%d lines total)", len(nonEmpty))
	}
	return result
}

// BuildToolInputSummary extracts a concise summary from tool input parameters.
//
// Guarantees a non-empty summary whenever a `toolName` we know how to describe
// is supplied, even if `input` is empty/unparseable. This prevents downstream
// activity cards from rendering a bare `[ToolName]` (no detail) which the
// Flutter app cannot turn into a meaningful card title — see
// agentapp/lib/screens/agent_detail_screen.dart `_buildActivityItem`.
//
// Streaming note: Anthropic stream-json emits `content_block_start` for a
// tool_use block before its input deltas have arrived, so `input` is `{}` at
// that point. We must still produce something useful for that early emission.
func (sp *StreamParser) BuildToolInputSummary(toolName string, input json.RawMessage) string {
	var params map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &params)
	}
	if params == nil {
		params = map[string]any{}
	}

	switch toolName {
	case "Bash":
		if cmd, ok := params["command"].(string); ok && cmd != "" {
			if len(cmd) > 100 {
				cmd = cmd[:100] + "..."
			}
			return cmd
		}
	case "Read":
		if path, ok := params["file_path"].(string); ok && path != "" {
			return path
		}
	case "Write":
		if path, ok := params["file_path"].(string); ok && path != "" {
			return path
		}
	case "Edit":
		if path, ok := params["file_path"].(string); ok && path != "" {
			return path
		}
	case "Grep":
		if pattern, ok := params["pattern"].(string); ok && pattern != "" {
			return "pattern: " + pattern
		}
	case "Glob":
		if pattern, ok := params["pattern"].(string); ok && pattern != "" {
			return pattern
		}
	case "Agent":
		if desc, ok := params["description"].(string); ok && desc != "" {
			return desc
		}
		if prompt, ok := params["prompt"].(string); ok && prompt != "" {
			if len(prompt) > 80 {
				prompt = prompt[:80] + "..."
			}
			return prompt
		}
		if subagent, ok := params["subagent_type"].(string); ok && subagent != "" {
			return subagent
		}
	case "SendMessage":
		to, _ := params["to"].(string)
		summary, _ := params["summary"].(string)
		if summary != "" && to != "" {
			return fmt.Sprintf("-> %s: %s", to, summary)
		}
		if to != "" {
			return "-> " + to
		}
		if summary != "" {
			return summary
		}
	case "TaskCreate":
		if subject, ok := params["subject"].(string); ok && subject != "" {
			return subject
		}
		if desc, ok := params["description"].(string); ok && desc != "" {
			return desc
		}
	case "TaskUpdate":
		taskId, _ := params["taskId"].(string)
		status, _ := params["status"].(string)
		if taskId != "" && status != "" {
			return fmt.Sprintf("#%s -> %s", taskId, status)
		}
		if taskId != "" {
			return "#" + taskId
		}
		if status != "" {
			return status
		}
	case "TaskList":
		return "查看任务列表"
	case "TodoWrite":
		return "更新任务"
	case "WebSearch":
		if query, ok := params["query"].(string); ok && query != "" {
			return query
		}
	case "WebFetch":
		if url, ok := params["url"].(string); ok && url != "" {
			return url
		}
	case "NotebookEdit":
		if path, ok := params["notebook_path"].(string); ok && path != "" {
			return path
		}
	}

	// Generic fallback: show first string value present in params.
	for _, v := range params {
		if s, ok := v.(string); ok && len(s) > 0 {
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	}

	// Last-resort fallback: if the tool name is non-empty, return a marker so
	// callers can produce `[ToolName: …]` rather than a bare `[ToolName]` whose
	// title would be empty after frontend parsing. The ellipsis signals
	// "details pending / unavailable" without being misleading.
	if toolName != "" {
		return "…"
	}
	return ""
}
