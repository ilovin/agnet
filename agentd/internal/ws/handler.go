package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/scanner"
)

type handler struct {
	server *Server
	conn   *websocket.Conn
	self   *client
}

func (h *handler) loop() {
	for {
		_, msg, err := h.conn.ReadMessage()
		if err != nil {
			return
		}
		var req RPCRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = h.self.writeJSON(errResp(nil, -32700, "parse error"))
			continue
		}
		resp, afterSend := h.dispatch(req)
		if err := h.self.writeJSON(resp); err != nil {
			log.Printf("ws write: %v", err)
			return
		}
		// Run post-send actions (e.g. broadcast) after the RPC response has
		// been written so the client sees the response before any push events.
		if afterSend != nil {
			afterSend()
		}
	}
}

func (h *handler) dispatch(req RPCRequest) (RPCResponse, func()) {
	switch req.Method {
	case "agent.list":
		return h.agentList(req), nil
	case "agent.create":
		return h.agentCreate(req)
	case "agent.stop":
		return h.agentStop(req)
	case "agent.restart":
		return h.agentRestart(req)
	case "agent.scan":
		return h.agentScan(req), nil
	case "agent.attach":
		return h.agentAttach(req)
	case "session.list":
		return h.sessionList(req), nil
	case "session.create":
		return h.sessionCreate(req)
	case "session.attach":
		return h.sessionAttach(req)
	case "session.catalog":
		return h.sessionCatalog(req), nil
	case "opencode.discover":
		return h.opencodeDiscover(req), nil
	case "conversation.send":
		return h.conversationSend(req), nil
	case "conversation.key":
		return h.conversationKey(req), nil
	case "conversation.history":
		return h.conversationHistory(req), nil
	case "conversation.permission_response":
		return h.conversationPermissionResponse(req), nil
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method), nil
	}
}

func (h *handler) agentList(req RPCRequest) RPCResponse {
	agents := h.server.manager.List()
	type agentInfo struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Provider       string `json:"provider"`
		WorkDir        string `json:"workDir"`
		ProjectName    string `json:"projectName,omitempty"`
		Status         string `json:"status"`
		HasHistory     bool   `json:"hasHistory"`
		AttachMode     string `json:"attachMode,omitempty"`
		ReadOnly       bool   `json:"readOnly"`
		ReadOnlyReason string `json:"readOnlyReason,omitempty"`
	}
	result := make([]agentInfo, 0, len(agents))
	for _, ag := range agents {
		lastSeq, _ := h.server.manager.LastPersistedSeq(ag.ID)
		projectName := ""
		if ag.WorkDir != "" {
			projectName = filepath.Base(strings.TrimRight(ag.WorkDir, "/"))
		}
		result = append(result, agentInfo{
			ID:             ag.ID,
			Name:           ag.Name,
			Provider:       ag.Provider,
			WorkDir:        ag.WorkDir,
			ProjectName:    projectName,
			Status:         string(ag.Status()),
			HasHistory:     lastSeq > 0,
			AttachMode:     ag.AttachMode(),
			ReadOnly:       ag.AttachReadOnly(),
			ReadOnlyReason: ag.AttachReadOnlyReason(),
		})
	}
	return okResp(req.ID, result)
}

func (h *handler) agentCreate(req RPCRequest) (RPCResponse, func()) {
	id, err := h.createAgent(req)
	if err != nil {
		return errResp(req.ID, -32000, err.Error()), nil
	}

	srv := h.server
	conn := h.conn
	return okResp(req.ID, map[string]any{"id": id}), func() {
		srv.broadcast(event("agent.status_changed", map[string]any{
			"agentId": id, "status": "idle",
		}), conn)
	}
}

func (h *handler) createAgent(req RPCRequest) (string, error) {
	name, _ := req.Params["name"].(string)
	provider, _ := req.Params["provider"].(string)
	cmd, _ := req.Params["cmd"].(string)
	workDir, _ := req.Params["workDir"].(string)
	sessionID, _ := req.Params["sessionId"].(string)
	model, _ := req.Params["model"].(string)

	var args []string
	if raw, ok := req.Params["args"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &args)
	}

	provider, cmd, args = resolveLaunch(provider, cmd, args, sessionID, model)

	if workDir == "" {
		workDir = "/tmp"
	}

	id, err := h.server.manager.Create(name, provider, cmd, args, workDir)
	if err != nil {
		return "", err
	}
	if sessionID != "" {
		if err := h.server.manager.UpdateResumeSessionID(id, sessionID); err != nil {
			log.Printf("update resume session id for %s: %v", id, err)
		}
	}
	return id, nil
}

func resolveLaunch(provider, cmd string, args []string, sessionID, model string) (string, string, []string) {
	resolvedProvider := provider
	resolvedCmd := cmd
	resolvedArgs := append([]string{}, args...)

	switch provider {
	case "opencode":
		resolvedCmd = "opencode"
		resolvedArgs = []string{}
		if sessionID != "" {
			resolvedArgs = []string{"-s", sessionID}
		}
	case "claude":
		resolvedCmd = "claude"
		// -p enables non-interactive print mode where --output-format works
		// --output-format stream-json gives us structured events for permission handling
		// --verbose is required for stream-json to work with -p
		// This is the proper non-interactive mode that avoids TUI menus
		resolvedArgs = []string{
			"-p",
			"--permission-mode", "bypassPermissions",
			"--output-format", "stream-json",
			"--include-partial-messages",
			"--verbose",
		}
		if sessionID != "" {
			resolvedArgs = append(resolvedArgs, "--resume", sessionID)
		}
		if model != "" {
			resolvedArgs = append(resolvedArgs, "--model", model)
		}
	default:
		if resolvedCmd == "" {
			resolvedCmd = "claude"
			resolvedArgs = []string{"--permission-mode", "bypassPermissions"}
			if sessionID != "" {
				resolvedArgs = append(resolvedArgs, "--resume", sessionID)
			}
			if model != "" {
				resolvedArgs = append(resolvedArgs, "--model", model)
			}
		}
	}

	return resolvedProvider, resolvedCmd, resolvedArgs
}

func (h *handler) sessionList(req RPCRequest) RPCResponse {
	return h.agentScan(req)
}

func toAnySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if raw, ok := v.([]any); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []any{}
	}
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		return []any{}
	}
	return out
}

func catalogSessionID(v any) string {
	entry, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if sessionID, _ := entry["sessionId"].(string); sessionID != "" {
		return sessionID
	}
	if sessionID, _ := entry["id"].(string); sessionID != "" {
		return sessionID
	}
	sessionFile, _ := entry["sessionFile"].(string)
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

func filterLiveClaudeFileSessions(attachableProcesses, claudeFiles []any) []any {
	liveClaudeSessionIDs := make(map[string]struct{})
	for _, raw := range attachableProcesses {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		provider, _ := entry["provider"].(string)
		if !strings.EqualFold(provider, "claude") {
			continue
		}
		sessionID := catalogSessionID(entry)
		if sessionID == "" {
			continue
		}
		liveClaudeSessionIDs[strings.ToLower(sessionID)] = struct{}{}
	}
	if len(liveClaudeSessionIDs) == 0 {
		return claudeFiles
	}

	filtered := make([]any, 0, len(claudeFiles))
	for _, raw := range claudeFiles {
		sessionID := catalogSessionID(raw)
		if sessionID != "" {
			if _, ok := liveClaudeSessionIDs[strings.ToLower(sessionID)]; ok {
				continue
			}
		}
		filtered = append(filtered, raw)
	}
	return filtered
}

func (h *handler) sessionCatalog(req RPCRequest) RPCResponse {
	type managedAgent struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Provider    string `json:"provider"`
		WorkDir     string `json:"workDir"`
		ProjectName string `json:"projectName,omitempty"`
		Status      string `json:"status"`
	}
	statusPriority := func(status string) int {
		switch status {
		case "working":
			return 0
		case "starting":
			return 1
		case "idle":
			return 2
		case "stopped":
			return 3
		case "crashed":
			return 4
		default:
			return 5
		}
	}
	managedAgents := h.server.manager.List()
	managedByKey := make(map[string]managedAgent)
	for _, ag := range managedAgents {
		projectName := ""
		if ag.WorkDir != "" {
			projectName = filepath.Base(strings.TrimRight(ag.WorkDir, "/"))
		}
		candidate := managedAgent{
			ID: ag.ID, Name: ag.Name, Provider: ag.Provider,
			WorkDir: ag.WorkDir, ProjectName: projectName, Status: string(ag.Status()),
		}
		if candidate.Status == "stopped" || candidate.Status == "crashed" {
			continue
		}
		key := strings.ToLower(candidate.Provider + "|" + candidate.Name)
		existing, ok := managedByKey[key]
		if !ok || statusPriority(candidate.Status) < statusPriority(existing.Status) {
			managedByKey[key] = candidate
		}
	}
	managed := make([]managedAgent, 0, len(managedByKey))
	for _, item := range managedByKey {
		managed = append(managed, item)
	}

	attachableResp := h.agentScan(req)
	if attachableResp.Error != nil {
		return attachableResp
	}

	opencodeResp := h.opencodeDiscover(req)
	if opencodeResp.Error != nil {
		return opencodeResp
	}

	claudeResp := h.claudeDiscover(req)
	if claudeResp.Error != nil {
		return claudeResp
	}

	attachable, _ := attachableResp.Result.(map[string]any)
	opencodeFiles, _ := opencodeResp.Result.(map[string]any)
	claudeFiles, _ := claudeResp.Result.(map[string]any)

	attachableProcesses := toAnySlice(attachable["processes"])
	files := toAnySlice(opencodeFiles["sessions"])
	claude := filterLiveClaudeFileSessions(attachableProcesses, toAnySlice(claudeFiles["sessions"]))

	return okResp(req.ID, map[string]any{
		"managed":       managed,
		"attachable":    attachableProcesses,
		"opencodeFiles": files,
		"claudeFiles":   claude,
	})
}

func (h *handler) sessionCreate(req RPCRequest) (RPCResponse, func()) {
	return h.agentCreate(req)
}

func (h *handler) sessionAttach(req RPCRequest) (RPCResponse, func()) {
	// If agentId is provided for an already managed session, treat attach as
	// selecting that managed agent. Restart the watcher if it isn't running.
	agentID, _ := req.Params["agentId"].(string)
	if agentID != "" {
		if ag := h.server.manager.Get(agentID); ag != nil {
			// If no watcher is running, try to start one
			if ag.Watcher() == nil {
				if err := h.server.manager.StartWatcherForAgent(agentID); err != nil {
					log.Printf("[session.attach] failed to start watcher for %s: %v", agentID, err)
				} else {
					srv := h.server
					conn := h.conn
					return okResp(req.ID, map[string]any{"id": agentID}), func() {
						srv.broadcast(event("agent.status_changed", map[string]any{
							"agentId": agentID, "status": "idle",
						}), conn)
					}
				}
			}
			return okResp(req.ID, map[string]any{"id": agentID}), nil
		}
	}

	sessionID, _ := req.Params["sessionId"].(string)
	if sessionID != "" {
		id, err := h.createAgent(req)
		if err != nil {
			return errResp(req.ID, -32000, err.Error()), nil
		}

		srv := h.server
		conn := h.conn
		return okResp(req.ID, map[string]any{"id": id}), func() {
			srv.broadcast(event("agent.status_changed", map[string]any{
				"agentId": id, "status": "idle",
			}), conn)
		}
	}

	if pid, ok := req.Params["pid"].(float64); ok && int(pid) > 0 {
		return h.agentAttach(req)
	}

	return errResp(req.ID, -32602, "pid, sessionId, or agentId is required"), nil
}

func (h *handler) agentStop(req RPCRequest) (RPCResponse, func()) {
	id, _ := req.Params["agentId"].(string)
	if err := h.server.manager.Stop(id); err != nil {
		return errResp(req.ID, -32000, err.Error()), nil
	}
	srv := h.server
	conn := h.conn
	return okResp(req.ID, map[string]any{"ok": true}), func() {
		srv.broadcast(event("agent.status_changed", map[string]any{
			"agentId": id, "status": "stopped",
		}), conn)
	}
}

func (h *handler) agentRestart(req RPCRequest) (RPCResponse, func()) {
	id, _ := req.Params["agentId"].(string)
	ag := h.server.manager.Get(id)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found"), nil
	}
	if err := h.server.manager.Stop(id); err != nil {
		log.Printf("stop agent %s on restart: %v", id, err)
	}

	_, modelSpecified := req.Params["model"]
	model, _ := req.Params["model"].(string)
	provider := ag.Provider
	cmd := ag.Cmd
	args := ag.Args
	if modelSpecified {
		provider, cmd, args = resolveLaunch(ag.Provider, ag.Cmd, ag.Args, "", model)
	}

	newID, err := h.server.manager.Create(ag.Name, provider, cmd, args, ag.WorkDir)
	if err != nil {
		return errResp(req.ID, -32000, err.Error()), nil
	}
	resp := okResp(req.ID, map[string]any{"id": newID})
	conn := h.conn
	return resp, func() {
		h.server.broadcast(event("agent.status_changed", map[string]any{
			"agentId": newID, "status": "idle",
		}), conn)
	}
}

func (h *handler) conversationSend(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	message, _ := req.Params["message"].(string)
	raw, _ := req.Params["raw"].(bool)
	if message == "" {
		return errResp(req.ID, -32602, "message is required")
	}
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}

	isPipeMode := ag.Provider == "claude" && ag.Process() != nil

	// For Claude in -p mode, restart the process in-place and send prompt via stdin.
	if isPipeMode {
		return h.agentRestartWithMessage(req, ag, message)
	}

	// Record user message to EventBuffer + persistent store BEFORE sending to PTY
	if _, err := h.server.manager.RecordConversationEvent(agentID, map[string]any{
		"role": "user",
		"text": message,
		"raw":  false,
	}); err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	// Broadcast user message to all clients
	h.server.broadcast(event("conversation.message", map[string]any{
		"agentId": agentID,
		"role":    "user",
		"text":    message,
	}), nil)

	input := message
	if !raw {
		input = message + "\n"
	}

	// For PTY-based providers, handle permission prompts
	// If permission prompt is active, resolve it first
	if ag.PermissionPromptActive() {
		log.Printf("[Permission] conversation.send resolving existing prompt for agent %s", agentID)
		if err := ag.WriteInput("\t\r\r"); err != nil {
			return errResp(req.ID, -32000, "resolve permission prompt: "+err.Error())
		}
		ag.SetPermissionPromptActive(false)
		time.Sleep(120 * time.Millisecond)
	}

	if err := ag.WriteInput(input); err != nil {
		msg := err.Error()
		if ag.AttachReadOnly() && ag.AttachReadOnlyReason() != "" {
			msg = "write to agent: attached session is read-only: " + ag.AttachReadOnlyReason()
		} else {
			msg = "write to agent: " + msg
		}
		return errResp(req.ID, -32000, msg)
	}

	// Handle prompt that appears immediately after message send.
	// Poll briefly so prompt detection has time to process subsequent PTY chunks.
	for i := 0; i < 8; i++ {
		time.Sleep(120 * time.Millisecond)
		if !ag.PermissionPromptActive() {
			continue
		}
		log.Printf("[Permission] conversation.send detected prompt after send for agent %s (poll=%d)", agentID, i)
		if err := ag.WriteInput("\t\r\r"); err != nil {
			return errResp(req.ID, -32000, "resolve permission prompt(after send): "+err.Error())
		}
		ag.SetPermissionPromptActive(false)
		time.Sleep(120 * time.Millisecond)
		if err := ag.WriteInput(input); err != nil {
			return errResp(req.ID, -32000, "re-send after permission prompt: "+err.Error())
		}
		log.Printf("[Permission] conversation.send re-sent input after prompt resolve for agent %s", agentID)
		break
	}

	return okResp(req.ID, map[string]any{"ok": true})
}

// agentRestartWithMessage restarts a Claude agent with a new message written to stdin.
// Used for -p mode where Claude exits after each response and reads prompt from stdin.
func (h *handler) agentRestartWithMessage(req RPCRequest, ag *agent.Agent, message string) RPCResponse {
	// Get the stored resume session ID for conversation continuity
	resumeSessionID, _ := h.server.manager.GetResumeSessionID(ag.ID)
	if resumeSessionID != "" {
		log.Printf("[Restart] Resuming session %s for agent %s", resumeSessionID, ag.ID)
	}

	// Build args with resume session ID for conversation continuity
	provider, cmd, args := resolveLaunch(ag.Provider, ag.Cmd, ag.Args, resumeSessionID, "")

	if err := h.server.manager.RestartInPlace(ag.ID, provider, cmd, args); err != nil {
		return errResp(req.ID, -32000, "restart with message: "+err.Error())
	}

	// Write message to the restarted agent's stdin
	restarted := h.server.manager.Get(ag.ID)
	if restarted == nil || restarted.Process() == nil {
		return errResp(req.ID, -32000, "restart with message: agent process not ready")
	}
	if err := restarted.WriteInput(message + "\n"); err != nil {
		return errResp(req.ID, -32000, "write to restarted agent: "+err.Error())
	}
	if proc := restarted.Process(); proc != nil {
		proc.CloseStdin()
	}

	// Record user message first, then broadcast for deterministic history/view sync.
	if _, err := h.server.manager.RecordConversationEvent(ag.ID, map[string]any{
		"role": "user",
		"text": message,
		"raw":  false,
	}); err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	h.server.broadcast(event("conversation.message", map[string]any{
		"agentId": ag.ID,
		"role":    "user",
		"text":    message,
	}), nil)

	h.server.broadcast(event("agent.status_changed", map[string]any{
		"agentId": ag.ID,
		"status":  "working",
	}), nil)

	return okResp(req.ID, map[string]any{"id": ag.ID})
}

func keyToBytes(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "up":
		return "\x1b[A", true
	case "down":
		return "\x1b[B", true
	case "right":
		return "\x1b[C", true
	case "left":
		return "\x1b[D", true
	case "enter":
		return "\r", true
	case "esc":
		return "\x1b", true
	case "tab":
		return "\t", true
	case "backspace":
		return "\x7f", true
	default:
		return "", false
	}
}

func (h *handler) conversationKey(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	key, _ := req.Params["key"].(string)
	if key == "" {
		return errResp(req.ID, -32602, "key is required")
	}
	seq, ok := keyToBytes(key)
	if !ok {
		return errResp(req.ID, -32602, "unsupported key: "+key)
	}

	repeat := 1
	if v, ok := req.Params["repeat"].(float64); ok {
		repeat = int(v)
	}
	if repeat < 1 {
		repeat = 1
	}
	if repeat > 32 {
		repeat = 32
	}

	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}

	if err := ag.WriteInput(strings.Repeat(seq, repeat)); err != nil {
		return errResp(req.ID, -32000, "write key to agent: "+err.Error())
	}
	// If user pressed Enter manually, treat this as resolving permission prompt.
	if strings.ToLower(key) == "enter" {
		ag.SetPermissionPromptActive(false)
	}
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) conversationHistory(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	var afterSeq uint64
	if v, ok := req.Params["cursor"].(float64); ok {
		afterSeq = uint64(v)
	}
	limit := 200
	if v, ok := req.Params["limit"].(float64); ok {
		if iv := int(v); iv > 0 {
			limit = iv
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	var beforeSeq uint64
	if v, ok := req.Params["before"].(float64); ok {
		beforeSeq = uint64(v)
	}

	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}

	// Get conversation events
	var events []eventbuf.Event
	if beforeSeq > 0 {
		if persisted, err := h.server.manager.LoadPersistedEventsBefore(agentID, beforeSeq, limit); err == nil {
			events = persisted
		}
	} else if afterSeq > 0 {
		events = ag.EventBuf().Since(afterSeq)
		if len(events) == 0 {
			if persisted, err := h.server.manager.LoadPersistedEventsSince(agentID, afterSeq, limit); err == nil {
				events = persisted
			}
		}
	} else {
		live := ag.EventBuf().Since(0)
		if len(live) > 0 {
			if len(live) > limit {
				events = live[len(live)-limit:]
			} else {
				events = live
			}
		} else {
			if persisted, err := h.server.manager.LoadPersistedEventsLatest(agentID, limit); err == nil {
				events = persisted
			}
		}
	}

	// Get pending permission requests
	pendingPerms := ag.PermissionManager().GetPendingRequests()
	var permissionRequests []map[string]any
	for _, perm := range pendingPerms {
		permissionRequests = append(permissionRequests, map[string]any{
			"request_id":             perm.RequestID,
			"tool_name":              perm.ToolName,
			"display_name":           perm.DisplayName,
			"title":                  perm.Title,
			"description":            perm.Description,
			"input":                  perm.Input,
			"permission_suggestions": perm.PermissionSuggestions,
			"tool_use_id":            perm.ToolUseID,
			"agent_id":               perm.AgentID,
			"blocked_path":           perm.BlockedPath,
			"decision_reason":        perm.DecisionReason,
			"ai_validation":          perm.AIValidation,
			"timestamp":              perm.Timestamp,
		})
	}

	lastSeq := ag.EventBuf().LastSeq()
	if persistedLast, err := h.server.manager.LastPersistedSeq(agentID); err == nil && persistedLast > lastSeq {
		lastSeq = persistedLast
	}

	// Flatten events: {seq, data: {role, text}} -> {seq, role, text}
	flattened := make([]map[string]any, 0, len(events))
	for _, e := range events {
		flat := map[string]any{
			"seq": e.Seq,
		}
		for k, v := range e.Data {
			flat[k] = v
		}
		flattened = append(flattened, flat)
	}

	var firstSeq uint64
	if len(events) > 0 {
		firstSeq = events[0].Seq
	}

	return okResp(req.ID, map[string]any{
		"events":             flattened,
		"lastSeq":            lastSeq,
		"firstSeq":           firstSeq,
		"permissionRequests": permissionRequests,
	})
}

func (h *handler) conversationPermissionResponse(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	requestID, _ := req.Params["requestId"].(string)
	behavior, _ := req.Params["behavior"].(string) // "allow" or "deny"

	if requestID == "" {
		return errResp(req.ID, -32602, "requestId is required")
	}
	if behavior != "allow" && behavior != "deny" {
		return errResp(req.ID, -32602, "behavior must be 'allow' or 'deny'")
	}

	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}

	// Build permission response
	resp := &agent.PermissionResponse{
		RequestID: requestID,
		Behavior:  behavior,
		Message:   "",
	}

	if msg, ok := req.Params["message"].(string); ok {
		resp.Message = msg
	}

	if updatedInput, ok := req.Params["updatedInput"].(map[string]any); ok {
		resp.UpdatedInput = updatedInput
	}

	// Handle the response
	handled := ag.PermissionManager().HandleResponse(resp)
	if !handled {
		return errResp(req.ID, -32000, "permission request not found or already handled")
	}

	// Send control response to Claude
	// This mimics the Companion protocol
	var responseData map[string]any
	if behavior == "allow" {
		responseData = map[string]any{
			"behavior":           "allow",
			"updatedInput":       resp.UpdatedInput,
			"updatedPermissions": resp.UpdatedPermissions,
		}
	} else {
		responseData = map[string]any{
			"behavior": "deny",
			"message":  resp.Message,
		}
	}

	controlResp := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   responseData,
		},
	}

	// Try to send to Claude process via stdin as NDJSON
	if ag.Process() != nil {
		ndjson, _ := json.Marshal(controlResp)
		ndjson = append(ndjson, '\n')
		if _, err := ag.Process().Write(ndjson); err != nil {
			log.Printf("[Permission] Failed to send response to Claude: %v", err)
		}
	}

	// Broadcast permission resolved event
	h.server.broadcast(event("permission.resolved", map[string]any{
		"agentId":   agentID,
		"requestId": requestID,
		"behavior":  behavior,
	}), nil)

	return okResp(req.ID, map[string]any{"ok": true})
}

// opencodeDiscover scans for available opencode sessions on the machine.
func (h *handler) opencodeDiscover(req RPCRequest) RPCResponse {
	home, err := os.UserHomeDir()
	if err != nil {
		return errResp(req.ID, -32000, "cannot get home dir: "+err.Error())
	}

	type sessionInfo struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		ModifiedAt time.Time `json:"modifiedAt"`
		Size       int64     `json:"size"`
	}

	sessionDirs := []string{
		filepath.Join(home, ".local", "share", "opencode", "storage", "session_diff"),
		filepath.Join(home, "Library", "Application Support", "opencode", "storage", "session_diff"),
		filepath.Join(home, "Library", "Application Support", "OpenCode", "storage", "session_diff"),
	}

	sessionsByID := make(map[string]sessionInfo)
	for _, sessionDir := range sessionDirs {
		entries, err := os.ReadDir(sessionDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			sessionID := strings.TrimSuffix(name, ".json")
			if !strings.HasPrefix(sessionID, "ses_") {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			candidate := sessionInfo{
				ID:         sessionID,
				Name:       sessionID,
				ModifiedAt: info.ModTime(),
				Size:       info.Size(),
			}
			if existing, ok := sessionsByID[sessionID]; !ok || candidate.ModifiedAt.After(existing.ModifiedAt) {
				sessionsByID[sessionID] = candidate
			}
		}
	}

	sessions := make([]sessionInfo, 0, len(sessionsByID))
	for _, s := range sessionsByID {
		sessions = append(sessions, s)
	}

	return okResp(req.ID, map[string]any{
		"sessions": sessions,
	})
}

func (h *handler) claudeDiscover(req RPCRequest) RPCResponse {
	home, err := os.UserHomeDir()
	if err != nil {
		return errResp(req.ID, -32000, "cannot get home dir: "+err.Error())
	}

	type sessionInfo struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		WorkDir     string    `json:"workDir"`
		SessionFile string    `json:"sessionFile"`
		ModifiedAt  time.Time `json:"modifiedAt"`
		Size        int64     `json:"size"`
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	projectEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return okResp(req.ID, map[string]any{"sessions": []sessionInfo{}})
	}

	sessionsByID := make(map[string]sessionInfo)
	for _, project := range projectEntries {
		if !project.IsDir() {
			continue
		}
		projectPath := filepath.Join(projectsDir, project.Name())
		entries, err := os.ReadDir(projectPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			sessionID := strings.TrimSuffix(entry.Name(), ".jsonl")
			if sessionID == "" {
				continue
			}
			candidate := sessionInfo{
				ID:          sessionID,
				Name:        sessionID,
				WorkDir:     project.Name(),
				SessionFile: filepath.Join(projectPath, entry.Name()),
				ModifiedAt:  info.ModTime(),
				Size:        info.Size(),
			}
			if existing, ok := sessionsByID[sessionID]; !ok || candidate.ModifiedAt.After(existing.ModifiedAt) {
				sessionsByID[sessionID] = candidate
			}
		}
	}

	sessions := make([]sessionInfo, 0, len(sessionsByID))
	for _, s := range sessionsByID {
		sessions = append(sessions, s)
	}

	return okResp(req.ID, map[string]any{"sessions": sessions})
}

// agentScan discovers all existing Claude/OpenCode processes on the system.
func (h *handler) agentScan(req RPCRequest) RPCResponse {
	processes, err := h.server.manager.ScanExisting()
	if err != nil {
		return errResp(req.ID, -32000, "scan failed: "+err.Error())
	}

	type processInfo struct {
		PID            int      `json:"pid"`
		Provider       string   `json:"provider"`
		WorkDir        string   `json:"workDir"`
		ProjectName    string   `json:"projectName,omitempty"`
		Args           []string `json:"args"`
		Session        string   `json:"session,omitempty"`
		SessionID      string   `json:"sessionId,omitempty"`
		Terminal       string   `json:"terminal,omitempty"`
		SessionFile    string   `json:"sessionFile,omitempty"`
		AttachMode     string   `json:"attachMode,omitempty"`
		ReadOnly       bool     `json:"readOnly"`
		ReadOnlyReason string   `json:"readOnlyReason,omitempty"`
	}

	result := make([]processInfo, 0, len(processes))
	for _, p := range processes {
		projectName := ""
		if p.WorkDir != "" {
			projectName = filepath.Base(strings.TrimRight(p.WorkDir, "/"))
		}
		result = append(result, processInfo{
			PID:            p.PID,
			Provider:       p.Provider,
			WorkDir:        p.WorkDir,
			ProjectName:    projectName,
			Args:           p.Args,
			Session:        p.Session,
			SessionID:      p.SessionID,
			Terminal:       p.Terminal,
			SessionFile:    p.FindSessionFile(),
			AttachMode:     p.AttachMode(),
			ReadOnly:       p.AttachReadOnly(),
			ReadOnlyReason: p.AttachReadOnlyReason(),
		})
	}

	return okResp(req.ID, map[string]any{
		"processes": result,
		"count":     len(result),
	})
}

// agentAttach takes over an existing process and converts it to a managed agent.
func (h *handler) agentAttach(req RPCRequest) (RPCResponse, func()) {
	pidFloat, _ := req.Params["pid"].(float64)
	pid := int(pidFloat)
	if pid <= 0 {
		return errResp(req.ID, -32602, "pid is required"), nil
	}

	// First scan to find the process
	processes, err := h.server.manager.ScanExisting()
	if err != nil {
		return errResp(req.ID, -32000, "scan failed: "+err.Error()), nil
	}

	var target *scanner.ProcessInfo
	for i := range processes {
		if processes[i].PID == pid {
			target = &processes[i]
			break
		}
	}

	if target == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("process %d not found or not a claude/opencode process", pid)), nil
	}

	// Attach to the process
	agent, err := h.server.manager.Attach(*target)
	if err != nil {
		return errResp(req.ID, -32000, "attach failed: "+err.Error()), nil
	}

	srv := h.server
	conn := h.conn
	return okResp(req.ID, map[string]any{
			"id":             agent.ID,
			"name":           agent.Name,
			"provider":       agent.Provider,
			"status":         string(agent.Status()),
			"attachMode":     agent.AttachMode(),
			"readOnly":       agent.AttachReadOnly(),
			"readOnlyReason": agent.AttachReadOnlyReason(),
		}), func() {
			srv.broadcast(event("agent.status_changed", map[string]any{
				"agentId": agent.ID,
				"status":  agent.Status(),
			}), conn)
		}
}
