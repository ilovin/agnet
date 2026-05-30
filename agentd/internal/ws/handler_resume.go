package ws

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractOpencodeText tries multiple known JSON structures to extract assistant text.
func extractOpencodeText(generic map[string]interface{}) string {
	evType, _ := generic["type"].(string)

	// OpenCode wraps events in "part": {"type":"tool","tool":"bash","state":{...}}
	var part map[string]interface{}
	if p, ok := generic["part"].(map[string]interface{}); ok {
		part = p
	}

	// Structure 0: OpenCode/Claude stream-json wrapped in "part"
	// tool_use events: {"type":"tool_use","part":{"type":"tool","tool":"bash","state":{...}}}
	if evType == "tool_use" && part != nil {
		toolName, _ := part["tool"].(string)
		if toolName == "" {
			toolName = "unknown"
		}
		if state, ok := part["state"].(map[string]interface{}); ok {
			if status, _ := state["status"].(string); status == "error" {
				if errStr, ok := state["error"].(string); ok && errStr != "" {
					return "❌ " + toolName + " 工具被拒绝: " + errStr
				}
				return "❌ " + toolName + " 工具调用被拒绝"
			}
			if status, _ := state["status"].(string); status == "completed" || status == "success" {
				if output, ok := state["output"].(string); ok && output != "" {
					return "✅ " + toolName + " 执行完成\n" + output
				}
			}
			if input, ok := state["input"].(map[string]interface{}); ok {
				if cmd, ok := input["command"].(string); ok && cmd != "" {
					return "🔧 " + toolName + ": " + cmd
				}
				if desc, ok := input["description"].(string); ok && desc != "" {
					return "🔧 " + toolName + ": " + desc
				}
			}
			return "🔧 正在使用 " + toolName + " 工具"
		}
		return "🔧 " + toolName
	}

	// step_start / step_finish have no text content
	if evType == "step_start" || evType == "step_finish" {
		return ""
	}

	// Structure 1: {"type":"text","part":{"type":"text","text":"..."}}
	if part != nil {
		if text, ok := part["text"].(string); ok && text != "" {
			return text
		}
	}

	// Structure 2: {"type":"message","role":"assistant","content":"..."}
	if role, _ := generic["role"].(string); role == "assistant" || role == "user" {
		if content, ok := generic["content"].(string); ok && content != "" {
			return content
		}
	}

	// Structure 3: {"type":"assistant","text":"..."} or {"type":"assistant","content":"..."}
	if text, ok := generic["text"].(string); ok && text != "" {
		return text
	}

	// Structure 4: nested content_block_delta like Claude stream-json
	// {"type":"content_block_delta","delta":{"text":"..."}}
	if delta, ok := generic["delta"].(map[string]interface{}); ok {
		if text, ok := delta["text"].(string); ok && text != "" {
			return text
		}
		if text, ok := delta["text_delta"].(string); ok && text != "" {
			return text
		}
	}

	// Structure 5: {"type":"stream_event","event":{"type":"...","delta":{"text":"..."}}}
	if eventData, ok := generic["event"].(map[string]interface{}); ok {
		if delta, ok := eventData["delta"].(map[string]interface{}); ok {
			if text, ok := delta["text"].(string); ok && text != "" {
				return text
			}
			if text, ok := delta["text_delta"].(string); ok && text != "" {
				return text
			}
		}
	}

	// Structure 6: {"type":"result","result":"..."}
	if result, ok := generic["result"].(string); ok && result != "" {
		return result
	}

	// Structure 7: {"output":"..."} or {"message":"..."}
	if output, ok := generic["output"].(string); ok && output != "" {
		return output
	}
	if msg, ok := generic["message"].(string); ok && msg != "" {
		return msg
	}

	return ""
}

// openCodeSendWithResume sends a message to an OpenCode session using resume mode.
// OpenCode doesn't support real-time PTY input, so we use `opencode run --session`.
func (h *handler) openCodeSendWithResume(req RPCRequest, ag *agent.Agent, message string) RPCResponse {
	log.Printf("[OpenCode] Sending message to agent %s with resume", ag.ID)

	// Get the stored resume session ID
	resumeSessionID, _ := h.server.manager.GetResumeSessionID(ag.ID)
	log.Printf("[OpenCode] agent %s resumeSessionID=%q", ag.ID, resumeSessionID)
	if resumeSessionID == "" {
		log.Printf("[OpenCode] ERROR: agent %s has no session ID for resume", ag.ID)
		return errResp(req.ID, -32000, "OpenCode agent has no session ID for resume")
	}

	// Record user message
	seq, err := h.server.manager.RecordConversationEvent(ag.ID, map[string]any{
		"role": "user",
		"text": message,
		"raw":  false,
	})
	if err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	// Broadcast user message with full routing fields
	userBroadcast := map[string]any{
		"agentId":   ag.ID,
		"nodeId":    h.server.nodeID,
		"sessionId": ag.ResumeSessionID(),
		"role":      "user",
		"text":      message,
		"timestamp": time.Now().UnixMilli(),
	}
	if seq > 0 {
		userBroadcast["seq"] = seq
	}
	h.server.broadcast(event("conversation.message", userBroadcast), nil)

	// Broadcast status change to working
	log.Printf("[OpenCode] Broadcasting working status for agent %s", ag.ID)
	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "working")), nil)

	// Extract model from agent args (set by resolveLaunch via -m flag)
	currentModel := h.service.CurrentOpenCodeModel(ag.Args)

	// Start OpenCode resume process in background
	go func() {
		// Build opencode run args
		ocArgs := []string{"run", "--session", resumeSessionID, "--format", "json"}
		if currentModel != "" {
			ocArgs = append(ocArgs, "-m", currentModel)
		}

		// Resolve opencode executable path via AgentService
		ocCmd := h.service.FindExecutable("opencode")
		if ocCmd == "opencode" && ag.Cmd != "" {
			ocCmd = ag.Cmd
		}

		// Use stdbuf to disable output buffering for real-time JSON streaming.
		// Fall back to direct opencode invocation if stdbuf is not available.
		var cmd *exec.Cmd
		if _, err := exec.LookPath("stdbuf"); err == nil {
			cmd = exec.Command("stdbuf", append([]string{"-o0", ocCmd}, ocArgs...)...)
		} else {
			log.Printf("[OpenCode] stdbuf not available, invoking opencode directly")
			cmd = exec.Command(ocCmd, ocArgs...)
		}
		cmd.Dir = ag.WorkDir

		// Capture stderr so failures are visible in logs
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf

		// Write message to stdin
		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Printf("[OpenCode] Failed to get stdin: %v", err)
			h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "idle")), nil)
			return
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("[OpenCode] Failed to get stdout: %v", err)
			h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "idle")), nil)
			return
		}

		if err := cmd.Start(); err != nil {
			log.Printf("[OpenCode] Failed to start command: %v", err)
			h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "idle")), nil)
			return
		}

		// Send message
		go func() {
			defer stdin.Close()
			stdin.Write([]byte(message + "\n"))
		}()

		// Read and parse JSON events in real-time
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		var fullResponse strings.Builder
		var rawBuf strings.Builder
		lineCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++

			if strings.TrimSpace(line) != "" {
				rawBuf.WriteString(line)
				rawBuf.WriteString("\n")
			}

			// Try to parse as generic JSON first to see structure
			var generic map[string]interface{}
			if err := json.Unmarshal([]byte(line), &generic); err != nil {
				continue
			}

			// Extract type and text from various possible structures
			evType, _ := generic["type"].(string)

			// Log first few lines for debugging
			if lineCount <= 5 {
				log.Printf("[OpenCode] JSON line %d type=%s raw=%s", lineCount, evType, line[:min(len(line), 120)])
			}

			text := extractOpencodeText(generic)
			if text != "" {
				log.Printf("[OpenCode] Got text (len=%d): %s", len(text), text[:min(len(text), 50)])
				fullResponse.WriteString(text)
				// Broadcast partial response for real-time display
				partialBroadcast := map[string]any{
					"agentId":   ag.ID,
					"nodeId":    h.server.nodeID,
					"sessionId": ag.ResumeSessionID(),
					"role":      "assistant",
					"text":      text,
					"partial":   true,
					"timestamp": time.Now().UnixMilli(),
				}
				h.server.broadcast(event("conversation.message", partialBroadcast), nil)
			}

			// Handle step_finish to detect completion
			if evType == "step_finish" || evType == "message_stop" || evType == "done" {
				log.Printf("[OpenCode] Step finished (%s), breaking", evType)
				break
			}
		}

		// Record final assistant response even if scanner broke early or fullResponse is empty
		// but stdout had content we couldn't parse (fallback to raw stdout).
		if fullResponse.Len() == 0 && rawBuf.Len() > 0 {
			log.Printf("[OpenCode] No parsed text from JSON stream for agent %s, using raw stdout fallback", ag.ID)
			fullResponse.WriteString(rawBuf.String())
		}

		if err := scanner.Err(); err != nil {
			log.Printf("[OpenCode] Scanner error: %v", err)
		}

		if err := cmd.Wait(); err != nil {
			if stderrBuf.Len() > 0 {
				log.Printf("[OpenCode] Process exited with error: %v; stderr: %s", err, stderrBuf.String())
			} else {
				log.Printf("[OpenCode] Process exited with error: %v", err)
			}
		} else if stderrBuf.Len() > 0 {
			log.Printf("[OpenCode] Process stderr: %s", stderrBuf.String())
		}

		// Record final assistant response
		if fullResponse.Len() > 0 {
			seq, _ := h.server.manager.RecordConversationEvent(ag.ID, map[string]any{
				"role": "assistant",
				"text": fullResponse.String(),
				"raw":  false,
			})
			// Broadcast final complete message with full routing fields
			finalBroadcast := map[string]any{
				"agentId":   ag.ID,
				"nodeId":    h.server.nodeID,
				"sessionId": ag.ResumeSessionID(),
				"role":      "assistant",
				"text":      fullResponse.String(),
				"final":     true,
				"timestamp": time.Now().UnixMilli(),
			}
			if seq > 0 {
				finalBroadcast["seq"] = seq
			}
			h.server.broadcast(event("conversation.message", finalBroadcast), nil)
		}

		// Broadcast status change back to idle
		h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "idle")), nil)

		log.Printf("[OpenCode] Resume process completed for agent %s", ag.ID)
	}()

	return okResp(req.ID, map[string]any{"ok": true})
}
