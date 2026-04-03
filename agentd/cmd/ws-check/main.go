package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	// agentd token
	agentdToken := "test-token"
	if data, err := os.ReadFile(os.Getenv("HOME") + "/.agentd/config.yaml"); err == nil {
		agentdToken = extractToken(string(data))
	}

	// agentgw token
	agentgwToken := "test-token"
	if data, err := os.ReadFile(os.Getenv("HOME") + "/.agentgw/config.yaml"); err == nil {
		agentgwToken = extractToken(string(data))
	}

	// 测试 agentd
	testAgentd(7373, agentdToken)

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("对比: 通过 agentgw 访问")
	fmt.Println(strings.Repeat("=", 60))

	// 测试 agentgw
	testAgentgw(7374, agentgwToken)
}

func extractToken(data string) string {
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "token:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "token:"))
		}
	}
	return "test-token"
}

func testAgentd(port int, token string) {
	agents := callWS(port, "agentd", token, "agent.list", nil)
	if agents == nil {
		return
	}
	printAgents(agents)
}

func testAgentgw(port int, token string) {
	// 先获取节点列表
	nodes := callWS(port, "agentgw", token, "node.list", nil)
	if nodes == nil {
		return
	}
	printNodes(nodes)

	// 获取每个节点的 agent
	if nodeList, ok := nodes.([]any); ok && len(nodeList) > 0 {
		for _, n := range nodeList {
			node := n.(map[string]any)
			nodeID := node["id"].(string)
			fmt.Printf("\n📍 节点 %s (%s) 的 Agents:\n", node["name"], nodeID)
			agents := callWS(port, "agentgw", token, "agent.list", map[string]any{"nodeId": nodeID})
			if agents != nil {
				printAgents(agents)
				// 查询每个 agent 的会话历史
				if agentList, ok := agents.([]any); ok {
					for _, a := range agentList {
						ag := a.(map[string]any)
						agentID := ag["id"].(string)
						agentName := ag["name"].(string)
						fmt.Printf("\n  💬 Agent %s 的会话历史:\n", agentName)
						history := callWS(port, "agentgw", token, "conversation.history", map[string]any{
							"nodeId":  nodeID,
							"agentId": agentID,
						})
						if history != nil {
							printHistory(history)
						}
					}
				}
			}
		}
	}
}

func callWS(port int, name, token, method string, params map[string]any) any {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second

	wsURL := fmt.Sprintf("ws://localhost:%d/ws?token=%s", port, token)

	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			fmt.Printf("✗ %s (port %d): %v (status: %d)\n", name, port, err, resp.StatusCode)
		} else {
			fmt.Printf("✗ %s (port %d): %v\n", name, port, err)
		}
		return nil
	}
	defer conn.Close()

	fmt.Printf("✓ Connected to %s (port %d)\n", name, port)

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
	if params == nil {
		req["params"] = map[string]any{}
	}

	if err := conn.WriteJSON(req); err != nil {
		fmt.Printf("✗ Send error: %v\n", err)
		return nil
	}

	var respData map[string]any
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.ReadJSON(&respData); err != nil {
		fmt.Printf("✗ Read error: %v\n", err)
		return nil
	}

	if err := respData["error"]; err != nil {
		fmt.Printf("✗ RPC error: %v\n", err)
		return nil
	}

	return respData["result"]
}

func printAgents(result any) {
	agents, ok := result.([]any)
	if !ok {
		fmt.Println("  (无效的 agent 数据)")
		return
	}

	fmt.Printf("📊 Agent 总数: %d\n", len(agents))

	if len(agents) == 0 {
		fmt.Println("   (暂无 agent)")
		return
	}

	statusIcons := map[string]string{
		"idle":     "🟡",
		"working":  "🔵",
		"stopped":  "⚫",
		"crashed":  "🔴",
		"starting": "🔄",
	}

	for i, a := range agents {
		ag := a.(map[string]any)
		status, _ := ag["status"].(string)
		icon := statusIcons[status]
		if icon == "" {
			icon = "⚪"
		}

		name, _ := ag["name"].(string)
		if name == "" {
			name = "unnamed"
		}

		fmt.Printf("\n  [%d] %s %s\n", i+1, icon, name)
		fmt.Printf("      ID:       %s\n", ag["id"])
		fmt.Printf("      Provider: %s\n", ag["provider"])
		fmt.Printf("      Status:   %s\n", status)
		fmt.Printf("      WorkDir:  %s\n", ag["workDir"])
	}
}

func printNodes(result any) {
	nodes, ok := result.([]any)
	if !ok {
		fmt.Println("  (无效的节点数据)")
		return
	}

	fmt.Printf("📍 节点总数: %d\n", len(nodes))

	if len(nodes) == 0 {
		fmt.Println("   (暂无节点，请先添加节点)")
		return
	}

	statusIcons := map[string]string{
		"connected":    "🟢",
		"disconnected": "🔴",
		"connecting":   "🔄",
		"error":        "⚠️",
	}

	for i, n := range nodes {
		node := n.(map[string]any)
		status, _ := node["status"].(string)
		icon := statusIcons[status]
		if icon == "" {
			icon = "⚪"
		}

		name, _ := node["name"].(string)
		if name == "" {
			name = "unnamed"
		}

		fmt.Printf("\n  [%d] %s %s\n", i+1, icon, name)
		fmt.Printf("      ID:     %s\n", node["id"])
		fmt.Printf("      Host:   %s\n", node["host"])
		fmt.Printf("      Status: %s\n", status)
	}
}

func printHistory(result any) {
	hist, ok := result.(map[string]any)
	if !ok {
		fmt.Println("    (无效的会话数据)")
		return
	}

	events, ok := hist["events"].([]any)
	if !ok || len(events) == 0 {
		fmt.Println("    (无会话历史)")
		return
	}

	fmt.Printf("    共 %d 条事件:\n", len(events))

	for i, e := range events {
		if i >= 10 {
			fmt.Printf("    ... 还有 %d 条\n", len(events)-10)
			break
		}
		event := e.(map[string]any)
		seq, _ := event["seq"].(float64)
		data, _ := event["data"].(map[string]any)

		var role, text string
		if data != nil {
			role, _ = data["role"].(string)
			text, _ = data["text"].(string)
		}

		if text == "" {
			text = "(无内容)"
		}
		if len(text) > 80 {
			text = text[:77] + "..."
		}

		roleIcon := "🤖"
		if role == "user" {
			roleIcon = "👤"
		}
		if role == "" {
			role = "system"
			roleIcon = "⚙️"
		}

		fmt.Printf("      [%d] %s %s: %s\n", int(seq), roleIcon, role, text)
	}
}
