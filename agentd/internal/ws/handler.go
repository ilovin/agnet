package ws

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/hermesclient"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/watcher"
)

type handler struct {
	server  *Server
	conn    *websocket.Conn
	self    *client
	service AgentService
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
		var p AgentCreateParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.agentCreate(req, p)
	case "agent.stop":
		var p AgentStopParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.agentStop(req, p)
	case "agent.restart":
		var p AgentRestartParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.agentRestart(req, p)
	case "agent.scan":
		return h.agentScan(req), nil
	case "agent.attach":
		var p AgentAttachParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.agentAttach(req, p)
	case "session.list":
		return h.sessionList(req), nil
	case "session.create":
		var p AgentCreateParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.sessionCreate(req, p)
	case "session.attach":
		var p SessionAttachParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.sessionAttach(req, p)
	case "session.catalog":
		return h.sessionCatalog(req), nil
	case "conversation.send":
		var p ConversationSendParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.conversationSend(req, p), nil
	case "conversation.key":
		var p ConversationKeyParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.conversationKey(req, p), nil
	case "conversation.history":
		var p ConversationHistoryParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.conversationHistory(req, p), nil
	case "conversation.image":
		var p ConversationImageParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.conversationImage(req, p), nil
	case "conversation.permission_response":
		var p ConversationPermissionResponseParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.conversationPermissionResponse(req, p), nil
	case "conversation.clear":
		var p ConversationClearParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.conversationClear(req, p), nil
	case "agent.rename":
		var p AgentRenameParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.agentRename(req, p), nil
	case "agent.remove":
		var p AgentRemoveParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.agentRemove(req, p), nil
	case "provider.list":
		var p ProviderListParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.providerList(req, p), nil
	case "provider.switch":
		var p ProviderSwitchParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.providerSwitch(req, p)
	case "provider.add":
		var p ProviderAddParams
		if err := decodeParams(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params"), nil
		}
		return h.providerAdd(req, p), nil
	case "opencode.models":
		return h.opencodeModels(req), nil
	case "system.info":
		return h.systemInfo(req), nil
	case "system.suggest_dirs":
		return h.systemSuggestDirs(req), nil
	case "system.mkdir":
		return h.systemMkdir(req), nil
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
			PermissionMode:         h.service.CurrentPermissionMode(ag.Args),
			LastMessageTime:        lastMsgTimeMs,
		})
	}
	return okResp(req.ID, result)
}

func (h *handler) agentCreate(req RPCRequest, p AgentCreateParams) (RPCResponse, func()) {
	id, err := h.createAgent(req, p)
	if err != nil {
		return errResp(req.ID, -32000, err.Error()), nil
	}

	srv := h.server
	conn := h.conn
	return okResp(req.ID, map[string]any{"id": id}), func() {
		srv.broadcast(event("agent.status_changed", h.statusChangedParams(id, "idle")), conn)
	}
}

func (h *handler) createAgent(req RPCRequest, p AgentCreateParams) (string, error) {
	name := p.Name
	provider := p.Provider
	cmd := p.Cmd
	workDir := p.WorkDir
	sessionID := p.SessionID
	model := p.Model
	args := p.Args

	provider, cmd, args, env := h.service.ResolveLaunch(provider, cmd, args, sessionID, model, "")

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

	attachable, _ := attachableResp.Result.(map[string]any)
	attachableProcesses := toAnySlice(attachable["processes"])

	return okResp(req.ID, map[string]any{
		"managed":    managed,
		"attachable": attachableProcesses,
	})
}

func (h *handler) sessionCreate(req RPCRequest, p AgentCreateParams) (RPCResponse, func()) {
	return h.agentCreate(req, p)
}

func (h *handler) sessionAttach(req RPCRequest, p SessionAttachParams) (RPCResponse, func()) {
	agentID := p.AgentID
	if agentID != "" {
		if ag := h.server.manager.Get(agentID); ag != nil {
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

	if p.SessionID != "" {
		createParams := AgentCreateParams{
			Name:      p.Name,
			Provider:  p.Provider,
			Cmd:       p.Cmd,
			WorkDir:   p.WorkDir,
			SessionID: p.SessionID,
			Model:     p.Model,
			Args:      p.Args,
		}
		id, err := h.createAgent(req, createParams)
		if err != nil {
			return errResp(req.ID, -32000, err.Error()), nil
		}

		srv := h.server
		conn := h.conn
		return okResp(req.ID, map[string]any{"id": id}), func() {
			srv.broadcast(event("agent.status_changed", h.statusChangedParams(id, "idle")), conn)
		}
	}

	if int(p.PID) > 0 {
		attachParams := AgentAttachParams{PID: p.PID}
		return h.agentAttach(req, attachParams)
	}

	return errResp(req.ID, -32602, "pid, sessionId, or agentId is required"), nil
}

func (h *handler) agentStop(req RPCRequest, p AgentStopParams) (RPCResponse, func()) {
	id := p.AgentID
	if err := h.server.manager.Stop(id); err != nil {
		return errResp(req.ID, -32000, err.Error()), nil
	}
	srv := h.server
	conn := h.conn
	return okResp(req.ID, map[string]any{"ok": true}), func() {
		srv.broadcast(event("agent.status_changed", h.statusChangedParams(id, "stopped")), conn)
	}
}

func (h *handler) agentRestart(req RPCRequest, p AgentRestartParams) (RPCResponse, func()) {
	id := p.AgentID
	ag := h.server.manager.Get(id)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found"), nil
	}
	modelSpecified := p.Model != ""
	providerSpecified := p.Provider != ""
	permissionMode := p.PermissionMode
	model := p.Model
	apiProvider := p.Provider

	// For attached agents with a model change, update settings.json without
	// restarting the process. Claude reads settings dynamically.
	if ag.Provider == "claude" && ag.AttachMode() != "" && modelSpecified && !providerSpecified && permissionMode == "" {
		settingsPath := h.service.FindClaudeSettings()
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
		ag.SetCurrentPermissionMode(permissionMode)
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
		ag.SetCurrentPermissionMode(permissionMode)
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
		provider, cmd, args, env = h.service.ResolveLaunch(launchProvider, ag.Cmd, ag.Args, resumeSessionID, model, permissionMode)
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

func (h *handler) conversationSend(req RPCRequest, p ConversationSendParams) RPCResponse {
	agentID := p.AgentID
	message := p.Message
	raw := p.Raw
	log.Printf("[conversationSend] received: agentId=%s messageLen=%d raw=%v", agentID, len(message), raw)
	if message == "" {
		return errResp(req.ID, -32602, "message is required")
	}

	// Extract image attachments (base64-encoded)
	var imageFiles []string // temp file paths to pass via --file
	var imagePaths []string // persisted paths stored in event history
	if len(p.Images) > 0 {
		dataDir := h.server.manager.DataDir()
		imgDir := filepath.Join(dataDir, "images", agentID, fmt.Sprintf("%d", time.Now().Unix()))
		for i, img := range p.Images {
			data := img.Data
			mimeType := img.MimeType
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

	ag := h.server.manager.Get(agentID)
	if ag == nil {
		log.Printf("[conversationSend] agent not found: %s", agentID)
		return errResp(req.ID, -32000, "agent not found")
	}

	attachMode := ag.AttachMode()
	isTmuxAttached := attachMode == "tmux"
	isOpenCode := ag.Provider == "opencode"
	isHermes := ag.Provider == "hermes"
	log.Printf("[conversationSend] agent=%s provider=%s attachMode=%s isTmuxAttached=%v isOpenCode=%v readOnly=%v", agentID, ag.Provider, attachMode, isTmuxAttached, isOpenCode, ag.AttachReadOnly())
	// Guard against Restart/Start on attached non-tmux agents (read-only watcher attach).
	// OpenCode agents are exempt: openCodeSendWithResume uses `opencode run --session`,
	// not PTY input, so the read-only watcher attach does not apply.
	// Hermes was previously exempt because it used HTTP. Post-M3 hermes goes
	// through the tmux send-keys path identical to Claude tmux-attached agents,
	// so the generic guard correctly covers it (no special-case needed).
	if attachMode != "" && !isTmuxAttached && !isOpenCode {
		reason := ag.AttachReadOnlyReason()
		if reason == "" {
			reason = "attached session is read-only"
		}
		log.Printf("[conversationSend] blocking non-tmux non-opencode attached agent: %s", reason)
		return errResp(req.ID, -32000, reason)
	}
	isPipeMode := ag.Provider == "claude" && ag.Process() != nil && !isTmuxAttached
	isFreshClaude := ag.Provider == "claude" && ag.Process() == nil && !isTmuxAttached

	// Hermes images: rejected even under tmux because the CLI consumes only
	// stdin, not file flags. (Same restriction as the previous HTTP path; the
	// generic tmux branch below also rejects images so this check is mostly
	// kept for a clearer error message, but the tmux branch covers it too.)
	if isHermes && len(imageFiles) > 0 {
		return errResp(req.ID, -32000, "hermes sessions do not support image attachments")
	}

	// For tmux-attached sessions, write directly to PTY via tmux send-keys.
	if isTmuxAttached {
		log.Printf("[conversationSend] taking tmux path for agent %s", agentID)
		// tmux-attached interactive sessions cannot receive --file flags dynamically;
		// images would be recorded in history but never passed to the CLI process.
		if len(imageFiles) > 0 {
			return errResp(req.ID, -32000, "tmux-attached sessions do not support image attachments")
		}
		// Hermes-only: verify the pane's foreground process is not a shell
		// before sending keys. After hermes CLI crashes, the pane falls back
		// to its parent bash; subsequent send-keys would be executed as shell
		// commands. Plan §M4 §2.2.
		if isHermes {
			tmuxTarget := ag.TmuxTarget()
			if err := agent.ValidateNonShellPane(tmuxTarget); err != nil {
				log.Printf("[conversationSend] hermes pane validation failed for agent %s: %v", agentID, err)
				ag.SetStatus(agent.StatusStopped)
				return errResp(req.ID, -32000, "hermes CLI not running in tmux pane: "+err.Error())
			}
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
		// Hermes-only: tightly scope BeginSend to the actual write call so the
		// HermesDBWatcher's session-switch suppression window is millisecond-
		// scale instead of HTTP-round-trip-scale. Plan §M4 §2.1.
		if isHermes {
			ag.BeginSend()
		}
		err := ag.WriteInput(input)
		if isHermes {
			ag.EndSend()
		}
		if err != nil {
			return errResp(req.ID, -32000, "write to tmux agent: "+err.Error())
		}
		// OpenCode responses are read by the OpenCodeDBWatcher polling the SQLite DB.
		// No need for tmux pane capture (which contains TUI noise like plan headers).
		return okResp(req.ID, map[string]any{"id": agentID})
	}

	// For Claude in -p mode with existing process, restart the process in-place and send prompt via stdin.
	if isPipeMode {
		log.Printf("[conversationSend] taking pipe mode path for agent %s", agentID)
		return h.agentRestartWithMessage(req, ag, message, imageFiles, imagePaths)
	}

	// For fresh Claude agent (no process yet), start with message
	if isFreshClaude {
		log.Printf("[conversationSend] taking fresh claude path for agent %s", agentID)
		return h.agentStartWithMessage(req, ag, message, imageFiles, imagePaths)
	}

	// For OpenCode attached sessions, use resume mode
	if isOpenCode {
		log.Printf("[conversationSend] taking opencode resume path for agent %s", agentID)
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
	mode := h.service.CurrentPermissionMode(ag.Args)
	provider, cmd, args, env := h.service.ResolveLaunch(ag.Provider, ag.Cmd, ag.Args, resumeSessionID, "", mode)

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

	ag.SetStatus(agent.StatusWorking)
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
	mode := h.service.CurrentPermissionMode(ag.Args)
	provider, cmd, args, env := h.service.ResolveLaunch(ag.Provider, ag.Cmd, ag.Args, resumeSessionID, "", mode)

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

	ag.SetStatus(agent.StatusWorking)
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

func (h *handler) conversationKey(req RPCRequest, p ConversationKeyParams) RPCResponse {
	agentID := p.AgentID
	key := p.Key
	if key == "" {
		return errResp(req.ID, -32602, "key is required")
	}
	seq, ok := keyToBytes(key)
	if !ok {
		return errResp(req.ID, -32602, "unsupported key: "+key)
	}

	repeat := 1
	if p.Repeat > 0 {
		repeat = int(p.Repeat)
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

func (h *handler) conversationImage(req RPCRequest, p ConversationImageParams) RPCResponse {
	imagePath := p.Path
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

func (h *handler) conversationHistory(req RPCRequest, p ConversationHistoryParams) RPCResponse {
	agentID := p.AgentID
	var afterSeq uint64
	if p.Cursor > 0 {
		afterSeq = uint64(p.Cursor)
	}
	limit := 200
	if p.Limit > 0 {
		limit = int(p.Limit)
	}
	if limit > 1000 {
		limit = 1000
	}
	var beforeSeq uint64
	if p.Before > 0 {
		beforeSeq = uint64(p.Before)
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
		"sessionId":          ag.ResumeSessionID(),
	})
}

func (h *handler) conversationPermissionResponse(req RPCRequest, p ConversationPermissionResponseParams) RPCResponse {
	agentID := p.AgentID
	requestID := p.RequestID
	behavior := p.Behavior

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

	resp := &agent.PermissionResponse{
		RequestID: requestID,
		Behavior:  behavior,
		Message:   p.Message,
		UpdatedInput: p.UpdatedInput,
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

func (h *handler) conversationClear(req RPCRequest, p ConversationClearParams) RPCResponse {
	agentID := p.AgentID
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	ag.EventBuf().Reset()
	if err := h.server.manager.ClearConversationEvents(agentID); err != nil {
		log.Printf("[conversationClear] failed to clear persisted history for %s: %v", agentID, err)
	}
	ag.SetStatus(agent.StatusIdle)
	ag.ResetWatcherOffset()
	h.server.broadcast(event("conversation.cleared", map[string]any{
		"nodeId":    p.NodeID,
		"agentId":   agentID,
		"sessionId": ag.ResumeSessionID(),
	}), nil)
	return okResp(req.ID, map[string]any{"ok": true})
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

func (h *handler) agentRename(req RPCRequest, p AgentRenameParams) RPCResponse {
	agentID := p.AgentID
	name := p.Name
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

func (h *handler) agentRemove(req RPCRequest, p AgentRemoveParams) RPCResponse {
	agentID := p.AgentID
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

func (h *handler) runtimeProviderFromRows(rows []map[string]any) (string, string) {
	settingsPath := h.service.FindClaudeSettings()
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
		if id := h.service.ProviderIDFromConfig(string(mergedConfig), runtimeEnv, runtimeModel); id != "" {
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
		runtimeID, runtimeReason = h.runtimeProviderFromRows(rows)
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
	params := map[string]any{
		"agentId": agentID,
		"nodeId":  h.server.nodeID,
	}
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
		mode := ag.CurrentPermissionMode()
		if mode == "" {
			mode = h.service.CurrentPermissionMode(ag.Args)
		}
		params["permissionMode"] = mode
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

func (h *handler) providerList(req RPCRequest, p ProviderListParams) RPCResponse {
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

	agentID := p.AgentID
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

func (h *handler) providerSwitch(req RPCRequest, p ProviderSwitchParams) (RPCResponse, func()) {
	providerID := p.ProviderID
	if providerID == "" {
		return errResp(req.ID, -32602, "providerId is required"), nil
	}

	agentID := p.AgentID
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

	settingsPath := h.service.FindClaudeSettings()
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
		mode := h.service.CurrentPermissionMode(targetAgent.Args)
		provider, cmd, args, env := h.service.ResolveLaunch("claude", targetAgent.Cmd, targetAgent.Args, resumeSessionID, model, mode)
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

func (h *handler) systemSuggestDirs(req RPCRequest) RPCResponse {
	home, err := os.UserHomeDir()
	if err != nil {
		return errResp(req.ID, -32000, "cannot get home dir: "+err.Error())
	}

	type dirEntry struct {
		Path        string `json:"path"`
		Display     string `json:"display"`
		HasGit      bool   `json:"hasGit"`
		IsDirectory bool   `json:"isDirectory"`
	}

	var result []dirEntry

	// Scan common project parent directories
	parentDirs := []string{
		home,
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Documents", "project"),
		filepath.Join(home, "Repo"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "workspace"),
		filepath.Join(home, "code"),
		filepath.Join(home, "src"),
		filepath.Join(home, "Downloads"),
	}

	seen := map[string]bool{}
	for _, parent := range parentDirs {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			fullPath := filepath.Join(parent, name)
			if seen[fullPath] {
				continue
			}
			seen[fullPath] = true

			rel, _ := filepath.Rel(home, fullPath)
			if rel == "" {
				rel = name
			}

			hasGit := false
			if fi, err := os.Stat(filepath.Join(fullPath, ".git")); err == nil && fi.IsDir() {
				hasGit = true
			}

			result = append(result, dirEntry{
				Path:        fullPath,
				Display:     "~/" + rel,
				HasGit:      hasGit,
				IsDirectory: true,
			})
		}
	}

	// Sort: git repos first, then alphabetical
	sort.Slice(result, func(i, j int) bool {
		if result[i].HasGit != result[j].HasGit {
			return result[i].HasGit
		}
		return result[i].Display < result[j].Display
	})

	return okResp(req.ID, map[string]any{
		"homeDir": home,
		"dirs":    result,
	})
}

func (h *handler) systemMkdir(req RPCRequest) RPCResponse {
	path, _ := req.Params["path"].(string)
	if path == "" {
		return errResp(req.ID, -32602, "path is required")
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return errResp(req.ID, -32000, "parent directory does not exist: "+filepath.Dir(path))
	}
	if !info.IsDir() {
		return errResp(req.ID, -32000, "parent is not a directory: "+filepath.Dir(path))
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if os.IsExist(err) {
			return okResp(req.ID, map[string]any{"ok": true, "existed": true})
		}
		return errResp(req.ID, -32000, "mkdir failed: "+err.Error())
	}
	return okResp(req.ID, map[string]any{"ok": true, "created": true})
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

func (h *handler) providerAdd(req RPCRequest, p ProviderAddParams) RPCResponse {
	if p.Name == "" {
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
	baseURL := p.BaseURL
	authToken := p.AuthToken
	model := p.Model

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
		id, "claude", p.Name, string(settingsJSON), now, 0,
	); err != nil {
		return errResp(req.ID, -32000, "insert provider: "+err.Error())
	}
	h.invalidateProviderCache()

	return okResp(req.ID, map[string]any{"ok": true, "id": id, "name": p.Name})
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
func (h *handler) agentAttach(req RPCRequest, p AgentAttachParams) (RPCResponse, func()) {
	pid := int(p.PID)
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

// hermesSend sends a message via the Hermes HTTP API, streaming SSE responses
// back to connected clients as conversation.message events.
func (h *handler) hermesSend(req RPCRequest, ag *agent.Agent, message string, imageFiles, imagePaths []string) RPCResponse {
	agentID := ag.ID

	// Mark this agent as actively sending so HermesDBWatcher's poll loop
	// suppresses session-switch firings until chunk.Done lands. Plan §3.6 / §4.1.
	ag.BeginSend()
	defer ag.EndSend()

	// Record user message to EventBuffer + persistent store
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

	// Broadcast user message to all clients
	broadcastData := map[string]any{
		"agentId":   agentID,
		"role":      "user",
		"text":      message,
		"imageCount": len(imageFiles),
		"timestamp": time.Now().UnixMilli(),
	}
	if len(imagePaths) > 0 {
		broadcastData["images"] = imagePaths
	}
	h.server.broadcast(event("conversation.message", broadcastData), nil)

	// Build message history from EventBuffer
	events := ag.EventBuf().Since(0)
	messages := make([]hermesclient.Message, 0, len(events))
	for _, e := range events {
		role, _ := e.Data["role"].(string)
		text, _ := e.Data["text"].(string)
		if text == "" || (role != "user" && role != "assistant") {
			continue
		}
		messages = append(messages, hermesclient.Message{Role: role, Content: text})
	}

	// Get the hermes client
	client := h.server.manager.HermesClient()

	// Get stored session ID for conversation continuity
	sessionID, _ := h.server.manager.GetResumeSessionID(agentID)

	// Call ChatCompletion with SSE streaming
	ctx := context.Background()
	chunkChan, err := client.ChatCompletion(ctx, sessionID, messages)
	if err != nil {
		return errResp(req.ID, -32000, "hermes chat completion: "+err.Error())
	}

	// Set status to working
	ag.SetStatus(agent.StatusWorking)
	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(agentID, "working")), nil)

	// Process SSE chunks with batched broadcast every 100ms
	var mu sync.Mutex
	var fullText strings.Builder
	var pendingText string
	var done bool

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			mu.Lock()
			if pendingText == "" || done {
				mu.Unlock()
				continue
			}
			text := pendingText
			pendingText = ""
			mu.Unlock()
			h.server.broadcast(event("conversation.message", map[string]any{
				"agentId":   agentID,
				"role":      "assistant",
				"text":      text,
				"partial":   true,
				"timestamp": time.Now().UnixMilli(),
			}), nil)
		}
	}()

	for chunk := range chunkChan {
		mu.Lock()
		if chunk.Error != nil {
			mu.Unlock()
			log.Printf("[Hermes] Stream error for agent %s: %v", agentID, chunk.Error)
			break
		}
		if chunk.Done {
			done = true
			// Save session ID if returned from server header
			if chunk.SessionID != "" {
				if err := h.server.manager.UpdateResumeSessionID(agentID, chunk.SessionID); err != nil {
					log.Printf("[Hermes] Failed to update session ID for %s: %v", agentID, err)
				}
				// Sync the session id into the live HermesDBWatcher so it
				// doesn't misread the next poll as a /clear-style switch.
				if hw, ok := ag.Watcher().(*watcher.HermesDBWatcher); ok {
					hw.SetSessionID(chunk.SessionID)
				}
			}
			// Flush any remaining pending text
			if pendingText != "" {
				h.server.broadcast(event("conversation.message", map[string]any{
					"agentId":   agentID,
					"role":      "assistant",
					"text":      pendingText,
					"partial":   true,
					"timestamp": time.Now().UnixMilli(),
				}), nil)
				pendingText = ""
			}
			// Persist final assistant message
			finalText := fullText.String()
			historyData := map[string]any{
				"role": "assistant",
				"text": finalText,
				"raw":  false,
			}
			if _, err := h.server.manager.RecordConversationEvent(agentID, historyData); err != nil {
				log.Printf("[Hermes] Failed to persist assistant message: %v", err)
			}
			// Broadcast final complete message
			h.server.broadcast(event("conversation.message", map[string]any{
				"agentId":   agentID,
				"role":      "assistant",
				"text":      finalText,
				"partial":   false,
				"timestamp": time.Now().UnixMilli(),
			}), nil)
			mu.Unlock()
			break
		}
		if chunk.Text != "" {
			fullText.WriteString(chunk.Text)
			pendingText += chunk.Text
		}
		mu.Unlock()
	}

	// Flush any remaining text if stream ended without Done marker
	mu.Lock()
	if pendingText != "" {
		h.server.broadcast(event("conversation.message", map[string]any{
			"agentId":   agentID,
			"role":      "assistant",
			"text":      pendingText,
			"partial":   true,
			"timestamp": time.Now().UnixMilli(),
		}), nil)
		pendingText = ""
	}
	mu.Unlock()

	// Set status back to idle
	ag.SetStatus(agent.StatusIdle)
	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(agentID, "idle")), nil)

	return okResp(req.ID, map[string]any{"id": agentID})
}
