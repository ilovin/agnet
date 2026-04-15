package ws

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"os/exec"
	"strings"

	"github.com/phone-talk/agentd/internal/agent"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// openCodeSendWithResume sends a message to an OpenCode session using resume mode.
// OpenCode doesn't support real-time PTY input, so we use `opencode run --session`.
func (h *handler) openCodeSendWithResume(req RPCRequest, ag *agent.Agent, message string) RPCResponse {
	log.Printf("[OpenCode] Sending message to agent %s with resume", ag.ID)

	// Get the stored resume session ID
	resumeSessionID, _ := h.server.manager.GetResumeSessionID(ag.ID)
	if resumeSessionID == "" {
		return errResp(req.ID, -32000, "OpenCode agent has no session ID for resume")
	}

	// Record user message
	if _, err := h.server.manager.RecordConversationEvent(ag.ID, map[string]any{
		"role": "user",
		"text": message,
		"raw":  false,
	}); err != nil {
		return errResp(req.ID, -32000, "record user message: "+err.Error())
	}

	// Broadcast user message
	h.server.broadcast(event("conversation.message", map[string]any{
		"agentId": ag.ID,
		"role":    "user",
		"text":    message,
	}), nil)

	// Broadcast status change to working
	h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "working")), nil)

	// Extract model from agent args (set by resolveLaunch via -m flag)
	currentModel := currentOpenCodeModel(ag.Args)

	// Start OpenCode resume process in background
	go func() {
		// Build opencode run args
		ocArgs := []string{"run", "--session", resumeSessionID, "--format", "json"}
		if currentModel != "" {
			ocArgs = append(ocArgs, "-m", currentModel)
		}

		// Use stdbuf to disable output buffering for real-time JSON streaming.
		// Fall back to direct opencode invocation if stdbuf is not available.
		var cmd *exec.Cmd
		if _, err := exec.LookPath("stdbuf"); err == nil {
			cmd = exec.Command("stdbuf", append([]string{"-o0", "opencode"}, ocArgs...)...)
		} else {
			log.Printf("[OpenCode] stdbuf not available, invoking opencode directly")
			cmd = exec.Command("opencode", ocArgs...)
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
		lineCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++

			// Try to parse as generic JSON first to see structure
			var generic map[string]interface{}
			if err := json.Unmarshal([]byte(line), &generic); err != nil {
				continue
			}

			// Extract type and text from various possible structures
			evType, _ := generic["type"].(string)

			// Log first few lines for debugging
			if lineCount <= 3 {
				log.Printf("[OpenCode] JSON line %d type=%s", lineCount, evType)
			}

			// Handle text events (structure: {"type":"text","part":{"type":"text","text":"..."}})
			if evType == "text" {
				if part, ok := generic["part"].(map[string]interface{}); ok {
					text, _ := part["text"].(string)
					if text != "" {
						log.Printf("[OpenCode] Got text (len=%d): %s", len(text), text[:min(len(text), 50)])
						fullResponse.WriteString(text)
						// Broadcast partial response for real-time display
						h.server.broadcast(event("conversation.message", map[string]any{
							"agentId": ag.ID,
							"role":    "assistant",
							"text":    text,
							"partial": true,
						}), nil)
					} else {
						log.Printf("[OpenCode] Text event but no text content")
					}
				} else {
					log.Printf("[OpenCode] Text event but no part field")
				}
			}

			// Handle step_finish to detect completion
			if evType == "step_finish" {
				log.Printf("[OpenCode] Step finished, breaking")
				break
			}
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
			h.server.manager.RecordConversationEvent(ag.ID, map[string]any{
				"role": "assistant",
				"text": fullResponse.String(),
				"raw":  false,
			})
			// Broadcast final complete message
			h.server.broadcast(event("conversation.message", map[string]any{
				"agentId": ag.ID,
				"role":    "assistant",
				"text":    fullResponse.String(),
				"final":   true,
			}), nil)
		}

		// Broadcast status change back to idle
		h.server.broadcast(event("agent.status_changed", h.statusChangedParams(ag.ID, "idle")), nil)

		log.Printf("[OpenCode] Resume process completed for agent %s", ag.ID)
	}()

	return okResp(req.ID, map[string]any{"ok": true})
}
