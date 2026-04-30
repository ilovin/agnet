package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// PermissionResolver handles permission prompt detection and text cleaning.
type PermissionResolver struct{}

// NewPermissionResolver creates a new PermissionResolver.
func NewPermissionResolver() *PermissionResolver {
	return &PermissionResolver{}
}

// CleanPermissionText removes ANSI sequences and normalizes permission prompt text.
func (pr *PermissionResolver) CleanPermissionText(text string) string {
	result := text
	// CSI sequences
	result = regexp.MustCompile(`\x1B\[[0-9;]*[a-zA-Z]`).ReplaceAllString(result, "")
	// OSC sequences
	result = regexp.MustCompile(`\x1B\][^\x07]*\x07`).ReplaceAllString(result, "")
	// Box drawing and UI symbols
	result = regexp.MustCompile(`[⏵❯⏸◉◆│─┌┐└┘❯▶▸▷⏹]`).ReplaceAllString(result, " ")
	// Normalize whitespace
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

// DetectPermissionPrompt checks if text contains a permission prompt.
func (pr *PermissionResolver) DetectPermissionPrompt(text string) bool {
	cleaned := pr.CleanPermissionText(text)
	lower := strings.ToLower(cleaned)

	if strings.Contains(lower, "bypass") && strings.Contains(lower, "permission") {
		return true
	}
	if strings.Contains(lower, "permission") && strings.Contains(lower, "shift") {
		return true
	}
	if strings.Contains(lower, "shift+tab") && strings.Contains(lower, "cycle") {
		return true
	}

	// Legacy patterns
	if strings.Contains(text, "⏵⏵") && strings.Contains(lower, "bypass") {
		return true
	}
	if strings.Contains(text, "❯") && strings.Contains(lower, "shift+tab") {
		return true
	}
	if strings.Contains(lower, "ctrl+g") && strings.Contains(lower, "vim") {
		return true
	}
	return false
}

// MaybeExtractSessionIDFromRaw attempts to extract a session ID from raw JSON text.
func (pr *PermissionResolver) MaybeExtractSessionIDFromRaw(text string) string {
	if text == "" {
		return ""
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(text), &event); err != nil {
		return ""
	}
	candidates := []any{
		event["session_id"],
		event["sessionId"],
	}
	if msg, ok := event["message"].(map[string]any); ok {
		candidates = append(candidates, msg["session_id"], msg["sessionId"])
	}
	if result, ok := event["result"].(map[string]any); ok {
		candidates = append(candidates, result["session_id"], result["sessionId"])
	}
	for _, c := range candidates {
		if s, ok := c.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}
