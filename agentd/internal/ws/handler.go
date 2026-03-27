package ws

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

type handler struct {
	server *Server
	conn   *websocket.Conn
}

func (h *handler) loop() {
	for {
		_, msg, err := h.conn.ReadMessage()
		if err != nil {
			return
		}
		var req RPCRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = h.conn.WriteJSON(errResp(nil, -32700, "parse error"))
			continue
		}
		resp, afterSend := h.dispatch(req)
		if err := h.conn.WriteJSON(resp); err != nil {
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
		return h.agentRestart(req), nil
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
	cmd, _ := req.Params["cmd"].(string)
	workDir, _ := req.Params["workDir"].(string)

	var args []string
	if raw, ok := req.Params["args"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &args)
	}

	if cmd == "" {
		cmd = "claude"
		args = []string{"--dangerously-skip-permissions"}
	}
	if workDir == "" {
		workDir = "/tmp"
	}

	id, err := h.server.manager.Create(name, cmd, args, workDir)
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

func (h *handler) agentRestart(req RPCRequest) RPCResponse {
	id, _ := req.Params["agentId"].(string)
	ag := h.server.manager.Get(id)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	_ = h.server.manager.Stop(id)
	newID, err := h.server.manager.Create(ag.Name, ag.Cmd, ag.Args, ag.WorkDir)
	if err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	return okResp(req.ID, map[string]any{"id": newID})
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
