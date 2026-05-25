package scanner

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"
)

var tuiDecorationRe = regexp.MustCompile(`[─━│┃┌┐└┘├┤┬┴┼╔╗╚╝║═⏺⏵✻※❯⎿]`)
var whitespaceRe = regexp.MustCompile(`\s+`)
var markdownRe = regexp.MustCompile("[*`#\\[\\]]")
var nonWordRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

const (
	contentMatchMinScore = 2
	contentMatchMinMargin = 2
)

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
	clean := strings.ToLower(raw)
	clean = markdownRe.ReplaceAllString(clean, " ")
	clean = tuiDecorationRe.ReplaceAllString(clean, " ")
	clean = nonWordRe.ReplaceAllString(clean, " ")
	clean = whitespaceRe.ReplaceAllString(clean, " ")
	return strings.TrimSpace(clean)
}

func appendTokenFingerprints(dst []string, token string) []string {
	token = cleanTUIText(token)
	if len(token) < 3 {
		return dst
	}
	if len(token) > 80 {
		token = token[:80]
	}
	return append(dst, token)
}

func appendToolUseFingerprints(dst []string, block map[string]interface{}) []string {
	name, _ := block["name"].(string)
	if name != "" {
		dst = appendTokenFingerprints(dst, "tool "+name)
		dst = appendTokenFingerprints(dst, name)
	}

	input, ok := block["input"].(map[string]interface{})
	if !ok {
		return dst
	}
	for _, key := range []string{"command", "description", "prompt", "query", "url", "pattern", "path", "file_path"} {
		v, ok := input[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		clean := cleanTUIText(v)
		if clean == "" {
			continue
		}
		parts := strings.Fields(clean)
		limit := 4
		if len(parts) < limit {
			limit = len(parts)
		}
		for i := 0; i < limit; i++ {
			dst = appendTokenFingerprints(dst, parts[i])
		}
	}
	return dst
}

// extractFingerprints extracts text snippets from a JSONL session file.
// Scans backwards from the end, collecting fingerprints from assistant text,
// user messages, and tool_use blocks. Stops when maxFPs fingerprints are collected.
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
			t := cleanTUIText(str)
			if len(t) >= 3 {
				if len(t) > 80 {
					t = t[:80]
				}
				fps = append(fps, t)
			}
			continue
		}

		// Array content (assistant messages)
		contentArr, ok := content.([]interface{})
		if !ok {
			continue
		}

		for _, item := range contentArr {
			if len(fps) >= maxFPs {
				break
			}
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				text = cleanTUIText(text)
				for _, l := range strings.Split(text, "\n") {
					l = cleanTUIText(l)
					if len(l) >= 8 && len(l) <= 120 {
						fps = append(fps, l)
						parts := strings.Fields(l)
						limit := 4
						if len(parts) < limit {
							limit = len(parts)
						}
						for i := 0; i < limit; i++ {
							if len(parts[i]) >= 3 {
								fps = append(fps, parts[i])
							}
						}
						if len(fps) >= maxFPs {
							break
						}
					}
				}
			case "tool_use":
				before := len(fps)
				fps = appendToolUseFingerprints(fps, block)
				if len(fps) > maxFPs {
					fps = fps[:maxFPs]
				}
				if len(fps) == before {
					continue
				}
			}
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

	return contentMatchSessionByPaneText(paneRaw, candidates, forcedSessionIDs)
}

func contentMatchSessionByPaneText(paneRaw string, candidates []SessionCandidate, forcedSessionIDs map[string]bool) *SessionCandidate {
	if len(candidates) == 0 {
		return nil
	}

	paneText := cleanTUIText(paneRaw)
	if utf8.RuneCountInString(paneText) < 20 {
		log.Printf("[ContentMatch] reject: pane text too short (%d runes)", utf8.RuneCountInString(paneText))
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
	secondBestScore := 0
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
			log.Printf("[ContentMatch] candidate=%s fpCount=%d toolFpCount=%d score=%d forced=%t", stageCandidates[i].SessionID, len(fps), countToolFingerprints(fps), score, isForced(stageCandidates[i].SessionID))
			if score > bestScore {
				secondBestScore = bestScore
				bestScore = score
				candidate := stageCandidates[i]
				bestCandidate = &candidate
			} else if score > secondBestScore {
				secondBestScore = score
			}
		}
		if bestScore >= contentMatchMinScore && (bestScore-secondBestScore) >= contentMatchMinMargin {
			break
		}
	}

	if bestCandidate == nil {
		log.Printf("[ContentMatch] reject: no candidate scored > 0 among %d candidates", len(candidates))
		return nil
	}

	margin := bestScore - secondBestScore
	if bestScore < contentMatchMinScore {
		log.Printf("[ContentMatch] reject: low confidence bestScore=%d minScore=%d", bestScore, contentMatchMinScore)
		return nil
	}
	if margin < contentMatchMinMargin {
		log.Printf("[ContentMatch] reject: ambiguous bestScore=%d secondBest=%d minMargin=%d", bestScore, secondBestScore, contentMatchMinMargin)
		return nil
	}

	log.Printf("[ContentMatch] pane match success session=%s bestScore=%d secondBest=%d margin=%d", bestCandidate.SessionID, bestScore, secondBestScore, margin)
	return bestCandidate
}

func countToolFingerprints(fps []string) int {
	count := 0
	for _, fp := range fps {
		if strings.HasPrefix(fp, "tool ") {
			count++
		}
	}
	return count
}
