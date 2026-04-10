package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/discover"
	"github.com/phone-talk/agentgw/internal/node"
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
	case "node.discover":
		return dispatchResult{resp: h.nodeDiscover(req)}
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

// nodeWithAgentCount wraps a node with its agent count for sorting.
type nodeWithAgentCount struct {
	node  *node.Node
	count int
}

func (h *handler) nodeList(req RPCRequest) RPCResponse {
	nodes := h.server.manager.List()

	// Sort nodes: those with agents come first, then by status
	type nodeWithCount struct {
		node      *node.Node
		agentCount int
	}
	wrapped := make([]nodeWithCount, 0, len(nodes))
	for _, n := range nodes {
		count := 0
		if p := n.GetProxy(); p != nil {
			if result, err := p.Call("agent.list", nil, 5*time.Second); err == nil {
				if m, ok := result.(map[string]any); ok {
					if agents, ok := m["agents"].([]any); ok {
						count = len(agents)
					}
				} else if agents, ok := result.([]any); ok {
					count = len(agents)
				}
			}
		}
		wrapped = append(wrapped, nodeWithCount{node: n, agentCount: count})
	}

	// Sort: nodes with agents first, then by status priority
	sort.Slice(wrapped, func(i, j int) bool {
		if wrapped[i].agentCount != wrapped[j].agentCount {
			return wrapped[i].agentCount > wrapped[j].agentCount
		}
		// Status priority: connected > connecting > error > disconnected
		priority := map[string]int{
			"connected":    0,
			"connecting":   1,
			"deploying":    2,
			"error":        3,
			"disconnected": 4,
		}
		pi := priority[string(wrapped[i].node.GetStatus())]
		pj := priority[string(wrapped[j].node.GetStatus())]
		if pi != pj {
			return pi < pj
		}
		return wrapped[i].node.Name < wrapped[j].node.Name
	})

	type nodeLocation struct {
		Type            string `json:"type"`
		Host            string `json:"host"`
		DisplayLocation string `json:"displayLocation"`
	}
	type nodeInfo struct {
		ID         string       `json:"id"`
		Name       string       `json:"name"`
		Host       string       `json:"host"`
		Status     string       `json:"status"`
		Location   nodeLocation `json:"location"`
		AgentCount int          `json:"agentCount"`
	}
	result := make([]nodeInfo, 0, len(wrapped))
	for _, w := range wrapped {
		locType := "remote"
		if w.node.IsLocal() {
			locType = "local"
		}
		result = append(result, nodeInfo{
			ID:         w.node.ID,
			Name:       w.node.Name,
			Host:       w.node.Host,
			Status:     string(w.node.GetStatus()),
			AgentCount: w.agentCount,
			Location: nodeLocation{
				Type:            locType,
				Host:            w.node.Host,
				DisplayLocation: w.node.DisplayLocation(),
			},
		})
	}
	return okResp(req.ID, result)
}

// nodeDiscover scans SSH config and discovers nodes with agentd running.
func (h *handler) nodeDiscover(req RPCRequest) RPCResponse {
	// Parse SSH config
	sshHosts, err := nodecfg.ParseSSHConfig()
	if err != nil {
		return errResp(req.ID, -32000, fmt.Sprintf("parse ssh config: %v", err))
	}

	// Filter out already configured hosts
	existing := h.server.manager.List()
	existingMap := make(map[string]bool)
	for _, n := range existing {
		existingMap[n.Host] = true
		if n.SSHAlias != "" {
			existingMap[n.SSHAlias] = true
		}
	}

	var toProbe []nodecfg.SSHHost
	for _, h := range sshHosts {
		if !existingMap[h.HostName] && !existingMap[h.Name] {
			toProbe = append(toProbe, h)
		}
	}

	// Probe hosts for agentd
	prober := discover.NewProber()
	results := prober.Discover(toProbe)

	// Collect found nodes
	found := make([]map[string]any, 0)
	for _, r := range results {
		if r.Found {
			found = append(found, map[string]any{
				"id":       r.NodeID,
				"name":     r.Host.Name,
				"host":     r.Host.HostName,
				"sshAlias": r.Host.Name,
				"sshPort":  r.Host.Port,
			})
		}
	}

	return okResp(req.ID, map[string]any{
		"scanned": len(toProbe),
		"found":   found,
	})
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

	// Inject nodeLocation for agent.list and session.catalog responses
	if req.Method == "agent.list" || req.Method == "session.catalog" {
		result = injectNodeLocation(result, n)
	}

	return okResp(req.ID, result)
}

// injectNodeLocation adds node location info to agent/session responses.
// For agent.list: injects into each agent object.
// For session.catalog: injects into managed and attachable arrays.
func injectNodeLocation(result any, n *node.Node) any {
	resMap, ok := result.(map[string]any)
	if !ok {
		return result
	}

	nodeLoc := map[string]any{
		"nodeId":      n.ID,
		"displayName": n.Name,
		"host":        n.Host,
	}

	// Create a copy to avoid mutating the original
	newResult := make(map[string]any, len(resMap))
	for k, v := range resMap {
		newResult[k] = v
	}

	// Inject into agents array if present
	if agents, ok := resMap["agents"].([]any); ok {
		newAgents := make([]any, len(agents))
		for i, a := range agents {
			if agentMap, ok := a.(map[string]any); ok {
				newAgent := make(map[string]any, len(agentMap)+1)
				for k, v := range agentMap {
					newAgent[k] = v
				}
				newAgent["nodeLocation"] = nodeLoc
				newAgents[i] = newAgent
			} else {
				newAgents[i] = a
			}
		}
		newResult["agents"] = newAgents
	}

	// Inject into managed array if present
	if managed, ok := resMap["managed"].([]any); ok {
		newManaged := make([]any, len(managed))
		for i, m := range managed {
			if mMap, ok := m.(map[string]any); ok {
				newM := make(map[string]any, len(mMap)+1)
				for k, v := range mMap {
					newM[k] = v
				}
				newM["nodeLocation"] = nodeLoc
				newManaged[i] = newM
			} else {
				newManaged[i] = m
			}
		}
		newResult["managed"] = newManaged
	}

	// Inject into attachable array if present
	if attachable, ok := resMap["attachable"].([]any); ok {
		newAttachable := make([]any, len(attachable))
		for i, a := range attachable {
			if aMap, ok := a.(map[string]any); ok {
				newA := make(map[string]any, len(aMap)+1)
				for k, v := range aMap {
					newA[k] = v
				}
				newA["nodeLocation"] = nodeLoc
				newAttachable[i] = newA
			} else {
				newAttachable[i] = a
			}
		}
		newResult["attachable"] = newAttachable
	}

	return newResult
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
