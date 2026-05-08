package ws

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

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

type providerSnapshot struct {
	CurrentProviderID      string
	RuntimeProviderID      string
	ProviderState          string
	ProviderStateReason    string
	ProviderScope          string
	ProviderWriteMode      string
	ProviderReadOnlyReason string
}

func (h *handler) loop() {
	// Generate a unique client ID for this connection
	clientID := generateClientID()
	defer func() {
		// Log and broadcast disconnect event when connection closes
		log.Printf("[Handler] connection closed: %s", clientID)
		h.server.broadcast(event("client.disconnected", map[string]any{
			"clientId": clientID,
			"time":     time.Now().Unix(),
		}), h.conn)
	}()
	// Broadcast connect event
	h.server.broadcast(event("client.connected", map[string]any{
		"clientId": clientID,
		"time":     time.Now().Unix(),
	}), h.conn)

	for {
		_, msg, err := h.conn.ReadMessage()
		if err != nil {
			log.Printf("[Handler] connection closed: %v", err)
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
	case "conversation.image":
		return h.conversationImage(req), nil
	case "conversation.permission_response":
		return h.conversationPermissionResponse(req), nil
	case "conversation.clear":
		return h.conversationClear(req), nil
	case "agent.rename":
		return h.agentRename(req), nil
	case "agent.remove":
		return h.agentRemove(req), nil
	case "provider.list":
		return h.providerList(req), nil
	case "provider.switch":
		return h.providerSwitch(req)
	case "provider.add":
		return h.providerAdd(req), nil
	case "opencode.models":
		return h.opencodeModels(req), nil
	case "system.info":
		return h.systemInfo(req), nil
	case "system.skills":
		return h.systemSkills(req), nil
	case "rpc.ping":
		return okResp(req.ID, map[string]any{"ok": true, "time": time.Now().Unix()}), nil
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method), nil
	}
}

// isSubAgentSession reports whether the given resume session ID belongs to a
// Claude Code team-mode sub-agent. Sub-agent sessions are stored in
// subagents/ directories under ~/.claude/projects/, distinct from main
// agent sessions which live at the project root.
func isSubAgentSession(workDir, resumeID string) bool {
	if workDir == "" || resumeID == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	subAgentPath := filepath.Join(projectDir, "subagents", resumeID+".jsonl")
	_, err = os.Stat(subAgentPath)
	return err == nil
}

// projectDirName mirrors Claude's project directory naming: replace / . _ with -.
func projectDirName(workDir string) string {
	s := strings.ReplaceAll(strings.TrimRight(workDir, "/"), "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func (h *handler) agentList(req RPCRequest) RPCResponse {
	agents := h.server.manager.List()
	type agentInfo struct {
		ID                     string `json:"id"`
		Name                   string `json:"name"`
		Provider               string `json:"provider"`
		WorkDir                string `json:"workDir"`
		ProjectName            string `json:"projectName,omitempty"`
		Status                 string `json:"status"`
		PID                    int    `json:"pid,omitempty"`
		HasHistory             bool   `json:"hasHistory"`
		AttachMode             string `json:"attachMode,omitempty"`
		ReadOnly               bool   `json:"readOnly"`
		ReadOnlyReason         string `json:"readOnlyReason,omitempty"`
		SessionID              string `json:"sessionId,omitempty"`
		RuntimeState           string `json:"runtimeState,omitempty"`
		SessionState           string `json:"sessionState,omitempty"`
		SessionStateReason     string `json:"sessionStateReason,omitempty"`
		SessionControl         string `json:"sessionControl,omitempty"`
		ProviderState          string `json:"providerState,omitempty"`
		ProviderScope          string `json:"providerScope,omitempty"`
		ProviderWriteMode      string `json:"providerWriteMode,omitempty"`
		ProviderReadOnlyReason string `json:"providerReadOnlyReason,omitempty"`
		PermissionMode         string `json:"permissionMode,omitempty"`
		LastMessageTime        int64  `json:"lastMessageTime,omitempty"`
	}
	result := make([]agentInfo, 0, len(agents))
	for _, ag := range agents {
		lastSeq, _ := h.server.manager.LastPersistedSeq(ag.ID)
		projectName := ""
		if ag.WorkDir != "" {
			projectName = filepath.Base(strings.TrimRight(ag.WorkDir, "/"))
		}
		resumeID, _ := h.server.manager.GetResumeSessionID(ag.ID)
		if isSubAgentSession(ag.WorkDir, resumeID) {
			continue
		}
		derived := h.server.manager.DeriveAgentState(ag.ID)
		provider := h.deriveProviderSnapshot(ag)
		var lastMsgTimeMs int64
		if t, err := h.server.manager.LastConversationEventTime(ag.ID); err == nil && !t.IsZero() {
			lastMsgTimeMs = t.UnixMilli()
		}
		result = append(result, agentInfo{
			ID:                     ag.ID,
			Name:                   ag.Name,
			Provider:               ag.Provider,
			WorkDir:                ag.WorkDir,
			ProjectName:            projectName,
			Status:                 string(ag.Status()),
			PID:                    ag.PID,
			HasHistory:             lastSeq > 0,
			AttachMode:             ag.AttachMode(),
			ReadOnly:               ag.AttachReadOnly(),
			ReadOnlyReason:         ag.AttachReadOnlyReason(),
			SessionID:              resumeID,
			RuntimeState:           derived.RuntimeState,
			SessionState:           derived.SessionState,
			SessionStateReason:     derived.SessionStateReason,
			SessionControl:         derived.SessionControl,
			ProviderState:          provider.ProviderState,
			ProviderScope:          provider.ProviderScope,
			ProviderWriteMode:      provider.ProviderWriteMode,
			ProviderReadOnlyReason: provider.ProviderReadOnlyReason,
			PermissionMode:         currentPermissionMode(ag.Args),
			LastMessageTime:        lastMsgTimeMs,
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
		srv.broadcast(event("agent.status_changed", h.statusChangedParams(id, "idle")), conn)
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

	provider, cmd, args, env := resolveLaunch(provider, cmd, args, sessionID, model, "")

	if workDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			workDir = "/tmp"
		} else {
			workDir = home
		}
	}

	id, err := h.server.manager.Create(name, provider, cmd, args, workDir, env)
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

// findExecutable searches for a binary in PATH and common user-local locations.
// When agentd runs as root, user-installed binaries (e.g. ~/.opencode/bin/opencode)
// are not in root's PATH, so we check /home/*/ directories as well.
func findExecutable(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	// Scan /home/*/ for common install locations
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			candidates := []string{
				filepath.Join("/home", e.Name(), ".local", "bin", name),
				filepath.Join("/home", e.Name(), "."+name, "bin", name),
				filepath.Join("/home", e.Name(), "bin", name),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					return c
				}
			}
		}
	}
	return name // fallback to bare name
}

// currentPermissionMode extracts the --permission-mode value from args.
func currentPermissionMode(args []string) string {
	for i, a := range args {
		if a == "--permission-mode" && i+1 < len(args) {
			return args[i+1]
		}
		if a == "--dangerously-skip-permissions" {
			return "bypassPermissions"
		}
	}
	return ""
}

// currentOpenCodeModel extracts the -m value from opencode args.
func currentOpenCodeModel(args []string) string {
	for i, a := range args {
		if a == "-m" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func resolveLaunch(provider, cmd string, args []string, sessionID, model, permissionMode string) (string, string, []string, []string) {
	resolvedProvider := provider
	resolvedCmd := cmd
	resolvedArgs := append([]string{}, args...)
	var env []string

	switch provider {
	case "opencode":
		resolvedCmd = findExecutable("opencode")
		resolvedArgs = []string{}
		if sessionID != "" {
			resolvedArgs = []string{"-s", sessionID}
		}
		if model != "" {
			resolvedArgs = append(resolvedArgs, "-m", model)
		}
	case "claude", "claude-bedrock", "claude-vertex":
		resolvedCmd = findExecutable("claude")
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
		if permissionMode != "" {
			resolvedArgs[2] = permissionMode
		}
		if sessionID != "" {
			resolvedArgs = append(resolvedArgs, "--resume", sessionID)
		}
		if model != "" {
			resolvedArgs = append(resolvedArgs, "--model", model)
		}
		// Provider-specific environment variables
		switch provider {
		case "claude-bedrock":
			env = append(env, "CLAUDE_CODE_USE_BEDROCK=1")
			resolvedProvider = "claude"
		case "claude-vertex":
			env = append(env, "CLAUDE_CODE_USE_VERTEX=1")
			resolvedProvider = "claude"
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

	return resolvedProvider, resolvedCmd, resolvedArgs, env
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
		PID         int    `json:"pid,omitempty"`
		SessionID   string `json:"sessionId,omitempty"`
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
		resumeID, _ := h.server.manager.GetResumeSessionID(ag.ID)
		if isSubAgentSession(ag.WorkDir, resumeID) {
			continue
		}
		candidate := managedAgent{
			ID: ag.ID, Name: ag.Name, Provider: ag.Provider,
			WorkDir: ag.WorkDir, ProjectName: projectName, Status: string(ag.Status()),
			PID: ag.PID, SessionID: resumeID,
		}
		// Skip crashed and stopped agents.
		// Active processes show in "attachable"; resumable sessions in file lists.
		if candidate.Status == "crashed" || candidate.Status == "stopped" {
			continue
		}
		// Prefer PID for live managed agents so multiple processes sharing one
		// session remain independently visible.
		key := strings.ToLower(candidate.Provider + "|agent|" + candidate.ID)
		switch {
		case candidate.PID > 0:
			key = strings.ToLower(fmt.Sprintf("%s|pid|%d", candidate.Provider, candidate.PID))
		case candidate.SessionID != "":
			key = strings.ToLower(candidate.Provider + "|session|" + candidate.SessionID)
		default:
			key = strings.ToLower(candidate.Provider + "|agent|" + candidate.ID)
		}
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
	filteredAttachable := make([]any, 0, len(attachableProcesses))
	for _, raw := range attachableProcesses {
		_, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		filteredAttachable = append(filteredAttachable, raw)
	}
	files := toAnySlice(opencodeFiles["sessions"])
	claude := filterLiveClaudeFileSessions(filteredAttachable, toAnySlice(claudeFiles["sessions"]))

	return okResp(req.ID, map[string]any{
		"managed":       managed,
		"attachable":    filteredAttachable,
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
						srv.broadcast(event("agent.status_changed", h.statusChangedParams(agentID, "idle")), conn)
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
			srv.broadcast(event("agent.status_changed", h.statusChangedParams(id, "idle")), conn)
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
		srv.broadcast(event("agent.status_changed", h.statusChangedParams(id, "stopped")), conn)
	}
}

func (h *handler) agentRestart(req RPCRequest) (RPCResponse, func()) {
	id, _ := req.Params["agentId"].(string)
	ag := h.server.manager.Get(id)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found"), nil
	}
	_, modelSpecified := req.Params["model"]
	_, providerSpecified := req.Params["provider"]
	permissionMode, _ := req.Params["permissionMode"].(string)
	model, _ := req.Params["model"].(string)
	apiProvider, _ := req.Params["provider"].(string)

	// For attached agents with a model change, update settings.json without
	// restarting the process. Claude reads settings dynamically.
	if ag.Provider == "claude" && ag.AttachMode() != "" && modelSpecified && !providerSpecified && permissionMode == "" {
		settingsPath := findClaudeSettings()
		if settingsPath == "" {
			return errResp(req.ID, -32000, "settings.json not found"), nil
		}
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			return errResp(req.ID, -32000, "read settings: "+err.Error()), nil
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			return errResp(req.ID, -32000, "parse settings: "+err.Error()), nil
		}
		settings["model"] = model
		if envMap, ok := settings["env"].(map[string]any); ok {
			envMap["ANTHROPIC_MODEL"] = model
		}
		newData, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return errResp(req.ID, -32000, "marshal settings: "+err.Error()), nil
		}
		if err := os.WriteFile(settingsPath, newData, 0644); err != nil {
			return errResp(req.ID, -32000, "write settings: "+err.Error()), nil
		}
		log.Printf("[Restart] Updated model to %q in settings.json for attached agent %s", model, id)
		return okResp(req.ID, map[string]any{"id": id}), nil
	}

	// OpenCode agents switch modes via Tab key (plan ↔ build).
	if ag.Provider == "opencode" && permissionMode != "" && !modelSpecified && !providerSpecified {
		if err := ag.WriteInput("\t"); err != nil {
			return errResp(req.ID, -32000, "write tab: "+err.Error()), nil
		}
		return okResp(req.ID, map[string]any{"id": id}), nil
	}

	if ag.Provider != "opencode" && ag.AttachMode() != "" && ag.AttachMode() != "tmux" {
		reason := ag.AttachReadOnlyReason()
		if reason == "" {
			reason = "attached session is read-only"
		}
		return errResp(req.ID, -32000, reason), nil
	}

	// For tmux-attached agents, switch mode via Shift+Tab (no restart needed).
	// Only applies when ONLY permissionMode is specified (no model/provider change).
	if ag.AttachMode() == "tmux" && permissionMode != "" && !modelSpecified && !providerSpecified {
		// Shift+Tab cycles modes in Claude TUI: bypassPermissions → plan → auto → ...
		if err := ag.WriteInput("\x1b[Z"); err != nil {
			return errResp(req.ID, -32000, "write shift-tab: "+err.Error()), nil
		}
		return okResp(req.ID, map[string]any{"id": id}), nil
	}

	provider := ag.Provider
	cmd := ag.Cmd
	args := ag.Args
	var env []string

	if modelSpecified || providerSpecified || permissionMode != "" {
		// Use the requested provider if specified, otherwise keep existing
		launchProvider := ag.Provider
		if providerSpecified && apiProvider != "" {
			launchProvider = apiProvider
			provider = apiProvider
		}
		// Preserve conversation via --resume
		resumeSessionID, _ := h.server.manager.GetResumeSessionID(id)
		provider, cmd, args, env = resolveLaunch(launchProvider, ag.Cmd, ag.Args, resumeSessionID, model, permissionMode)
	}

	// Restart in-place to keep the same agent ID and conversation history
	if err := h.server.manager.RestartInPlace(id, provider, cmd, args, env); err != nil {
		return errResp(req.ID, -32000, err.Error()), nil
	}

	// Save provider to store if it was explicitly specified
	if providerSpecified {
		if err := h.server.manager.UpdateAgentProvider(id, provider); err != nil {
			log.Printf("[Handler] update provider in store: %v", err)
		}
	}

	resp := okResp(req.ID, map[string]any{"id": id})
	conn := h.conn
	return resp, func() {
		h.server.broadcast(event("agent.status_changed", h.statusChangedParams(id, "idle")), conn)
	}
}

func (h *handler) conversationSend(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	message, _ := req.Params["message"].(string)
	raw, _ := req.Params["raw"].(bool)
	if message == "" {
		return errResp(req.ID, -32602, "message is required")
	}

	// Extract image attachments (base64-encoded)
	var imageFiles []string // temp file paths to pass via --file
	var imagePaths []string // persisted paths stored in event history
	if rawImages, ok := req.Params["images"]; ok {
		if images, ok := rawImages.([]any); ok {
			dataDir := h.server.manager.DataDir()
			imgDir := filepath.Join(dataDir, "images", agentID, fmt.Sprintf("%d", time.Now().Unix()))
			for i, img := range images {
				imgMap, ok := img.(map[string]any)
				if !ok {
					continue
				}
				data, _ := imgMap["data"].(string)
				mimeType, _ := imgMap["mimeType"].(string)
				if data == "" {
					continue
				}
				ext := ".png"
				switch mimeType {
				case "image/jpeg":
					ext = ".jpg"
				case "image/gif":
					ext = ".gif"
				case "image/webp":
					ext = ".webp"
				}
				decoded, err := base64Decode(data)
				if err != nil {
					log.Printf("[conversationSend] decode image %d: %v", i, err)
					continue
				}
				// Persist to dataDir so history can retrieve it later
				persistedFile := filepath.Join(imgDir, fmt.Sprintf("img%d%s", i, ext))
				if err := os.MkdirAll(filepath.Dir(persistedFile), 0755); err == nil {
					if err := os.WriteFile(persistedFile, decoded, 0644); err == nil {
						imagePaths = append(imagePaths, persistedFile)
					} else {
						log.Printf("[conversationSend] persist image %d: %v", i, err)
					}
				}
				// Also write a temp file for --file CLI argument
				tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("agentd-img-%s-%d%s", agentID[:8], i, ext))
				if err := os.WriteFile(tmpFile, decoded, 0644); err != nil {
					log.Printf("[conversationSend] write temp image %d: %v", i, err)
					continue
				}
				imageFiles = append(imageFiles, tmpFile)
			}
		}
	}

	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}

	attachMode := ag.AttachMode()
	isTmuxAttached := attachMode == "tmux"
	isOpenCode := ag.Provider == "opencode"
	// Guard against Restart/Start on attached non-tmux agents (read-only watcher attach).
	// OpenCode agents are exempt: openCodeSendWithResume uses `opencode run --session`,
	// not PTY input, so the read-only watcher attach does not apply.
	if attachMode != "" && !isTmuxAttached && !isOpenCode {
		reason := ag.AttachReadOnlyReason()
		if reason == "" {
			reason = "attached session is read-only"
		}
		return errResp(req.ID, -32000, reason)
	}
	isPipeMode := ag.Provider == "claude" && ag.Process() != nil && !isTmuxAttached
	isFreshClaude := ag.Provider == "claude" && ag.Process() == nil && !isTmuxAttached

	// For tmux-attached sessions, write directly to PTY via tmux send-keys.
	if isTmuxAttached {
		// tmux-attached interactive sessions cannot receive --file flags dynamically;
		// images would be recorded in history but never passed to the CLI process.
		if len(imageFiles) > 0 {
			return errResp(req.ID, -32000, "tmux-attached sessions do not support image attachments")
		}
		// Same as the generic PTY path below, but skip the opencode/fresh checks
		eventData := map[string]any{
			"role":       "user",
			"text":       message,
			"raw":        false,
			"imageCount": len(imageFiles),
		}
		if len(imagePaths) > 0 {
			eventData["images"] = imagePaths
		}
		if _, err := h.server.manager.RecordConversationEvent(agentID, eventData); err != nil {
			return errResp(req.ID, -32000, "record user message: "+err.Error())
		}
		h.server.broadcast(event("conversation.message", map[string]any{
			"agentId":    agentID,
			"role":       "user",
			"text":       message,
			"imageCount": len(imageFiles),
			"images":     imagePaths,
		}), nil)
		input := message
		if !raw {
			input = message + "\n"
		}
		if err := ag.WriteInput(input); err != nil {
			return errResp(req.ID, -32000, "write to tmux agent: "+err.Error())
		}
		// For tmux-attached OpenCode agents, start pane capture to read responses.
		// Claude tmux agents are handled by the existing ClaudeWatcher.
		if isOpenCode {
			go h.captureOpenCodeTmuxResponses(ag, message)
		}
		return okResp(req.ID, map[string]any{"id": agentID})
	}

	// For Claude in -p mode with existing process, restart the process in-place and send prompt via stdin.
	if isPipeMode {
		return h.agentRestartWithMessage(req, ag, message, imageFiles, imagePaths)
	}

	// For fresh Claude agent (no process yet), start with message
	if isFreshClaude {
		return h.agentStartWithMessage(req, ag, message, imageFiles, imagePaths)
	}

	// For OpenCode attached sessions, use resume mode
	if isOpenCode {
		// OpenCode resume mode does not support image attachments.
		if len(imageFiles) > 0 {
			return errResp(req.ID, -32000, "opencode sessions do not support image attachments")
		}
		return h.openCodeSendWithResume(req, ag, message)
	}

	// Record user message to EventBuffer + persistent store BEFORE sending to PTY
	ptyEventData := map[string]any{
		"role":       "user",
		"text":       message,
		"raw":        false,
		"imageCount": len(imageFiles),
	}
	if len(imagePaths) > 0 {
		ptyEventData["images"] = imagePaths
	}
	if _, err := h.server.manager.RecordConversationEvent(agentID, ptyEventData); err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	// Broadcast user message to all clients
	broadcastData := map[string]any{
		"agentId":    agentID,
		"role":       "user",
		"text":       message,
		"imageCount": len(imageFiles),
		"timestamp":  time.Now().UnixMilli(),
	}
	if len(imagePaths) > 0 {
		broadcastData["images"] = imagePaths
	}
	h.server.broadcast(event("conversation.message", broadcastData), nil)

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
func (h *handler) agentRestartWithMessage(req RPCRequest, ag *agent.Agent, message string, imageFiles []string, imagePaths []string) RPCResponse {
	if ag.AttachMode() != "" && ag.AttachMode() != "tmux" {
		reason := ag.AttachReadOnlyReason()
		if reason == "" {
			reason = "attached session is read-only"
		}
		return errResp(req.ID, -32000, reason)
	}
	// Get the stored resume session ID for conversation continuity
	resumeSessionID, _ := h.server.manager.GetResumeSessionID(ag.ID)
	if resumeSessionID != "" {
		log.Printf("[Restart] Resuming session %s for agent %s", resumeSessionID, ag.ID)
	}

	// Build args with resume session ID for conversation continuity.
	// Preserve the current permission mode from agent's existing args.
	mode := currentPermissionMode(ag.Args)
	provider, cmd, args, env := resolveLaunch(ag.Provider, ag.Cmd, ag.Args, resumeSessionID, "", mode)

	// Append --file flags for image attachments
	for _, f := range imageFiles {
		args = append(args, "--file", f)
	}

	if err := h.server.manager.RestartInPlace(ag.ID, provider, cmd, args, env); err != nil {
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
	historyData := map[string]any{
		"role":       "user",
		"text":       message,
		"raw":        false,
		"imageCount": len(imageFiles),
	}
	if len(imagePaths) > 0 {
		historyData["images"] = imagePaths
	}
	if _, err := h.server.manager.RecordConversationEvent(ag.ID, historyData); err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	broadcastData := map[string]any{
		"agentId":    ag.ID,
		"role":       "user",
		"text":       message,
		"imageCount": len(imageFiles),
		"timestamp":  time.Now().UnixMilli(),
	}
	if len(imagePaths) > 0 {
		broadcastData["images"] = imagePaths
	}
	h.server.broadcast(event("conversation.message", broadcastData), nil)

	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "working")), nil)

	return okResp(req.ID, map[string]any{"id": ag.ID})
}

// agentStartWithMessage starts a fresh Claude agent with the first message.
// Used when a Claude agent was created without starting a process.
func (h *handler) agentStartWithMessage(req RPCRequest, ag *agent.Agent, message string, imageFiles []string, imagePaths []string) RPCResponse {
	if ag.AttachMode() != "" && ag.AttachMode() != "tmux" {
		reason := ag.AttachReadOnlyReason()
		if reason == "" {
			reason = "attached session is read-only"
		}
		return errResp(req.ID, -32000, reason)
	}
	log.Printf("[Start] Starting fresh Claude agent %s with message", ag.ID)

	// Get the stored resume session ID if any
	resumeSessionID, _ := h.server.manager.GetResumeSessionID(ag.ID)

	// Build args with the message as input.
	// Preserve the current permission mode from agent's existing args.
	mode := currentPermissionMode(ag.Args)
	provider, cmd, args, env := resolveLaunch(ag.Provider, ag.Cmd, ag.Args, resumeSessionID, "", mode)

	// Append --file flags for image attachments
	for _, f := range imageFiles {
		args = append(args, "--file", f)
	}

	// Record user message first
	historyData := map[string]any{
		"role":       "user",
		"text":       message,
		"raw":        false,
		"imageCount": len(imageFiles),
	}
	if len(imagePaths) > 0 {
		historyData["images"] = imagePaths
	}
	if _, err := h.server.manager.RecordConversationEvent(ag.ID, historyData); err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	broadcastData := map[string]any{
		"agentId":    ag.ID,
		"role":       "user",
		"text":       message,
		"imageCount": len(imageFiles),
	}
	if len(imagePaths) > 0 {
		broadcastData["images"] = imagePaths
	}
	h.server.broadcast(event("conversation.message", broadcastData), nil)

	// Start the agent with the message
	if err := h.server.manager.StartInPlaceWithMessage(ag.ID, provider, cmd, args, env, message); err != nil {
		return errResp(req.ID, -32000, "start with message: "+err.Error())
	}

	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "working")), nil)

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
	case "ctrl_c":
		return "\x03", true
	case "ctrl_d":
		return "\x04", true
	case "ctrl_z":
		return "\x1a", true
	case "ctrl_a":
		return "\x01", true
	case "ctrl_e":
		return "\x05", true
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

func (h *handler) conversationImage(req RPCRequest) RPCResponse {
	imagePath, _ := req.Params["path"].(string)
	if imagePath == "" {
		return errResp(req.ID, -32602, "path is required")
	}
	dataDir := h.server.manager.DataDir()
	// Security: only allow reading files under dataDir/images
	if !strings.HasPrefix(filepath.Clean(imagePath), filepath.Join(dataDir, "images")) {
		return errResp(req.ID, -32000, "invalid image path")
	}
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return errResp(req.ID, -32000, "read image: "+err.Error())
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return okResp(req.ID, map[string]any{"data": b64})
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

func (h *handler) conversationClear(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	ag.EventBuf().Reset()
	if err := h.server.manager.ClearConversationEvents(agentID); err != nil {
		log.Printf("[conversationClear] failed to clear persisted history for %s: %v", agentID, err)
	}
	// Reset status to idle so the UI does not block new sends.
	ag.SetStatus(agent.StatusIdle)
	// Reset watcher offset so old session-file lines are not re-read.
	ag.ResetWatcherOffset()
	h.server.broadcast(event("conversation.cleared", map[string]any{
		"nodeId":  req.Params["nodeId"],
		"agentId": agentID,
	}), nil)
	return okResp(req.ID, map[string]any{"ok": true})
}

// opencodeDiscover scans for available opencode sessions on the machine.
// It scans both the current user's home directory and all other users' home directories
// (useful when agentd runs as root but sessions are in user directories).
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

	// Build list of directories to scan.
	// OpenCode may store sessions under session_diff or session subdirectories.
	opencodeStorageDirs := []string{
		filepath.Join(home, ".local", "share", "opencode", "storage"),
		filepath.Join(home, "Library", "Application Support", "opencode", "storage"),
		filepath.Join(home, "Library", "Application Support", "OpenCode", "storage"),
	}

	// On Linux, also scan all user home directories under /home
	// This allows agentd running as root to find sessions from all users
	if _, err := os.Stat("/home"); err == nil {
		entries, err := os.ReadDir("/home")
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				userHome := filepath.Join("/home", entry.Name())
				opencodeStorageDirs = append(opencodeStorageDirs, filepath.Join(userHome, ".local", "share", "opencode", "storage"))
			}
		}
	}

	// Expand storage dirs into candidate session subdirectories
	var sessionDirs []string
	for _, storageDir := range opencodeStorageDirs {
		sessionDirs = append(sessionDirs,
			filepath.Join(storageDir, "session_diff"),
			filepath.Join(storageDir, "session"),
		)
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
			// Accept any session file; ses_ prefix is common but not guaranteed
			if sessionID == "" {
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
		ProjectName string    `json:"projectName,omitempty"`
		SessionFile string    `json:"sessionFile"`
		ModifiedAt  time.Time `json:"modifiedAt"`
		Size        int64     `json:"size"`
	}

	// extractProjectName extracts a human-readable project name from directory name
	// e.g., "-Users-fengming-xie-Downloads" -> "Downloads"

	extractProjectName := func(dirName string) string {
		// Remove leading dash
		s := strings.TrimPrefix(dirName, "-")
		// Split by "-" and take the last part
		parts := strings.Split(s, "-")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return dirName
	}
	projectsDir := filepath.Join(home, ".claude", "projects")

	// Collect all projects dirs to scan (current user + /home/*)
	projectsDirs := []string{projectsDir}
	if _, err := os.Stat("/home"); err == nil {
		entries, err := os.ReadDir("/home")
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				userProjects := filepath.Join("/home", entry.Name(), ".claude", "projects")
				if _, err := os.Stat(userProjects); err == nil {
					projectsDirs = append(projectsDirs, userProjects)
				}
			}
		}
	}

	sessionsByID := make(map[string]sessionInfo)
	cutoff := time.Now().Add(-7 * 24 * time.Hour) // Only include sessions from the last 7 days
	for _, pDir := range projectsDirs {
		projectEntries, err := os.ReadDir(pDir)
		if err != nil {
			continue
		}
		for _, project := range projectEntries {
			if !project.IsDir() {
				continue
			}
			projectPath := filepath.Join(pDir, project.Name())
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
				// Skip old sessions
				if info.ModTime().Before(cutoff) {
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
					ProjectName: extractProjectName(project.Name()),
					SessionFile: filepath.Join(projectPath, entry.Name()),
					ModifiedAt:  info.ModTime(),
					Size:        info.Size(),
				}
				if existing, ok := sessionsByID[sessionID]; !ok || candidate.ModifiedAt.After(existing.ModifiedAt) {
					sessionsByID[sessionID] = candidate
				}
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
	for _, proc := range processes {
		candidate := h.server.manager.ClassifyAttachCandidate(proc)
		if candidate.Decision == agent.AttachDecisionSkip || candidate.Decision == agent.AttachDecisionAmbiguous {
			log.Printf("[agent.scan] hiding %s pid %d: %s", proc.Provider, proc.PID, candidate.Reason)
			continue
		}
		p := candidate.Process
		projectName := ""
		if p.WorkDir != "" {
			projectName = filepath.Base(strings.TrimRight(p.WorkDir, "/"))
		}
		// Display candidates are shown for informational purposes only;
		// don't claim a session file since their session association is uncertain.
		sessionFile := ""
		if candidate.Decision != agent.AttachDecisionDisplay {
			sessionFile = p.FindSessionFile()
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
			SessionFile:    sessionFile,
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

func (h *handler) agentRename(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	name, _ := req.Params["name"].(string)
	if agentID == "" || name == "" {
		return errResp(req.ID, -32602, "agentId and name are required")
	}
	if err := h.server.manager.Rename(agentID, name); err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	params := h.statusChangedParams(agentID, nil)
	params["name"] = name
	h.server.broadcast(event("agent.status_changed", params), nil)
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) agentRemove(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	if agentID == "" {
		return errResp(req.ID, -32602, "agentId is required")
	}
	if err := h.server.manager.Remove(agentID); err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	params := h.statusChangedParams(agentID, nil)
	params["status"] = "removed"
	h.server.broadcast(event("agent.status_changed", params), nil)
	return okResp(req.ID, map[string]any{"ok": true})
}

func openCCSwitchDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	return db, nil
}

func ensureCCSwitchProvidersTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS providers (
		id TEXT NOT NULL,
		app_type TEXT NOT NULL,
		name TEXT NOT NULL,
		settings_config TEXT NOT NULL,
		website_url TEXT,
		category TEXT,
		created_at INTEGER,
		sort_index INTEGER,
		notes TEXT,
		icon TEXT,
		icon_color TEXT,
		meta TEXT NOT NULL DEFAULT '{}',
		is_current BOOLEAN NOT NULL DEFAULT 0,
		in_failover_queue BOOLEAN NOT NULL DEFAULT 0,
		cost_multiplier TEXT NOT NULL DEFAULT '1.0',
		limit_daily_usd TEXT,
		limit_monthly_usd TEXT,
		provider_type TEXT,
		PRIMARY KEY (id, app_type)
	)`)
	return err
}

func (h *handler) loadProviderRows(dbPath string) ([]map[string]any, error) {
	db, err := openCCSwitchDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, name, is_current, settings_config FROM providers WHERE app_type=? ORDER BY sort_index, name`, "claude")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	providers := make([]map[string]any, 0)
	for rows.Next() {
		var id string
		var name string
		var isCurrent any
		var settingsConfig string
		if err := rows.Scan(&id, &name, &isCurrent, &settingsConfig); err != nil {
			return nil, err
		}
		providers = append(providers, map[string]any{
			"id":              id,
			"name":            name,
			"is_current":      isCurrent,
			"settings_config": settingsConfig,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return providers, nil
}

func (h *handler) invalidateProviderCache() {
	h.server.providerCache.mu.Lock()
	defer h.server.providerCache.mu.Unlock()
	h.server.providerCache.rows = nil
	h.server.providerCache.resp = RPCResponse{}
	h.server.providerCache.ok = false
	h.server.providerCache.currentID = ""
	h.server.providerCache.currentReason = ""
	h.server.providerCache.runtimeID = ""
	h.server.providerCache.runtimeReason = ""
	h.server.providerCache.at = time.Time{}
}

func findCCSwitchDB() string {
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".cc-switch", "cc-switch.db")}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, filepath.Join("/home", e.Name(), ".cc-switch", "cc-switch.db"))
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func findClaudeSettings() string {
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".claude", "settings.json")}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, filepath.Join("/home", e.Name(), ".claude", "settings.json"))
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func providerIDFromConfig(configJSON string, runtimeEnv map[string]any, runtimeModel string) string {
	if configJSON == "" {
		return ""
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return ""
	}
	cfgEnv, _ := cfg["env"].(map[string]any)
	providerURL, _ := cfgEnv["ANTHROPIC_BASE_URL"].(string)
	providerToken, _ := cfgEnv["ANTHROPIC_AUTH_TOKEN"].(string)
	providerModel, _ := cfg["model"].(string)
	if providerModel == "" {
		providerModel, _ = cfgEnv["ANTHROPIC_MODEL"].(string)
	}

	actualURL, _ := runtimeEnv["ANTHROPIC_BASE_URL"].(string)
	actualToken, _ := runtimeEnv["ANTHROPIC_AUTH_TOKEN"].(string)
	actualModel := runtimeModel
	if actualModel == "" {
		actualModel, _ = runtimeEnv["ANTHROPIC_MODEL"].(string)
	}

	if providerURL != "" && providerURL != actualURL {
		return ""
	}
	if providerToken != "" && providerToken != actualToken {
		return ""
	}
	if providerModel != "" && providerModel != actualModel {
		return ""
	}
	id, _ := cfg["id"].(string)
	return id
}

func runtimeProviderFromRows(rows []map[string]any) (string, string) {
	settingsPath := findClaudeSettings()
	if settingsPath == "" {
		return "", "settings.json not found"
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return "", "read settings.json failed"
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return "", "parse settings.json failed"
	}
	runtimeEnv, _ := settings["env"].(map[string]any)
	runtimeModel, _ := settings["model"].(string)
	for _, r := range rows {
		configJSON, _ := r["settings_config"].(string)
		if configJSON == "" {
			continue
		}
		var cfg map[string]any
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			continue
		}
		cfg["id"] = r["id"]
		mergedConfig, _ := json.Marshal(cfg)
		if id := providerIDFromConfig(string(mergedConfig), runtimeEnv, runtimeModel); id != "" {
			return id, "matched by settings.json env"
		}
	}
	return "", "runtime settings did not match any provider"
}

func currentProviderFromRows(rows []map[string]any) (string, string) {
	for _, r := range rows {
		isCurrent := false
		switch v := r["is_current"].(type) {
		case bool:
			isCurrent = v
		case int:
			isCurrent = v != 0
		case int64:
			isCurrent = v != 0
		case float64:
			isCurrent = v != 0
		case string:
			isCurrent = v == "1" || strings.EqualFold(v, "true")
		}
		if isCurrent {
			if id, ok := r["id"].(string); ok {
				return id, "derived from cc-switch is_current"
			}
		}
	}
	return "", "cc-switch current provider not set"
}

func (h *handler) providerRows() ([]map[string]any, RPCResponse, bool) {
	dbPath := findCCSwitchDB()
	if dbPath == "" {
		return nil, okResp(nil, map[string]any{"providers": []any{}, "current": ""}), false
	}
	rows, err := h.loadProviderRows(dbPath)
	if err != nil {
		return nil, errResp(nil, -32000, "read cc-switch db: "+err.Error()), false
	}
	return rows, RPCResponse{}, true
}

func (h *handler) cachedProviderData() ([]map[string]any, RPCResponse, bool, string, string, string, string) {
	h.server.providerCache.mu.Lock()
	defer h.server.providerCache.mu.Unlock()
	if time.Since(h.server.providerCache.at) < time.Second && !h.server.providerCache.at.IsZero() {
		return h.server.providerCache.rows, h.server.providerCache.resp, h.server.providerCache.ok,
			h.server.providerCache.currentID, h.server.providerCache.currentReason,
			h.server.providerCache.runtimeID, h.server.providerCache.runtimeReason
	}
	rows, resp, ok := h.providerRows()
	var currentID, currentReason, runtimeID, runtimeReason string
	if ok {
		currentID, currentReason = currentProviderFromRows(rows)
		runtimeID, runtimeReason = runtimeProviderFromRows(rows)
	}
	h.server.providerCache.rows = rows
	h.server.providerCache.resp = resp
	h.server.providerCache.ok = ok
	h.server.providerCache.currentID = currentID
	h.server.providerCache.currentReason = currentReason
	h.server.providerCache.runtimeID = runtimeID
	h.server.providerCache.runtimeReason = runtimeReason
	h.server.providerCache.at = time.Now()
	return rows, resp, ok, currentID, currentReason, runtimeID, runtimeReason
}

func (h *handler) deriveProviderScope(ag *agent.Agent) string {
	if ag == nil {
		return "standalone"
	}
	if ag.AttachMode() != "" {
		return "root"
	}
	for _, candidate := range h.server.manager.List() {
		if candidate == nil || candidate.ID == ag.ID {
			continue
		}
		resumeID, _ := h.server.manager.GetResumeSessionID(candidate.ID)
		agResumeID, _ := h.server.manager.GetResumeSessionID(ag.ID)
		if resumeID != "" && agResumeID != "" && resumeID == agResumeID {
			if candidate.AttachMode() != "" || candidate.PID > 0 {
				return "inherited"
			}
		}
	}
	return "standalone"
}

func (h *handler) deriveProviderSnapshot(ag *agent.Agent) providerSnapshot {
	scope := h.deriveProviderScope(ag)

	// opencode does not use cc-switch provider switching.
	if ag != nil && ag.Provider == "opencode" {
		return providerSnapshot{
			ProviderScope: scope,
		}
	}

	_, _, ok, currentProviderID, currentReason, runtimeProviderID, runtimeReason := h.cachedProviderData()
	if !ok {
		snapshot := providerSnapshot{
			ProviderScope:       scope,
			ProviderState:       "unknown",
			ProviderStateReason: "provider state unavailable",
			ProviderWriteMode:   "writable",
		}
		switch {
		case scope == "inherited":
			snapshot.ProviderWriteMode = "read_only"
			snapshot.ProviderReadOnlyReason = "provider scope is inherited from root session"
		case ag != nil && ag.AttachMode() != "":
			snapshot.ProviderWriteMode = "read_only"
			snapshot.ProviderReadOnlyReason = "attached runtime cannot guarantee immediate provider switch"
		}
		return snapshot
	}
	snapshot := providerSnapshot{
		CurrentProviderID: currentProviderID,
		RuntimeProviderID: runtimeProviderID,
		ProviderScope:     scope,
	}
	switch {
	case currentProviderID == "" || runtimeProviderID == "":
		snapshot.ProviderState = "unknown"
		if runtimeProviderID == "" {
			snapshot.ProviderStateReason = runtimeReason
		} else {
			snapshot.ProviderStateReason = currentReason
		}
	case currentProviderID == runtimeProviderID:
		snapshot.ProviderState = "synced"
		snapshot.ProviderStateReason = runtimeReason
	default:
		snapshot.ProviderState = "drifted"
		snapshot.ProviderStateReason = fmt.Sprintf("runtime provider %s differs from selected provider %s", runtimeProviderID, currentProviderID)
	}
	snapshot.ProviderWriteMode = "writable"
	switch {
	case scope == "inherited":
		snapshot.ProviderWriteMode = "read_only"
		snapshot.ProviderReadOnlyReason = "provider scope is inherited from root session"
	case ag != nil && ag.AttachMode() != "":
		snapshot.ProviderWriteMode = "read_only"
		snapshot.ProviderReadOnlyReason = "attached runtime cannot guarantee immediate provider switch"
	}
	return snapshot
}

func (h *handler) statusChangedParams(agentID string, status any) map[string]any {
	params := map[string]any{"agentId": agentID}
	ag := h.server.manager.Get(agentID)
	if status != nil {
		switch v := status.(type) {
		case string:
			params["status"] = v
		case agent.Status:
			params["status"] = string(v)
		}
	} else if ag != nil {
		params["status"] = string(ag.Status())
	}
	derived := h.server.manager.DeriveAgentState(agentID)
	params["runtimeState"] = derived.RuntimeState
	params["sessionState"] = derived.SessionState
	params["sessionStateReason"] = derived.SessionStateReason
	params["sessionControl"] = derived.SessionControl
	if ag != nil {
		projectName := ""
		if ag.WorkDir != "" {
			projectName = filepath.Base(strings.TrimRight(ag.WorkDir, "/"))
		}
		resumeID, _ := h.server.manager.GetResumeSessionID(ag.ID)
		params["name"] = ag.Name
		params["provider"] = ag.Provider
		params["workDir"] = ag.WorkDir
		params["projectName"] = projectName
		params["pid"] = ag.PID
		params["sessionId"] = resumeID
		attachMode := ag.AttachMode()
		if attachMode != "" {
			params["attachMode"] = attachMode
		}
		params["readOnly"] = ag.AttachReadOnly()
		if reason := ag.AttachReadOnlyReason(); reason != "" {
			params["readOnlyReason"] = reason
		}
		params["permissionMode"] = currentPermissionMode(ag.Args)
		provider := h.deriveProviderSnapshot(ag)
		params["providerState"] = provider.ProviderState
		params["providerScope"] = provider.ProviderScope
		params["providerWriteMode"] = provider.ProviderWriteMode
		params["providerReadOnlyReason"] = provider.ProviderReadOnlyReason
		var lastMsgTimeMs int64
		if t, err := h.server.manager.LastConversationEventTime(ag.ID); err == nil && !t.IsZero() {
			lastMsgTimeMs = t.UnixMilli()
		}
		params["lastMessageTime"] = lastMsgTimeMs
	}
	return params
}

func (h *handler) providerList(req RPCRequest) RPCResponse {
	rows, resp, ok, _, _, _, _ := h.cachedProviderData()
	if !ok {
		if resp.Error == nil {
			return okResp(req.ID, map[string]any{
				"providers":              []any{},
				"current":                "",
				"runtimeProviderId":      "",
				"providerState":          "unknown",
				"providerStateReason":    "provider state unavailable",
				"providerScope":          "standalone",
				"providerWriteMode":      "read_only",
				"providerReadOnlyReason": "provider state unavailable",
			})
		}
		resp.ID = req.ID
		return resp
	}

	agentID, _ := req.Params["agentId"].(string)
	var targetAgent *agent.Agent
	if agentID != "" {
		targetAgent = h.server.manager.Get(agentID)
	}
	snapshot := h.deriveProviderSnapshot(targetAgent)
	switch {
	case agentID == "":
		snapshot.ProviderWriteMode = "read_only"
		snapshot.ProviderReadOnlyReason = "agentId is required to determine safe provider switching"
	case targetAgent == nil:
		snapshot.ProviderWriteMode = "read_only"
		snapshot.ProviderReadOnlyReason = "agent not found"
	}

	return okResp(req.ID, map[string]any{
		"providers":              rows,
		"current":                snapshot.CurrentProviderID,
		"runtimeProviderId":      snapshot.RuntimeProviderID,
		"providerState":          snapshot.ProviderState,
		"providerStateReason":    snapshot.ProviderStateReason,
		"providerScope":          snapshot.ProviderScope,
		"providerWriteMode":      snapshot.ProviderWriteMode,
		"providerReadOnlyReason": snapshot.ProviderReadOnlyReason,
	})
}

func (h *handler) providerSwitch(req RPCRequest) (RPCResponse, func()) {
	providerID, _ := req.Params["providerId"].(string)
	if providerID == "" {
		return errResp(req.ID, -32602, "providerId is required"), nil
	}

	agentID, _ := req.Params["agentId"].(string)
	var targetAgent *agent.Agent
	if agentID != "" {
		targetAgent = h.server.manager.Get(agentID)
		if targetAgent == nil {
			return errResp(req.ID, -32000, "agent not found"), nil
		}
		snapshot := h.deriveProviderSnapshot(targetAgent)
		if snapshot.ProviderWriteMode == "read_only" {
			msg := snapshot.ProviderReadOnlyReason
			if msg == "" {
				msg = "provider switch is read-only for this session"
			}
			return errResp(req.ID, -32000, msg), nil
		}
	}

	dbPath := findCCSwitchDB()
	if dbPath == "" {
		return errResp(req.ID, -32000, "cc-switch not found"), nil
	}

	h.server.providerDBMu.Lock()
	defer h.server.providerDBMu.Unlock()

	// Get provider config from DB (also fetch name for display)
	db, err := openCCSwitchDB(dbPath)
	if err != nil {
		return errResp(req.ID, -32000, "open provider db: "+err.Error()), nil
	}
	defer db.Close()

	var providerName string
	var configJSON string
	if err := db.QueryRow(`SELECT name, settings_config FROM providers WHERE id=? AND app_type=?`, providerID, "claude").Scan(&providerName, &configJSON); err != nil {
		if err == sql.ErrNoRows {
			return errResp(req.ID, -32000, "provider not found: "+providerID), nil
		}
		return errResp(req.ID, -32000, "read provider: "+err.Error()), nil
	}

	var providerConfig map[string]any
	if err := json.Unmarshal([]byte(configJSON), &providerConfig); err != nil {
		return errResp(req.ID, -32000, "parse provider config: "+err.Error()), nil
	}

	settingsPath := findClaudeSettings()
	if settingsPath == "" {
		// Create settings.json in the default location
		home, _ := os.UserHomeDir()
		settingsPath = filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
			return errResp(req.ID, -32000, "create .claude dir: "+err.Error()), nil
		}
	}

	settingsData, _ := os.ReadFile(settingsPath)
	var settings map[string]any
	if len(settingsData) > 0 {
		_ = json.Unmarshal(settingsData, &settings)
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	// Merge provider env into settings env
	if providerEnv, ok := providerConfig["env"].(map[string]any); ok {
		existingEnv, _ := settings["env"].(map[string]any)
		if existingEnv == nil {
			existingEnv = make(map[string]any)
		}
		for k, v := range providerEnv {
			existingEnv[k] = v
		}
		settings["env"] = existingEnv
	}

	// Merge other top-level keys from provider config
	for k, v := range providerConfig {
		if k != "env" {
			settings[k] = v
		}
	}

	newData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return errResp(req.ID, -32000, "marshal settings: "+err.Error()), nil
	}
	if err := os.WriteFile(settingsPath, newData, 0644); err != nil {
		return errResp(req.ID, -32000, "write settings: "+err.Error()), nil
	}

	// Update is_current in DB
	if _, err := db.Exec(`UPDATE providers SET is_current=CASE WHEN id=? AND app_type=? THEN 1 ELSE 0 END WHERE app_type=?`, providerID, "claude", "claude"); err != nil {
		return errResp(req.ID, -32000, "update provider selection: "+err.Error()), nil
	}
	h.invalidateProviderCache()

	// Sync selected provider to cc-switch settings.json so the standalone CC Switch UI
	// stays consistent with the in-app switch.
	ccSwitchSettingsPath := filepath.Join(filepath.Dir(dbPath), "settings.json")
	if data, err := os.ReadFile(ccSwitchSettingsPath); err == nil {
		var ccSettings map[string]any
		if err := json.Unmarshal(data, &ccSettings); err == nil {
			ccSettings["currentProviderClaude"] = providerID
			if newCCData, err := json.MarshalIndent(ccSettings, "", "  "); err == nil {
				if writeErr := os.WriteFile(ccSwitchSettingsPath, newCCData, 0644); writeErr != nil {
					log.Printf("[providerSwitch] update cc-switch settings.json: %v", writeErr)
				}
			}
		}
	}

	// Extract model from provider config for the response and restart
	model, _ := providerConfig["model"].(string)
	if model == "" {
		// Fallback: check ANTHROPIC_MODEL in env
		if envMap, ok := providerConfig["env"].(map[string]any); ok {
			model, _ = envMap["ANTHROPIC_MODEL"].(string)
		}
	}

	var afterSend func()
	if targetAgent != nil {
		resumeSessionID, _ := h.server.manager.GetResumeSessionID(agentID)
		mode := currentPermissionMode(targetAgent.Args)
		provider, cmd, args, env := resolveLaunch("claude", targetAgent.Cmd, targetAgent.Args, resumeSessionID, model, mode)
		if err := h.server.manager.RestartInPlace(agentID, provider, cmd, args, env); err != nil {
			log.Printf("[providerSwitch] restart agent %s: %v", agentID, err)
		} else {
			srv := h.server
			conn := h.conn
			afterSend = func() {
				srv.broadcast(event("agent.status_changed", h.statusChangedParams(agentID, "idle")), conn)
			}
		}
	}

	return okResp(req.ID, map[string]any{
		"ok":           true,
		"providerId":   providerID,
		"providerName": providerName,
		"model":        model,
	}), afterSend
}

func (h *handler) systemInfo(req RPCRequest) RPCResponse {
	home, err := os.UserHomeDir()
	if err != nil {
		return errResp(req.ID, -32000, "cannot get home dir: "+err.Error())
	}
	return okResp(req.ID, map[string]any{
		"homeDir": home,
	})
}

func (h *handler) systemSkills(req RPCRequest) RPCResponse {
	type skillEntry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Command     string `json:"command"`
	}

	skills := []skillEntry{}
	seen := map[string]bool{}

	// Scan all candidate home directories for .claude/skills/
	var homes []string
	if home, err := os.UserHomeDir(); err == nil {
		homes = append(homes, home)
	}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				homes = append(homes, filepath.Join("/home", e.Name()))
			}
		}
	}

	for _, home := range homes {
		skillsDir := filepath.Join(home, ".claude", "skills")
		entries, err := os.ReadDir(skillsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if seen[name] {
				continue
			}
			seen[name] = true
			cmd := "/" + name
			desc := ""
			// Try to read description from skill.md or first .md file
			for _, candidate := range []string{"skill.md", "index.md"} {
				p := filepath.Join(skillsDir, name, candidate)
				if data, err := os.ReadFile(p); err == nil {
					lines := strings.Split(string(data), "\n")
					for _, line := range lines {
						line = strings.TrimSpace(line)
						if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "---") {
							desc = line
							break
						}
					}
					break
				}
			}
			skills = append(skills, skillEntry{
				Name:        name,
				Description: desc,
				Command:     cmd,
			})
		}
	}

	return okResp(req.ID, map[string]any{
		"skills": skills,
	})
}

func (h *handler) providerAdd(req RPCRequest) RPCResponse {
	name, _ := req.Params["name"].(string)
	if name == "" {
		return errResp(req.ID, -32602, "name is required")
	}

	h.server.providerDBMu.Lock()
	defer h.server.providerDBMu.Unlock()

	dbPath := findCCSwitchDB()
	if dbPath == "" {
		// Create cc-switch directory and DB if not exists
		home, _ := os.UserHomeDir()
		ccDir := filepath.Join(home, ".cc-switch")
		_ = os.MkdirAll(ccDir, 0755)
		dbPath = filepath.Join(ccDir, "cc-switch.db")
	}

	db, err := openCCSwitchDB(dbPath)
	if err != nil {
		return errResp(req.ID, -32000, "open provider db: "+err.Error())
	}
	defer db.Close()
	if err := ensureCCSwitchProvidersTable(db); err != nil {
		return errResp(req.ID, -32000, "init provider db: "+err.Error())
	}

	// Generate UUID for new provider
	id := generateUUID()

	// Build settings_config from params
	baseURL, _ := req.Params["baseUrl"].(string)
	authToken, _ := req.Params["authToken"].(string)
	model, _ := req.Params["model"].(string)

	settingsConfig := map[string]any{}
	env := map[string]any{}
	if baseURL != "" {
		env["ANTHROPIC_BASE_URL"] = baseURL
	}
	if authToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = authToken
	}
	if model != "" {
		env["ANTHROPIC_MODEL"] = model
		settingsConfig["model"] = model
	}
	if len(env) > 0 {
		settingsConfig["env"] = env
	}
	settingsJSON, _ := json.Marshal(settingsConfig)

	now := time.Now().Unix()
	if _, err := db.Exec(
		`INSERT INTO providers (
			id, app_type, name, settings_config, created_at, sort_index, meta, is_current, in_failover_queue, cost_multiplier
		) VALUES (?, ?, ?, ?, ?, ?, '{}', 0, 0, '1.0')`,
		id, "claude", name, string(settingsJSON), now, 0,
	); err != nil {
		return errResp(req.ID, -32000, "insert provider: "+err.Error())
	}
	h.invalidateProviderCache()

	return okResp(req.ID, map[string]any{"ok": true, "id": id, "name": name})
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

var (
	clientIDCounter uint64
	clientIDMu      sync.Mutex
)

// generateClientID generates a unique client ID for each connection.
func generateClientID() string {
	clientIDMu.Lock()
	defer clientIDMu.Unlock()
	clientIDCounter++
	return fmt.Sprintf("client-%d-%d", time.Now().Unix(), clientIDCounter)
}

// base64Decode decodes a base64 string, handling both standard and URL-safe encoding,
// and optional data URI prefix (e.g. "data:image/png;base64,...").
func base64Decode(s string) ([]byte, error) {
	// Strip data URI prefix if present
	if idx := strings.Index(s, ","); idx >= 0 && strings.Contains(s[:idx], "base64") {
		s = s[idx+1:]
	}
	return base64.StdEncoding.DecodeString(s)
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
			srv.broadcast(event("agent.status_changed", h.statusChangedParams(agent.ID, agent.Status())), conn)
		}
}

// opencodeModels reads the opencode.json config and returns available models.
func (h *handler) opencodeModels(req RPCRequest) RPCResponse {
	candidates := []string{}
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".config", "opencode", "opencode.json"),
		)
	}
	if os.Getuid() == 0 {
		entries, _ := os.ReadDir("/home")
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates,
					filepath.Join("/home", e.Name(), ".config", "opencode", "opencode.json"),
				)
			}
		}
	}

	var configPath string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			configPath = p
			break
		}
	}
	if configPath == "" {
		return okResp(req.ID, map[string]any{"models": []any{}, "current": ""})
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return errResp(req.ID, -32000, "read opencode.json: "+err.Error())
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return errResp(req.ID, -32000, "parse opencode.json: "+err.Error())
	}

	currentModel, _ := config["model"].(string)
	providers, _ := config["provider"].(map[string]any)

	type modelEntry struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}

	var models []modelEntry
	for provName, provVal := range providers {
		provMap, ok := provVal.(map[string]any)
		if !ok {
			continue
		}
		modelsMap, _ := provMap["models"].(map[string]any)
		for modelName, modelVal := range modelsMap {
			displayName := modelName
			if m, ok := modelVal.(map[string]any); ok {
				if name, ok := m["name"].(string); ok && name != "" {
					displayName = name
				}
			}
			models = append(models, modelEntry{
				ID:       provName + "/" + modelName,
				Name:     displayName,
				Provider: provName,
			})
		}
	}

	return okResp(req.ID, map[string]any{
		"models":  models,
		"current": currentModel,
	})
}
