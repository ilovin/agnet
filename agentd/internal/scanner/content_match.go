package scanner

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var tuiDecorationRe = regexp.MustCompile(`[─━│┃┌┐└┘├┤┬┴┼╔╗╚╝║═⏺⏵✻※❯⎿]`)
var whitespaceRe = regexp.MustCompile(`\s+`)
var markdownRe = regexp.MustCompile("[*`#\\[\\]]")

// captureTmuxPane captures the visible content of a tmux pane.
func captureTmuxPane(target string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-200")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// cleanTUIText removes TUI decorations and normalizes whitespace for matching.
func cleanTUIText(raw string) string {
	clean := tuiDecorationRe.ReplaceAllString(raw, " ")
	clean = whitespaceRe.ReplaceAllString(clean, " ")
	return strings.TrimSpace(clean)
}

// extractFingerprints extracts text snippets from a JSONL session file.
// Scans backwards from the end, collecting fingerprints from assistant text
// and user messages. Pure tool_use messages (no text block) are skipped.
// Stops when maxFPs fingerprints are collected.
func extractFingerprints(jsonlPath string, maxFPs int) []string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Read the last 64KB to find recent messages
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	tailSize := int64(65536)
	if info.Size() < tailSize {
		tailSize = info.Size()
	}
	buf := make([]byte, tailSize)
	if _, err := f.ReadAt(buf, info.Size()-tailSize); err != nil {
		return nil
	}

	lines := strings.Split(string(buf), "\n")
	var fps []string

	for i := len(lines) - 1; i >= 0 && len(fps) < maxFPs; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		msgType, _ := obj["type"].(string)
		if msgType != "assistant" && msgType != "user" {
			continue
		}

		message, ok := obj["message"].(map[string]interface{})
		if !ok {
			continue
		}

		content := message["content"]

		// String content (user messages)
		if str, ok := content.(string); ok {
			t := strings.TrimSpace(str)
			if len(t) >= 3 && len(t) <= 80 {
				fps = append(fps, t)
			}
			continue
		}

		// Array content (assistant messages)
		contentArr, ok := content.([]interface{})
		if !ok {
			continue
		}

		hasText := false
		for _, item := range contentArr {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if block["type"] != "text" {
				continue
			}
			hasText = true
			text, _ := block["text"].(string)
			text = strings.TrimSpace(text)
			text = markdownRe.ReplaceAllString(text, "")
			for _, l := range strings.Split(text, "\n") {
				l = strings.TrimSpace(l)
				if len(l) > 15 && len(l) < 80 {
					fps = append(fps, l)
					if len(fps) >= maxFPs {
						break
					}
				}
			}
			if len(fps) >= maxFPs {
				break
			}
		}
		// Pure tool_use (no text block) — skip, don't count
		if !hasText {
			continue
		}
	}

	return fps
}

// matchScore counts how many fingerprints appear as substrings in paneText.
func matchScore(paneText string, fingerprints []string) int {
	hits := 0
	for _, fp := range fingerprints {
		if strings.Contains(paneText, fp) {
			hits++
		}
	}
	return hits
}

// contentMatchSession finds the best matching session from candidates by comparing
// tmux pane content with JSONL fingerprints. Returns the matched SessionCandidate
// or nil if no confident match.
func contentMatchSession(tmuxTarget string, candidates []SessionCandidate, forcedSessionIDs map[string]bool) *SessionCandidate {
	if tmuxTarget == "" || len(candidates) == 0 {
		return nil
	}

	paneRaw, err := captureTmuxPane(tmuxTarget)
	if err != nil {
		log.Printf("[ContentMatch] capture-pane failed for %s: %v", tmuxTarget, err)
		return nil
	}
	paneText := cleanTUIText(paneRaw)
	if len(paneText) < 20 {
		log.Printf("[ContentMatch] reject: pane text too short (%d)", len(paneText))
		return nil
	}

	isForced := func(sid string) bool {
		if forcedSessionIDs == nil {
			return false
		}
		return forcedSessionIDs[sid]
	}

	maxByStage := []int{5, 12, 20}
	if len(candidates) < 20 {
		maxByStage = []int{5, 12, len(candidates)}
	}

	log.Printf("[ContentMatch] candidate_total=%d staged_max=%v", len(candidates), maxByStage)

	bestScore := 0
	var bestCandidate *SessionCandidate
	seen := make(map[string]bool, len(candidates))

	for _, stageMax := range maxByStage {
		if stageMax <= 0 {
			continue
		}
		if stageMax > len(candidates) {
			stageMax = len(candidates)
		}

		stageCandidates := make([]SessionCandidate, 0, stageMax)
		for i := 0; i < stageMax; i++ {
			if seen[candidates[i].SessionID] {
				continue
			}
			stageCandidates = append(stageCandidates, candidates[i])
		}
		for _, c := range candidates {
			if !isForced(c.SessionID) || seen[c.SessionID] {
				continue
			}
			already := false
			for _, existing := range stageCandidates {
				if existing.SessionID == c.SessionID {
					already = true
					break
				}
			}
			if !already {
				stageCandidates = append(stageCandidates, c)
			}
		}

		if len(stageCandidates) == 0 {
			continue
		}
		log.Printf("[ContentMatch] stage_max=%d scored_candidates=%d", stageMax, len(stageCandidates))

		for i := range stageCandidates {
			seen[stageCandidates[i].SessionID] = true
			fps := extractFingerprints(stageCandidates[i].JSONLPath, 20)
			score := matchScore(paneText, fps)
			log.Printf("[ContentMatch] candidate=%s fpCount=%d toolFpCount=%d score=%d forced=%t", stageCandidates[i].SessionID, len(fps), 0, score, isForced(stageCandidates[i].SessionID))
			if score > bestScore {
				bestScore = score
				candidate := stageCandidates[i]
				bestCandidate = &candidate
			}
		}
		if bestScore > 0 {
			break
		}
	}

	if bestCandidate != nil && bestScore > 0 {
		log.Printf("[ContentMatch] pane %s → session %s (score %d)", tmuxTarget, bestCandidate.SessionID, bestScore)
		return bestCandidate
	}

	log.Printf("[ContentMatch] reject: no confident match among %d candidates", len(candidates))
	return nil
}
