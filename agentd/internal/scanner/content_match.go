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

// Fuzzy-match thresholds. Scores are float in [0, len(fingerprints)*1.0].
//   - contentMatchMinScore: minimum aggregate score to accept any match
//   - contentMatchMinMarginRatio: best/runner-up relative margin to accept
//   - contentMatchStrongMarginRatio: above this margin we treat the match as
//     strong and use the long cache TTL; otherwise weak TTL.
const (
	contentMatchMinScore           = 2.0
	contentMatchMinMarginRatio     = 0.30
	contentMatchStrongMarginRatio  = 0.50
)

// Cache TTLs are vars so tests can inject shorter values.
//   - contentMatchCacheTTL is the long TTL used for STRONG matches (margin
//     >= contentMatchStrongMarginRatio).
//   - contentMatchWeakCacheTTL is the short TTL used for WEAK matches so
//     ambiguous results re-evaluate quickly instead of being locked in for 30s.
var (
	contentMatchCacheTTL     = 30 * time.Second
	contentMatchWeakCacheTTL = 5 * time.Second
	fingerprintsCacheTTL     = 60 * time.Second
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

// ClearContentMatchCacheFor invalidates all cached content-match results for a
// specific tmux target. Watchers call this after detecting a session switch
// (e.g. via task fd or the PID->session map) so the next scan recomputes the
// match instead of returning a stale, pre-switch winner.
//
// The cache key is built as `tmuxTarget + "|" + ...`, so we delete every entry
// whose key starts with the target prefix. Other targets are untouched.
func ClearContentMatchCacheFor(tmuxTarget string) {
	if tmuxTarget == "" {
		return
	}
	prefix := tmuxTarget + "|"
	contentMatchCacheMu.Lock()
	for k := range contentMatchCache {
		if strings.HasPrefix(k, prefix) {
			delete(contentMatchCache, k)
		}
	}
	contentMatchCacheMu.Unlock()
}

// PrimeContentMatchCacheForTest is exported only for cross-package tests in
// the watcher package. It runs the normal cache-populating scoring path so
// tests can verify that ClearContentMatchCacheFor evicts the resulting entry.
func PrimeContentMatchCacheForTest(tmuxTarget, paneRaw string, candidates []SessionCandidate) {
	_ = contentMatchSessionWithCache(tmuxTarget, paneRaw, candidates, nil)
}

// ContentMatchCacheHasTargetForTest reports whether any cache entry exists for
// a tmux target. Exported only for cross-package tests.
func ContentMatchCacheHasTargetForTest(tmuxTarget string) bool {
	prefix := tmuxTarget + "|"
	contentMatchCacheMu.Lock()
	defer contentMatchCacheMu.Unlock()
	for k := range contentMatchCache {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
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

// FuzzyMatchScore is the exported view of fuzzyMatchScore. The watcher package
// uses it for its own contentMatch step (in claude.go) so both code paths
// share the same scoring algorithm and avoid divergent oscillation behavior.
func FuzzyMatchScore(haystack, needle string) float64 {
	return fuzzyMatchScore(haystack, needle)
}

// fuzzyMatchScore computes how well `needle` appears in `haystack` using a
// simplified fzf-style scoring algorithm. Returns a normalized score in [0, 1]:
//
//   - 1.0  perfect substring match (all needle runes appear consecutively
//     somewhere inside haystack)
//   - >0   partial fuzzy match: needle runes appear in order but with gaps;
//     consecutive runs are rewarded, gaps penalized
//   - 0    needle runes don't all appear in order
//
// Why fuzzy (instead of strings.Contains): tmux pane buffers contain CJK +
// terminal redraw artifacts (re-rendered lines, escape sequences, partial
// tokens). A pure substring match flips between hit and miss as the pane
// redraws, causing oscillation between candidate sessions. Fuzzy matching
// gives a stable, gradient signal that doesn't whip-saw between 0 and 1.
//
// Algorithm: walk haystack runes, advancing a pointer into needle when the
// runes match (case-insensitive). Track the longest run of consecutive matches.
// Score = (matched_runes / needle_runes) weighted by run-length bonus.
func fuzzyMatchScore(haystack, needle string) float64 {
	needleRunes := []rune(needle)
	if len(needleRunes) == 0 {
		return 0
	}

	// Fast path: exact (case-folded) substring match scores 1.0.
	if strings.Contains(haystack, needle) {
		return 1.0
	}

	hayRunes := []rune(haystack)
	if len(hayRunes) < len(needleRunes) {
		return 0
	}

	// Walk haystack matching needle runes in order. Track:
	//   matched: number of needle runes matched so far
	//   bestRun: longest contiguous run of matches in haystack
	//   curRun:  current contiguous run length
	matched := 0
	bestRun := 0
	curRun := 0
	prevMatched := false
	for _, hr := range hayRunes {
		if matched < len(needleRunes) && foldEqual(hr, needleRunes[matched]) {
			matched++
			if prevMatched {
				curRun++
			} else {
				curRun = 1
			}
			if curRun > bestRun {
				bestRun = curRun
			}
			prevMatched = true
		} else {
			prevMatched = false
			curRun = 0
		}
	}

	if matched == 0 {
		return 0
	}
	if matched < len(needleRunes) {
		// Not all needle runes matched in order — partial credit only.
		// Scale partial coverage so a half-match doesn't cross the threshold.
		return 0.4 * float64(matched) / float64(len(needleRunes))
	}

	// All matched. Reward longer consecutive runs (closer to substring).
	coverage := float64(bestRun) / float64(len(needleRunes))
	// 0.6 base + up to 0.4 from run coverage. A perfect run gets ~1.0 (and the
	// fast path above would have already returned 1.0); a fragmented match
	// floor is 0.6.
	return 0.6 + 0.4*coverage
}

// foldEqual is case-insensitive ASCII-fast equality. Falls back to lowercase
// folding for non-ASCII runes.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	if a < utf8.RuneSelf && b < utf8.RuneSelf {
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		return a == b
	}
	// Non-ASCII: rely on already-lowercased input (cleanTUIText lowercases).
	return false
}

// matchScoreFuzzy aggregates fuzzy scores for each fingerprint against the
// pane text. Returns the sum so the result is comparable across candidates
// with different fingerprint counts (a candidate with more fingerprints can
// accumulate more score, which is the desired behavior — more matched signals
// = higher confidence). A perfect substring hit per fingerprint contributes
// 1.0 to the sum.
func matchScoreFuzzy(paneText string, fingerprints []string) float64 {
	score := 0.0
	for _, fp := range fingerprints {
		score += fuzzyMatchScore(paneText, fp)
	}
	return score
}

// matchScore retains the old name as a thin float-returning wrapper so the
// scoring pipeline is consistent. Callers should use matchScoreFuzzy directly.
func matchScore(paneText string, fingerprints []string) float64 {
	return matchScoreFuzzy(paneText, fingerprints)
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
//
// Cache TTL is adaptive: a confidently-strong match (margin/best ratio above
// contentMatchStrongMarginRatio) is kept for the long TTL; weaker matches use
// a shorter TTL so the next scan re-evaluates promptly. Negative results
// (nil) also use the short TTL — there's no point pinning "no match" for 30s
// when a session might appear at any moment.
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

	result, strong := contentMatchSessionByPaneTextWithStrength(paneRaw, candidates, forcedSessionIDs)

	ttl := contentMatchWeakCacheTTL
	if strong {
		ttl = contentMatchCacheTTL
	}

	contentMatchCacheMu.Lock()
	if len(contentMatchCache) >= contentMatchCacheMaxEntries {
		contentMatchCache = map[string]contentMatchCacheEntry{}
	}
	contentMatchCache[key] = contentMatchCacheEntry{
		result: result,
		expiry: now.Add(ttl),
	}
	contentMatchCacheMu.Unlock()

	return result
}

// contentMatchSessionByPaneText is the original entrypoint kept for tests and
// the existing call sites that don't care about strength.
func contentMatchSessionByPaneText(paneRaw string, candidates []SessionCandidate, forcedSessionIDs map[string]bool) *SessionCandidate {
	result, _ := contentMatchSessionByPaneTextWithStrength(paneRaw, candidates, forcedSessionIDs)
	return result
}

// contentMatchSessionByPaneTextWithStrength runs the fuzzy scoring pipeline
// and reports both the chosen candidate and whether the match was strong
// (margin >= contentMatchStrongMarginRatio relative to bestScore).
func contentMatchSessionByPaneTextWithStrength(paneRaw string, candidates []SessionCandidate, forcedSessionIDs map[string]bool) (*SessionCandidate, bool) {
	if len(candidates) == 0 {
		return nil, false
	}

	paneText := cleanTUIText(paneRaw)
	if utf8.RuneCountInString(paneText) < 20 {
		log.Printf("[ContentMatch] reject: pane text too short (%d runes)", utf8.RuneCountInString(paneText))
		return nil, false
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

	bestScore := 0.0
	secondBestScore := 0.0
	var bestCandidate *SessionCandidate
	tiedWithBest := false // true when the current best was picked via activity tie-break against an equal-scoring rival
	seen := make(map[string]bool, len(candidates))

	// considerScore decides whether to replace the current best/second-best
	// with `c` at `score`. When score == bestScore (tie), prefer the candidate
	// with the more recent LastActivity to break the tie deterministically.
	// Without this tie-break, slice ordering decides the winner and the result
	// flips between scans causing oscillation.
	considerScore := func(score float64, c SessionCandidate) {
		if score > bestScore {
			secondBestScore = bestScore
			bestScore = score
			cc := c
			bestCandidate = &cc
			tiedWithBest = false
			return
		}
		if score == bestScore && bestCandidate != nil {
			// Tie. Pick the more recent activity; the loser becomes runner-up.
			tiedWithBest = true
			if c.LastActivity.After(bestCandidate.LastActivity) {
				secondBestScore = bestScore
				cc := c
				bestCandidate = &cc
			} else if score > secondBestScore {
				secondBestScore = score
			}
			return
		}
		if score > secondBestScore {
			secondBestScore = score
		}
	}

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
			score := matchScoreFuzzy(paneText, fps)
			log.Printf("[ContentMatch] candidate=%s fpCount=%d toolFpCount=%d score=%.2f forced=%t", stageCandidates[i].SessionID, len(fps), countToolFingerprints(fps), score, isForced(stageCandidates[i].SessionID))
			considerScore(score, stageCandidates[i])
		}
		if bestScore >= contentMatchMinScore && marginRatio(bestScore, secondBestScore) >= contentMatchMinMarginRatio {
			break
		}
	}

	if bestCandidate == nil {
		log.Printf("[ContentMatch] reject: no candidate scored > 0 among %d candidates", len(candidates))
		return nil, false
	}

	mr := marginRatio(bestScore, secondBestScore)
	if bestScore < contentMatchMinScore {
		log.Printf("[ContentMatch] reject: low confidence bestScore=%.2f minScore=%.2f", bestScore, contentMatchMinScore)
		return nil, false
	}
	// If scores are tied, accept the activity-based tie-break (otherwise the
	// system oscillates: rejecting at margin=0 means we never converge while
	// two candidates have identical content/scores). Mark as weak so the
	// cache TTL is short and we re-evaluate quickly when conditions change.
	if mr < contentMatchMinMarginRatio && !tiedWithBest {
		log.Printf("[ContentMatch] reject: ambiguous bestScore=%.2f secondBest=%.2f marginRatio=%.2f", bestScore, secondBestScore, mr)
		return nil, false
	}

	strong := mr >= contentMatchStrongMarginRatio && !tiedWithBest
	log.Printf("[ContentMatch] pane match success session=%s bestScore=%.2f secondBest=%.2f marginRatio=%.2f strong=%t tied=%t", bestCandidate.SessionID, bestScore, secondBestScore, mr, strong, tiedWithBest)
	return bestCandidate, strong
}

// marginRatio is the relative margin of bestScore over secondBest, where
// bestScore is the denominator. Returns 1.0 when secondBest == 0 (and
// bestScore > 0); 0.0 when bestScore == 0.
func marginRatio(bestScore, secondBest float64) float64 {
	if bestScore <= 0 {
		return 0
	}
	return (bestScore - secondBest) / bestScore
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
