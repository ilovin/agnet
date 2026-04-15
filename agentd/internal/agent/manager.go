package agent

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/phone-talk/agentd/internal/eventbuf"
	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// containsString checks if a string slice contains a specific string.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// newUUID generates a random UUID v4 string without external dependencies.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Manager creates, tracks, and controls Agent instances.
type Manager struct {
	mu             sync.RWMutex
	agents         map[string]*Agent
	store          *store.Store
	dataDir        string
	onOutput       func(agentID string, data map[string]any) // broadcast hook for messages
	onStatusChange func(agentID string, data map[string]any) // broadcast hook for status changes
	sessionParents map[string]string                         // childAgentID -> parentAgentID for session continuity
	scanExisting   func() ([]scanner.ProcessInfo, error)
}

type DerivedAgentState struct {
	RuntimeState       string
	SessionState       string
	SessionStateReason string
	SessionControl     string
}

func NewManager(s *store.Store, dataDir string) *Manager {
	m := &Manager{
		agents:         make(map[string]*Agent),
		store:          s,
		dataDir:        dataDir,
		sessionParents: make(map[string]string),
	}
	m.scanExisting = func() ([]scanner.ProcessInfo, error) {
		s := scanner.New()
		return s.Scan()
	}
	return m
}

// LoadFromStore loads persisted agents from the store into memory.
// Only agents whose process is still running are restored as idle;
// stopped agents (pid=0 or dead process) are cleaned up from the store.
func (m *Manager) LoadFromStore() error {
	records, err := m.store.ListAgents()
	if err != nil {
		return fmt.Errorf("list agents from store: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	loadedAttached := make(map[string]struct{})
	for _, r := range records {
		// Skip agents whose process is no longer running — they will be
		// re-discovered by ScanExisting/Attach if the process comes back.
		if r.PID <= 0 || !isProcessRunning(r.PID, r.Provider) {
			log.Printf("[LoadFromStore] Skipping stopped agent %s (%s, PID %d)", r.ID, r.Name, r.PID)
			_ = m.store.DeleteAgent(r.ID)
			continue
		}

		if strings.Contains(r.Name, "-attached-") {
			key := r.Provider + "|" + r.Name
			if _, ok := loadedAttached[key]; ok {
				continue
			}
			loadedAttached[key] = struct{}{}
		}
		var cmd string
		var args []string
		switch r.Provider {
		case "opencode":
			cmd = "opencode"
			if r.ResumeSessionID != "" {
				args = []string{"-s", r.ResumeSessionID}
			}
		default:
			cmd = "claude"
			args = []string{"--dangerously-skip-permissions"}
		}
		ag := newAgent(r.ID, r.Name, r.Provider, r.WorkDir, cmd, args)
		m.wireStatusCallback(ag)
		ag.setStatus(StatusIdle)
		log.Printf("[LoadFromStore] Agent %s (PID %d) is still running, setting status to idle", r.ID, r.PID)

		// Initialize buffer seq from persisted events so new appends continue after existing data
		if lastSeq, err := m.store.LastConversationSeq(r.ID); err == nil && lastSeq > 0 {
			ag.InitSeq(lastSeq)
		}
		m.agents[r.ID] = ag
	}
	return nil
}

// isProcessRunning checks if a process with the given PID is still running
// and matches the expected provider (claude or opencode).
func isProcessRunning(pid int, provider string) bool {
	if pid <= 0 {
		return false
	}
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		// Process doesn't exist or we can't read it
		return false
	}
	cmdline := strings.ToLower(string(data))
	return strings.Contains(cmdline, strings.ToLower(provider))
}

// SetOnOutput registers a callback invoked whenever an agent produces PTY output.
func (m *Manager) SetOnOutput(fn func(agentID string, data map[string]any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onOutput = fn
}

// SetOnStatusChange registers a callback invoked whenever an agent's status changes.
func (m *Manager) SetOnStatusChange(fn func(agentID string, data map[string]any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStatusChange = fn
}

func (m *Manager) appendAndPersistEvent(agentID string, ag *Agent, data map[string]any) uint64 {
	seq := ag.AppendEvent(data)
	if err := m.store.SaveConversationEvent(agentID, seq, data); err != nil {
		log.Printf("save conversation event agent=%s seq=%d: %v", agentID, seq, err)
	}
	return seq
}

func maybeExtractSessionIDFromRaw(text string) string {
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

// cleanPermissionText removes ANSI sequences and normalizes permission prompt text.
func cleanPermissionText(text string) string {
	// Remove ANSI escape sequences
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

func detectPermissionPrompt(text string) bool {
	// First clean the text for more reliable detection
	cleaned := cleanPermissionText(text)
	lower := strings.ToLower(cleaned)

	// Match fragmented text patterns
	// "bypasspermissionson", "asspermissionson", etc.
	if strings.Contains(lower, "bypass") && strings.Contains(lower, "permission") {
		return true
	}
	if strings.Contains(lower, "permission") && strings.Contains(lower, "shift") {
		return true
	}
	if strings.Contains(lower, "shift+tab") && strings.Contains(lower, "cycle") {
		return true
	}

	// Legacy patterns for original/complete text
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

func (m *Manager) RecordConversationEvent(agentID string, data map[string]any) (uint64, error) {
	ag := m.Get(agentID)
	if ag == nil {
		return 0, fmt.Errorf("agent %q not found", agentID)
	}
	return m.appendAndPersistEvent(agentID, ag, data), nil
}

// readStreamJSONOutput reads and parses Claude's --output-format stream-json output
func (m *Manager) readStreamJSONOutput(agentID string, ag *Agent, provider string) {
	// Only process stream-json for Claude provider
	if provider != "claude" {
		return
	}

	p := ag.Process()
	if p == nil {
		return
	}

	// Create a pipe to capture stdout separately
	// Note: This requires changes to pty.Spawn to support stdout capture
	// For now, we rely on the session file watcher for structured content
	log.Printf("[StreamJSON] Stream JSON reader started for agent %s", agentID)
}

// readPTYForPermissionPrompts reads PTY output only for permission prompt detection
func (m *Manager) readPTYForPermissionPrompts(agentID string, ag *Agent, provider string, p *agentpty.Process) {
	defer func() {
		// Only update status if this is still the active process (not replaced by restart).
		if ag.Process() == p {
			ag.setProcess(nil)
			ag.setStatus(StatusStopped)
		}
	}()

	buf := make([]byte, 4096)
	var lastAutoResolveTime time.Time
	const autoResolveCooldown = 2 * time.Second // Prevent rapid re-detection

	for {
		n, err := p.Read(buf)
		if n > 0 {
			text := string(buf[:n])

			// Extract session ID from raw output (for resume functionality)
			if provider == "claude" {
				if sessionID := maybeExtractSessionIDFromRaw(strings.TrimSpace(text)); sessionID != "" {
					if err := m.store.UpdateResumeSessionID(agentID, sessionID); err != nil {
						log.Printf("update resume session from stream for %s: %v", agentID, err)
					}
				}
			}

			// Check for permission prompt (TUI menu)
			// Only check if we haven't recently auto-resolved (cooldown period)
			if time.Since(lastAutoResolveTime) > autoResolveCooldown && detectPermissionPrompt(text) {
				log.Printf("[Permission] Detected prompt for agent %s", agentID)
				ag.setPermissionPromptActive(true)
				// Auto-resolve permission prompt immediately
				if err := ag.WriteInput("\t\r\r"); err == nil {
					log.Printf("[Permission] Auto-resolved prompt for agent %s", agentID)
					ag.SetPermissionPromptActive(false)
					lastAutoResolveTime = time.Now()
				} else {
					log.Printf("[Permission] Auto-resolve failed for agent %s: %v", agentID, err)
				}
			}

			// Check for stream-json events in PTY output (Claude outputs JSON to PTY in stream-json mode)
			if provider == "claude" {
				if ev := m.tryParseStreamJSON(text); ev != nil {
					m.handleStreamJSONEvent(agentID, ag, ev)
					continue
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[PTY] Read error for agent %s: %v", agentID, err)
			}
			break
		}
	}
}

// tryParseStreamJSON attempts to parse a line as stream-json format
type streamJSONEvent struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Raw       map[string]any  `json:"-"` // Original raw data for accessing extra fields
}

func (m *Manager) tryParseStreamJSON(text string) *streamJSONEvent {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
		return nil
	}

	var ev streamJSONEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return nil
	}
	ev.Raw = rawMap

	// Validate it's a known stream-json event type
	switch ev.Type {
	case "init", "message", "user", "assistant", "tool_use", "tool_result", "result", "permission_prompt", "control_request", "stream_event", "system":
		return &ev
	default:
		return nil
	}
}

// buildToolResultSummary extracts a concise summary from a tool result output.
// toolName is optional (may be empty if not available in the event).
func buildToolResultSummary(toolName string, output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "(no output)"
	}
	// Strip surrounding JSON string quotes if present
	if len(text) >= 2 && text[0] == '"' {
		var s string
		if err := json.Unmarshal(output, &s); err == nil {
			text = strings.TrimSpace(s)
		}
	}

	lines := strings.Split(text, "\n")
	nonEmpty := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}

	switch toolName {
	case "Bash":
		// Show first 5 non-empty lines
		preview := nonEmpty
		if len(preview) > 5 {
			preview = preview[:5]
			return strings.Join(preview, "\n") + fmt.Sprintf("\n... (%d lines total)", len(nonEmpty))
		}
		return strings.Join(preview, "\n")
	case "Grep":
		return fmt.Sprintf("%d matches", len(nonEmpty))
	case "Read":
		return fmt.Sprintf("%d lines", len(nonEmpty))
	case "Write", "Edit":
		return "done"
	}

	// Generic: first 3 lines, max 300 chars
	preview := nonEmpty
	if len(preview) > 3 {
		preview = preview[:3]
	}
	result := strings.Join(preview, "\n")
	if len(result) > 300 {
		result = result[:300] + "..."
	}
	if len(nonEmpty) > 3 {
		result += fmt.Sprintf("\n... (%d lines total)", len(nonEmpty))
	}
	return result
}

func buildToolInputSummary(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal(input, &params); err != nil {
		return ""
	}

	switch toolName {
	case "Bash":
		if cmd, ok := params["command"].(string); ok {
			if len(cmd) > 100 {
				cmd = cmd[:100] + "..."
			}
			return cmd
		}
	case "Read":
		if path, ok := params["file_path"].(string); ok {
			return path
		}
	case "Write":
		if path, ok := params["file_path"].(string); ok {
			return path
		}
	case "Edit":
		if path, ok := params["file_path"].(string); ok {
			return path
		}
	case "Grep":
		if pattern, ok := params["pattern"].(string); ok {
			return "pattern: " + pattern
		}
	case "Glob":
		if pattern, ok := params["pattern"].(string); ok {
			return pattern
		}
	case "Agent":
		if desc, ok := params["description"].(string); ok && desc != "" {
			return desc
		}
		if prompt, ok := params["prompt"].(string); ok && prompt != "" {
			if len(prompt) > 80 {
				prompt = prompt[:80] + "..."
			}
			return prompt
		}
	case "SendMessage":
		to, _ := params["to"].(string)
		summary, _ := params["summary"].(string)
		if summary != "" && to != "" {
			return fmt.Sprintf("→ %s: %s", to, summary)
		}
		if to != "" {
			return "→ " + to
		}
	case "TaskCreate":
		if subject, ok := params["subject"].(string); ok && subject != "" {
			return subject
		}
	case "TaskUpdate":
		taskId, _ := params["taskId"].(string)
		status, _ := params["status"].(string)
		if taskId != "" && status != "" {
			return fmt.Sprintf("#%s → %s", taskId, status)
		}
		if status != "" {
			return status
		}
	case "TaskList":
		return "查看任务列表"
	case "TodoWrite":
		return "更新任务"
	case "WebSearch":
		if query, ok := params["query"].(string); ok && query != "" {
			return query
		}
	case "WebFetch":
		if url, ok := params["url"].(string); ok && url != "" {
			return url
		}
	case "NotebookEdit":
		if path, ok := params["notebook_path"].(string); ok && path != "" {
			return path
		}
	}

	// Generic fallback: show first string value
	for _, v := range params {
		if s, ok := v.(string); ok && len(s) > 0 {
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	}
	return ""
}

func (m *Manager) handleStreamJSONEvent(agentID string, ag *Agent, ev *streamJSONEvent) {
	var data map[string]any

	switch ev.Type {
	case "init", "system":
		// Initialize session info from init event or system event with subtype=init
		if ev.Raw != nil {
			if subtype, ok := ev.Raw["subtype"].(string); ok && (subtype == "init" || ev.Type == "init") {
				if sessionID, ok := ev.Raw["session_id"].(string); ok && sessionID != "" {
					log.Printf("[StreamJSON] Init event received for agent %s, SessionID=%s", agentID, sessionID)
					// Use m.UpdateResumeSessionID (not m.store.UpdateResumeSessionID) to also update parent
					if err := m.UpdateResumeSessionID(agentID, sessionID); err != nil {
						log.Printf("[StreamJSON] Failed to update session id for %s: %v", agentID, err)
					} else {
						log.Printf("[StreamJSON] Saved session ID %s for agent %s", sessionID, agentID)
					}
				}
			}
		}
		return

	case "user", "assistant":
		role := ev.Type
		if ev.Role != "" {
			role = ev.Role
		}

		// Parse content (can be string or array)
		var text string
		var contentArr []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}
		if err := json.Unmarshal(ev.Content, &text); err == nil {
			// Simple string content
		} else if err := json.Unmarshal(ev.Content, &contentArr); err == nil {
			// Array of content blocks
			for _, block := range contentArr {
				if block.Type == "text" {
					text += block.Text
				}
			}
		}

		if role == "assistant" {
			ag.setStatus(StatusWorking)
		}

		kind := role // "user" or "assistant"
		data = map[string]any{
			"role": role,
			"text": text,
			"raw":  false,
			"kind": kind,
		}

		// Update status based on content
		if role == "assistant" {
			ag.setStatus(StatusWorking)
		}

	case "tool_use":
		summary := buildToolInputSummary(ev.Name, ev.Input)
		var toolText string
		if summary != "" {
			toolText = fmt.Sprintf("[%s: %s]", ev.Name, summary)
		} else {
			toolText = fmt.Sprintf("[%s]", ev.Name)
		}
		data = map[string]any{
			"role":     "assistant",
			"text":     toolText,
			"raw":      false,
			"kind":     "tool_use",
			"toolName": ev.Name,
		}
		ag.setStatus(StatusWorking)

	case "tool_result":
		toolName, _ := ev.Raw["tool_name"].(string)
		resultText := buildToolResultSummary(toolName, ev.Output)
		data = map[string]any{
			"role":     "assistant",
			"text":     resultText,
			"raw":      false,
			"kind":     "tool_result",
			"result":   true,
			"toolName": toolName,
		}

	case "result":
		// Final result with complete response
		if result, ok := ev.Raw["result"].(string); ok && result != "" {
			data = map[string]any{
				"role": "assistant",
				"text": result,
				"raw":  false,
				"kind": "result",
			}
			seq := m.appendAndPersistEvent(agentID, ag, data)
			data["seq"] = seq

			m.mu.RLock()
			cb := m.onOutput
			m.mu.RUnlock()
			if cb != nil {
				cb(agentID, data)
			}
		}
		ag.setStatus(StatusIdle)
		data = nil
		return

	case "stream_event":
		// Handle stream_event from Claude's -p mode
		if ev.Raw == nil {
			return
		}
		eventData, ok := ev.Raw["event"].(map[string]any)
		if !ok {
			return
		}
		eventType, _ := eventData["type"].(string)

		switch eventType {
		case "message_start":
			ag.setStatus(StatusWorking)
		case "content_block_delta":
			if delta, ok := eventData["delta"].(map[string]any); ok {
				// Claude stream-json variants may emit either {"text": ...} or {"text_delta": ...}
				text, _ := delta["text"].(string)
				if text == "" {
					text, _ = delta["text_delta"].(string)
				}
				if text != "" {
					data = map[string]any{
						"role": "assistant",
						"text": text,
						"raw":  false,
						"kind": "text_delta",
					}
					seq := m.appendAndPersistEvent(agentID, ag, data)
					data["seq"] = seq

					m.mu.RLock()
					cb := m.onOutput
					m.mu.RUnlock()
					if cb != nil {
						cb(agentID, data)
					}
					data = nil
				}
			}
		case "message_stop":
			ag.setStatus(StatusIdle)
		}
		data = nil

	case "control_request":
		// Handle permission requests from Claude
		if ev.Raw == nil {
			return
		}
		if subtype, ok := ev.Raw["subtype"].(string); ok && subtype == "can_use_tool" {
			req := &PermissionRequest{
				RequestID:      getString(ev.Raw, "request_id"),
				ToolName:       getString(ev.Raw, "tool_name"),
				DisplayName:    getString(ev.Raw, "display_name"),
				Title:          getString(ev.Raw, "title"),
				Description:    getString(ev.Raw, "description"),
				ToolUseID:      getString(ev.Raw, "tool_use_id"),
				AgentID:        getString(ev.Raw, "agent_id"),
				BlockedPath:    getString(ev.Raw, "blocked_path"),
				DecisionReason: getString(ev.Raw, "decision_reason"),
			}

			// Parse input
			if input, ok := ev.Raw["input"].(map[string]any); ok {
				req.Input = input
			}

			// Parse AI validation
			if aiVal, ok := ev.Raw["ai_validation"].(map[string]any); ok {
				req.AIValidation = &AIValidationInfo{
					Verdict: getString(aiVal, "verdict"),
					Reason:  getString(aiVal, "reason"),
				}
			}

			// Parse permission suggestions
			if suggestions, ok := ev.Raw["permission_suggestions"].([]any); ok {
				for _, s := range suggestions {
					if sugMap, ok := s.(map[string]any); ok {
						sug := PermissionSuggestion{
							Type:        getString(sugMap, "type"),
							Mode:        getString(sugMap, "mode"),
							Destination: getString(sugMap, "destination"),
						}
						if dirs, ok := sugMap["directories"].([]any); ok {
							for _, d := range dirs {
								if ds, ok := d.(string); ok {
									sug.Directories = append(sug.Directories, ds)
								}
							}
						}
						req.PermissionSuggestions = append(req.PermissionSuggestions, sug)
					}
				}
			}

			ag.PermissionManager().AddRequest(req)

			data := map[string]any{
				"role":              "system",
				"text":              "需要权限确认: " + req.ToolName,
				"raw":               false,
				"kind":              "permission_request",
				"permissionRequest": req,
			}
			seq := m.appendAndPersistEvent(agentID, ag, data)
			data["seq"] = seq

			m.mu.RLock()
			cb := m.onOutput
			m.mu.RUnlock()
			if cb != nil {
				cb(agentID, data)
			}
		}
		return

	case "permission_prompt":
		data = map[string]any{
			"role":               "system",
			"text":               "Claude 需要权限确认",
			"raw":                false,
			"kind":               "permission_prompt",
			"awaitingPermission": true,
		}
		ag.setPermissionPromptActive(true)
	}

	if data != nil {
		seq := m.appendAndPersistEvent(agentID, ag, data)
		data["seq"] = seq

		m.mu.RLock()
		cb := m.onOutput
		m.mu.RUnlock()
		if cb != nil {
			cb(agentID, data)
		}
	}
}

// Create spawns a new agent process using the given command/args.
// wireStatusCallback sets up the status change callback for an agent.
func (m *Manager) wireStatusCallback(ag *Agent) {
	ag.SetOnStatusChange(func(agentID string, oldStatus, newStatus Status) {
		// Use a separate goroutine to avoid deadlock when called from within a lock
		go func() {
			m.mu.RLock()
			cb := m.onStatusChange
			m.mu.RUnlock()
			if cb != nil {
				derived := m.DeriveAgentState(agentID)
				cb(agentID, map[string]any{
					"jsonrpc": "2.0",
					"method":  "agent.status_changed",
					"params": map[string]any{
						"agentId":            agentID,
						"status":             string(newStatus),
						"runtimeState":       derived.RuntimeState,
						"sessionState":       derived.SessionState,
						"sessionStateReason": derived.SessionStateReason,
						"sessionControl":     derived.SessionControl,
					},
				})
			}
		}()
	})
}

func (m *Manager) DeriveAgentState(id string) DerivedAgentState {
	ag := m.Get(id)
	if ag == nil {
		return DerivedAgentState{}
	}
	resumeID, _ := m.GetResumeSessionID(id)
	return deriveAgentState(ag, resumeID)
}

func deriveAgentState(ag *Agent, resumeSessionID string) DerivedAgentState {
	status := ag.Status()
	watcher := ag.Watcher()
	process := ag.Process()
	attachMode := ag.AttachMode()
	attachReadOnly := ag.AttachReadOnly()

	state := DerivedAgentState{
		RuntimeState:   deriveRuntimeState(status, process != nil, watcher != nil),
		SessionControl: deriveSessionControl(process != nil, watcher != nil, attachMode, attachReadOnly, resumeSessionID),
	}
	state.SessionState, state.SessionStateReason = deriveSessionState(status, process != nil, watcher != nil, resumeSessionID)
	return state
}

func deriveRuntimeState(status Status, hasProcess bool, hasWatcher bool) string {
	switch status {
	case StatusStarting:
		return "starting"
	case StatusStopped:
		return "stopped"
	case StatusCrashed:
		return "crashed"
	case StatusWorking:
		return "live"
	case StatusIdle:
		if hasProcess || hasWatcher {
			return "live"
		}
		return "exited"
	default:
		if hasProcess || hasWatcher {
			return "live"
		}
		return "exited"
	}
}

func deriveSessionState(status Status, hasProcess bool, hasWatcher bool, resumeSessionID string) (string, string) {
	switch status {
	case StatusWorking:
		return "active", "agent is currently producing output"
	case StatusStarting:
		return "none", "agent runtime is starting"
	case StatusStopped:
		if resumeSessionID != "" {
			return "resumable", "agent was stopped but session can be resumed"
		}
		return "none", "agent was stopped and no resumable session is stored"
	case StatusCrashed:
		if resumeSessionID != "" {
			return "broken", "agent crashed; resumable session may need rebind"
		}
		return "broken", "agent crashed"
	case StatusIdle:
		if hasWatcher {
			return "standby", "watcher attached"
		}
		if hasProcess {
			return "standby", "runtime is idle"
		}
		if resumeSessionID != "" {
			return "resumable", "runtime exited; resume session is available"
		}
		return "none", "no resumable session is stored"
	default:
		if resumeSessionID != "" {
			return "resumable", "resume session is available"
		}
		return "none", "no resumable session is stored"
	}
}

func deriveSessionControl(hasProcess bool, hasWatcher bool, attachMode string, attachReadOnly bool, resumeSessionID string) string {
	if attachMode == scanner.AttachModeTmux {
		return "attachable"
	}
	if attachMode != "" {
		if attachReadOnly {
			return "read_only"
		}
		return "attachable"
	}
	if hasProcess || hasWatcher {
		return "managed"
	}
	if resumeSessionID != "" {
		return "rebindable"
	}
	return "unavailable"
}

// readPipeOutputAndWait reads stream-json output from pipe and waits for process exit.
// Used for Claude -p mode where the process exits after each response.
// Set initial to true when called from Create() to prevent immediate stopped status.
func (m *Manager) readPipeOutputAndWait(agentID string, ag *Agent, p *agentpty.Process, initial bool) {
	defer func() {
		if ag.Process() == p {
			ag.setProcess(nil)
			log.Printf("[Pipe] Agent %s process exited, initial=%v, current status=%s", agentID, initial, ag.Status())
			if !initial {
				// Claude -p exits normally after each response — keep idle so the
				// agent shows as "Standby" (usable) instead of "Stopped" (dead).
				// Don't override if Stop() or a crash already set stopped/crashed.
				cur := ag.Status()
				if cur != StatusStopped && cur != StatusCrashed {
					ag.setStatus(StatusIdle)
					log.Printf("[Pipe] Agent %s process exited normally, status stays idle", agentID)
				} else {
					log.Printf("[Pipe] Agent %s status is %s, keeping it", agentID, cur)
				}
			}
		}
	}()
	log.Printf("[Pipe] Started reading output for agent %s (initial=%v)", agentID, initial)

	buf := make([]byte, 4096)
	var lineBuffer strings.Builder
	var fullText strings.Builder

	for {
		n, err := p.Read(buf)
		if n > 0 {
			text := string(buf[:n])
			lineBuffer.WriteString(text)

			// Process complete lines (stream-json is NDJSON format)
			content := lineBuffer.String()
			lines := strings.Split(content, "\n")
			// Keep the last (potentially incomplete) line in buffer
			lineBuffer.Reset()
			if len(lines) > 0 && !strings.HasSuffix(content, "\n") {
				lineBuffer.WriteString(lines[len(lines)-1])
				lines = lines[:len(lines)-1]
			}

			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				// Try to parse as stream-json event
				if ev := m.tryParseStreamJSON(line); ev != nil {
					log.Printf("[StreamJSON] Parsed event type=%s for agent %s", ev.Type, agentID)
					m.handleStreamJSONEvent(agentID, ag, ev)
					// Also accumulate text content for full response
					if ev.Type == "assistant" && ev.Content != nil {
						var text string
						var contentArr []struct {
							Type string `json:"type"`
							Text string `json:"text,omitempty"`
						}
						if err := json.Unmarshal(ev.Content, &text); err == nil {
							fullText.WriteString(text)
						} else if err := json.Unmarshal(ev.Content, &contentArr); err == nil {
							for _, block := range contentArr {
								if block.Type == "text" {
									fullText.WriteString(block.Text)
								}
							}
						}
					}
				} else {
					// Not valid JSON, treat as plain text output
					if len(line) > 0 {
						preview := line
						if len(preview) > 80 {
							preview = preview[:80]
						}
						log.Printf("[StreamJSON] Failed to parse line: %s...", preview)
						fullText.WriteString(line)
						fullText.WriteString("\n")
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[Pipe] Read error for agent %s: %v", agentID, err)
			}
			break
		}
	}

	// Process any remaining content in buffer
	remaining := strings.TrimSpace(lineBuffer.String())
	if remaining != "" {
		if ev := m.tryParseStreamJSON(remaining); ev != nil {
			m.handleStreamJSONEvent(agentID, ag, ev)
		} else {
			fullText.WriteString(remaining)
		}
	}

	// Store complete response as a single event if we accumulated text
	// Don't flush fullText here — events are already stored in real-time
	// by handleStreamJSONEvent. fullText was only used as a fallback when
	// assistant text events were being skipped.

	// Wait for process to complete
	if err := p.Wait(); err != nil {
		log.Printf("[Process] Agent %s exited with error: %v", agentID, err)
	} else {
		log.Printf("[Process] Agent %s completed successfully", agentID)
	}
	log.Printf("[Pipe] Finished reading output for agent %s, captured %d chars", agentID, fullText.Len())
}

// readPipeOutputAndWait reads stream-json output from pipe and waits for process exit.
// Used for Claude -p mode where the process exits after each response.

func (m *Manager) Create(name, provider, cmd string, args []string, workDir string, env []string) (string, error) {
	id := newUUID()
	if provider == "" {
		provider = "custom"
	}
	ag := newAgent(id, name, provider, workDir, cmd, args)
	m.wireStatusCallback(ag)

	m.mu.Lock()
	m.agents[id] = ag
	m.mu.Unlock()

	_ = m.store.SaveAgent(store.AgentRecord{
		ID: id, Name: name, Provider: provider, WorkDir: workDir,
		// PID will be updated after process is spawned
	})

	ag.setStatus(StatusStarting)

	// For Claude with -p mode, don't start process on initial creation
	// because -p requires stdin input. Process will be started on first message.
	isClaudePrintMode := provider == "claude" && containsString(args, "-p")
	if isClaudePrintMode {
		ag.setStatus(StatusIdle)
		log.Printf("[Create] Agent %s created in idle mode (Claude -p, will start on first message)", id)
		return id, nil
	}

	// Use pipe mode for Claude to avoid TUI permission menus
	// Pipe mode with -p flag makes Claude run in non-interactive mode where --permission-mode works correctly
	var p *agentpty.Process
	var err error
	if provider == "claude" {
		// Claude with -p flag exits after one response, so we don't start permanent readers
		p, err = agentpty.SpawnPipes(cmd, args, workDir, env)
	} else {
		p, err = agentpty.Spawn(cmd, args, workDir, env)
	}
	if err != nil {
		ag.setStatus(StatusCrashed)
		return id, fmt.Errorf("spawn: %w", err)
	}
	ag.setProcess(p)
	ag.setStatus(StatusIdle)
	log.Printf("[Create] Agent %s started with pid=%d status=%s", id, p.Pid(), ag.Status())

	// Update store with PID
	_ = m.store.SaveAgent(store.AgentRecord{
		ID:       id,
		Name:     name,
		Provider: provider,
		WorkDir:  workDir,
		PID:      p.Pid(),
	})

	if provider != "claude" {
		// For interactive providers (opencode), use session file watcher
		go m.startSessionWatcher(id, ag, p.Pid(), workDir)
		go m.readPTYForPermissionPrompts(id, ag, provider, p)
	} else {
		// For Claude -p mode: read output directly from pipe, process will exit after response
		// Pass initial=false since this is a message restart, not initial creation
		log.Printf("[Create] Starting Claude in -p mode, agent %s, pid %d", id, p.Pid())
		go m.readPipeOutputAndWait(id, ag, p, false)
	}

	return id, nil
}

// startSessionWatcher tries to find the session file for a newly created agent
// and starts the appropriate watcher. For opencode it uses OpenCodeDBWatcher;
// for claude/others it uses ClaudeWatcher on the JSONL file.
func (m *Manager) startSessionWatcher(agentID string, ag *Agent, pid int, workDir string) {
	log.Printf("[Watcher] Starting session watcher for agent %s (PID %d)", agentID, pid)

	// For opencode, we can start the DB watcher immediately once we know the session ID
	if ag.Provider == "opencode" {
		sessionID := m.findOpenCodeSessionID(pid)
		if sessionID != "" {
			cb := m.makeWatcherCallback(agentID, ag)
			w := watcher.NewOpenCodeDBWatcher(sessionID, cb)
			if err := w.Start(); err != nil {
				log.Printf("[Watcher] OpenCode DB watcher start failed for agent %s: %v", agentID, err)
				return
			}
			ag.setWatcher(w)
			log.Printf("[Watcher] Started OpenCode DB watcher for agent %s (session %s)", agentID, sessionID)
			return
		}
		// Fall through to file-based watcher if we can't find the session ID
	}

	var sessionFile string
	retryCount := 0
	maxRetries := 300 // Retry for up to 5 minutes (TUI agents may take a while for first message)

	for sessionFile == "" && retryCount < maxRetries {
		if ag.Status() == StatusStopped {
			log.Printf("[Watcher] Agent %s stopped, aborting watcher", agentID)
			return
		}

		sessionFile = m.findSessionFile(pid, workDir)
		if sessionFile == "" {
			retryCount++
			if retryCount%10 == 0 {
				log.Printf("[Watcher] Still looking for session file for agent %s (retry %d)", agentID, retryCount)
			}
			time.Sleep(1 * time.Second)
		}
	}

	if sessionFile == "" {
		log.Printf("[Watcher] Could not find session file for agent %s (PID %d) after %d retries", agentID, pid, maxRetries)
		return
	}
	log.Printf("[Watcher] Found session file for agent %s: %s", agentID, sessionFile)

	cb := m.makeWatcherCallback(agentID, ag)
	w := watcher.NewClaudeWatcher(sessionFile, cb)
	w.SetWorkDir(workDir)
	w.SetPID(pid)
	if err := w.Start(); err != nil {
		log.Printf("[Watcher] Watcher start failed for agent %s: %v", agentID, err)
		return
	}
	ag.setWatcher(w)
	log.Printf("[Watcher] Started session watcher for agent %s", agentID)
}

// findSessionFile attempts to find the Claude JSONL session file for a given PID.
// It searches all candidate home directories so agentd running as root can find
// sessions belonging to non-root users.
func (m *Manager) findSessionFile(pid int, workDir string) string {
	for _, home := range allClaudeHomeDirs() {
		// Step 1: Check ~/.claude/sessions/<PID>.json to get sessionId
		sessionsDir := filepath.Join(home, ".claude", "sessions")
		pidFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", pid))

		if _, err := os.Stat(pidFile); err == nil {
			data, err := os.ReadFile(pidFile)
			if err == nil {
				var pidInfo struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(data, &pidInfo); err == nil && pidInfo.SessionID != "" {
					projectsBase := filepath.Join(home, ".claude", "projects")
					entries, _ := os.ReadDir(projectsBase)
					for _, entry := range entries {
						if entry.IsDir() {
							jsonlPath := filepath.Join(projectsBase, entry.Name(), pidInfo.SessionID+".jsonl")
							if _, err := os.Stat(jsonlPath); err == nil {
								return jsonlPath
							}
						}
					}
				}
			}
		}

		// Step 2: Fallback - look for JSONL created after this agent started (within the last 5 min)
		dirName := strings.ReplaceAll(workDir, "/", "-")
		if dirName == "" || dirName == "-" {
			dirName = "-"
		}

		projectsDir := filepath.Join(home, ".claude", "projects", dirName)
		entries, err := os.ReadDir(projectsDir)
		if err != nil {
			continue
		}

		cutoff := time.Now().Add(-5 * time.Minute)
		var latest string
		var latestTime time.Time
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".jsonl") {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(cutoff) && info.ModTime().After(latestTime) {
					latestTime = info.ModTime()
					latest = filepath.Join(projectsDir, entry.Name())
				}
			}
		}
		if latest != "" {
			return latest
		}
	}

	return ""
}

func (m *Manager) RestartInPlace(id, provider, cmd string, args []string, env []string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}

	ag.kill()
	ag.setProcess(nil)
	ag.setWatcher(nil)
	ag.setStatus(StatusStarting)

	ag.mu.Lock()
	ag.Provider = provider
	ag.Cmd = cmd
	ag.Args = append([]string{}, args...)
	workDir := ag.WorkDir
	ag.mu.Unlock()

	var (
		p   *agentpty.Process
		err error
	)
	if provider == "claude" {
		p, err = agentpty.SpawnPipes(cmd, args, workDir, env)
	} else {
		p, err = agentpty.Spawn(cmd, args, workDir, env)
	}
	if err != nil {
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("spawn: %w", err)
	}

	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	if provider != "claude" {
		go m.startSessionWatcher(id, ag, p.Pid(), workDir)
		go m.readPTYForPermissionPrompts(id, ag, provider, p)
	} else {
		log.Printf("[Restart] Starting Claude in -p mode, agent %s, pid %d", id, p.Pid())
		// Pass initial=false since this is a restart with message, not initial creation
		go m.readPipeOutputAndWait(id, ag, p, false)
	}

	return nil
}

// StartInPlaceWithMessage starts a fresh agent with a message written to stdin.
// Similar to RestartInPlace but for agents that haven't been started yet.
func (m *Manager) StartInPlaceWithMessage(id, provider, cmd string, args []string, env []string, message string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}

	ag.setStatus(StatusStarting)

	ag.mu.Lock()
	workDir := ag.WorkDir
	ag.mu.Unlock()

	var (
		p   *agentpty.Process
		err error
	)
	if provider == "claude" {
		p, err = agentpty.SpawnPipes(cmd, args, workDir, env)
	} else {
		p, err = agentpty.Spawn(cmd, args, workDir, env)
	}
	if err != nil {
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("spawn: %w", err)
	}

	// Write message to stdin
	if _, err := p.Write([]byte(message + "\n")); err != nil {
		p.Kill()
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("write message: %w", err)
	}
	p.CloseStdin()

	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	if provider == "claude" {
		log.Printf("[Start] Starting Claude in -p mode, agent %s, pid %d", id, p.Pid())
		// Pass initial=false since this is a message send, not initial creation
		go m.readPipeOutputAndWait(id, ag, p, false)
	}

	return nil
}

// CreateWithWatcher spawns an agent and starts the appropriate watcher.
func (m *Manager) CreateWithWatcher(name, provider, cmd string, args []string, workDir, sessionFile string) (string, error) {
	id, err := m.Create(name, provider, cmd, args, workDir, nil)
	if err != nil {
		return id, err
	}
	ag := m.Get(id)
	if ag == nil {
		return id, nil
	}

	sessionID := ""
	if sessionFile != "" {
		parts := strings.Split(sessionFile, "/")
		fileName := parts[len(parts)-1]
		sessionID = strings.TrimSuffix(strings.TrimSuffix(fileName, ".jsonl"), ".json")
	}

	cb := m.makeWatcherCallback(id, ag)
	w := m.newSessionWatcher(provider, sessionID, sessionFile, workDir, 0, cb)

	if err := w.Start(); err != nil {
		return id, fmt.Errorf("watcher start: %w", err)
	}
	ag.setWatcher(w)
	return id, nil
}

// List returns a snapshot of all agents.
func (m *Manager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Agent, 0, len(m.agents))
	for _, ag := range m.agents {
		out = append(out, ag)
	}
	return out
}

func (m *Manager) SetScanExisting(fn func() ([]scanner.ProcessInfo, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanExisting = fn
}

// DataDir returns the data directory used for persistent storage.
func (m *Manager) DataDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dataDir
}

// Get returns an agent by ID or nil.
func (m *Manager) Get(id string) *Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agents[id]
}

func (m *Manager) LoadPersistedEventsSince(agentID string, afterSeq uint64, limit int) ([]eventbuf.Event, error) {
	records, err := m.store.ListConversationEventsSince(agentID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	events := make([]eventbuf.Event, 0, len(records))
	for _, r := range records {
		data := map[string]any{
			"role": r.Role,
			"text": r.Text,
			"raw":  r.Raw,
			"kind": r.Kind,
		}
		events = append(events, eventbuf.Event{Seq: r.Seq, Data: data})
	}
	return events, nil
}

func (m *Manager) LoadPersistedEventsBefore(agentID string, beforeSeq uint64, limit int) ([]eventbuf.Event, error) {
	records, err := m.store.ListConversationEventsBefore(agentID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	events := make([]eventbuf.Event, 0, len(records))
	for _, r := range records {
		data := map[string]any{
			"role": r.Role,
			"text": r.Text,
			"raw":  r.Raw,
			"kind": r.Kind,
		}
		events = append(events, eventbuf.Event{Seq: r.Seq, Data: data})
	}
	return events, nil
}

func (m *Manager) LoadPersistedEventsLatest(agentID string, limit int) ([]eventbuf.Event, error) {
	records, err := m.store.ListConversationEventsLatest(agentID, limit)
	if err != nil {
		return nil, err
	}
	events := make([]eventbuf.Event, 0, len(records))
	for _, r := range records {
		data := map[string]any{
			"role": r.Role,
			"text": r.Text,
			"raw":  r.Raw,
			"kind": r.Kind,
		}
		events = append(events, eventbuf.Event{Seq: r.Seq, Data: data})
	}
	return events, nil
}

func (m *Manager) LastPersistedSeq(agentID string) (uint64, error) {
	return m.store.LastConversationSeq(agentID)
}

func (m *Manager) UpdateResumeSessionID(id, sessionID string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}
	if sessionID == "" {
		return nil
	}
	if err := m.store.UpdateResumeSessionID(id, sessionID); err != nil {
		return err
	}

	// Also update the parent agent if this is a child agent (for session continuity)
	m.mu.RLock()
	parentID, hasParent := m.sessionParents[id]
	parentCount := len(m.sessionParents)
	m.mu.RUnlock()
	log.Printf("[Session] Checking parent for agent %s: hasParent=%v, parentID=%s, mapSize=%d", id, hasParent, parentID, parentCount)
	if hasParent && parentID != "" {
		if err := m.store.UpdateResumeSessionID(parentID, sessionID); err != nil {
			log.Printf("[Session] Failed to update parent agent %s session ID: %v", parentID, err)
		} else {
			log.Printf("[Session] Also saved session ID %s to parent agent %s", sessionID, parentID)
		}
	}

	return nil
}

func (m *Manager) UpdateAgentProvider(id, provider string) error {
	m.mu.Lock()
	ag, ok := m.agents[id]
	if ok {
		ag.Provider = provider
	}
	m.mu.Unlock()
	return m.store.UpdateAgentProvider(id, provider)
}

// SetSessionParent sets the parent agent ID for session tracking.
// When a child agent extracts a session ID, it will also be saved to the parent.
func (m *Manager) SetSessionParent(childID, parentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionParents[childID] = parentID
	log.Printf("[Session] Set parent of agent %s to %s", childID, parentID)
}

// GetResumeSessionID retrieves the stored resume session ID for an agent.
func (m *Manager) GetResumeSessionID(id string) (string, error) {
	records, err := m.store.ListAgents()
	if err != nil {
		return "", err
	}
	for _, r := range records {
		if r.ID == id {
			return r.ResumeSessionID, nil
		}
	}
	return "", nil
}

// StartWatcherForAgent locates the session file for an agent (using its stored
// ResumeSessionID) and starts a ClaudeWatcher. If the agent already has a
// running watcher it is stopped first. The agent status is set to idle.
func (m *Manager) StartWatcherForAgent(id string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}

	sessionID, _ := m.GetResumeSessionID(id)
	if sessionID == "" {
		return fmt.Errorf("agent %q has no stored session ID", id)
	}

	// Search all candidate home directories so agentd running as root finds
	// sessions belonging to non-root users.
	var sessionFile string
	for _, home := range allClaudeHomeDirs() {
		projectsBase := filepath.Join(home, ".claude", "projects")
		entries, err := os.ReadDir(projectsBase)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(projectsBase, entry.Name(), sessionID+".jsonl")
			if _, err := os.Stat(candidate); err == nil {
				sessionFile = candidate
				break
			}
		}
		if sessionFile != "" {
			break
		}
	}
	if sessionFile == "" {
		return fmt.Errorf("session file not found for session %s", sessionID)
	}

	// Stop existing watcher if any
	if old := ag.Watcher(); old != nil {
		old.Stop()
	}

	// For opencode, use DB watcher instead of file watcher
	if ag.Provider == "opencode" && sessionID != "" {
		cb := m.makeWatcherCallback(id, ag)
		w := watcher.NewOpenCodeDBWatcher(sessionID, cb)
		if err := w.Start(); err != nil {
			return fmt.Errorf("watcher start: %w", err)
		}
		ag.setWatcher(w)
		ag.setStatus(StatusIdle)
		log.Printf("[Watcher] Started OpenCode DB watcher for loaded agent %s (session %s)", id, sessionID)
		return nil
	}

	// Start new watcher
	cb := m.makeWatcherCallback(id, ag)
	w := watcher.NewClaudeWatcher(sessionFile, cb)

	if err := w.Start(); err != nil {
		return fmt.Errorf("watcher start: %w", err)
	}
	ag.setWatcher(w)
	ag.setStatus(StatusIdle)
	log.Printf("[Watcher] Started watcher for loaded agent %s on %s", id, sessionFile)
	return nil
}

// Rename updates the display name of an agent.
func (m *Manager) Rename(agentID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ag, ok := m.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	ag.Name = name
	return nil
}

// Stop kills the agent process.
func (m *Manager) Stop(id string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}
	ag.kill()
	ag.setStatus(StatusStopped)
	return nil
}

// Remove stops and removes the agent from tracking.
func (m *Manager) Remove(id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.agents, id)
	m.mu.Unlock()
	// Clean up persisted images for this agent
	imgDir := filepath.Join(m.dataDir, "images", id)
	if err := os.RemoveAll(imgDir); err != nil {
		log.Printf("[Remove] failed to clean image dir for %s: %v", id, err)
	}
	return m.store.DeleteAgent(id)
}

// allClaudeHomeDirs returns candidate home directories to search for Claude sessions.
// When agentd runs as root it also includes all /home/* user directories so that
// sessions belonging to non-root users are discovered correctly.
func allClaudeHomeDirs() []string {
	seen := map[string]struct{}{}
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join("/home", e.Name()))
			}
		}
	}
	return dirs
}

// findSessionFileByWorkDir attempts to find a session file by work directory.
// This is a fallback for older Claude processes that don't have PID files.
// It searches all candidate home directories so agentd running as root can find
// sessions belonging to non-root users.
func (m *Manager) findSessionFileByWorkDir(workDir string) string {
	// Convert workDir to project directory name format used by Claude
	// Claude replaces '/', '.', and '_' with '-'
	dirName := strings.ReplaceAll(workDir, "/", "-")
	dirName = strings.ReplaceAll(dirName, ".", "-")
	dirName = strings.ReplaceAll(dirName, "_", "-")

	for _, home := range allClaudeHomeDirs() {
		projectDir := filepath.Join(home, ".claude", "projects", dirName)
		log.Printf("[findSessionFileByWorkDir] Looking in: %s", projectDir)

		entries, err := os.ReadDir(projectDir)
		if err != nil {
			log.Printf("[findSessionFileByWorkDir] ReadDir error: %v", err)
			continue
		}

		var latestFile string
		var latestTime int64
		count := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
				count++
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Unix() > latestTime {
					latestTime = info.ModTime().Unix()
					latestFile = filepath.Join(projectDir, entry.Name())
				}
			}
		}
		// Conservative rule: only use workDir fallback when there is exactly one
		// candidate session file. Multiple jsonl files in the same project dir are
		// ambiguous (could be old sessions or sub-agents) and must not be auto-bound.
		if count == 1 && latestFile != "" {
			log.Printf("[findSessionFileByWorkDir] Returning unique candidate: %s", latestFile)
			return latestFile
		}
		if count > 1 {
			log.Printf("[findSessionFileByWorkDir] Ambiguous project dir %s: %d jsonl files, refusing fallback", projectDir, count)
		}
	}
	log.Printf("[findSessionFileByWorkDir] No unambiguous session file found for workDir=%s", workDir)
	return ""
}

// ScanExisting discovers running Claude/OpenCode processes on the system.
func (m *Manager) ScanExisting() ([]scanner.ProcessInfo, error) {
	if m.scanExisting != nil {
		return m.scanExisting()
	}
	s := scanner.New()
	return s.Scan()
}

type AttachDecision string

const (
	AttachDecisionAuto      AttachDecision = "auto"
	AttachDecisionDisplay   AttachDecision = "display"
	AttachDecisionAmbiguous AttachDecision = "ambiguous"
	AttachDecisionSkip      AttachDecision = "skip"
)

type AttachCandidate struct {
	Process  scanner.ProcessInfo
	Decision AttachDecision
	Reason   string
}

func classifySessionFileID(sessionFile string) string {
	if sessionFile == "" {
		return ""
	}
	base := filepath.Base(sessionFile)
	if strings.HasSuffix(base, ".jsonl") {
		return strings.TrimSuffix(base, ".jsonl")
	}
	if strings.HasSuffix(base, ".json") {
		return strings.TrimSuffix(base, ".json")
	}
	return ""
}

func (m *Manager) ClassifyAttachCandidate(info scanner.ProcessInfo) AttachCandidate {
	if info.PID <= 0 {
		return AttachCandidate{Process: info, Decision: AttachDecisionSkip, Reason: "missing pid"}
	}
	if info.Provider != "claude" {
		return AttachCandidate{Process: info, Decision: AttachDecisionAuto, Reason: "non-claude process"}
	}
	if info.SessionID != "" {
		return AttachCandidate{Process: info, Decision: AttachDecisionAuto, Reason: "stable session identity"}
	}
	if info.WorkDir != "" {
		if sessionFile := m.findSessionFileByWorkDir(info.WorkDir); sessionFile != "" {
			info.SessionFile = sessionFile
			info.SessionID = classifySessionFileID(sessionFile)
			return AttachCandidate{Process: info, Decision: AttachDecisionDisplay, Reason: "live claude without pid map; display only"}
		}
	}
	return AttachCandidate{Process: info, Decision: AttachDecisionAmbiguous, Reason: "claude process has no stable session id"}
}

func (m *Manager) AutoAttachExisting() {
	procs, err := m.ScanExisting()
	if err != nil {
		log.Printf("warning: scan existing processes: %v", err)
		return
	}
	for _, proc := range procs {
		candidate := m.ClassifyAttachCandidate(proc)
		if candidate.Decision != AttachDecisionAuto {
			log.Printf("[AutoAttach] Skipping %s pid %d: %s", proc.Provider, proc.PID, candidate.Reason)
			continue
		}
		if ag, err := m.Attach(proc); err != nil {
			log.Printf("warning: auto-attach pid %d: %v", proc.PID, err)
		} else {
			log.Printf("[AutoAttach] Attached to %s (pid %d) as agent %s", proc.Provider, proc.PID, ag.ID)
		}
	}
}

// Attach takes over an existing process by watching its session file.
// It does NOT kill the existing process - instead it creates a watcher
// that monitors the session file for changes, allowing local and remote
// sessions to coexist and synchronize through the shared session file.
func (m *Manager) Attach(info scanner.ProcessInfo) (*Agent, error) {
	// Verify the process is still the expected provider (prevent PID reuse attack)
	if !validateProcess(info.PID, info.Provider) {
		return nil, fmt.Errorf("process %d is not a valid %s process", info.PID, info.Provider)
	}

	// Find session file for watching
	sessionFile := info.FindSessionFile()
	log.Printf("[Attach] PID %d: FindSessionFile returned: %s", info.PID, sessionFile)
	if sessionFile == "" {
		// Fallback: try to find session file by workDir for older Claude processes
		// that don't have PID files in ~/.claude/sessions/
		log.Printf("[Attach] PID %d: Trying fallback, WorkDir=%s, Provider=%s", info.PID, info.WorkDir, info.Provider)
		if info.WorkDir != "" && info.Provider == "claude" {
			sessionFile = m.findSessionFileByWorkDir(info.WorkDir)
			log.Printf("[Attach] PID %d: findSessionFileByWorkDir returned: %s", info.PID, sessionFile)
		}
		if sessionFile == "" {
			return nil, fmt.Errorf("no session file found for process %d", info.PID)
		}
	}

	// Extract session ID from filename
	parts := strings.Split(sessionFile, "/")
	fileName := parts[len(parts)-1]
	// Handle both .json (OpenCode) and .jsonl (Claude) extensions
	sessionID := strings.TrimSuffix(strings.TrimSuffix(fileName, ".jsonl"), ".json")

	applyAttachMetadata := func(ag *Agent) {
		ag.SetAttachInputRoute(info.AttachMode(), info.AttachReadOnly(), info.AttachReadOnlyReason(), info.TmuxTarget)
	}

	// projectNameFromDir extracts the last path segment as a project name.
	projectNameFromDir := func(dir string) string {
		dir = strings.TrimRight(dir, "/")
		if dir == "" {
			return ""
		}
		return filepath.Base(dir)
	}

	// Reuse existing managed attached agent only when it refers to the same
	// live process or the same session. Reusing purely by friendly project name
	// lets team-mode sub-agents hijack the parent session watcher.
	m.mu.RLock()
	for _, existing := range m.agents {
		if existing.Provider != info.Provider {
			continue
		}
		existingResumeID, _ := m.GetResumeSessionID(existing.ID)
		samePID := existing.PID > 0 && existing.PID == info.PID
		sameSession := sessionID != "" && existingResumeID == sessionID
		if !samePID && !sameSession {
			continue
		}
		m.mu.RUnlock()
		// Refresh attach metadata in case tmux/TTY availability changed.
		applyAttachMetadata(existing)
		// Re-attach: restart watcher and update status
		existing.setStatus(StatusIdle)
		if existing.Watcher() != nil {
			existing.Watcher().Stop()
			existing.setWatcher(nil)
		}
		sessionFile := info.FindSessionFile()
		if sessionFile != "" || info.Provider == "opencode" {
			cb := m.makeWatcherCallback(existing.ID, existing)
			w := m.newSessionWatcher(info.Provider, sessionID, sessionFile, info.WorkDir, info.PID, cb)
			if err := w.Start(); err != nil {
				log.Printf("[ReAttach] Warning: watcher start failed for %s: %v", existing.ID, err)
			} else {
				existing.setWatcher(w)
				log.Printf("[ReAttach] Restarted watcher for agent %s", existing.ID)
			}
		}
		return existing, nil
	}
	m.mu.RUnlock()

	// Create a managed agent that watches the existing session
	// WITHOUT killing or restarting the original process
	projectName := projectNameFromDir(info.WorkDir)
	var name string
	if projectName != "" {
		name = fmt.Sprintf("%s (%s)", projectName, info.Provider)
	} else {
		name = fmt.Sprintf("%s-attached-%d", info.Provider, info.PID)
	}
	id := newUUID()

	// Create agent with existing session info
	ag := newAgent(id, name, info.Provider, info.WorkDir, info.Cmd, info.Args)
	ag.PID = info.PID
	m.wireStatusCallback(ag)
	ag.setStatus(StatusIdle)
	applyAttachMetadata(ag)

	m.mu.Lock()
	m.agents[id] = ag
	m.mu.Unlock()

	// Save to store for persistence
	_ = m.store.SaveAgent(store.AgentRecord{
		ID:              id,
		Name:            name,
		Provider:        info.Provider,
		WorkDir:         info.WorkDir,
		ResumeSessionID: sessionID,
		PID:             info.PID,
	})

	// Load historical events for OpenCode sessions from SQLite DB
	if info.Provider == "opencode" && sessionID != "" {
		historyEvents, err := watcher.OpenCodeDBHistory(sessionID)
		if err != nil {
			log.Printf("[Attach] Warning: failed to load OpenCode history for %s: %v", id, err)
		} else {
			log.Printf("[Attach] Loaded %d historical events for OpenCode agent %s", len(historyEvents), id)
			for _, ev := range historyEvents {
				data := map[string]any{
					"role": ev.Role,
					"text": ev.Text,
					"raw":  false,
				}
				m.appendAndPersistEvent(id, ag, data)
			}
		}
	}

	// Start the appropriate watcher
	cb := m.makeWatcherCallback(id, ag)
	w := m.newSessionWatcher(info.Provider, sessionID, sessionFile, info.WorkDir, info.PID, cb)

	if err := w.Start(); err != nil {
		log.Printf("[Attach] Warning: watcher start failed for %s: %v", id, err)
	} else {
		ag.setWatcher(w)
		log.Printf("[Attach] Started watcher for agent %s", id)
	}

	return ag, nil
}

func validateProcess(pid int, expectedProvider string) bool {
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		// On non-Linux systems (e.g., macOS), /proc doesn't exist.
		// Skip validation on these platforms as PID reuse is less of a concern.
		if os.IsNotExist(err) {
			return true
		}
		return false
	}
	cmdline := strings.ToLower(string(data))
	return strings.Contains(cmdline, strings.ToLower(expectedProvider))
}

// getString safely extracts a string value from a map
func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// makeWatcherCallback builds the standard callback used by all session watchers.
func (m *Manager) makeWatcherCallback(agentID string, ag *Agent) func(watcher.ConversationEvent) {
	return func(e watcher.ConversationEvent) {
		data := map[string]any{
			"role": e.Role,
			"text": e.Text,
			"raw":  false,
		}
		if e.ToolSummary != "" {
			data["toolSummary"] = e.ToolSummary
		}
		if e.StatusChange != nil {
			data["statusChange"] = string(*e.StatusChange)
			if *e.StatusChange == watcher.StatusWorking {
				ag.setStatus(StatusWorking)
			} else {
				ag.setStatus(StatusIdle)
			}
		}
		m.appendAndPersistEvent(agentID, ag, data)
		m.mu.RLock()
		cb := m.onOutput
		m.mu.RUnlock()
		if cb != nil {
			cb(agentID, data)
		}
	}
}

// newSessionWatcher creates the appropriate watcher for the given provider.
// For opencode it returns an OpenCodeDBWatcher that reads from the SQLite DB;
// for claude (and others) it returns a ClaudeWatcher on the JSONL file.
func (m *Manager) newSessionWatcher(provider, sessionID, sessionFile, workDir string, pid int, cb func(watcher.ConversationEvent)) watcher.SessionWatcher {
	if provider == "opencode" && sessionID != "" {
		return watcher.NewOpenCodeDBWatcher(sessionID, cb)
	}
	w := watcher.NewClaudeWatcher(sessionFile, cb)
	w.SetWorkDir(workDir)
	w.SetPID(pid)
	return w
}

// findOpenCodeSessionID queries the opencode SQLite DB to find the most recent
// session for the given PID's working directory.
func (m *Manager) findOpenCodeSessionID(pid int) string {
	// Try to get working directory first
	cwd, err := getWorkingDirectory(pid)
	if err != nil {
		return ""
	}

	dbPath := watcher.FindOpenCodeDB()
	if dbPath == "" {
		return ""
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return ""
	}
	defer db.Close()

	var sessionID string
	err = db.QueryRow(`
		SELECT id FROM session
		WHERE directory = ?
		ORDER BY time_updated DESC
		LIMIT 1`, cwd).Scan(&sessionID)
	if err != nil {
		return ""
	}
	return sessionID
}

// getWorkingDirectory attempts to get the working directory for a PID
func getWorkingDirectory(pid int) (string, error) {
	// Try reading from /proc (Linux)
	cwdLink := fmt.Sprintf("/proc/%d/cwd", pid)
	if cwd, err := os.Readlink(cwdLink); err == nil {
		return cwd, nil
	}

	// Try lsof as fallback (works on macOS and Linux)
	cmd := exec.Command("lsof", "-p", fmt.Sprintf("%d", pid), "-F", "cwd")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// lsof output format: first character is the field type, rest is the value
	// 'c' is current working directory
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if len(line) > 0 && line[0] == 'c' {
			return line[1:], nil
		}
	}

	return "", fmt.Errorf("could not determine working directory")
}

// PeriodicScanAndAttach runs periodically to discover new Claude/OpenCode processes
// and attach to them as new agents.
func (m *Manager) PeriodicScanAndAttach() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Scan for new processes
		s := scanner.New()
		processes, err := s.Scan()
		if err != nil {
			log.Printf("[PeriodicScan] Scan failed: %v", err)
			continue
		}

		// Filter out processes that are children of existing agents
		var filteredProcesses []scanner.ProcessInfo
		m.mu.RLock()
		for _, proc := range processes {
			isChildAgent := false
			for _, ag := range m.agents {
				if ag.PID > 0 && proc.PPID == ag.PID {
					isChildAgent = true
					break
				}
			}
			if !isChildAgent {
				filteredProcesses = append(filteredProcesses, proc)
			}
		}
		m.mu.RUnlock()

		// Get current agents to compare
		m.mu.RLock()
		currentAgents := make(map[string]bool)
		for id := range m.agents {
			currentAgents[id] = true
		}
		m.mu.RUnlock()

		// Track which PIDs we already have
		existingPIDs := make(map[int]bool)
		for _, proc := range filteredProcesses {
			existingPIDs[proc.PID] = true
		}

		// Collect session IDs already tracked by existing agents.
		// Sub-agents (team mode children) share the same session file as their
		// parent, so a session-ID overlap means the process is a child we should
		// skip.
		m.mu.RLock()
		trackedSessionIDs := make(map[string]bool)
		for _, ag := range m.agents {
			rid, _ := m.GetResumeSessionID(ag.ID)
			if rid != "" {
				trackedSessionIDs[rid] = true
			}
		}
		m.mu.RUnlock()

		// Find new processes to attach to
		for _, proc := range filteredProcesses {
			candidate := m.ClassifyAttachCandidate(proc)
			if candidate.Decision != AttachDecisionAuto {
				continue
			}

			// Skip if this process's session is already tracked (sub-agent).
			if proc.SessionID != "" && trackedSessionIDs[proc.SessionID] {
				continue
			}

			// Check if we already have an agent for this PID
			found := false
			m.mu.RLock()
			for _, ag := range m.agents {
				if ag.PID == proc.PID && ag.Provider == proc.Provider {
					found = true
					break
				}
			}
			m.mu.RUnlock()

			if !found {
				log.Printf("[PeriodicScan] Found new %s process (PID %d), attaching...", proc.Provider, proc.PID)
				if ag, err := m.Attach(proc); err != nil {
					log.Printf("[PeriodicScan] Failed to attach to PID %d: %v", proc.PID, err)
				} else {
					log.Printf("[PeriodicScan] Successfully attached to %s (PID %d) as agent %s",
						proc.Provider, proc.PID, ag.ID)
				}
			}
		}
	}
}
