package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/nodecfg"
)

type handler struct {
	server *Server
	conn   *websocket.Conn
	self   *client
}

// dispatchResult bundles an RPC response with an optional post-send callback.
// postSend (if non-nil) is called after the response has been written to the client,
// ensuring the RPC reply is delivered before any broadcasts.
type dispatchResult struct {
	resp     RPCResponse
	postSend func()
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
		dr := h.dispatch(req)
		if err := h.self.writeJSON(dr.resp); err != nil {
			log.Printf("ws write: %v", err)
			return
		}
		if dr.postSend != nil {
			go dr.postSend()
		}
	}
}

func (h *handler) dispatch(req RPCRequest) dispatchResult {
	switch req.Method {
	case "node.list":
		return dispatchResult{resp: h.nodeList(req)}
	case "node.add":
		return h.nodeAdd(req)
	case "node.remove":
		return dispatchResult{resp: h.nodeRemove(req)}
	case "node.connect":
		return h.nodeConnect(req)
	case "node.deploy":
		return h.nodeDeploy(req)
	case "agent.list", "agent.create", "agent.stop", "agent.restart",
		"conversation.history", "conversation.send", "conversation.key",
		"session.list", "session.create", "session.attach", "session.catalog":
		return dispatchResult{resp: h.proxyToNode(req)}
	case "session.list_all":
		return dispatchResult{resp: h.sessionListAll(req)}
	case "session.catalog_all":
		return dispatchResult{resp: h.sessionCatalogAll(req)}
	case "system.health":
		return dispatchResult{resp: h.systemHealth(req)}
	default:
		return dispatchResult{resp: errResp(req.ID, -32601, "method not found: "+req.Method)}
	}
}

func (h *handler) nodeList(req RPCRequest) RPCResponse {
	nodes := h.server.manager.List()
	type nodeInfo struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Host   string `json:"host"`
		Status string `json:"status"`
	}
	result := make([]nodeInfo, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, nodeInfo{
			ID: n.ID, Name: n.Name, Host: n.Host, Status: string(n.GetStatus()),
		})
	}
	return okResp(req.ID, result)
}

func (h *handler) nodeAdd(req RPCRequest) dispatchResult {
	name, _ := req.Params["name"].(string)
	host, _ := req.Params["host"].(string)
	token, _ := req.Params["token"].(string)
	sshKeyPath, _ := req.Params["sshKeyPath"].(string)

	sshPort := 22
	if v, ok := req.Params["sshPort"].(float64); ok {
		sshPort = int(v)
	}
	agentdPort := 7373
	if v, ok := req.Params["agentdPort"].(float64); ok {
		agentdPort = int(v)
	}

	if host == "" {
		return dispatchResult{resp: errResp(req.ID, -32602, "host is required")}
	}

	id, err := h.server.manager.Add(nodecfg.NodeEntry{
		Name: name, Host: host,
		SSHPort: sshPort, AgentdPort: agentdPort,
		Token: token, SSHKeyPath: sshKeyPath,
	})
	if err != nil {
		return dispatchResult{resp: errResp(req.ID, -32000, err.Error())}
	}

	// postSend runs after the RPC response is written, so broadcasts don't
	// race with the response delivery on this connection.
	postSend := func() {
		h.server.Broadcast(newEvent("node.status_changed", map[string]any{
			"nodeId": id, "status": "disconnected",
		}))
		if err := h.server.manager.Connect(id); err != nil {
			log.Printf("auto-connect node %s: %v", id, err)
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": id, "status": "error",
			}))
		} else {
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": id, "status": "connected",
			}))
		}
	}

	return dispatchResult{
		resp:     okResp(req.ID, map[string]any{"nodeId": id}),
		postSend: postSend,
	}
}

func (h *handler) nodeRemove(req RPCRequest) RPCResponse {
	nodeID, _ := req.Params["nodeId"].(string)
	if err := h.server.manager.Remove(nodeID); err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) nodeConnect(req RPCRequest) dispatchResult {
	nodeID, _ := req.Params["nodeId"].(string)
	n := h.server.manager.Get(nodeID)
	if n == nil {
		return dispatchResult{resp: errResp(req.ID, -32000, fmt.Sprintf("node %q not found", nodeID))}
	}
	postSend := func() {
		if err := h.server.manager.Connect(nodeID); err != nil {
			log.Printf("connect node %s: %v", nodeID, err)
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": nodeID, "status": "error",
			}))
		} else {
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": nodeID, "status": "connected",
			}))
		}
	}
	return dispatchResult{
		resp:     okResp(req.ID, map[string]any{"ok": true, "message": "connecting"}),
		postSend: postSend,
	}
}

func (h *handler) nodeDeploy(req RPCRequest) dispatchResult {
	nodeID, _ := req.Params["nodeId"].(string)
	remoteDir, _ := req.Params["remoteDir"].(string)
	if remoteDir == "" {
		remoteDir = "/opt/agentd"
	}
	n := h.server.manager.Get(nodeID)
	if n == nil {
		return dispatchResult{resp: errResp(req.ID, -32000, fmt.Sprintf("node %q not found", nodeID))}
	}
	postSend := func() {
		h.server.Broadcast(newEvent("node.status_changed", map[string]any{
			"nodeId": nodeID, "status": "deploying",
		}))
		if err := h.server.manager.Deploy(nodeID, remoteDir); err != nil {
			log.Printf("deploy node %s: %v", nodeID, err)
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": nodeID, "status": "error", "error": err.Error(),
			}))
		} else {
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": nodeID, "status": "deployed",
			}))
			// Auto-connect after successful deploy
			if err := h.server.manager.Connect(nodeID); err != nil {
				log.Printf("auto-connect node %s after deploy: %v", nodeID, err)
				h.server.Broadcast(newEvent("node.status_changed", map[string]any{
					"nodeId": nodeID, "status": "error", "error": err.Error(),
				}))
			} else {
				h.server.Broadcast(newEvent("node.status_changed", map[string]any{
					"nodeId": nodeID, "status": "connected",
				}))
			}
		}
	}
	return dispatchResult{
		resp:     okResp(req.ID, map[string]any{"ok": true, "message": "deploy started"}),
		postSend: postSend,
	}
}

func (h *handler) sessionListAll(req RPCRequest) RPCResponse {
	nodes := h.server.manager.List()
	items := make([]map[string]any, 0)
	errors := make([]map[string]any, 0)

	for _, n := range nodes {
		result, err := h.server.manager.ForwardCall(n.ID, "session.list", nil, 30*time.Second)
		if err != nil {
			errors = append(errors, map[string]any{
				"nodeId": n.ID,
				"error":  err.Error(),
			})
			continue
		}

		resMap, ok := result.(map[string]any)
		if !ok {
			errors = append(errors, map[string]any{
				"nodeId": n.ID,
				"error":  "invalid session.list response",
			})
			continue
		}

		rawProcesses, ok := resMap["processes"].([]any)
		if !ok {
			errors = append(errors, map[string]any{
				"nodeId": n.ID,
				"error":  "missing processes in session.list response",
			})
			continue
		}

		for _, raw := range rawProcesses {
			proc, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			item := make(map[string]any, len(proc)+1)
			for k, v := range proc {
				item[k] = v
			}
			item["nodeId"] = n.ID
			items = append(items, item)
		}
	}

	return okResp(req.ID, map[string]any{
		"items":  items,
		"errors": errors,
	})
}

func (h *handler) sessionCatalogAll(req RPCRequest) RPCResponse {
	nodes := h.server.manager.List()
	items := make([]map[string]any, 0, len(nodes))
	errors := make([]map[string]any, 0)

	for _, n := range nodes {
		result, err := h.server.manager.ForwardCall(n.ID, "session.catalog", nil, 30*time.Second)
		if err != nil {
			errors = append(errors, map[string]any{
				"nodeId": n.ID,
				"error":  err.Error(),
			})
			continue
		}

		resMap, ok := result.(map[string]any)
		if !ok {
			errors = append(errors, map[string]any{
				"nodeId": n.ID,
				"error":  "invalid session.catalog response",
			})
			continue
		}

		item := map[string]any{
			"nodeId":        n.ID,
			"managed":       []any{},
			"attachable":    []any{},
			"opencodeFiles": []any{},
		}
		if managed, ok := resMap["managed"].([]any); ok {
			item["managed"] = managed
		}
		if attachable, ok := resMap["attachable"].([]any); ok {
			item["attachable"] = attachable
		}
		if files, ok := resMap["opencodeFiles"].([]any); ok {
			item["opencodeFiles"] = files
		}

		items = append(items, item)
	}

	return okResp(req.ID, map[string]any{
		"items":  items,
		"errors": errors,
	})
}

func (h *handler) proxyToNode(req RPCRequest) RPCResponse {
	nodeID, _ := req.Params["nodeId"].(string)
	n := h.server.manager.Get(nodeID)
	if n == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("node %q not found", nodeID))
	}
	p := n.GetProxy()
	if p == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("node %q not connected", nodeID))
	}

	forwardParams := make(map[string]any)
	for k, v := range req.Params {
		if k != "nodeId" {
			forwardParams[k] = v
		}
	}

	result, err := p.Call(req.Method, forwardParams, 30*time.Second)
	if err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	return okResp(req.ID, result)
}

func (h *handler) systemHealth(req RPCRequest) RPCResponse {
	nodes := h.server.manager.List()

	type nodeHealth struct {
		Status    string `json:"status"`
		LatencyMs int64  `json:"latency_ms"`
		Agents    int    `json:"agents"`
		Error     string `json:"error,omitempty"`
	}

	nodesHealth := make(map[string]nodeHealth, len(nodes))
	healthyCount := 0
	connectedCount := 0

	for _, n := range nodes {
		status := string(n.GetStatus())
		nh := nodeHealth{Status: status}

		p := n.GetProxy()
		if p != nil {
			connectedCount++
			// Ping the node to measure latency and verify responsiveness
			start := time.Now()
			result, err := p.Call("agent.list", nil, 5*time.Second)
			nh.LatencyMs = time.Since(start).Milliseconds()

			if err != nil {
				nh.Status = "unresponsive"
				nh.Error = err.Error()
			} else {
				healthyCount++
				nh.Status = "connected"
				if m, ok := result.(map[string]any); ok {
					if agents, ok := m["agents"].([]any); ok {
						nh.Agents = len(agents)
					}
				}
			}
		}

		nodesHealth[n.Name] = nh
	}

	overallStatus := "down"
	if len(nodes) == 0 {
		overallStatus = "healthy" // no nodes configured is ok
	} else if healthyCount == len(nodes) {
		overallStatus = "healthy"
	} else if healthyCount > 0 || connectedCount > 0 {
		overallStatus = "degraded"
	}

	return okResp(req.ID, map[string]any{
		"status":         overallStatus,
		"nodes":          nodesHealth,
		"uptime_seconds": int64(time.Since(h.server.startTime).Seconds()),
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	})
}
