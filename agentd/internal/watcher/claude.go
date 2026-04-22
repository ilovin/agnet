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
}

// ClaudeWatcher tails a Claude Code JSONL session file and emits ConversationEvents.
// It also auto-detects when a newer session file appears (e.g. after /clear) and switches to it.
type ClaudeWatcher struct {
	path     string // current JSONL file path
	workDir  string // project working directory (for finding newer sessions)
	pid      int    // Claude process PID (for session lookup)
	callback func(ConversationEvent)
	stop     chan struct{}
	once     sync.Once
	offset   int64

	mu             sync.Mutex
	lastRefreshAt  time.Time // rate-limit refreshSessionFile (lsof is expensive)
	lastSwitchAt   time.Time // cooldown after switchToFile to prevent oscillation
	onSwitch       func(newPath string) // called when session file changes
}

func NewClaudeWatcher(path string, callback func(ConversationEvent)) *ClaudeWatcher {
	return &ClaudeWatcher{path: path, callback: callback, stop: make(chan struct{})}
}

// SetWorkDir sets the project directory used for auto-detecting newer session files.
func (w *ClaudeWatcher) SetWorkDir(dir string) {
	w.workDir = dir
}

// SetPID sets the Claude process PID for session tracking.
func (w *ClaudeWatcher) SetPID(pid int) {
	w.pid = pid
}

// OnSessionSwitch registers a callback invoked when the watcher switches to a
// different session file (e.g. after /clear).
func (w *ClaudeWatcher) OnSessionSwitch(fn func(newPath string)) {
	w.onSwitch = fn
}

func (w *ClaudeWatcher) Start() error {
	// Parse existing content first
	if err := w.poll(); err != nil && !os.IsNotExist(err) {
		return err
	}
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
// Rate-limited to at most once every 15 seconds because the lsof fallback on
// macOS blocks an OS thread per call.
func (w *ClaudeWatcher) refreshSessionFile() {
	w.mu.Lock()
	if time.Since(w.lastRefreshAt) < 15*time.Second {
		w.mu.Unlock()
		return
	}
	w.lastRefreshAt = time.Now()
	w.mu.Unlock()

	if w.pid > 0 {
		if taskSession := w.findSessionFromTasks(); taskSession != "" {
			if taskSession != w.path {
				w.switchToFile(taskSession)
			}
			return
		}
	}
}

func (w *ClaudeWatcher) pidMappedSessionFile() string {
	if w.pid <= 0 || w.workDir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	pidFile := filepath.Join(home, ".claude", "sessions", strconv.Itoa(w.pid)+".json")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return ""
	}
	var pidInfo struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &pidInfo); err != nil || pidInfo.SessionID == "" {
		return ""
	}
	candidate := filepath.Join(home, ".claude", "projects", projectDirName(w.workDir), pidInfo.SessionID+".jsonl")
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}

// findSessionFromTasks finds the session whose task directory is open by this PID.
// After /clear, Claude creates a new session and task directory but may not update
// the PID mapping file. We detect the active session by checking which task dir
// the PID has open via /proc (Linux) or native syscalls (macOS).
func (w *ClaudeWatcher) findSessionFromTasks() string {
	if w.pid <= 0 || w.workDir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	tasksDir := filepath.Join(home, ".claude", "tasks")
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(w.workDir))
	sessionIDs := make(map[string]struct{})

	// Linux: /proc/<pid>/fd contains symlinks to open files/dirs
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
		return w.bestTaskSession(projectDir, tasksDir, sessionIDs)
	}

	// macOS: use lsof to find which task dirs the PID has open.
	// This is the only exec.Command on the hot path — rate-limited to 15s
	// by refreshSessionFile. On Linux the /proc path above handles it without exec.
	cmd := exec.Command("lsof", "-p", strconv.Itoa(w.pid), "-Fn")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 || line[0] != 'n' {
			continue
		}
		path := line[1:]
		if sessionID := taskSessionID(tasksDir, path); sessionID != "" {
			sessionIDs[sessionID] = struct{}{}
		}
	}
	return w.bestTaskSession(projectDir, tasksDir, sessionIDs)
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

func (w *ClaudeWatcher) bestTaskSession(projectDir, tasksDir string, sessionIDs map[string]struct{}) string {
	var best string
	var bestTaskTime time.Time
	var bestSessionTime time.Time
	for sessionID := range sessionIDs {
		candidate := filepath.Join(projectDir, sessionID+".jsonl")
		sessionInfo, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		taskTime := latestTaskActivity(filepath.Join(tasksDir, sessionID))
		if best == "" || taskTime.After(bestTaskTime) || (taskTime.Equal(bestTaskTime) && sessionInfo.ModTime().After(bestSessionTime)) {
			best = candidate
			bestTaskTime = taskTime
			bestSessionTime = sessionInfo.ModTime()
		}
	}
	return best
}

func latestTaskActivity(taskDir string) time.Time {
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

func (w *ClaudeWatcher) switchToFile(newPath string) {
	w.mu.Lock()
	if time.Since(w.lastSwitchAt) < 30*time.Second {
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
	if count == 0 {
		w.refreshSessionFile()
	}
	return nil
}

// claudeLine is the minimal structure we need from Claude's JSONL output.
type claudeLine struct {
	Type    string `json:"type"`
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

func parseLine(data []byte) (ConversationEvent, bool) {
	var line claudeLine
	if err := json.Unmarshal(data, &line); err != nil {
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
				}
			}
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
