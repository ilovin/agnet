package scanner

import (
	"encoding/json"
	"log"
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
	return extractFingerprintsWithFS(RealFileSystem{}, jsonlPath, maxFPs)
}

func extractFingerprintsWithFS(fs FileSystem, jsonlPath string, maxFPs int) []string {
	f, err := fs.Open(jsonlPath)
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
// or nil if no match (all scores are 0).
// maxCandidates limits how many candidates are actually fingerprinted.
func contentMatchSession(tmuxTarget string, candidates []SessionCandidate, maxCandidates int) *SessionCandidate {
	return contentMatchSessionWithFS(RealFileSystem{}, tmuxTarget, candidates, maxCandidates)
}

func contentMatchSessionWithFS(fs FileSystem, tmuxTarget string, candidates []SessionCandidate, maxCandidates int) *SessionCandidate {
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
		return nil
	}

	top := candidates
	if len(top) > maxCandidates {
		top = top[:maxCandidates]
	}

	bestScore := 0
	var bestCandidate *SessionCandidate

	for i := range top {
		fps := extractFingerprintsWithFS(fs, top[i].JSONLPath, 20)
		score := matchScore(paneText, fps)
		if score > bestScore {
			bestScore = score
			bestCandidate = &top[i]
		}
	}

	if bestCandidate != nil {
		log.Printf("[ContentMatch] pane %s → session %s (score %d)", tmuxTarget, bestCandidate.SessionID, bestScore)
	}
	return bestCandidate
}
