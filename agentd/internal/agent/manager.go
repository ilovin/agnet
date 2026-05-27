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
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/hermesclient"
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

func attachedDisplayName(pid int, sessionID, provider, workDir string) string {
	projectName := strings.TrimRight(workDir, "/")
	if projectName != "" {
		projectName = filepath.Base(projectName)
	}
	if projectName != "" && pid > 0 {
		return fmt.Sprintf("%s - %d", projectName, pid)
	}
	if projectName != "" {
		return projectName
	}
	if pid > 0 {
		return fmt.Sprintf("%d", pid)
	}
	if sessionID != "" {
		return sessionID
	}
	return provider
}

func isAttachedAutoName(name string, pid int, sessionID, provider, workDir string) bool {
	return name == attachedDisplayName(pid, sessionID, provider, workDir)
}

type Manager struct {
	mu                sync.RWMutex
	agents            map[string]*Agent
	store             *store.Store
	dataDir           string
	onOutput          func(agentID string, data map[string]any) // broadcast hook for messages
	onStatusChange    func(agentID string, data map[string]any) // broadcast hook for status changes
	sessionParents    map[string]string                         // childAgentID -> parentAgentID for session continuity
	hermesClient      *hermesclient.Client
	scanExisting      func() ([]scanner.ProcessInfo, error)
	sessionFileFinder func(scanner.ProcessInfo) string    // test hook to override session file discovery
	processRunning    func(pid int, provider string) bool // test hook for process liveness check
	hermesGatewayRun  func(pid int) bool                  // test hook for hermes gateway daemon detection
	events            *EventManager
	parser            *StreamParser
	processes         *ProcessManager
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
		events:         NewEventManager(s),
		parser:         NewStreamParser(),
	}
	m.scanExisting = func() ([]scanner.ProcessInfo, error) {
		s := scanner.New()
		return s.Scan()
	}
	m.processRunning = isProcessRunning
	m.hermesGatewayRun = isHermesGatewayRunPID
	m.processes = NewProcessManager(s, m.parser, dataDir)
	m.processes.SetHandleStreamJSONEvent(m.handleStreamJSONEvent)
	m.processes.SetMakeWatcherCallback(m.makeWatcherCallback)
	return m
}

// LoadFromStore loads persisted agents from the store into memory.
// Only agents whose process is still running are restored as idle;
// stopped agents (pid=0 or dead process) are cleaned up from the store.
func (m *Manager) SetHermesBaseURL(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if url != "" {
		m.hermesClient = hermesclient.NewClient(url, "")
	}
}

// HermesClient returns the hermes HTTP client, lazily creating it with defaults if needed.
func (m *Manager) HermesClient() *hermesclient.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hermesClient == nil {
		key := os.Getenv("HERMES_API_KEY")
		m.hermesClient = hermesclient.NewClient("http://127.0.0.1:8642", key)
	}
	return m.hermesClient
}

func (m *Manager) LoadFromStore() error {
	records, err := m.store.ListAgents()
	if err != nil {
		return fmt.Errorf("list agents from store: %w", err)
	}
	// claudeWatcherTodo collects Claude agents whose jsonl watcher must be
	// re-spawned after agentd restart. We can't start the watchers while
	// holding m.mu because newSessionWatcher() calls m.Get() (which takes
	// m.mu.RLock) and would deadlock. So defer watcher startup until after
	// the lock is released below.
	type claudeWatcherTodo struct {
		agentID   string
		ag        *Agent
		workDir   string
		pid       int
		sessionID string
	}
	var claudeTodos []claudeWatcherTodo

	// hermesAttachTodo collects Hermes agents whose attach metadata
	// (mode, readOnly, reason, tmuxTarget) must be re-derived from a fresh
	// scanner.ProcessInfo after agentd restart. Like claudeTodos, this is
	// processed after m.mu is released because ScanExisting() / m.Get()
	// take m.mu.RLock internally and would deadlock if called while held.
	type hermesAttachTodo struct {
		agentID string
		ag      *Agent
		pid     int
	}
	var hermesTodos []hermesAttachTodo

	// opencodeWatcherTodo collects OpenCode agents whose DB watcher must
	// be re-spawned after agentd restart. Same deadlock concern as
	// claudeTodos: newSessionWatcher() takes m.mu.RLock internally.
	type opencodeWatcherTodo struct {
		agentID   string
		ag        *Agent
		workDir   string
		pid       int
		sessionID string
	}
	var opencodeTodos []opencodeWatcherTodo

	m.mu.Lock()
	loadedAttached := make(map[string]struct{})
	loadedByPID := make(map[string]struct{})
	loadedBySession := make(map[string]struct{})
	for _, r := range records {
		if r.Provider == "hermes" && r.PID > 0 && m.hermesGatewayRun != nil && m.hermesGatewayRun(r.PID) {
			log.Printf("[LoadFromStore] Removing persisted hermes gateway daemon %s (PID %d)", r.ID, r.PID)
			_ = m.store.DeleteAgent(r.ID)
			continue
		}

		isHermesProcessless := r.Provider == "hermes" && r.PID <= 0
		if !isHermesProcessless {
			if r.PID <= 0 || m.processRunning == nil || !m.processRunning(r.PID, r.Provider) {
				log.Printf("[LoadFromStore] Skipping stopped agent %s (%s, PID %d)", r.ID, r.Name, r.PID)
				_ = m.store.DeleteAgent(r.ID)
				continue
			}
		}

		if strings.Contains(r.Name, "-attached-") {
			key := r.Provider + "|" + r.Name
			if _, ok := loadedAttached[key]; ok {
				_ = m.store.DeleteAgent(r.ID)
				continue
			}
			loadedAttached[key] = struct{}{}
		}

		// Deduplicate by PID+Provider and ResumeSessionID+Provider
		if r.PID > 0 {
			pidKey := fmt.Sprintf("%s|%d", r.Provider, r.PID)
			if _, ok := loadedByPID[pidKey]; ok {
				log.Printf("[LoadFromStore] Skipping duplicate agent %s (same PID %d)", r.ID, r.PID)
				_ = m.store.DeleteAgent(r.ID)
				continue
			}
			loadedByPID[pidKey] = struct{}{}
		}

		if r.ResumeSessionID != "" {
			sessionKey := fmt.Sprintf("%s|%d|%s", r.Provider, r.PID, r.ResumeSessionID)
			if _, ok := loadedBySession[sessionKey]; ok {
				log.Printf("[LoadFromStore] Skipping duplicate agent %s (same pid %d and session %s)", r.ID, r.PID, r.ResumeSessionID)
				_ = m.store.DeleteAgent(r.ID)
				continue
			}
			loadedBySession[sessionKey] = struct{}{}
		}

		var cmd string
		var args []string
		switch r.Provider {
		case "opencode":
			cmd = "opencode"
			if r.ResumeSessionID != "" {
				args = []string{"-s", r.ResumeSessionID}
			}
		case "hermes":
			cmd = "hermes"
			args = []string{"gateway", "run"}
		default:
			cmd = "claude"
			args = []string{"--dangerously-skip-permissions"}
		}
		ag := newAgent(r.ID, r.Name, r.Provider, r.WorkDir, cmd, args)
		ag.PID = r.PID
		m.wireStatusCallback(ag)
		ag.setStatus(StatusIdle)
		log.Printf("[LoadFromStore] Agent %s (PID %d) is still running, setting status to idle", r.ID, r.PID)

		// Initialize buffer seq from persisted events so new appends continue after existing data
		if lastSeq, err := m.store.LastConversationSeq(r.ID); err == nil && lastSeq > 0 {
			ag.InitSeq(lastSeq)
		}
		// Load Hermes CLI history from state.db on restart so conversation data
		// is available even after agentd restart.
		if r.Provider == "hermes" {
			if hist, _, err := watcher.HermesStateDBHistory(); err == nil && len(hist) > 0 {
				log.Printf("[LoadFromStore] Loaded %d historical events for Hermes agent %s from state.db", len(hist), r.ID)
				for _, ev := range hist {
					data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
					_ = m.appendAndPersistEvent(r.ID, ag, data)
				}
			}
			// Start state.db poller so /clear-style switches in the running
			// hermes CLI are detected after agentd restart.
			cb := m.makeWatcherCallback(r.ID, ag)
			w := m.newSessionWatcher(r.Provider, r.ResumeSessionID, "", r.WorkDir, r.PID, cb, r.ID)
			w.SetSkipExisting(true)
			if err := w.Start(); err != nil {
				log.Printf("[LoadFromStore] Warning: hermes watcher start failed for %s: %v", r.ID, err)
			} else {
				ag.setWatcher(w)
			}
		}
		m.agents[r.ID] = ag

		// Queue Claude agents for jsonl watcher re-spawn after the lock is
		// released. Without this, the agent's EventBuf stays frozen at the
		// pre-restart state and live appends by the running Claude CLI are
		// never reflected in the API. Mirrors the re-attach path's watcher
		// startup logic (see manager.go ReAttach Claude branch).
		if r.Provider == "claude" && r.PID > 0 {
			claudeTodos = append(claudeTodos, claudeWatcherTodo{
				agentID:   r.ID,
				ag:        ag,
				workDir:   r.WorkDir,
				pid:       r.PID,
				sessionID: r.ResumeSessionID,
			})
		}

		// Queue Hermes agents for attach metadata rescan after the lock is
		// released. ScanExisting() / m.Get() take m.mu.RLock internally,
		// so we can't call them while holding m.mu.
		if r.Provider == "hermes" && r.PID > 0 {
			hermesTodos = append(hermesTodos, hermesAttachTodo{
				agentID: r.ID,
				ag:      ag,
				pid:     r.PID,
			})
		}

		// Queue OpenCode agents for DB watcher re-spawn after the lock is
		// released. Without this, the agent's EventBuf stays frozen at the
		// pre-restart state and live messages from the running OpenCode CLI
		// are never reflected in the API. Mirrors the Claude jsonl watcher
		// fix (commit 8d16b8f).
		if r.Provider == "opencode" && r.PID > 0 {
			opencodeTodos = append(opencodeTodos, opencodeWatcherTodo{
				agentID:   r.ID,
				ag:        ag,
				workDir:   r.WorkDir,
				pid:       r.PID,
				sessionID: r.ResumeSessionID,
			})
		}
	}
	m.mu.Unlock()

	// Spawn jsonl watchers for restored Claude agents now that the lock is
	// released; newSessionWatcher() acquires m.mu.RLock internally.
	for _, t := range claudeTodos {
		sessionFile := scanner.FindClaudeSessionFile(t.pid, t.workDir)
		if sessionFile == "" {
			log.Printf("[LoadFromStore] Could not find jsonl session file for Claude agent %s (PID %d); watcher not started", t.agentID, t.pid)
			continue
		}
		cb := m.makeWatcherCallback(t.agentID, t.ag)
		w := m.newSessionWatcher("claude", t.sessionID, sessionFile, t.workDir, t.pid, cb, t.agentID)
		w.SetSkipExisting(true)
		if err := w.Start(); err != nil {
			log.Printf("[LoadFromStore] Watcher start failed for Claude agent %s: %v", t.agentID, err)
			continue
		}
		t.ag.setWatcher(w)
		log.Printf("[LoadFromStore] Spawned jsonl watcher for Claude agent %s (PID %d, session %s)", t.agentID, t.pid, sessionFile)
	}

	// Rescan attach metadata for restored Hermes agents. The Agent struct
	// is created fresh in LoadFromStore with empty attach fields; the
	// scanner is the source of truth for tmux pane availability. M2 of
	// the hermes tmux migration plan §6.2.
	if len(hermesTodos) > 0 {
		procs, err := m.ScanExisting()
		if err != nil {
			log.Printf("[LoadFromStore] Hermes attach rescan: ScanExisting failed: %v", err)
		} else {
			for _, t := range hermesTodos {
				var info *scanner.ProcessInfo
				for i := range procs {
					if procs[i].PID == t.pid && procs[i].Provider == "hermes" {
						info = &procs[i]
						break
					}
				}
				if info == nil {
					// PID gone or no longer a hermes process; leave the
					// agent's existing attach fields untouched.
					continue
				}
				t.ag.SetAttachInputRoute(info.AttachMode(), info.AttachReadOnly(), info.AttachReadOnlyReason(), info.TmuxTarget)
			}
		}
	}

	// Spawn DB watchers for restored OpenCode agents now that the lock is
	// released; newSessionWatcher() acquires m.mu.RLock internally.
	for _, t := range opencodeTodos {
		cb := m.makeWatcherCallback(t.agentID, t.ag)
		w := m.newSessionWatcher("opencode", t.sessionID, "", t.workDir, t.pid, cb, t.agentID)
		if w == nil {
			log.Printf("[LoadFromStore] No watcher built for OpenCode agent %s (session %q); skipping", t.agentID, t.sessionID)
			continue
		}
		w.SetSkipExisting(true)
		if err := w.Start(); err != nil {
			log.Printf("[LoadFromStore] Watcher start failed for OpenCode agent %s: %v", t.agentID, err)
			continue
		}
		t.ag.setWatcher(w)
		log.Printf("[LoadFromStore] Spawned DB watcher for OpenCode agent %s (PID %d, session %s)", t.agentID, t.pid, t.sessionID)
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
		if os.IsNotExist(err) {
			// On non-Linux (e.g., macOS), /proc doesn't exist. Fall back to ps.
			cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
			output, err := cmd.Output()
			if err != nil {
				return false
			}
			comm := strings.TrimSpace(string(output))
			return strings.Contains(strings.ToLower(comm), strings.ToLower(provider))
		}
		return false
	}
	cmdline := strings.ToLower(string(data))
	return strings.Contains(cmdline, strings.ToLower(provider))
}

func isHermesGatewayRunPID(pid int) bool {
	if pid <= 0 {
		return false
	}

	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	if data, err := os.ReadFile(cmdlinePath); err == nil {
		parts := strings.Split(strings.ToLower(string(data)), "\x00")
		hasHermes := false
		for _, part := range parts {
			if strings.Contains(part, "hermes") {
				hasHermes = true
				break
			}
		}
		if !hasHermes {
			return false
		}
		for i := 0; i+1 < len(parts); i++ {
			if strings.TrimSpace(parts[i]) == "gateway" && strings.TrimSpace(parts[i+1]) == "run" {
				return true
			}
		}
		return false
	}

	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(string(output))))
	if len(fields) == 0 {
		return false
	}
	hasHermes := false
	for _, field := range fields {
		if strings.Contains(field, "hermes") {
			hasHermes = true
			break
		}
	}
	if !hasHermes {
		return false
	}
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "gateway" && fields[i+1] == "run" {
			return true
		}
	}
	return false
}
func (m *Manager) SetOnOutput(fn func(agentID string, data map[string]any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onOutput = fn
}

// FireOnOutput invokes the onOutput callback synchronously.
// Intended for use in tests that need to simulate an agent output broadcast
// without running a real agent process.
func (m *Manager) FireOnOutput(agentID string, data map[string]any) {
	m.mu.Lock()
	cb := m.onOutput
	m.mu.Unlock()
	if cb != nil {
		cb(agentID, data)
	}
}

// SetOnStatusChange registers a callback invoked whenever an agent's status changes.
func (m *Manager) SetOnStatusChange(fn func(agentID string, data map[string]any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStatusChange = fn
}

func (m *Manager) appendAndPersistEvent(agentID string, ag *Agent, data map[string]any) uint64 {
	return m.events.AppendAndPersistEvent(agentID, ag, data)
}

// updateOrAppendEvent handles streaming message updates: if data contains a
// msg_id matching an existing event, it updates in place (buffer + store) and
// returns (existingSeq, true). Otherwise it appends a new event.
func (m *Manager) updateOrAppendEvent(agentID string, ag *Agent, data map[string]any) (uint64, bool) {
	return m.events.UpdateOrAppendEvent(agentID, ag, data)
}

func maybeExtractSessionIDFromRaw(text string) string {
	return defaultResolver.MaybeExtractSessionIDFromRaw(text)
}

func cleanPermissionText(text string) string {
	return defaultResolver.CleanPermissionText(text)
}

func detectPermissionPrompt(text string) bool {
	return defaultResolver.DetectPermissionPrompt(text)
}

// defaultResolver is a package-level PermissionResolver for standalone functions.
var defaultResolver = NewPermissionResolver()

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
	var lineBuffer strings.Builder
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
				lineBuffer.WriteString(text)
				content := lineBuffer.String()
				lines := strings.Split(content, "\n")
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
					if ev := m.tryParseStreamJSON(line); ev != nil {
						m.handleStreamJSONEvent(agentID, ag, ev)
					}
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

	// Process any remaining content in buffer
	if provider == "claude" {
		remaining := strings.TrimSpace(lineBuffer.String())
		if remaining != "" {
			if ev := m.tryParseStreamJSON(remaining); ev != nil {
				m.handleStreamJSONEvent(agentID, ag, ev)
			}
		}
	}
}

// streamJSONEvent is an alias for backward compatibility within this package.
type streamJSONEvent = StreamJSONEvent

func (m *Manager) tryParseStreamJSON(text string) *streamJSONEvent {
	return m.parser.TryParseStreamJSON(text)
}

// HandleStreamJSONEvent is an exported wrapper for handleStreamJSONEvent (tests only).
func (m *Manager) HandleStreamJSONEvent(agentID string, ag *Agent, ev *streamJSONEvent) {
	m.handleStreamJSONEvent(agentID, ag, ev)
}

// buildToolResultSummary extracts a concise summary from a tool result output.
// toolName is optional (may be empty if not available in the event).
func buildToolResultSummary(toolName string, output []byte) string {
	return defaultParser.BuildToolResultSummary(toolName, output)
}

func buildToolInputSummary(toolName string, input json.RawMessage) string {
	return defaultParser.BuildToolInputSummary(toolName, input)
}

// defaultParser is a package-level StreamParser for use by standalone functions.
var defaultParser = NewStreamParser()

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
		var hasToolUse bool
		var contentArr []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}
		if err := json.Unmarshal(ev.Content, &text); err == nil {
			// Simple string content
		} else if err := json.Unmarshal(ev.Content, &contentArr); err == nil {
			// Array of content blocks
			for _, block := range contentArr {
				switch block.Type {
				case "text":
					text += block.Text
				case "tool_use":
					hasToolUse = true
				case "tool_result":
					// Tool results are system-level messages; skip them.
				}
			}
		}

		// Skip empty user messages that have no text and no tool_use.
		if text == "" && !hasToolUse {
			return
		}

		// Deduplicate user messages against the EventBuffer so conversation.send
		// recordings don't clash with stream-json events, while still preserving
		// messages typed directly in the CLI.
		if role == "user" {
			last := ag.EventBuf().LastEvent()
			if lastEventRole, _ := last.Data["role"].(string); lastEventRole == "user" {
				if lastEventText, _ := last.Data["text"].(string); lastEventText == text {
					return
				}
			}
			// User messages (including interrupts) should reset status to idle.
			// When a user interrupts a running request, Claude writes a user-type
			// message, and we need to transition away from StatusWorking.
			// NOTE: status change is deferred until after the event is persisted
			// so that agent.status_changed carries the up-to-date lastMessageTime.
		}

		kind := role // "user" or "assistant"
		data = map[string]any{
			"role": role,
			"text": text,
			"raw":  false,
			"kind": kind,
		}
		if ev.Timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				data["timestamp"] = parsed.UnixMilli()
			} else if parsed, err := time.Parse(time.RFC3339, ev.Timestamp); err == nil {
				data["timestamp"] = parsed.UnixMilli()
			}
		}

	case "tool_use":
		toolUseID, _ := ev.Raw["id"].(string)
		if kind, payload, ok := ParseInteractiveToolUse(ev.Name, toolUseID, ev.Input); ok {
			// AskUserQuestion or ExitPlanMode: emit structured interactive event.
			payloadBytes, _ := json.Marshal(payload)
			var payloadMap map[string]any
			_ = json.Unmarshal(payloadBytes, &payloadMap)
			data = map[string]any{
				"role": "assistant",
				"raw":  false,
				"kind": kind,
			}
			if key := PayloadKeyForKind(kind); key != "" {
				data[key] = payloadMap
			}
			if ev.Timestamp != "" {
				if parsed, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
					data["timestamp"] = parsed.UnixMilli()
				} else if parsed, err := time.Parse(time.RFC3339, ev.Timestamp); err == nil {
					data["timestamp"] = parsed.UnixMilli()
				}
			}
			ag.setStatus(StatusWorking)
			break
		}
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
		if ev.Timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				data["timestamp"] = parsed.UnixMilli()
			} else if parsed, err := time.Parse(time.RFC3339, ev.Timestamp); err == nil {
				data["timestamp"] = parsed.UnixMilli()
			}
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
		if ev.Timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				data["timestamp"] = parsed.UnixMilli()
			} else if parsed, err := time.Parse(time.RFC3339, ev.Timestamp); err == nil {
				data["timestamp"] = parsed.UnixMilli()
			}
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

	case "message_start":
		ag.setStatus(StatusWorking)
		return

	case "content_block_start":
		if ev.Raw == nil {
			return
		}
		if contentBlock, ok := ev.Raw["content_block"].(map[string]any); ok {
			blockType, _ := contentBlock["type"].(string)
			if blockType == "tool_use" {
				name, _ := contentBlock["name"].(string)
				toolUseID, _ := contentBlock["id"].(string)
				inputRaw, _ := json.Marshal(contentBlock["input"])
				var blockData map[string]any
				if kind, payload, ok := ParseInteractiveToolUse(name, toolUseID, inputRaw); ok {
					payloadBytes, _ := json.Marshal(payload)
					var payloadMap map[string]any
					_ = json.Unmarshal(payloadBytes, &payloadMap)
					blockData = map[string]any{
						"role": "assistant",
						"raw":  false,
						"kind": kind,
					}
					if key := PayloadKeyForKind(kind); key != "" {
						blockData[key] = payloadMap
					}
				} else {
					summary := buildToolInputSummary(name, inputRaw)
					var toolText string
					if summary != "" {
						toolText = fmt.Sprintf("[%s: %s]", name, summary)
					} else {
						toolText = fmt.Sprintf("[%s]", name)
					}
					blockData = map[string]any{
						"role":     "assistant",
						"text":     toolText,
						"raw":      false,
						"kind":     "tool_use",
						"toolName": name,
					}
				}
				seq := m.appendAndPersistEvent(agentID, ag, blockData)
				blockData["seq"] = seq
				m.mu.RLock()
				cb := m.onOutput
				m.mu.RUnlock()
				if cb != nil {
					cb(agentID, blockData)
				}
				ag.setStatus(StatusWorking)
			}
		}
		return

	case "content_block_delta":
		if ev.Raw == nil {
			return
		}
		if delta, ok := ev.Raw["delta"].(map[string]any); ok {
			text, _ := delta["text"].(string)
			if text == "" {
				text, _ = delta["text_delta"].(string)
			}
			if text != "" {
				data := map[string]any{
					"role":    "assistant",
					"text":    text,
					"raw":     false,
					"kind":    "text_delta",
					"partial": true,
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
		}
		return

	case "message_stop":
		ag.setStatus(StatusIdle)
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

		// Set status after persistence so that status-changed events carry
		// the up-to-date lastMessageTime (LastConversationEventTime reads DB).
		switch ev.Type {
		case "user":
			ag.setStatus(StatusIdle)
		case "assistant":
			ag.setStatus(StatusWorking)
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

	hasProcess := process != nil
	hasWatcher := watcher != nil
	// For display-only agents (no watcher, no process object) with a running PID,
	// check process liveness to avoid showing "exited" for a running external process.
	hasRunningPID := false
	if !hasProcess && !hasWatcher && status != StatusStopped && status != StatusCrashed {
		if ag.PID > 0 {
			hasRunningPID = isProcessRunning(ag.PID, ag.Provider)
		}
	}

	state := DerivedAgentState{
		RuntimeState:   deriveRuntimeState(status, hasProcess, hasWatcher, hasRunningPID),
		SessionControl: deriveSessionControl(process != nil, watcher != nil, attachMode, attachReadOnly, resumeSessionID),
	}
	state.SessionState, state.SessionStateReason = deriveSessionState(status, process != nil, watcher != nil, resumeSessionID)
	return state
}

func deriveRuntimeState(status Status, hasProcess bool, hasWatcher bool, hasRunningPID bool) string {
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
		if hasProcess || hasWatcher || hasRunningPID {
			return "live"
		}
		return "exited"
	default:
		if hasProcess || hasWatcher || hasRunningPID {
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

	// For Hermes, no process spawn needed (HTTP API based)
	if provider == "hermes" {
		ag.setStatus(StatusIdle)
		log.Printf("[Create] Agent %s created in idle mode (Hermes HTTP API)", id)
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

		sessionFile = scanner.FindClaudeSessionFile(pid, workDir)
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
	w.SetTmuxTarget(ag.TmuxTarget())
	if err := w.Start(); err != nil {
		log.Printf("[Watcher] Watcher start failed for agent %s: %v", agentID, err)
		return
	}
	ag.setWatcher(w)
	log.Printf("[Watcher] Started session watcher for agent %s", agentID)
}

// FindSessionFileProjectDirName exposes scanner.ProjectDirName for testing.
func (m *Manager) FindSessionFileProjectDirName(workDir string) string {
	return scanner.ProjectDirName(workDir)
}

func (m *Manager) RestartInPlace(id, provider, cmd string, args []string, env []string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}

	ag.kill()
	// Wait for old process to fully exit before spawning new one.
	// Do NOT clear ag.process here — old goroutines check ag.Process() == p
	// in their deferred cleanup; clearing it orphans them.
	if p := ag.Process(); p != nil {
		_ = p.Wait()
	}
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
	w := m.newSessionWatcher(provider, sessionID, sessionFile, workDir, 0, cb, id)

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

// SetFindSessionFile overrides the default session file discovery for testing.
// When set, Attach uses this function instead of info.FindSessionFile().
func (m *Manager) SetFindSessionFile(fn func(scanner.ProcessInfo) string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionFileFinder = fn
}

func (m *Manager) SetProcessRunningChecker(fn func(pid int, provider string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processRunning = fn
}

func (m *Manager) SetHermesGatewayRunChecker(fn func(pid int) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hermesGatewayRun = fn
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
	return m.events.LoadPersistedEventsSince(agentID, afterSeq, limit)
}

func (m *Manager) LoadPersistedEventsBefore(agentID string, beforeSeq uint64, limit int) ([]eventbuf.Event, error) {
	return m.events.LoadPersistedEventsBefore(agentID, beforeSeq, limit)
}

func (m *Manager) LoadPersistedEventsLatest(agentID string, limit int) ([]eventbuf.Event, error) {
	return m.events.LoadPersistedEventsLatest(agentID, limit)
}

func (m *Manager) LastPersistedSeq(agentID string) (uint64, error) {
	return m.events.LastPersistedSeq(agentID)
}

func (m *Manager) LastConversationEventTime(agentID string) (time.Time, error) {
	return m.events.LastConversationEventTime(agentID)
}

func (m *Manager) ClearConversationEvents(agentID string) error {
	return m.events.ClearConversationEvents(agentID)
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
	w.SetSkipExisting(true)

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
	if err := m.store.UpdateAgentName(agentID, name); err != nil {
		return err
	}
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

func isHermesGatewayRunProcess(info scanner.ProcessInfo) bool {
	if info.Provider != "hermes" {
		return false
	}
	for i := 0; i+1 < len(info.Args); i++ {
		if strings.EqualFold(info.Args[i], "gateway") && strings.EqualFold(info.Args[i+1], "run") {
			return true
		}
	}
	return false
}

func isHermesInteractiveProcess(info scanner.ProcessInfo) bool {
	if info.Provider != "hermes" {
		return false
	}
	if info.AttachMode() == scanner.AttachModeTmux {
		return true
	}
	terminal := strings.TrimSpace(info.Terminal)
	if terminal == "" {
		return false
	}
	if terminal == "/dev/null" || strings.HasPrefix(terminal, "pipe:") {
		return false
	}
	return strings.HasPrefix(terminal, "/dev/tty") || strings.HasPrefix(terminal, "/dev/pts/")
}

func (m *Manager) ClassifyAttachCandidate(info scanner.ProcessInfo) AttachCandidate {
	if info.PID <= 0 {
		return AttachCandidate{Process: info, Decision: AttachDecisionSkip, Reason: "missing pid"}
	}
	if isHermesGatewayRunProcess(info) {
		return AttachCandidate{Process: info, Decision: AttachDecisionSkip, Reason: "hermes gateway daemon process"}
	}
	if info.Provider == "hermes" {
		if !isHermesInteractiveProcess(info) {
			return AttachCandidate{Process: info, Decision: AttachDecisionSkip, Reason: "hermes process is not interactive"}
		}
		return AttachCandidate{Process: info, Decision: AttachDecisionAuto, Reason: "interactive hermes process"}
	}
	if info.Provider != "claude" {
		return AttachCandidate{Process: info, Decision: AttachDecisionAuto, Reason: "non-claude process"}
	}
	if info.SessionID != "" {
		return AttachCandidate{Process: info, Decision: AttachDecisionAuto, Reason: "stable session identity"}
	}
	return AttachCandidate{Process: info, Decision: AttachDecisionDisplay, Reason: "claude process has no stable session id"}
}

func (m *Manager) AutoAttachExisting() {
	procs, err := m.ScanExisting()
	if err != nil {
		log.Printf("warning: scan existing processes: %v", err)
		return
	}

	// Collect existing session keys from store to prevent duplicates
	// across periodic scans for the same provider+sessionID.
	existingSessions := make(map[string]bool)
	records, err := m.store.ListAgents()
	if err == nil {
		for _, r := range records {
			if r.ResumeSessionID != "" {
				existingSessions[r.Provider+"|"+r.ResumeSessionID] = true
			}
		}
	}
	seenSessions := make(map[string]bool) // within-scan deduplication

	for _, proc := range procs {
		candidate := m.ClassifyAttachCandidate(proc)
		if candidate.Decision != AttachDecisionAuto && candidate.Decision != AttachDecisionDisplay {
			log.Printf("[AutoAttach] Skipping %s pid %d: %s", proc.Provider, proc.PID, candidate.Reason)
			continue
		}

		// Deduplicate: skip if we already track this provider+sessionID.
		if proc.SessionID != "" {
			key := proc.Provider + "|" + proc.SessionID
			if existingSessions[key] || seenSessions[key] {
				log.Printf("[AutoAttach] Skipping %s pid %d: already tracking %s session %s", proc.Provider, proc.PID, proc.Provider, proc.SessionID)
				continue
			}
			seenSessions[key] = true
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
	var sessionFile string
	m.mu.RLock()
	if m.sessionFileFinder != nil {
		sessionFile = m.sessionFileFinder(info)
	} else {
		sessionFile = info.FindSessionFile()
	}
	m.mu.RUnlock()
	log.Printf("[Attach] PID %d: FindSessionFile returned: %s", info.PID, sessionFile)

	// Extract session ID from filename
	var sessionID string
	if sessionFile != "" {
		parts := strings.Split(sessionFile, "/")
		fileName := parts[len(parts)-1]
		// Handle both .json (OpenCode) and .jsonl (Claude) extensions
		sessionID = strings.TrimSuffix(strings.TrimSuffix(fileName, ".jsonl"), ".json")
	}
	if sessionID == "" && info.Provider == "hermes" {
		sessionID = strings.TrimSpace(info.SessionID)
	}

	// For claude processes without a session file, create a display-only agent
	// so they still appear in agent.list. No watcher is started since there's
	// nothing to watch.
	isDisplayOnly := sessionFile == "" && info.Provider == "claude"

	applyAttachMetadata := func(ag *Agent) {
		// Don't downgrade from tmux to watcher: when multiple processes
		// share the same session (e.g. opencode main + daemon), the one
		// in tmux should win.
		if ag.AttachMode() == "tmux" && info.AttachMode() != "tmux" {
			return
		}
		ag.SetAttachInputRoute(info.AttachMode(), info.AttachReadOnly(), info.AttachReadOnlyReason(), info.TmuxTarget)
	}

	rebindAttachedSession := func(ag *Agent, newSessionID string, newSessionFile string) {
		if oldWatcher := ag.Watcher(); oldWatcher != nil {
			oldWatcher.Stop()
			ag.setWatcher(nil)
		}
		// Reset the in-memory EventBuf so new-session events start from seq=0,
		ag.EventBuf().Reset()
		currentName := ag.Name
		currentPID := ag.PID
		currentSessionID, _ := m.GetResumeSessionID(ag.ID)
		if ag.PID != info.PID {
			ag.PID = info.PID
			if err := m.store.UpdateAgentPID(ag.ID, info.PID); err != nil {
				log.Printf("[Attach] Warning: failed to update pid for %s: %v", ag.ID, err)
			}
		}
		newAutoName := attachedDisplayName(info.PID, newSessionID, info.Provider, info.WorkDir)
		if isAttachedAutoName(currentName, currentPID, currentSessionID, info.Provider, info.WorkDir) && currentName != newAutoName {
			if err := m.Rename(ag.ID, newAutoName); err != nil {
				log.Printf("[Attach] Warning: failed to rename %s: %v", ag.ID, err)
			}
		}
		if newSessionID != "" {
			if err := m.UpdateResumeSessionID(ag.ID, newSessionID); err != nil {
				log.Printf("[Attach] Warning: failed to update session ID for %s: %v", ag.ID, err)
			}
		}
	}

	// For opencode: tmux mode takes precedence over watcher mode.
	// If an agent with the same session already exists (different PID),
	// resolve the conflict before proceeding.
	if info.Provider == "opencode" && sessionID != "" {
		m.mu.RLock()
		var conflict *Agent
		for _, existing := range m.agents {
			if existing.Provider != "opencode" {
				continue
			}
			existingResumeID, _ := m.GetResumeSessionID(existing.ID)
			if existingResumeID == sessionID && existing.PID != info.PID {
				conflict = existing
				break
			}
		}
		m.mu.RUnlock()

		if conflict != nil {
			isNewTmux := info.AttachMode() == scanner.AttachModeTmux
			isConflictTmux := conflict.AttachMode() == scanner.AttachModeTmux
			if isNewTmux && !isConflictTmux {
				log.Printf("[Attach] Replacing watcher agent %s with tmux agent for session %s", conflict.ID, sessionID)
				_ = m.Remove(conflict.ID)
			} else if !isNewTmux && isConflictTmux {
				log.Printf("[Attach] Skipping non-tmux opencode PID %d: tmux agent %s already owns session %s", info.PID, conflict.ID, sessionID)
				return nil, fmt.Errorf("tmux agent already exists for session %s", sessionID)
			}
		}
	}

	// Reuse an existing attached agent only when it refers to the same
	// live process. Different PIDs can legitimately share one session ID,
	// so session identity alone must not collapse them.
	m.mu.RLock()
	for _, existing := range m.agents {
		if existing.Provider != info.Provider {
			continue
		}
		existingResumeID, _ := m.GetResumeSessionID(existing.ID)
		samePID := existing.PID > 0 && existing.PID == info.PID
		if !samePID {
			continue
		}
		m.mu.RUnlock()
		// Refresh attach metadata in case tmux/TTY availability changed.
		applyAttachMetadata(existing)
		if samePID && sessionID != "" && existingResumeID != sessionID && !(info.Provider == "hermes" && existingResumeID != "") {
			rebindAttachedSession(existing, sessionID, sessionFile)

		} else {
			currentName := existing.Name
			currentPID := existing.PID
			if existing.PID != info.PID {
				existing.PID = info.PID
				if err := m.store.UpdateAgentPID(existing.ID, info.PID); err != nil {
					log.Printf("[ReAttach] Warning: failed to update pid for %s: %v", existing.ID, err)
				}
			}
			autoName := attachedDisplayName(info.PID, sessionID, info.Provider, info.WorkDir)
			if isAttachedAutoName(currentName, currentPID, existingResumeID, info.Provider, info.WorkDir) && currentName != autoName {
				if err := m.Rename(existing.ID, autoName); err != nil {
					log.Printf("[ReAttach] Warning: failed to rename %s: %v", existing.ID, err)
				}
			}
		}
		// Re-attach: restart watcher and update status
		existing.setStatus(StatusIdle)
		if existing.Watcher() != nil {
			existing.Watcher().Stop()
			existing.setWatcher(nil)
		}

		// Load historical events on re-attach so conversation.history has data.
		if info.Provider == "opencode" && sessionID != "" {
			if historyEvents, err := watcher.OpenCodeDBHistory(sessionID); err == nil && len(historyEvents) > 0 {
				log.Printf("[ReAttach] Loaded %d historical events for OpenCode agent %s", len(historyEvents), existing.ID)
				for _, ev := range historyEvents {
					data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
					m.appendAndPersistEvent(existing.ID, existing, data)
				}
			}
		} else if info.Provider == "claude" && sessionFile != "" {
			if historyEvents, err := watcher.LoadClaudeJSONLHistory(sessionFile); err == nil && len(historyEvents) > 0 {
				log.Printf("[ReAttach] Loaded %d historical events for Claude agent %s", len(historyEvents), existing.ID)
				for _, ev := range historyEvents {
					data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
					m.appendAndPersistEvent(existing.ID, existing, data)
				}
			}
		} else if info.Provider == "hermes" {
			if historyEvents, sessionDBID, err := watcher.HermesStateDBHistory(); err == nil && len(historyEvents) > 0 {
				log.Printf("[ReAttach] Loaded %d historical events for Hermes agent %s (session %s)", len(historyEvents), existing.ID, sessionDBID)
				for _, ev := range historyEvents {
					data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
					m.appendAndPersistEvent(existing.ID, existing, data)
				}
			}
		}

		if !isDisplayOnly && (sessionFile != "" || info.Provider == "opencode" || info.Provider == "hermes") {
			cb := m.makeWatcherCallback(existing.ID, existing)
			watcherSessionID := sessionID
			if info.Provider == "hermes" {
				// Preserve any existing (possibly server-issued) resume id; the
				// scanner-supplied --session arg is not authoritative for hermes.
				if curID, _ := m.GetResumeSessionID(existing.ID); curID != "" {
					watcherSessionID = curID
				}
			}
			w := m.newSessionWatcher(info.Provider, watcherSessionID, sessionFile, info.WorkDir, info.PID, cb, existing.ID)
			w.SetSkipExisting(true)
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

	// For hermes processes, create a managed agent with no watcher or process.
	// This runs after same-PID reuse logic so repeated attach reuses existing agent.
	if info.Provider == "hermes" {
		name := attachedDisplayName(info.PID, sessionID, info.Provider, info.WorkDir)
		id := newUUID()
		ag := newAgent(id, name, info.Provider, info.WorkDir, info.Cmd, info.Args)
		ag.PID = info.PID
		ag.SetAttachInputRoute(info.AttachMode(), info.AttachReadOnly(), info.AttachReadOnlyReason(), info.TmuxTarget)
		ag.setStatus(StatusIdle)

		m.mu.Lock()
		m.agents[id] = ag
		m.mu.Unlock()

		_ = m.store.SaveAgent(store.AgentRecord{
			ID:              id,
			Name:            name,
			Provider:        info.Provider,
			WorkDir:         info.WorkDir,
			ResumeSessionID: sessionID,
			PID:             info.PID,
		})

		// Load historical events from Hermes state.db so conversation.history is non-empty
		if historyEvents, sessionDBID, err := watcher.HermesStateDBHistory(); err == nil && len(historyEvents) > 0 {
			log.Printf("[Attach] Loaded %d historical events for Hermes agent %s (session %s)", len(historyEvents), id, sessionDBID)
			for _, ev := range historyEvents {
				data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
				m.appendAndPersistEvent(id, ag, data)
			}
		} else if err != nil {
			log.Printf("[Attach] Warning: failed to load Hermes history for %s: %v", id, err)
		}

		// Start state.db poller so /clear-style session switches are detected
		// without requiring a fresh hermesSend round-trip.
		cb := m.makeWatcherCallback(id, ag)
		w := m.newSessionWatcher(info.Provider, sessionID, "", info.WorkDir, info.PID, cb, id)
		w.SetSkipExisting(true)
		if err := w.Start(); err != nil {
			log.Printf("[Attach] Warning: hermes watcher start failed for %s: %v", id, err)
		} else {
			ag.setWatcher(w)
			log.Printf("[Attach] Started hermes watcher for agent %s", id)
		}

		return ag, nil
	}

	if sessionID != "" {
		m.mu.RLock()
		for _, existing := range m.agents {
			if existing.PID == info.PID || existing.PID <= 0 {
				continue
			}
			existingResumeID, _ := m.GetResumeSessionID(existing.ID)
			if existingResumeID == sessionID {
				if !isProcessRunning(existing.PID, existing.Provider) {
					log.Printf("[Attach] Skipping CONFLICT with dead agent %s (PID %d, session %s); removing stale agent",
						existing.ID, existing.PID, sessionID)
					existing.setStatus(StatusStopped)
					existing.setProcess(nil)
					_ = m.store.DeleteAgent(existing.ID)
					delete(m.agents, existing.ID)
					continue
				}
				log.Printf("[Attach] CONFLICT: existing agent %s (PID %d) already maps to session %s; new PID %d will be display-only",
					existing.ID, existing.PID, sessionID, info.PID)
				isDisplayOnly = true
				break
			}
		}
		m.mu.RUnlock()
	}

	// Create a managed agent that watches the existing session
	// WITHOUT killing or restarting the original process
	name := attachedDisplayName(info.PID, sessionID, info.Provider, info.WorkDir)
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

	// Load historical events for Claude sessions from JSONL file
	if info.Provider == "claude" && sessionFile != "" {
		historyEvents, err := watcher.LoadClaudeJSONLHistory(sessionFile)
		if err != nil {
			log.Printf("[Attach] Warning: failed to load Claude history for %s: %v", id, err)
		} else if len(historyEvents) > 0 {
			log.Printf("[Attach] Loaded %d historical events for Claude agent %s", len(historyEvents), id)
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

	// Start the appropriate watcher (skip for display-only agents)
	if !isDisplayOnly {
		cb := m.makeWatcherCallback(id, ag)
		w := m.newSessionWatcher(info.Provider, sessionID, sessionFile, info.WorkDir, info.PID, cb, id)
		w.SetSkipExisting(true)
		if err := w.Start(); err != nil {
			log.Printf("[Attach] Warning: watcher start failed for %s: %v", id, err)
		} else {
			ag.setWatcher(w)
			log.Printf("[Attach] Started watcher for agent %s", id)
		}
	} else {
		log.Printf("[Attach] Display-only agent %s (PID %d) created without watcher", id, info.PID)
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

// MakeWatcherCallback exposes makeWatcherCallback for tests that need to drive
// the watcher → DB → broadcast pipeline without spinning up a real session
// watcher. Production code calls makeWatcherCallback (lowercase) directly.
func (m *Manager) MakeWatcherCallback(agentID string, ag *Agent) func(watcher.ConversationEvent) {
	return m.makeWatcherCallback(agentID, ag)
}

// makeWatcherCallback builds the standard callback used by all session watchers.
func (m *Manager) makeWatcherCallback(agentID string, ag *Agent) func(watcher.ConversationEvent) {
	return func(e watcher.ConversationEvent) {
		// Deduplicate user messages: if the last event in the buffer is an identical
		// user message (already recorded by conversation.send), skip it. Otherwise,
		// record it so CLI-originated messages are not lost.
		if e.Role == "user" {
			last := ag.EventBuf().LastEvent()
			if lastEventRole, _ := last.Data["role"].(string); lastEventRole == "user" {
				if lastEventText, _ := last.Data["text"].(string); lastEventText == e.Text {
					return
				}
			}
		}

		// Interactive tools (AskUserQuestion / ExitPlanMode): the JSONL line
		// carries tool_use payload that the Flutter app needs as a structured
		// event (kind + camelCase payload), not just a "[ToolName]" text
		// bubble. This mirrors makeStreamJSONCallback so both ingest paths
		// (jsonl watcher + stream-json) deliver identical events. R-010 T2-bis.
		if e.ToolUseName != "" {
			if kind, payload, ok := ParseInteractiveToolUse(e.ToolUseName, e.ToolUseID, e.ToolUseInput); ok {
				payloadBytes, _ := json.Marshal(payload)
				var payloadMap map[string]any
				_ = json.Unmarshal(payloadBytes, &payloadMap)
				data := map[string]any{
					"role": "assistant",
					"raw":  false,
					"kind": kind,
				}
				if key := PayloadKeyForKind(kind); key != "" {
					data[key] = payloadMap
				}
				if e.StatusChange != nil {
					data["statusChange"] = string(*e.StatusChange)
					if *e.StatusChange == watcher.StatusWorking {
						ag.setStatus(StatusWorking)
					} else {
						ag.setStatus(StatusIdle)
					}
				} else {
					// Interactive tool_use without an explicit status change
					// still means the agent is working (waiting on user).
					ag.setStatus(StatusWorking)
				}
				seq := m.appendAndPersistEvent(agentID, ag, data)
				data["seq"] = seq

				m.mu.RLock()
				cb := m.onOutput
				m.mu.RUnlock()
				if cb != nil {
					cb(agentID, data)
				}
				return
			}
		}

		data := map[string]any{
			"role": e.Role,
			"text": e.Text,
			"raw":  false,
		}
		if e.MsgID != "" {
			data["msg_id"] = e.MsgID
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

		// For events with a MsgID (opencode streaming), use update-or-append
		// so the same message gets its text replaced as more parts arrive.
		var isUpdate bool
		if e.MsgID != "" {
			var seq uint64
			seq, isUpdate = m.updateOrAppendEvent(agentID, ag, data)
			data["seq"] = seq
		} else {
			seq := m.appendAndPersistEvent(agentID, ag, data)
			data["seq"] = seq
		}

		m.mu.RLock()
		cb := m.onOutput
		m.mu.RUnlock()
		if cb != nil {
			if isUpdate {
				cb(agentID, map[string]any{
					"_update": true,
					"agentId": agentID,
					"msg_id":  e.MsgID,
					"text":    e.Text,
					"seq":     data["seq"],
				})
			} else {
				cb(agentID, data)
			}
		}
	}
}

// newSessionWatcher creates the appropriate watcher for the given provider.
// For opencode it returns an OpenCodeDBWatcher that reads from the SQLite DB;
// for claude (and others) it returns a ClaudeWatcher on the JSONL file.
func (m *Manager) newSessionWatcher(provider, sessionID, sessionFile, workDir string, pid int, cb func(watcher.ConversationEvent), agentID string) watcher.SessionWatcher {
	if provider == "hermes" {
		w := watcher.NewHermesDBWatcher(agentID, sessionID, cb)
		// Suppress switch detection while a hermesSend is in flight; chunk.Done
		// is the authoritative source in that window. See plan §3.6.
		w.SetSendingChecker(func() bool {
			ag := m.Get(agentID)
			if ag == nil {
				return false
			}
			return ag.IsSending()
		})
		// Plan §M4 §2.3: detect hermes CLI process death so the agent flips to
		// stopped without waiting for a user send to discover the dead pane.
		// pid==0 (LoadFromStore-after-restart path that doesn't carry the live
		// pid) is a no-op inside the watcher.
		if pid > 0 {
			w.SetPID(pid)
			w.SetOnProcessDead(func() {
				ag := m.Get(agentID)
				if ag == nil {
					return
				}
				log.Printf("[HermesDBWatcher] hermes pid %d gone, marking agent %s stopped", pid, agentID)
				// setStatus(StatusStopped) → wireStatusCallback fires
				// onStatusChange which broadcasts agent.status_changed.
				ag.setStatus(StatusStopped)
			})
		}
		w.OnSessionSwitch(func(newSessionID string) {
			ag := m.Get(agentID)
			if ag == nil {
				return
			}
			// Plan §3.3 mandates this strict order: 1) UpdateResumeSessionID
			// (so any send already racing in uses the new id), 2) clear
			// persisted events, 3) reset live EventBuf, 4) reload new
			// session's history into both, 5) broadcast cleared +
			// status_changed.
			if err := m.UpdateResumeSessionID(agentID, newSessionID); err != nil {
				log.Printf("[Watcher] Failed to update session ID for %s on hermes session switch: %v", agentID, err)
			}
			if err := m.ClearConversationEvents(agentID); err != nil {
				log.Printf("[Watcher] Warning: failed to clear persisted history for %s on hermes session switch: %v", agentID, err)
			}
			ag.EventBuf().Reset()
			if historyEvents, err := watcher.HermesStateDBLoadSession(newSessionID); err == nil && len(historyEvents) > 0 {
				log.Printf("[Watcher] Loaded %d historical events for hermes session switch %s → %s", len(historyEvents), sessionID, newSessionID)
				for _, ev := range historyEvents {
					data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
					m.appendAndPersistEvent(agentID, ag, data)
				}
			}
			// Tell the watcher about the new session so subsequent in-session
			// emits use the right lastTS baseline.
			if hw, ok := ag.Watcher().(*watcher.HermesDBWatcher); ok {
				hw.SetSessionID(newSessionID)
			}
			m.mu.RLock()
			onOut := m.onOutput
			m.mu.RUnlock()
			if onOut != nil {
				onOut(agentID, map[string]any{
					"type":    "conversation.cleared",
					"agentId": agentID,
				})
			}
			derived := m.DeriveAgentState(agentID)
			m.mu.RLock()
			onSC := m.onStatusChange
			m.mu.RUnlock()
			if onSC != nil {
				params := map[string]any{
					"agentId":            agentID,
					"status":             string(ag.Status()),
					"runtimeState":       derived.RuntimeState,
					"sessionState":       derived.SessionState,
					"sessionStateReason": derived.SessionStateReason,
					"sessionControl":     derived.SessionControl,
				}
				if ag.Name != "" {
					params["name"] = ag.Name
				}
				resumeID, _ := m.GetResumeSessionID(agentID)
				if resumeID != "" {
					params["sessionId"] = resumeID
				}
				onSC(agentID, map[string]any{
					"jsonrpc": "2.0",
					"method":  "agent.status_changed",
					"params":  params,
				})
			}
		})
		return w
	}
	if provider == "opencode" && sessionID != "" {
		w := watcher.NewOpenCodeDBWatcher(sessionID, cb)
		w.SetWorkDir(workDir)
		w.OnSessionSwitch(func(newSessionID string) {
			ag := m.Get(agentID)
			if ag == nil {
				return
			}
			if err := m.UpdateResumeSessionID(agentID, newSessionID); err != nil {
				log.Printf("[Watcher] Failed to update session ID for %s on opencode session switch: %v", agentID, err)
			}
			ag.EventBuf().Reset()
			if err := m.ClearConversationEvents(agentID); err != nil {
				log.Printf("[Watcher] Warning: failed to clear persisted history for %s on opencode session switch: %v", agentID, err)
			}
			// Load history for the new session so conversation.history has data
			if historyEvents, err := watcher.OpenCodeDBHistory(newSessionID); err == nil && len(historyEvents) > 0 {
				log.Printf("[Watcher] Loaded %d historical events for session switch %s → %s", len(historyEvents), sessionID, newSessionID)
				for _, ev := range historyEvents {
					data := map[string]any{"role": ev.Role, "text": ev.Text, "raw": false}
					m.appendAndPersistEvent(agentID, ag, data)
				}
			}
			m.mu.RLock()
			onOut := m.onOutput
			m.mu.RUnlock()
			if onOut != nil {
				onOut(agentID, map[string]any{
					"type":    "conversation.cleared",
					"agentId": agentID,
				})
			}
			derived := m.DeriveAgentState(agentID)
			m.mu.RLock()
			onSC := m.onStatusChange
			m.mu.RUnlock()
			if onSC != nil {
				params := map[string]any{
					"agentId":            agentID,
					"status":             string(ag.Status()),
					"runtimeState":       derived.RuntimeState,
					"sessionState":       derived.SessionState,
					"sessionStateReason": derived.SessionStateReason,
					"sessionControl":     derived.SessionControl,
				}
				if ag.Name != "" {
					params["name"] = ag.Name
				}
				resumeID, _ := m.GetResumeSessionID(agentID)
				if resumeID != "" {
					params["sessionId"] = resumeID
				}
				onSC(agentID, map[string]any{
					"jsonrpc": "2.0",
					"method":  "agent.status_changed",
					"params":  params,
				})
			}
		})
		return w
	}
	w := watcher.NewClaudeWatcher(sessionFile, cb)
	w.SetWorkDir(workDir)
	w.SetPID(pid)
	if ag := m.Get(agentID); ag != nil {
		w.SetTmuxTarget(ag.TmuxTarget())
	}
	w.OnSessionSwitch(func(newPath string) {
		newSessionID := strings.TrimSuffix(filepath.Base(newPath), ".jsonl")
		ag := m.Get(agentID)
		if ag == nil {
			return
		}
		currentName := ag.Name
		currentSessionID, _ := m.GetResumeSessionID(agentID)
		newName := attachedDisplayName(pid, newSessionID, provider, workDir)
		if isAttachedAutoName(currentName, pid, currentSessionID, provider, workDir) && currentName != newName {
			if err := m.Rename(agentID, newName); err != nil {
				log.Printf("[Watcher] Failed to rename agent %s on session switch: %v", agentID, err)
			} else {
				log.Printf("[Watcher] Renamed agent %s to %s after session switch", agentID, newName)
			}
		}
		if newSessionID != "" {
			if err := m.UpdateResumeSessionID(agentID, newSessionID); err != nil {
				log.Printf("[Watcher] Failed to update session ID for %s: %v", agentID, err)
			}
		}
		ag.EventBuf().Reset()
		if err := m.ClearConversationEvents(agentID); err != nil {
			log.Printf("[Watcher] Warning: failed to clear persisted history for %s on session switch: %v", agentID, err)
		}
		m.mu.RLock()
		onOut := m.onOutput
		m.mu.RUnlock()
		if onOut != nil {
			onOut(agentID, map[string]any{
				"type":    "conversation.cleared",
				"agentId": agentID,
			})
		}
		derived := m.DeriveAgentState(agentID)
		m.mu.RLock()
		onSC := m.onStatusChange
		m.mu.RUnlock()
		if onSC != nil {
			params := map[string]any{
				"agentId":            agentID,
				"status":             string(ag.Status()),
				"runtimeState":       derived.RuntimeState,
				"sessionState":       derived.SessionState,
				"sessionStateReason": derived.SessionStateReason,
				"sessionControl":     derived.SessionControl,
			}
			if ag.Name != "" {
				params["name"] = ag.Name
			}
			onSC(agentID, map[string]any{
				"jsonrpc": "2.0",
				"method":  "agent.status_changed",
				"params":  params,
			})
		}
	})
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

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
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
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.RunPeriodicScanOnce()
	}
}

// RunPeriodicScanOnce executes one iteration of the periodic scan-and-attach
// loop. It is exported so tests can drive it directly without waiting for the
// ticker. The logic mirrors what PeriodicScanAndAttach runs on each tick.
func (m *Manager) RunPeriodicScanOnce() {
	// Scan for new processes (uses the scanExisting hook in tests).
	processes, err := m.ScanExisting()
	if err != nil {
		log.Printf("[PeriodicScan] Scan failed: %v", err)
		return
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

	// Find new processes to attach to, and rescue existing ones whose watcher
	// is nil (e.g. after agentd restart with ambiguous contentMatch).
	for _, proc := range filteredProcesses {
		candidate := m.ClassifyAttachCandidate(proc)
		if candidate.Decision != AttachDecisionAuto {
			continue
		}

		// Check if we already have an agent for this PID.
		// If found but the watcher is nil, fall through to the attach path so
		// the agent gets a live watcher (rescue path for Bug 2).
		foundWithWatcher := false
		m.mu.RLock()
		for _, ag := range m.agents {
			if ag.PID == proc.PID && ag.Provider == proc.Provider {
				if ag.Watcher() != nil {
					foundWithWatcher = true
				} else {
					log.Printf("[PeriodicScan] Rescuing %s pid %d: watcher is nil, triggering attach", proc.Provider, proc.PID)
				}
				break
			}
		}
		m.mu.RUnlock()

		if foundWithWatcher {
			// Already tracked with an active watcher — nothing to do.
			continue
		}

		log.Printf("[PeriodicScan] Attaching to %s process (PID %d)...", proc.Provider, proc.PID)
		if ag, err := m.Attach(proc); err != nil {
			log.Printf("[PeriodicScan] Failed to attach to PID %d: %v", proc.PID, err)
		} else {
			log.Printf("[PeriodicScan] Successfully attached to %s (PID %d) as agent %s",
				proc.Provider, proc.PID, ag.ID)
		}
	}

	// Cleanup dead agents
	m.CleanupDeadAgents()
}

// CleanupDeadAgents removes agents whose underlying process is no longer running.
// When a dead agent is removed, any display-only agents sharing the same session
// are promoted to full agents with a watcher.
func (m *Manager) CleanupDeadAgents() {
	procRunning := m.processRunning
	if procRunning == nil {
		procRunning = isProcessRunning
	}
	m.mu.Lock()
	var removedSessions []string
	for id, ag := range m.agents {
		if ag.PID > 0 && !procRunning(ag.PID, ag.Provider) {
			log.Printf("[PeriodicScan] Cleaning up dead agent %s (%s, PID %d)", ag.ID, ag.Name, ag.PID)
			resumeID, _ := m.GetResumeSessionID(ag.ID)
			if resumeID != "" {
				removedSessions = append(removedSessions, resumeID)
			}
			ag.setStatus(StatusStopped)
			ag.setProcess(nil)
			_ = m.store.DeleteAgent(ag.ID)
			delete(m.agents, id)
		}
	}

	// Promote display-only agents whose conflicting session was just freed.
	for _, sessionID := range removedSessions {
		for _, ag := range m.agents {
			if ag.Watcher() != nil || ag.PID <= 0 {
				continue
			}
			agentResumeID, _ := m.GetResumeSessionID(ag.ID)
			if agentResumeID != sessionID {
				continue
			}
			if !procRunning(ag.PID, ag.Provider) {
				continue
			}
			log.Printf("[PeriodicScan] Promoting display-only agent %s (PID %d) for freed session %s", ag.ID, ag.PID, sessionID)
			info := scanner.ProcessInfo{
				PID:      ag.PID,
				Provider: ag.Provider,
				WorkDir:  ag.WorkDir,
			}
			sessionFile := info.FindSessionFile()
			cb := m.makeWatcherCallback(ag.ID, ag)
			w := m.newSessionWatcher(ag.Provider, sessionID, sessionFile, ag.WorkDir, ag.PID, cb, ag.ID)
			if err := w.Start(); err != nil {
				log.Printf("[PeriodicScan] Warning: watcher start failed for promoted agent %s: %v", ag.ID, err)
			} else {
				ag.setWatcher(w)
				log.Printf("[PeriodicScan] Promoted agent %s (PID %d) with watcher for session %s", ag.ID, ag.PID, sessionID)
			}
		}
	}
	m.mu.Unlock()
}

func parseEventRowTimestamp(createdAt string) int64 {
	if createdAt == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}
