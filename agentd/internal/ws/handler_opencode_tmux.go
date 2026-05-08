package ws

import (
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
)

// ansiRegex matches ANSI escape sequences like \x1b[31m, \x1b[0;1;32m, etc.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// spinnerChars is the set of Unicode characters commonly used in TUI spinners.
const spinnerChars = "\u2b9d\u25a0\u25cf\u25cb\u25d0\u25d1\u25d2\u25d3\u2591\u2592\u2593\u2588"

// isSpinnerLine detects whether a line is a spinner/progress animation line.
// It strips ANSI codes, then checks:
//   1. Known footer pattern: "esc interrupt" (OpenCode's status footer)
//   2. Heuristic: >50% of non-space characters are spinner chars
func isSpinnerLine(line string) bool {
	stripped := stripANSI(line)
	trimmed := strings.TrimSpace(stripped)
	if trimmed == "" {
		return false
	}

	// Known pattern: OpenCode footer line with esc interrupt
	if strings.Contains(trimmed, "esc interrupt") {
		return true
	}

	// Heuristic: count spinner chars vs non-space chars
	spinnerCount := 0
	nonSpaceCount := 0
	for _, r := range trimmed {
		if r == ' ' {
			continue
		}
		nonSpaceCount++
		if strings.ContainsRune(spinnerChars, r) {
			spinnerCount++
		}
	}

	if nonSpaceCount > 0 && float64(spinnerCount)/float64(nonSpaceCount) > 0.5 {
		return true
	}

	return false
}

// captureOpenCodeTmuxResponses polls a tmux pane for OpenCode output after
// sending a user message. It captures pane content, diffs against the previous
// capture, and broadcasts new assistant text as conversation.message events.
//
// This runs in a background goroutine started by conversationSend when the
// agent is tmux-attached and provider is opencode.
func (h *handler) captureOpenCodeTmuxResponses(ag *agent.Agent, userMessage string) {
	agentID := ag.ID
	tmuxTarget := ag.TmuxTarget()
	if tmuxTarget == "" {
		log.Printf("[OpenCodeTmux] no tmux target for agent %s, skipping capture", agentID)
		return
	}

	log.Printf("[OpenCodeTmux] Starting pane capture for agent %s target=%s", agentID, tmuxTarget)

	// Broadcast working status
	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(agentID, "working")), nil)

	// Wait for OpenCode to finish its "thinking" spinner phase before capturing.
	time.Sleep(3 * time.Second)

	// Initial capture before the user message appears (or just after send)
	baseline, err := captureTmuxPaneWithEscapes(tmuxTarget)
	if err != nil {
		log.Printf("[OpenCodeTmux] initial capture failed for %s: %v", tmuxTarget, err)
		baseline = ""
	}
	// Trim the user message from baseline so we don't echo it back
	baseline = stripUserMessageFromBaseline(baseline, userMessage)

	lastCaptured := baseline
	var fullResponse strings.Builder
	idleCount := 0
	const maxIdlePolls = 30 // 30 * 1.5s = 45s max wait after last change
	const pollInterval = 1500 * time.Millisecond

	for i := 0; i < maxIdlePolls; i++ {
		time.Sleep(pollInterval)

		paneContent, err := captureTmuxPaneWithEscapes(tmuxTarget)
		if err != nil {
			log.Printf("[OpenCodeTmux] capture-pane failed for %s: %v", tmuxTarget, err)
			continue
		}

		// Diff: find new content since last capture
		newText := diffPaneContent(lastCaptured, paneContent)
		if newText != "" {
			log.Printf("[OpenCodeTmux] agent %s new text (len=%d): %q", agentID, len(newText), truncate(newText, 80))
			fullResponse.WriteString(newText)
			lastCaptured = paneContent
			idleCount = 0

			// Broadcast partial response for real-time display
			h.server.broadcast(event("conversation.message", map[string]any{
				"agentId": agentID,
				"role":    "assistant",
				"text":    newText,
				"partial": true,
			}), nil)
		} else {
			idleCount++
		}

		// Heuristic: stop polling if pane has been idle for a while and we have content
		if idleCount >= 4 && fullResponse.Len() > 0 { // 6 seconds idle with content
			break
		}
	}

	// Record final assistant response
	if fullResponse.Len() > 0 {
		finalText := fullResponse.String()
		if _, err := h.server.manager.RecordConversationEvent(agentID, map[string]any{
			"role": "assistant",
			"text": finalText,
			"raw":  false,
		}); err != nil {
			log.Printf("[OpenCodeTmux] record assistant message: %v", err)
		}
		h.server.broadcast(event("conversation.message", map[string]any{
			"agentId": agentID,
			"role":    "assistant",
			"text":    finalText,
			"final":   true,
		}), nil)
		log.Printf("[OpenCodeTmux] agent %s final response (len=%d)", agentID, len(finalText))
	} else {
		log.Printf("[OpenCodeTmux] agent %s no response captured", agentID)
	}

	// Broadcast status back to idle
	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(agentID, "idle")), nil)
	log.Printf("[OpenCodeTmux] capture completed for agent %s", agentID)
}

// captureTmuxPaneWithEscapes captures tmux pane content including ANSI escape
// sequences (-e flag) so color and formatting are preserved for the frontend.
func captureTmuxPaneWithEscapes(target string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-e", "-S", "-500")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// stripUserMessageFromBaseline removes the just-sent user message from the
// baseline capture so diffing doesn't echo it back as assistant output.
func stripUserMessageFromBaseline(baseline, userMessage string) string {
	// OpenCode TUI typically shows the user message near the bottom.
	// Remove exact match and any trailing lines that look like the prompt.
	idx := strings.LastIndex(baseline, userMessage)
	if idx >= 0 {
		return baseline[:idx]
	}
	return baseline
}

// diffPaneContent computes the text added between oldContent and newContent.
// It handles the case where newContent is a longer version of oldContent
// (scrolling terminal) or where oldContent is a suffix of newContent.
//
// ANSI escape sequences are stripped for comparison so that TUI re-renders
// with different color codes but identical visible text do not produce
// spurious diffs.
func diffPaneContent(oldContent, newContent string) string {
	if oldContent == "" {
		return newContent
	}
	if newContent == "" {
		return ""
	}

	// Fast path: exact match
	if oldContent == newContent {
		return ""
	}

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Build stripped versions for comparison
	oldStripped := make([]string, len(oldLines))
	for i, line := range oldLines {
		oldStripped[i] = stripANSI(line)
	}
	newStripped := make([]string, len(newLines))
	for i, line := range newLines {
		newStripped[i] = stripANSI(line)
	}

	// Find first differing line from the top.
	firstDiff := 0
	maxFirst := min(len(oldStripped), len(newStripped))
	for firstDiff < maxFirst {
		if oldStripped[firstDiff] != newStripped[firstDiff] {
			break
		}
		firstDiff++
	}

	// If all lines match and new is longer, only the tail is new.
	if firstDiff == len(oldStripped) && firstDiff < len(newStripped) {
		changed := newLines[firstDiff:]
		filtered := filterSpinnerLines(changed)
		if len(filtered) == 0 {
			return ""
		}
		return strings.Join(filtered, "\n")
	}

	// If every line in new is already present in old, nothing is new.
	if firstDiff == len(newStripped) {
		return ""
	}

	// Find last differing line from the bottom.
	lastDiffOld := len(oldStripped) - 1
	lastDiffNew := len(newStripped) - 1
	for lastDiffOld >= firstDiff && lastDiffNew >= firstDiff {
		if oldStripped[lastDiffOld] == newStripped[lastDiffNew] {
			lastDiffOld--
			lastDiffNew--
		} else {
			break
		}
	}

	// Return only the actually changed lines, filtering out spinner lines.
	if firstDiff <= lastDiffNew {
		changed := newLines[firstDiff : lastDiffNew+1]
		filtered := filterSpinnerLines(changed)
		if len(filtered) == 0 {
			return ""
		}
		return strings.Join(filtered, "\n")
	}

	// Fallback: try to find oldContent as a substring of newContent
	if idx := strings.Index(newContent, oldContent); idx >= 0 {
		// oldContent appears inside newContent; return the new prefix + suffix
		prefix := newContent[:idx]
		suffix := ""
		if idx+len(oldContent) < len(newContent) {
			suffix = newContent[idx+len(oldContent):]
		}
		return prefix + suffix
	}

	// Old content may have scrolled out; try suffix/prefix overlap match
	maxOverlap := min(len(oldContent), len(newContent))
	for overlap := maxOverlap; overlap > 0; overlap-- {
		if strings.HasSuffix(oldContent, newContent[:overlap]) {
			return newContent[overlap:]
		}
	}

	// Fallback: if newContent is longer, return the tail
	if len(newContent) > len(oldContent) && strings.HasPrefix(newContent, oldContent[:min(len(oldContent), 100)]) {
		return newContent[len(oldContent):]
	}

	// No clear relationship; return all new content
	return newContent
}

// filterSpinnerLines returns a new slice with spinner lines removed.
// If all lines are spinner lines, returns an empty slice.
func filterSpinnerLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if !isSpinnerLine(line) {
			result = append(result, line)
		}
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
