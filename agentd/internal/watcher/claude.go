package watcher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AgentStatus string

const (
	StatusWorking AgentStatus = "working"
	StatusStandby AgentStatus = "standby"
)

// ConversationEvent represents a parsed line from the Claude JSONL session file.
type ConversationEvent struct {
	Role         string       // "user" or "assistant"
	Text         string       // combined text content
	ToolSummary  string       // human-readable tool call summary (e.g. "Bash: go test ./...")
	StatusChange *AgentStatus // non-nil when this line changes agent status
	MsgID        string       // unique message ID for update tracking (opencode DB message.id)
}

// SessionWatcher is the interface implemented by all session watchers
// (ClaudeWatcher, OpenCodeDBWatcher, etc.)
type SessionWatcher interface {
	Start() error
	Stop()
	SetSkipExisting(bool)
}

// ClaudeWatcher tails a Claude Code JSONL session file and emits ConversationEvents.
// It also auto-detects when a newer session file appears (e.g. after /clear) and switches to it.
type ClaudeWatcher struct {
	path       string // current JSONL file path
	workDir    string // project working directory (for finding newer sessions)
	pid        int    // Claude process PID (for session lookup)
	tmuxTarget string // tmux pane target (for content matching)
	callback   func(ConversationEvent)
	stop       chan struct{}
	once       sync.Once
	offset     int64

	mu            sync.Mutex
	lastRefreshAt time.Time              // rate-limit refreshSessionFile (lsof is expensive)
	lastSwitchAt  time.Time              // cooldown after switchToFile to prevent oscillation
	onSwitch      func(newPath string)   // called when session file changes

	skipExisting bool // when true, Start() skips existing file content (for restarted watchers)
	hasPolled    bool // true after the first poll; gates refreshSessionFile on empty polls

	findSessionIDsFromTasksFunc func(tasksDir string) []string // test hook
	startTime                   time.Time                     // watcher start time; used to filter out old sessions
}

func NewClaudeWatcher(path string, callback func(ConversationEvent)) *ClaudeWatcher {
	return &ClaudeWatcher{
		path:      path,
		callback:  callback,
		stop:      make(chan struct{}),
		startTime: time.Now(),
	}
}

// SetWorkDir sets the project directory used for auto-detecting newer session files.
func (w *ClaudeWatcher) SetWorkDir(dir string) {
	w.workDir = dir
}

// SetPID sets the Claude process PID for session tracking.
func (w *ClaudeWatcher) SetPID(pid int) {
	w.pid = pid
}

// SetTmuxTarget sets the tmux pane target for content-based session matching.
func (w *ClaudeWatcher) SetTmuxTarget(target string) {
	w.tmuxTarget = target
}

// SetSkipExisting tells the watcher to skip existing file content on Start().
// Use this when restarting a watcher for an agent that already has persisted
// events, so historical lines are not re-processed with fresh timestamps.
func (w *ClaudeWatcher) SetSkipExisting(skip bool) {
	w.skipExisting = skip
}

// ResetOffset resets the file read offset to the current end-of-file.
// Call this after conversation.clear so the watcher does not re-read
// historical lines that were already cleared from the backend buffer.
func (w *ClaudeWatcher) ResetOffset() {
	if fi, err := os.Stat(w.path); err == nil {
		w.offset = fi.Size()
	} else {
		w.offset = 0
	}
}

// OnSessionSwitch registers a callback invoked when the watcher switches to a
// different session file (e.g. after /clear).
func (w *ClaudeWatcher) OnSessionSwitch(fn func(newPath string)) {
	w.onSwitch = fn
}

func (w *ClaudeWatcher) Start() error {
	if w.skipExisting {
		if fi, err := os.Stat(w.path); err == nil {
			w.offset = fi.Size()
		}
	}
	if err := w.poll(); err != nil && !os.IsNotExist(err) {
		return err
	}
	w.hasPolled = true
	go w.loop()
	return nil
}

func (w *ClaudeWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

func (w *ClaudeWatcher) loop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

// refreshSessionFile detects session switches (e.g. after /clear or resume)
// and switches the watcher to the new file. Called from poll() when no new
// events are read — which signals the process may have moved to a new session.
// Rate-limited to at most once every 15 seconds.
//
// Uses the same discovery pipeline as scanner.findClaudeSessionInfo:
// task fd → time filter → content match → fallback to most active.
func (w *ClaudeWatcher) refreshSessionFile() {
	w.mu.Lock()
	if time.Since(w.lastRefreshAt) < 15*time.Second {
		w.mu.Unlock()
		return
	}
	w.lastRefreshAt = time.Now()
	w.mu.Unlock()

	if w.pid <= 0 || w.workDir == "" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	tasksDir := filepath.Join(home, ".claude", "tasks")
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(w.workDir))

	// Step 1: Task fd discovery
	var taskSessions []string
	if w.findSessionIDsFromTasksFunc != nil {
		taskSessions = w.findSessionIDsFromTasksFunc(tasksDir)
	} else {
		taskSessions = w.findSessionIDsFromTasks(tasksDir)
	}

	if len(taskSessions) == 1 {
		candidate := filepath.Join(projectDir, taskSessions[0]+".jsonl")
		if candidate != w.path {
			if _, err := os.Stat(candidate); err == nil {
				w.switchToFile(candidate)
			}
		}
		return
	}

	// When PID fd shows multiple sessions (e.g. old fd not closed after /clear),
	// restrict candidates to ONLY those sessions. Never scan the whole project dir
	// when fd info is available — that would include other processes' sessions.
	if len(taskSessions) > 1 {
		var fdCandidates []sessionCandidate
		for _, sid := range taskSessions {
			jsonlPath := filepath.Join(projectDir, sid+".jsonl")
			if _, err := os.Stat(jsonlPath); err == nil {
				fdCandidates = append(fdCandidates, sessionCandidate{
					sessionID:    sid,
					jsonlPath:    jsonlPath,
					lastActivity: getLastActivityTimeFromJSONL(jsonlPath),
				})
			}
		}
		if len(fdCandidates) > 0 {
			sort.Slice(fdCandidates, func(i, j int) bool {
				return fdCandidates[i].lastActivity.After(fdCandidates[j].lastActivity)
			})
			if fdCandidates[0].jsonlPath != w.path {
				w.switchToFile(fdCandidates[0].jsonlPath)
			}
		}
		return
	}

	// Step 2: Build candidate list.
	// Only scan the project dir when there is no fd info at all.
	// Before scanning, verify the current file is actually stale — if it's
	// still being modified, don't risk switching to another process' session.
	if w.path != "" {
		if fi, err := os.Stat(w.path); err == nil {
			if time.Since(fi.ModTime()) < 30*time.Second {
				// Current file is still active; don't scan project dir
				return
			}
		}
	}

	candidates := listSessionCandidatesInDir(projectDir)

	// Exclude sessions that existed before this watcher started.
	// When PID fd info is unavailable, this prevents old sessions and
	// other processes' early sessions from being considered.
	var filtered []sessionCandidate
	for _, c := range candidates {
		if !c.lastActivity.Before(w.startTime.Add(-5 * time.Minute)) {
			filtered = append(filtered, c)
		}
	}
	candidates = filtered

	if len(candidates) == 0 {
		return
	}

	// Helper: check if current binding is still valid.
	// 1. If current file is in candidates → still valid (still a candidate).
	// 2. If current file is NOT in candidates AND len(candidates) == 0 → check
	//    os.Stat; if exists AND modified within last 2 minutes → valid (protect
	//    externally-provided files that are still active).
	// 3. Otherwise → not valid (allow switching to a more active candidate).
	currentBound := func() bool {
		for _, c := range candidates {
			if c.jsonlPath == w.path {
				return true
			}
		}
		if w.path != "" && len(candidates) == 0 {
			if fi, err := os.Stat(w.path); err == nil {
				if time.Since(fi.ModTime()) < 2*time.Minute {
					return true
				}
			}
		}
		return false
	}

	if len(candidates) == 1 {
		if candidates[0].jsonlPath != w.path && !currentBound() {
			w.switchToFile(candidates[0].jsonlPath)
		}
		return
	}

	// Step 3: Time-based filtering (optional)
	paneActivity := getPaneActivityFunc(w.tmuxTarget)
	candidates = filterCandidatesByPaneActivity(candidates, paneActivity, 5*time.Minute)

	if len(candidates) == 1 {
		if candidates[0].jsonlPath != w.path && !currentBound() {
			w.switchToFile(candidates[0].jsonlPath)
		}
		return
	}

	// Step 4: Content matching
	if w.tmuxTarget != "" {
		if matched := contentMatchFromCandidates(w.tmuxTarget, candidates, 5); matched != "" {
			if matched != w.path && !currentBound() {
				w.switchToFile(matched)
			}
			return
		}
	}

	// If current binding is still a valid candidate, don't switch on ambiguous match.
	// This prevents empty sessions (e.g. after /clear) from being re-bound to the
	// wrong session when fingerprints can't differentiate them.
	if currentBound() {
		return
	}

	// Fallback: most active candidate
	if len(candidates) > 0 && candidates[0].jsonlPath != w.path {
		w.switchToFile(candidates[0].jsonlPath)
	}
}

// findSessionIDsFromTasks returns session IDs found via task fd discovery for this watcher's PID.
func (w *ClaudeWatcher) findSessionIDsFromTasks(tasksDir string) []string {
	sessionIDs := make(map[string]struct{})

	procFdDir := fmt.Sprintf("/proc/%d/fd", w.pid)
	if fdEntries, err := os.ReadDir(procFdDir); err == nil {
		for _, fd := range fdEntries {
			link, err := os.Readlink(filepath.Join(procFdDir, fd.Name()))
			if err != nil {
				continue
			}
			if sessionID := taskSessionID(tasksDir, link); sessionID != "" {
				sessionIDs[sessionID] = struct{}{}
			}
		}
	} else {
		cmd := exec.Command("lsof", "-p", strconv.Itoa(w.pid), "-Fn")
		output, err := cmd.Output()
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				if len(line) < 2 || line[0] != 'n' {
					continue
				}
				if sessionID := taskSessionID(tasksDir, line[1:]); sessionID != "" {
					sessionIDs[sessionID] = struct{}{}
				}
			}
		}
	}

	var result []string
	for sid := range sessionIDs {
		result = append(result, sid)
	}
	return result
}

// sessionCandidate is a local type for watcher-internal session matching.
type sessionCandidate struct {
	sessionID    string
	jsonlPath    string
	lastActivity time.Time
}

// getLastActivityTimeFromJSONL reads the last few lines of a JSONL file to extract
// the most recent timestamp. Falls back to file mtime.
func getLastActivityTimeFromJSONL(jsonlPath string) time.Time {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return time.Time{}
	}
	tailSize := int64(8192)
	if info.Size() < tailSize {
		tailSize = info.Size()
	}
	buf := make([]byte, tailSize)
	if _, err := f.ReadAt(buf, info.Size()-tailSize); err != nil {
		return info.ModTime()
	}

	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var obj struct {
			Timestamp string `json:"timestamp"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if obj.Timestamp == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, obj.Timestamp); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, obj.Timestamp); err == nil {
			return t
		}
	}
	return info.ModTime()
}

// getPaneActivity returns the last activity time of a tmux pane, or nil if unavailable.
var getPaneActivityFunc = func(tmuxTarget string) *time.Time {
	if tmuxTarget == "" {
		return nil
	}
	cmd := exec.Command("tmux", "display", "-t", tmuxTarget, "-p", "#{pane_activity}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "0" {
		return nil
	}
	epoch, err := strconv.ParseInt(s, 10, 64)
	if err != nil || epoch <= 0 {
		return nil
	}
	t := time.Unix(epoch, 0)
	return &t
}

// listSessionCandidatesInDir lists all .jsonl files in a directory with their activity times.
// Files inside subagents/ directories are excluded (team-mode sub-agent sessions).
func listSessionCandidatesInDir(projectDir string) []sessionCandidate {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}
	var candidates []sessionCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		jsonlPath := filepath.Join(projectDir, entry.Name())
		if strings.Contains(jsonlPath, "subagents/") {
			continue
		}
		candidates = append(candidates, sessionCandidate{
			sessionID:    strings.TrimSuffix(entry.Name(), ".jsonl"),
			jsonlPath:    jsonlPath,
			lastActivity: getLastActivityTimeFromJSONL(jsonlPath),
		})
	}
	return candidates
}

// filterCandidatesByPaneActivity filters and sorts candidates by proximity to pane activity time.
// If paneActivity is nil, sorts by lastActivity descending.
func filterCandidatesByPaneActivity(candidates []sessionCandidate, paneActivity *time.Time, tolerance time.Duration) []sessionCandidate {
	if paneActivity == nil {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].lastActivity.After(candidates[j].lastActivity)
		})
		return candidates
	}

	pa := *paneActivity
	var filtered []sessionCandidate
	for _, c := range candidates {
		diff := c.lastActivity.Sub(pa)
		if diff < 0 {
			diff = -diff
		}
		if diff <= tolerance {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].lastActivity.After(candidates[j].lastActivity)
		})
		return candidates
	}

	sort.Slice(filtered, func(i, j int) bool {
		di := filtered[i].lastActivity.Sub(pa)
		if di < 0 {
			di = -di
		}
		dj := filtered[j].lastActivity.Sub(pa)
		if dj < 0 {
			dj = -dj
		}
		return di < dj
	})
	return filtered
}

// contentMatchFromCandidates captures tmux pane content and matches against candidates.
// Returns the jsonlPath of the best match, or "" if no match.
func contentMatchFromCandidates(tmuxTarget string, candidates []sessionCandidate, maxCandidates int) string {
	paneRaw, err := capturePaneContentFunc(tmuxTarget)
	if err != nil {
		return ""
	}
	paneText := cleanPaneText(paneRaw)
	if len(paneText) < 20 {
		return ""
	}

	top := candidates
	if len(top) > maxCandidates {
		top = top[:maxCandidates]
	}

	bestScore := 0
	secondBest := 0
	bestPath := ""
	for _, c := range top {
		fps := extractFingerprintsFunc(c.jsonlPath, 20)
		score := 0
		for _, fp := range fps {
			if strings.Contains(paneText, fp) {
				score++
			}
		}
		if score > bestScore {
			secondBest = bestScore
			bestScore = score
			bestPath = c.jsonlPath
		} else if score > secondBest {
			secondBest = score
		}
	}

	if bestScore >= minContentMatchScore && (bestScore-secondBest) >= minContentMatchMargin {
		log.Printf("[ContentMatch] pane %s matched %s (score %d, runner-up %d)", tmuxTarget, filepath.Base(bestPath), bestScore, secondBest)
		return bestPath
	}
	if bestScore > 0 {
		log.Printf("[ContentMatch] pane %s ambiguous (best %d, runner-up %d), skip switch", tmuxTarget, bestScore, secondBest)
	}
	return ""
}

// capturePaneContent captures the visible content of a tmux pane.
func capturePaneContent(target string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-200")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// cleanPaneText removes TUI decorations and normalizes whitespace.
func cleanPaneText(raw string) string {
	clean := tuiDecoRe.ReplaceAllString(raw, " ")
	clean = wsRe.ReplaceAllString(clean, " ")
	return strings.TrimSpace(clean)
}

var tuiDecoRe = regexp.MustCompile(`[─━│┃┌┐└┘├┤┬┴┼╔╗╚╝║═⏺⏵✻※❯⎿]`)
var wsRe = regexp.MustCompile(`\s+`)
var mdRe = regexp.MustCompile("[*`#\\[\\]]")

const (
	minContentMatchScore   = 3
	minContentMatchMargin  = 2
)

var capturePaneContentFunc = capturePaneContent
var extractFingerprintsFunc = extractFingerprintsFromJSONL

// extractFingerprintsFromJSONL extracts text snippets from a JSONL file for matching.
// Skips pure tool_use messages. Stops when maxFPs fingerprints are collected.
func extractFingerprintsFromJSONL(jsonlPath string, maxFPs int) []string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil
	}
	defer f.Close()

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

		if str, ok := content.(string); ok {
			t := strings.TrimSpace(str)
			if len(t) >= 3 && len(t) <= 80 {
				fps = append(fps, t)
			}
			continue
		}

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
			text = mdRe.ReplaceAllString(text, "")
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
		if !hasText {
			continue
		}
	}

	return fps
}

func (w *ClaudeWatcher) switchToFile(newPath string) {
	w.mu.Lock()
	if time.Since(w.lastSwitchAt) < 10*time.Second {
		w.mu.Unlock()
		return
	}
	w.lastSwitchAt = time.Now()
	w.mu.Unlock()
	oldPath := w.path
	w.path = newPath
	w.offset = 0
	log.Printf("[Watcher] Switching session file: %s -> %s", oldPath, newPath)
	if w.onSwitch != nil {
		w.onSwitch(newPath)
	}
}

func taskSessionID(tasksDir, path string) string {
	normalizedTasksDir := normalizeTaskPath(tasksDir)
	normalizedPath := normalizeTaskPath(path)
	if normalizedTasksDir == "" || normalizedPath == "" {
		return ""
	}
	rel, err := filepath.Rel(normalizedTasksDir, normalizedPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

func normalizeTaskPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, "/private/") {
		return strings.TrimPrefix(path, "/private")
	}
	return path
}

func projectDirName(workDir string) string {
	// Must match Claude's project directory naming: replace / . _ with -
	s := strings.ReplaceAll(strings.TrimRight(workDir, "/"), "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func (w *ClaudeWatcher) poll() error {
	f, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Detect file truncation (e.g. from context compaction):
	// if the file is now smaller than our saved offset, reset to 0.
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() < w.offset {
		w.offset = 0
	}

	if _, err := f.Seek(w.offset, io.SeekStart); err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line size
	var count int
	for scanner.Scan() {
		line := scanner.Bytes()
		if ev, ok := parseLine(line); ok {
			w.callback(ev)
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Get the actual file position after scanning to avoid newline-encoding assumptions
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	w.offset = pos

	// No new events — the process may have switched sessions (e.g. /clear, resume).
	// Trigger immediate refresh instead of waiting for a timer.
	// Skip refresh on the very first poll (hasPolled==false) so that a watcher
	// restarted with skipExisting does not immediately hop to another session.
	if count == 0 && w.hasPolled {
		w.refreshSessionFile()
	}
	return nil
}

// claudeLine is the minimal structure we need from Claude's JSONL output.
type claudeLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	Content string `json:"content,omitempty"`
	Message struct {
		Role       string      `json:"role"`
		Content    interface{} `json:"content"`     // Can be string or array
		StopReason string      `json:"stop_reason"` // "end_turn", "tool_use", or empty if still streaming
	} `json:"message"`
}

// buildToolSummary generates a human-readable summary for a tool_use block.
func buildToolSummary(name string, input map[string]interface{}) string {
	switch name {
	case "Glob":
		pattern, _ := input["pattern"].(string)
		path, _ := input["path"].(string)
		if path != "" {
			return fmt.Sprintf("Glob %s in %s", pattern, path)
		}
		return fmt.Sprintf("Glob %s", pattern)
	case "Grep":
		pattern, _ := input["pattern"].(string)
		glob, _ := input["glob"].(string)
		if glob != "" {
			return fmt.Sprintf("Grep /%s/ %s", pattern, glob)
		}
		return fmt.Sprintf("Grep /%s/", pattern)
	case "Read":
		filePath, _ := input["file_path"].(string)
		base := filepath.Base(filePath)
		offset, hasOffset := input["offset"]
		limit, hasLimit := input["limit"]
		if hasOffset || hasLimit {
			offsetStr := fmt.Sprintf("%v", offset)
			limitStr := fmt.Sprintf("%v", limit)
			return fmt.Sprintf("Read %s:%s-%s", base, offsetStr, limitStr)
		}
		return fmt.Sprintf("Read %s", base)
	case "Bash":
		cmd, _ := input["command"].(string)
		cmd = strings.TrimSpace(cmd)
		if len(cmd) > 60 {
			cmd = cmd[:60]
		}
		return cmd
	case "Edit":
		filePath, _ := input["file_path"].(string)
		return fmt.Sprintf("Edit %s", filepath.Base(filePath))
	case "Write":
		filePath, _ := input["file_path"].(string)
		return fmt.Sprintf("Write %s", filepath.Base(filePath))
	default:
		return ""
	}
}

// LoadClaudeJSONLHistory reads all conversation events from a .jsonl file.
// Each line is a JSON object. Returns events for lines that represent
// user or assistant messages.
func LoadClaudeJSONLHistory(path string) ([]ConversationEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	var events []ConversationEvent
	for scanner.Scan() {
		line := scanner.Bytes()
		if ev, ok := parseLine(line); ok {
			events = append(events, ev)
		}
	}
	return events, scanner.Err()
}

func parseLocalCommandContent(content string) (ConversationEvent, bool) {
	stdoutStart := strings.Index(content, "<local-command-stdout>")
	if stdoutStart >= 0 {
		start := stdoutStart + len("<local-command-stdout>")
		endRel := strings.Index(content[start:], "</local-command-stdout>")
		if endRel >= 0 {
			text := strings.TrimSpace(content[start : start+endRel])
			if text == "" {
				text = "Local command completed"
			}
			s := StatusStandby
			return ConversationEvent{Role: "assistant", Text: text, StatusChange: &s}, true
		}
	}

	stderrStart := strings.Index(content, "<local-command-stderr>")
	if stderrStart >= 0 {
		start := stderrStart + len("<local-command-stderr>")
		endRel := strings.Index(content[start:], "</local-command-stderr>")
		if endRel >= 0 {
			text := strings.TrimSpace(content[start : start+endRel])
			if text == "" {
				text = "Local command failed"
			}
			s := StatusStandby
			return ConversationEvent{Role: "assistant", Text: text, StatusChange: &s}, true
		}
	}

	return ConversationEvent{}, false
}

func parseLine(data []byte) (ConversationEvent, bool) {
	var line claudeLine
	if err := json.Unmarshal(data, &line); err != nil {
		return ConversationEvent{}, false
	}
	if line.Type == "system" && line.Subtype == "local_command" {
		if ev, ok := parseLocalCommandContent(line.Content); ok {
			return ev, true
		}
		return ConversationEvent{}, false
	}
	// Only process user and assistant messages
	if line.Type != "user" && line.Type != "assistant" {
		return ConversationEvent{}, false
	}

	ev := ConversationEvent{Role: line.Message.Role}

	// Content can be either a string or an array of content blocks
	switch content := line.Message.Content.(type) {
	case string:
		// Simple text content
		ev.Text = content
	case []interface{}:
		// Array of content blocks (text, tool_use, etc.)
		hasToolUse := false
		isTextStop := false
		for _, item := range content {
			if block, ok := item.(map[string]interface{}); ok {
				blockType, _ := block["type"].(string)
				switch blockType {
				case "text":
					if text, ok := block["text"].(string); ok {
						ev.Text += text
						isTextStop = true
					}
				case "tool_use":
					hasToolUse = true
					if name, ok := block["name"].(string); ok {
						input, _ := block["input"].(map[string]interface{})
						if input == nil {
							input = map[string]interface{}{}
						}
						summary := buildToolSummary(name, input)
						if summary != "" {
							ev.Text += fmt.Sprintf("[%s: %s]", name, summary)
							if ev.ToolSummary == "" {
								ev.ToolSummary = summary
							}
						} else {
							cmd, _ := input["command"].(string)
							if cmd != "" {
								ev.Text += fmt.Sprintf("[%s: %s]", name, cmd)
							} else {
								ev.Text += fmt.Sprintf("[%s]", name)
							}
						}
					}
					case "tool_result":
						// Tool results are system-level messages (responses to tool_use).
						// They should not appear as conversation text in the dashboard.
						// Skip them entirely - they contribute no visible content.
					}
				}
			}
			// If the content array had only tool_result blocks (no text, no tool_use),
			// the event has no meaningful content - skip it to avoid empty user messages.
			if ev.Text == "" && !hasToolUse {
				return ConversationEvent{}, false
			}
		// Status change detection
		if line.Type == "assistant" {
			if hasToolUse {
				s := StatusWorking
				ev.StatusChange = &s
			} else if isTextStop {
				if line.Message.StopReason == "" {
					s := StatusWorking
					ev.StatusChange = &s
				} else if line.Message.StopReason == "end_turn" {
					s := StatusStandby
					ev.StatusChange = &s
				}
			}
		}
	default:
		// Unknown content type, skip
		return ConversationEvent{}, false
	}

	// A user message that interrupts a running request should reset status to standby.
	// Claude writes "[Request interrupted by user]" as a user-type message, which the
	// assistant-only status detection above would miss, leaving status stuck on working.
	if line.Type == "user" {
		s := StatusStandby
		ev.StatusChange = &s
	}

	return ev, true
}
