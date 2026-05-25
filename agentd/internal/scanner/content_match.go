package scanner

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var tuiDecorationRe = regexp.MustCompile(`[─━│┃┌┐└┘├┤┬┴┼╔╗╚╝║═⏺⏵✻※❯⎿]`)
var whitespaceRe = regexp.MustCompile(`\s+`)
var markdownRe = regexp.MustCompile("[*`#\\[\\]]")
var nonWordRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

const (
	contentMatchMinScore  = 2
	contentMatchMinMargin = 2
)

// Cache TTLs are vars so tests can inject shorter values.
var (
	contentMatchCacheTTL = 30 * time.Second
	fingerprintsCacheTTL = 60 * time.Second
)

// contentMatchCacheMaxEntries caps both caches to keep memory bounded.
const contentMatchCacheMaxEntries = 200

// contentMatchCacheEntry stores a cached match result with expiry.
// Result may be nil (negative cache) — both presence-of-entry and result==nil are valid.
type contentMatchCacheEntry struct {
	result *SessionCandidate
	expiry time.Time
}

var (
	contentMatchCacheMu sync.Mutex
	contentMatchCache   = map[string]contentMatchCacheEntry{}
)

// fingerprintsCacheEntry stores extracted fingerprints keyed by path+mtime+size.
type fingerprintsCacheEntry struct {
	fps      []string
	maxFPs   int
	expiry   time.Time
}

var (
	fingerprintsCacheMu sync.Mutex
	fingerprintsCache   = map[string]fingerprintsCacheEntry{}
)

// extractFingerprintsCallCount counts every full extractFingerprints recompute.
// Used by tests via atomicLoadFingerprintCallCount to verify cache behavior.
var extractFingerprintsCallCount int64

func atomicLoadFingerprintCallCount() int64 {
	return atomic.LoadInt64(&extractFingerprintsCallCount)
}

func clearContentMatchCache() {
	contentMatchCacheMu.Lock()
	contentMatchCache = map[string]contentMatchCacheEntry{}
	contentMatchCacheMu.Unlock()
}

func clearFingerprintsCache() {
	fingerprintsCacheMu.Lock()
	fingerprintsCache = map[string]fingerprintsCacheEntry{}
	fingerprintsCacheMu.Unlock()
}

// candidateSetHash builds a stable hash from sorted session IDs so that any
// change in candidate set (add/remove) yields a different cache key.
func candidateSetHash(candidates []SessionCandidate) string {
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.SessionID)
	}
	sort.Strings(ids)
	h := sha1.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{'|'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// forcedSetHash hashes forcedSessionIDs deterministically; nil/empty -> "".
func forcedSetHash(forcedSessionIDs map[string]bool) string {
	if len(forcedSessionIDs) == 0 {
		return ""
	}
	ids := make([]string, 0, len(forcedSessionIDs))
	for k, v := range forcedSessionIDs {
		if v {
			ids = append(ids, k)
		}
	}
	sort.Strings(ids)
	h := sha1.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{'|'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

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
//
// Results are cached by (path, mtime, size). Cache is invalidated when the file
// is rewritten (mtime/size change) or when the entry exceeds fingerprintsCacheTTL.
func extractFingerprints(jsonlPath string, maxFPs int) []string {
	info, err := os.Stat(jsonlPath)
	if err != nil {
		return nil
	}

	cacheKey := jsonlPath + "|" + info.ModTime().UTC().Format(time.RFC3339Nano) + "|" +
		strconvFormatInt(info.Size())

	now := time.Now()
	fingerprintsCacheMu.Lock()
	if entry, ok := fingerprintsCache[cacheKey]; ok && now.Before(entry.expiry) && entry.maxFPs >= maxFPs {
		// Slice down to requested size.
		fps := entry.fps
		if len(fps) > maxFPs {
			fps = fps[:maxFPs]
		}
		fingerprintsCacheMu.Unlock()
		return fps
	}
	fingerprintsCacheMu.Unlock()

	fps := extractFingerprintsUncached(jsonlPath, maxFPs)
	atomic.AddInt64(&extractFingerprintsCallCount, 1)

	fingerprintsCacheMu.Lock()
	if len(fingerprintsCache) >= contentMatchCacheMaxEntries {
		// Simple eviction: clear the whole cache when full.
		fingerprintsCache = map[string]fingerprintsCacheEntry{}
	}
	fingerprintsCache[cacheKey] = fingerprintsCacheEntry{
		fps:    fps,
		maxFPs: maxFPs,
		expiry: now.Add(fingerprintsCacheTTL),
	}
	fingerprintsCacheMu.Unlock()

	return fps
}

// strconvFormatInt avoids importing strconv just for size formatting.
func strconvFormatInt(n int64) string {
	// Tiny manual base-10 formatter; size is non-negative.
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// extractFingerprintsUncached is the actual JSONL parsing path.
func extractFingerprintsUncached(jsonlPath string, maxFPs int) []string {
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

	return contentMatchSessionWithCache(tmuxTarget, paneRaw, candidates, forcedSessionIDs)
}

// contentMatchSessionWithCache wraps the scoring path with a result cache keyed
// by tmuxTarget + candidate set hash + forced set hash. The cache TTL must be
// longer than the AutoAttachExisting scan period (15s) so consecutive scans
// find a hit; default 30s.
//
// Cached negative results (nil) are also returned to skip recomputation when
// no candidate matches the pane.
func contentMatchSessionWithCache(tmuxTarget, paneRaw string, candidates []SessionCandidate, forcedSessionIDs map[string]bool) *SessionCandidate {
	if len(candidates) == 0 {
		return nil
	}

	key := tmuxTarget + "|" + candidateSetHash(candidates) + "|" + forcedSetHash(forcedSessionIDs)

	now := time.Now()
	contentMatchCacheMu.Lock()
	if entry, ok := contentMatchCache[key]; ok && now.Before(entry.expiry) {
		contentMatchCacheMu.Unlock()
		log.Printf("[ContentMatch] cache hit key=%s", key)
		return entry.result
	}
	contentMatchCacheMu.Unlock()

	result := contentMatchSessionByPaneText(paneRaw, candidates, forcedSessionIDs)

	contentMatchCacheMu.Lock()
	if len(contentMatchCache) >= contentMatchCacheMaxEntries {
		contentMatchCache = map[string]contentMatchCacheEntry{}
	}
	contentMatchCache[key] = contentMatchCacheEntry{
		result: result,
		expiry: now.Add(contentMatchCacheTTL),
	}
	contentMatchCacheMu.Unlock()

	return result
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
