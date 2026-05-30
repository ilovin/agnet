package agent

import (
	"encoding/json"

	"github.com/phone-talk/agentd/internal/watcher"
)

// NormalizeWatcherEvent converts a watcher event into a canonical data payload.
// Returns false when the event carries no renderable content.
func NormalizeWatcherEvent(ev watcher.ConversationEvent, sessionID string) (map[string]any, bool) {
	// Interactive tool payloads use structured event kinds.
	if ev.ToolUseName != "" {
		if kind, payload, ok := ParseInteractiveToolUse(ev.ToolUseName, ev.ToolUseID, ev.ToolUseInput); ok {
			payloadBytes, _ := json.Marshal(payload)
			var payloadMap map[string]any
			_ = json.Unmarshal(payloadBytes, &payloadMap)
			data := map[string]any{
				"role": "assistant",
				"raw":  false,
				"kind": kind,
			}
			if sessionID != "" {
				data["sessionId"] = sessionID
			}
			if key := PayloadKeyForKind(kind); key != "" {
				data[key] = payloadMap
			}
			if ev.StatusChange != nil {
				data["statusChange"] = string(*ev.StatusChange)
			}
			return data, true
		}
	}

	if ev.Text == "" && ev.Role == "" && ev.Kind == "" && ev.ToolName == "" && ev.ToolSummary == "" {
		return nil, false
	}

	data := map[string]any{
		"role": ev.Role,
		"text": ev.Text,
		"raw":  false,
	}
	if ev.Kind != "" {
		data["kind"] = ev.Kind
	}
	if ev.ToolName != "" {
		data["toolName"] = ev.ToolName
	}
	if ev.ToolSummary != "" {
		data["toolSummary"] = ev.ToolSummary
	}
	if ev.MsgID != "" {
		data["msg_id"] = ev.MsgID
	}
	if sessionID != "" {
		data["sessionId"] = sessionID
	}
	if ev.StatusChange != nil {
		data["statusChange"] = string(*ev.StatusChange)
	}
	return data, true
}
