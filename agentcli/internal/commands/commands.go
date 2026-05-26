package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/phone-talk/agentcli/internal/client"
	"github.com/spf13/cobra"
)

var (
	cliClient  *client.Client
	outputJSON bool
)

func SetClient(c *client.Client) {
	cliClient = c
}

func output(data any) {
	if outputJSON {
		b, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(b))
		return
	}

	switch v := data.(type) {
	case map[string]any:
		for key, val := range v {
			fmt.Printf("%s: %v\n", key, val)
		}
	case []any:
		for _, item := range v {
			fmt.Printf("- %v\n", item)
		}
	default:
		fmt.Printf("%v\n", data)
	}
}

func toAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []any:
		return s
	default:
		return nil
	}
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case float64:
		if s == float64(int64(s)) {
			return fmt.Sprintf("%.0f", s)
		}
		return fmt.Sprintf("%v", s)
	case int:
		return fmt.Sprintf("%d", s)
	case bool:
		return fmt.Sprintf("%t", s)
	default:
		return fmt.Sprintf("%v", s)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// fetchLastMessage fetches the last meaningful message text for an agent.
func fetchLastMessage(nodeID, agentID string) string {
	resp, err := cliClient.Call("conversation.history", map[string]any{
		"agentId": agentID,
		"nodeId":  nodeID,
		"limit":   10,
	})
	if err != nil || resp.Error != nil {
		return ""
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		return ""
	}
	events := toAnySlice(result["events"])
	for i := len(events) - 1; i >= 0; i-- {
		if ev, ok := events[i].(map[string]any); ok {
			role := stringify(ev["role"])
			text := stringify(ev["text"])
			if text == "" {
				text = stringify(ev["content"])
			}
			text = strings.TrimSpace(text)
			if text == "" || text == "[Agent]" || len(text) < 5 {
				continue
			}
			if role == "user" || len(text) > 20 {
				lines := strings.Split(text, "\n")
				lastLine := strings.TrimSpace(lines[len(lines)-1])
				if len(lastLine) > 60 {
					lastLine = lastLine[:57] + "..."
				}
				return lastLine
			}
		}
	}
	return ""
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agentcli",
		Short: "CLI client for phone-talk agent gateway",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			url, _ := cmd.Flags().GetString("url")
			token, _ := cmd.Flags().GetString("token")

			if url == "" {
				url = os.Getenv("AGENTGW_URL")
			}
			if token == "" {
				token = os.Getenv("AGENTGW_TOKEN")
			}

			if url == "" {
				return fmt.Errorf("--url or AGENTGW_URL required")
			}
			if token == "" {
				return fmt.Errorf("--token or AGENTGW_TOKEN required")
			}

			if !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
				url = "ws://" + url
			}

			cliClient = client.New(url, token)
			if err := cliClient.Connect(); err != nil {
				return err
			}

			outputJSON, _ = cmd.Flags().GetBool("json")
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if cliClient != nil {
				cliClient.Close()
			}
		},
	}

	root.PersistentFlags().String("url", "", "WebSocket URL (env: AGENTGW_URL)")
	root.PersistentFlags().String("token", "", "Auth token (env: AGENTGW_TOKEN)")
	root.PersistentFlags().Bool("json", false, "Output in JSON format")

	root.AddCommand(newListAgentsCmd())
	root.AddCommand(newSendMessageCmd())
	root.AddCommand(newWatchEventsCmd())
	root.AddCommand(newAgentStatusCmd())
	root.AddCommand(newListNodesCmd())
	root.AddCommand(newDashboardCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newInteractiveCmd())

	return root
}

func getLocalNodeID() (string, error) {
	resp, err := cliClient.Call("node.list", nil)
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("RPC error: %s", resp.Error.Message)
	}

	nodes, ok := resp.Result.([]any)
	if !ok {
		return "", fmt.Errorf("unexpected node.list response format")
	}

	for _, raw := range nodes {
		if n, ok := raw.(map[string]any); ok {
			if stringify(n["name"]) == "local" {
				return stringify(n["id"]), nil
			}
		}
	}

	for _, raw := range nodes {
		if n, ok := raw.(map[string]any); ok {
			if stringify(n["status"]) == "connected" {
				return stringify(n["id"]), nil
			}
		}
	}

	return "", fmt.Errorf("no connected node found")
}

// nodeRef identifies a node when iterating multi-node aggregations.
type nodeRef struct {
	ID   string
	Name string
}

// getAllConnectedNodes returns every node currently reported as connected
// by the gateway. Used by list-agents and dashboard to aggregate state
// across local + remote nodes (e.g. local + oracle).
func getAllConnectedNodes() ([]nodeRef, error) {
	resp, err := cliClient.Call("node.list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", resp.Error.Message)
	}

	nodes, ok := resp.Result.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected node.list response format")
	}

	out := make([]nodeRef, 0, len(nodes))
	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringify(n["status"]) != "connected" {
			continue
		}
		out = append(out, nodeRef{
			ID:   stringify(n["id"]),
			Name: stringify(n["name"]),
		})
	}
	return out, nil
}

func newListAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-agents",
		Short: "List all agents with status",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := getAllConnectedNodes()
			if err != nil {
				return err
			}
			if len(nodes) == 0 {
				return fmt.Errorf("no connected node found")
			}

			type agentWithNode struct {
				agent  map[string]any
				nodeID string
			}

			var collected []agentWithNode
			var allAgents []map[string]any
			var rpcErrs []string
			for _, n := range nodes {
				resp, err := cliClient.Call("agent.list", map[string]any{"nodeId": n.ID})
				if err != nil {
					rpcErrs = append(rpcErrs, fmt.Sprintf("%s: %v", n.Name, err))
					continue
				}
				if resp.Error != nil {
					rpcErrs = append(rpcErrs, fmt.Sprintf("%s: %s", n.Name, resp.Error.Message))
					continue
				}
				agents, ok := resp.Result.([]any)
				if !ok {
					continue
				}
				for _, raw := range agents {
					ag, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					// Annotate each agent with its source node so JSON
					// consumers (and the table renderer) can distinguish.
					ag["nodeId"] = n.ID
					ag["nodeName"] = n.Name
					collected = append(collected, agentWithNode{agent: ag, nodeID: n.ID})
					allAgents = append(allAgents, ag)
				}
			}

			if outputJSON {
				output(allAgents)
				return nil
			}

			if len(allAgents) == 0 {
				if len(rpcErrs) > 0 {
					fmt.Fprintf(os.Stderr, "warning: agent.list errors: %s\n", strings.Join(rpcErrs, "; "))
				}
				fmt.Println("No agents.")
				return nil
			}

			fmt.Printf("%-3s %-10s %-12s %-20s %-10s %-8s %-15s %-10s %-12s  %s\n",
				"", "NODE", "STATUS", "NAME", "PROVIDER", "PID", "PROJECT", "RUNTIME", "S-STATE", "MESSAGE")
			for _, item := range collected {
				ag := item.agent
				status := stringify(ag["status"])
				name := stringify(ag["name"])
				provider := stringify(ag["provider"])
				pid := 0
				if p, ok := ag["pid"].(float64); ok {
					pid = int(p)
				}
				project := stringify(ag["projectName"])
				if project == "" {
					project = stringify(ag["workDir"])
				}
				runtimeState := stringify(ag["runtimeState"])
				sessionState := stringify(ag["sessionState"])
				agentID := stringify(ag["id"])
				nodeName := stringify(ag["nodeName"])

				statusIcon := "●"
				if status == "working" {
					statusIcon = "🟢"
				} else if status == "idle" {
					statusIcon = "🟡"
				} else if status == "stopped" || status == "crashed" {
					statusIcon = "🔴"
				}

				runtimeIcon := ""
				if runtimeState == "live" {
					runtimeIcon = "🟢"
				} else if runtimeState == "exited" {
					runtimeIcon = "⚫"
				}

				lastMsg := fetchLastMessage(item.nodeID, agentID)

				fmt.Printf("%-3s %-10s %-12s %-20s %-10s %-8d %-15s %-10s %-12s  %s\n",
					statusIcon, truncate(nodeName, 10), status, truncate(name, 20), provider, pid, truncate(project, 15),
					runtimeIcon+runtimeState, sessionState, lastMsg)
			}
			if len(rpcErrs) > 0 {
				fmt.Fprintf(os.Stderr, "warning: agent.list errors: %s\n", strings.Join(rpcErrs, "; "))
			}
			return nil
		},
	}
}

func newSendMessageCmd() *cobra.Command {
	var agentID, message string

	cmd := &cobra.Command{
		Use:   "send-message",
		Short: "Send a message to an agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentID == "" {
				return fmt.Errorf("--agent-id required")
			}
			if message == "" {
				return fmt.Errorf("--message required")
			}

			nodeID, err := getLocalNodeID()
			if err != nil {
				return err
			}

			resp, err := cliClient.Call("conversation.send", map[string]any{
				"agentId": agentID,
				"message": message,
				"nodeId":  nodeID,
			})
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("RPC error: %s", resp.Error.Message)
			}
			output(resp.Result)
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Target agent ID")
	cmd.Flags().StringVar(&message, "message", "", "Message to send")

	return cmd
}

func newWatchEventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch-events",
		Short: "Watch real-time events from the gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			events := []string{
				"agent.status_changed",
				"conversation.message",
				"client.connected",
				"client.disconnected",
			}

			for _, event := range events {
				eventName := event
				cliClient.OnEvent(eventName, func(params any) {
					if outputJSON {
						b, _ := json.Marshal(map[string]any{
							"event":  eventName,
							"params": params,
							"time":   time.Now().Format(time.RFC3339),
						})
						fmt.Println(string(b))
					} else {
						fmt.Printf("[%s] %s: %v\n", time.Now().Format("15:04:05"), eventName, params)
					}
				})
			}

			fmt.Println("Watching events... Press Ctrl+C to exit")
			select {}
		},
	}
}

func newAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent-status [agent-id]",
		Short: "Get detailed agent status including working/idle state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := getLocalNodeID()
			if err != nil {
				return err
			}

			resp, err := cliClient.Call("agent.list", map[string]any{"nodeId": nodeID})
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("RPC error: %s", resp.Error.Message)
			}

			agents, ok := resp.Result.([]any)
			if !ok {
				return fmt.Errorf("unexpected response format")
			}

			for _, agent := range agents {
				if a, ok := agent.(map[string]any); ok {
					if a["id"] == args[0] {
						if outputJSON {
							output(a)
							return nil
						}

						status := stringify(a["status"])
						name := stringify(a["name"])
						provider := stringify(a["provider"])
						pid := 0
						if p, ok := a["pid"].(float64); ok {
							pid = int(p)
						}
						workDir := stringify(a["workDir"])
						sessionID := stringify(a["sessionId"])
						runtimeState := stringify(a["runtimeState"])
						sessionState := stringify(a["sessionState"])
						providerState := stringify(a["providerState"])
						permissionMode := stringify(a["permissionMode"])
						readOnly := false
						if r, ok := a["readOnly"].(bool); ok {
							readOnly = r
						}
						hasHistory := false
						if h, ok := a["hasHistory"].(bool); ok {
							hasHistory = h
						}

						statusIcon := "●"
						if status == "working" {
							statusIcon = "🟢"
						} else if status == "idle" {
							statusIcon = "🟡"
						} else if status == "stopped" || status == "crashed" {
							statusIcon = "🔴"
						}

						fmt.Printf("═══ Agent: %s ═══\n", name)
						fmt.Printf("ID:       %s\n", args[0])
						fmt.Printf("Status:   %s %s\n", statusIcon, status)
						fmt.Printf("Provider: %s\n", provider)
						fmt.Printf("PID:      %d\n", pid)
						fmt.Printf("WorkDir:  %s\n", workDir)
						if sessionID != "" {
							fmt.Printf("Session:  %s\n", sessionID)
						}
						fmt.Printf("History:  %v\n", hasHistory)
						fmt.Printf("ReadOnly: %v\n", readOnly)
						if runtimeState != "" {
							fmt.Printf("Runtime:  %s\n", runtimeState)
						}
						if sessionState != "" {
							fmt.Printf("Session:  %s\n", sessionState)
						}
						if providerState != "" {
							fmt.Printf("Provider: %s\n", providerState)
						}
						if permissionMode != "" {
							fmt.Printf("Permission: %s\n", permissionMode)
						}

						// Fetch recent messages
						histResp, err := cliClient.Call("conversation.history", map[string]any{
							"agentId": args[0],
							"nodeId":  nodeID,
							"limit":   3,
						})
						if err == nil && histResp.Error == nil {
							if histResult, ok := histResp.Result.(map[string]any); ok {
								if events := toAnySlice(histResult["events"]); len(events) > 0 {
									fmt.Println()
									fmt.Println("Recent messages:")
									for _, raw := range events {
										if ev, ok := raw.(map[string]any); ok {
											role := stringify(ev["role"])
											text := stringify(ev["text"])
											if text == "" {
												text = stringify(ev["content"])
											}
											if text != "" && (role == "user" || role == "assistant") {
												roleIcon := "👤"
												if role == "assistant" {
													roleIcon = "🤖"
												}
												firstLine := strings.Split(strings.TrimSpace(text), "\n")[0]
												fmt.Printf("  %s %s\n", roleIcon, truncate(firstLine, 80))
											}
										}
									}
								}
							}
						}

						return nil
					}
				}
			}

			return fmt.Errorf("agent %s not found", args[0])
		},
	}
}

func newListNodesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-nodes",
		Short: "List all connected nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := cliClient.Call("node.list", nil)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("RPC error: %s", resp.Error.Message)
			}
			output(resp.Result)
			return nil
		},
	}
}

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Show dashboard with managed and attachable agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := getAllConnectedNodes()
			if err != nil {
				return err
			}
			if len(nodes) == 0 {
				return fmt.Errorf("no connected node found")
			}

			type managedItem struct {
				ag     map[string]any
				nodeID string
				node   string
			}
			type attachableItem struct {
				ag   map[string]any
				node string
			}

			var managedAll []managedItem
			var attachableAll []attachableItem
			// Aggregate JSON view: items keyed by node, plus a flat list.
			aggregated := map[string]any{
				"items":      []any{},
				"managed":    []any{},
				"attachable": []any{},
			}
			itemsByNode := make([]any, 0, len(nodes))
			flatManaged := make([]any, 0)
			flatAttachable := make([]any, 0)

			// Aggregate agent.list across all nodes for the summary line.
			var allAgents []map[string]any

			for _, n := range nodes {
				// Pull catalog (managed/attachable) per node.
				cResp, err := cliClient.Call("session.catalog", map[string]any{"nodeId": n.ID})
				if err == nil && cResp.Error == nil {
					if result, ok := cResp.Result.(map[string]any); ok {
						managed := toAnySlice(result["managed"])
						attachable := toAnySlice(result["attachable"])

						nodeManaged := make([]any, 0, len(managed))
						nodeAttachable := make([]any, 0, len(attachable))

						for _, raw := range managed {
							if ag, ok := raw.(map[string]any); ok {
								ag["nodeId"] = n.ID
								ag["nodeName"] = n.Name
								managedAll = append(managedAll, managedItem{ag: ag, nodeID: n.ID, node: n.Name})
								nodeManaged = append(nodeManaged, ag)
								flatManaged = append(flatManaged, ag)
							}
						}
						for _, raw := range attachable {
							if ag, ok := raw.(map[string]any); ok {
								ag["nodeId"] = n.ID
								ag["nodeName"] = n.Name
								attachableAll = append(attachableAll, attachableItem{ag: ag, node: n.Name})
								nodeAttachable = append(nodeAttachable, ag)
								flatAttachable = append(flatAttachable, ag)
							}
						}

						itemsByNode = append(itemsByNode, map[string]any{
							"nodeId":     n.ID,
							"nodeName":   n.Name,
							"managed":    nodeManaged,
							"attachable": nodeAttachable,
						})
					}
				}

				// agent.list for the summary header (status/runtime counts).
				aResp, err := cliClient.Call("agent.list", map[string]any{"nodeId": n.ID})
				if err == nil && aResp.Error == nil {
					if agents, ok := aResp.Result.([]any); ok {
						for _, raw := range agents {
							if ag, ok := raw.(map[string]any); ok {
								allAgents = append(allAgents, ag)
							}
						}
					}
				}
			}

			aggregated["items"] = itemsByNode
			aggregated["managed"] = flatManaged
			aggregated["attachable"] = flatAttachable

			if outputJSON {
				output(aggregated)
				return nil
			}

			fmt.Println("═══ Dashboard ═══")
			fmt.Println()

			// Summary across all nodes.
			total := len(allAgents)
			working := 0
			idle := 0
			stopped := 0
			live := 0
			exited := 0
			for _, ag := range allAgents {
				switch stringify(ag["status"]) {
				case "working":
					working++
				case "idle":
					idle++
				case "stopped", "crashed":
					stopped++
				}
				switch stringify(ag["runtimeState"]) {
				case "live":
					live++
				case "exited":
					exited++
				}
			}
			fmt.Printf("Nodes: %d  Total: %d  Working: %d  Idle: %d  Stopped: %d\n", len(nodes), total, working, idle, stopped)
			fmt.Printf("Live: %d  Exited: %d\n", live, exited)
			fmt.Println()

			if len(managedAll) > 0 {
				fmt.Printf("Managed Agents (%d):\n", len(managedAll))
				for _, item := range managedAll {
					ag := item.ag
					status := stringify(ag["status"])
					name := stringify(ag["name"])
					provider := stringify(ag["provider"])
					pid := 0
					if p, ok := ag["pid"].(float64); ok {
						pid = int(p)
					}
					project := stringify(ag["projectName"])
					if project == "" {
						project = stringify(ag["workDir"])
					}
					statusIcon := "●"
					if status == "working" {
						statusIcon = "🟢"
					} else if status == "idle" {
						statusIcon = "🟡"
					} else if status == "stopped" || status == "crashed" {
						statusIcon = "🔴"
					}

					agentID := stringify(ag["id"])
					lastMsg := fetchLastMessage(item.nodeID, agentID)

					fmt.Printf("  %s [%s] %-10s %-15s %-10s PID:%-6d %s\n",
						statusIcon, item.node, status, name, provider, pid, project)
					if lastMsg != "" {
						fmt.Printf("    └─ %s\n", lastMsg)
					}
				}
			} else {
				fmt.Println("Managed Agents: none")
			}
			fmt.Println()

			if len(attachableAll) > 0 {
				fmt.Printf("Attachable Processes (%d):\n", len(attachableAll))
				for _, item := range attachableAll {
					ag := item.ag
					provider := stringify(ag["provider"])
					pid := 0
					if p, ok := ag["pid"].(float64); ok {
						pid = int(p)
					}
					sessionID := stringify(ag["sessionId"])
					workDir := stringify(ag["workDir"])
					fmt.Printf("  [%s] %-10s PID:%-6d Session:%-20s %s\n",
						item.node, provider, pid, sessionID, workDir)
				}
			} else {
				fmt.Println("Attachable Processes: none")
			}

			return nil
		},
	}
}

func newHistoryCmd() *cobra.Command {
	var agentID string
	var limit int

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show conversation history for an agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentID == "" {
				return fmt.Errorf("--agent-id required")
			}

			nodeID, err := getLocalNodeID()
			if err != nil {
				return err
			}

			params := map[string]any{
				"agentId": agentID,
				"nodeId":  nodeID,
			}
			if limit > 0 {
				params["limit"] = limit
			}

			resp, err := cliClient.Call("conversation.history", params)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("RPC error: %s", resp.Error.Message)
			}

			if outputJSON {
				output(resp.Result)
				return nil
			}

			result, ok := resp.Result.(map[string]any)
			if !ok {
				return fmt.Errorf("unexpected response format")
			}

			events := toAnySlice(result["events"])
			lastSeq := uint64(0)
			if ls, ok := result["lastSeq"].(float64); ok {
				lastSeq = uint64(ls)
			}

			fmt.Printf("═══ History for %s (lastSeq: %d) ═══\n\n", agentID, lastSeq)

			if len(events) == 0 {
				fmt.Println("No messages.")
				return nil
			}

			for _, raw := range events {
				if ev, ok := raw.(map[string]any); ok {
					seq := uint64(0)
					if s, ok := ev["seq"].(float64); ok {
						seq = uint64(s)
					}
					role := stringify(ev["role"])
					text := stringify(ev["text"])
					if text == "" {
						text = stringify(ev["content"])
					}
					if text == "" {
						delete(ev, "seq")
						delete(ev, "role")
						for k, v := range ev {
							if text == "" {
								text = fmt.Sprintf("%s: %v", k, v)
							}
						}
					}

					roleIcon := "💬"
					if role == "user" {
						roleIcon = "👤"
					} else if role == "assistant" {
						roleIcon = "🤖"
					} else if role == "system" {
						roleIcon = "⚙️"
					}

					fmt.Printf("%s [%d] %s: %s\n", roleIcon, seq, role, truncate(text, 100))
				}
			}

			perms := toAnySlice(result["permissionRequests"])
			if len(perms) > 0 {
				fmt.Printf("\nPending Permissions (%d):\n", len(perms))
				for _, raw := range perms {
					if p, ok := raw.(map[string]any); ok {
						tool := stringify(p["tool_name"])
						reqID := stringify(p["request_id"])
						fmt.Printf("  ⏳ %s (req: %s)\n", tool, reqID)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Target agent ID")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max messages to show")

	return cmd
}

func newInteractiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "interactive",
		Short: "Interactive REPL mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Phone-Talk CLI Interactive Mode")
			fmt.Println("Commands: list-agents, send-message, watch-events, quit")
			fmt.Println()

			for {
				fmt.Print("> ")
				var input string
				fmt.Scanln(&input)

				switch input {
				case "quit", "exit", "q":
					return nil
				case "list-agents", "la":
					resp, err := cliClient.Call("agent.list", nil)
					if err != nil {
						fmt.Printf("Error: %v\n", err)
						continue
					}
					output(resp.Result)
				case "watch-events", "we":
					fmt.Println("Starting event watcher...")
					cliClient.OnEvent("agent.status_changed", func(params any) {
						fmt.Printf("[Status] %v\n", params)
					})
					cliClient.OnEvent("conversation.message", func(params any) {
						fmt.Printf("[Message] %v\n", params)
					})
					select {}
				default:
					fmt.Printf("Unknown command: %s\n", input)
				}
			}
		},
	}
}
