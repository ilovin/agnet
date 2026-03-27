package ws

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
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
	case "opencode.discover":
		return h.opencodeDiscover(req), nil
	case "conversation.send":
		return h.conversationSend(req), nil
	case "conversation.history":
		return h.conversationHistory(req), nil
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method), nil
	}
}

func (h *handler) agentList(req RPCRequest) RPCResponse {
	agents := h.server.manager.List()
	type agentInfo struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		WorkDir  string `json:"workDir"`
		Status   string `json:"status"`
	}
	result := make([]agentInfo, 0, len(agents))
	for _, ag := range agents {
		result = append(result, agentInfo{
			ID: ag.ID, Name: ag.Name, Provider: ag.Provider,
			WorkDir: ag.WorkDir, Status: string(ag.Status()),
		})
	}
	return okResp(req.ID, result)
}

func (h *handler) agentCreate(req RPCRequest) (RPCResponse, func()) {
	name, _ := req.Params["name"].(string)
	provider, _ := req.Params["provider"].(string)
	cmd, _ := req.Params["cmd"].(string)
	workDir, _ := req.Params["workDir"].(string)
	sessionID, _ := req.Params["sessionId"].(string)

	var args []string
	if raw, ok := req.Params["args"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &args)
	}

	// Determine command based on provider
	switch provider {
	case "opencode":
		cmd = "opencode"
		if sessionID != "" {
			args = []string{"-s", sessionID}
		} else {
			args = []string{}
		}
	default: // "claude" or empty
		if cmd == "" {
			cmd = "claude"
			args = []string{"--dangerously-skip-permissions"}
		}
	}

	if workDir == "" {
		workDir = "/tmp"
	}

	id, err := h.server.manager.Create(name, provider, cmd, args, workDir)
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
	newID, err := h.server.manager.Create(ag.Name, ag.Provider, ag.Cmd, ag.Args, ag.WorkDir)
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
	if message == "" {
		return errResp(req.ID, -32602, "message is required")
	}
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	if err := ag.WriteInput(message + "\n"); err != nil {
		return errResp(req.ID, -32000, "write to agent: "+err.Error())
	}
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) conversationHistory(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	var afterSeq uint64
	if v, ok := req.Params["cursor"].(float64); ok {
		afterSeq = uint64(v)
	}
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	events := ag.EventBuf().Since(afterSeq)
	return okResp(req.ID, map[string]any{
		"events":  events,
		"lastSeq": ag.EventBuf().LastSeq(),
	})
}

// opencodeDiscover scans for available opencode sessions on the machine.
func (h *handler) opencodeDiscover(req RPCRequest) RPCResponse {
	home, err := os.UserHomeDir()
	if err != nil {
		return errResp(req.ID, -32000, "cannot get home dir: "+err.Error())
	}

	// Scan session_diff directory
	sessionDir := filepath.Join(home, ".local", "share", "opencode", "storage", "session_diff")
	type sessionInfo struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		ModifiedAt time.Time `json:"modifiedAt"`
		Size       int64     `json:"size"`
	}

	var sessions []sessionInfo

	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		// Directory might not exist, return empty list
		return okResp(req.ID, map[string]any{
			"sessions": sessions,
		})
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Extract session ID from filename (ses_xxx.json -> ses_xxx)
		sessionID := strings.TrimSuffix(name, ".json")
		if !strings.HasPrefix(sessionID, "ses_") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		sessions = append(sessions, sessionInfo{
			ID:         sessionID,
			Name:       sessionID,
			ModifiedAt: info.ModTime(),
			Size:       info.Size(),
		})
	}

	return okResp(req.ID, map[string]any{
		"sessions": sessions,
	})
}
