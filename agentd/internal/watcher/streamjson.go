package watcher

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// StreamJSONEvent represents an event from Claude's --output-format stream-json
type StreamJSONEvent struct {
	Type       string          `json:"type"`
	Role       string          `json:"role,omitempty"`
	Message    *MessageContent `json:"message,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
	Status     string          `json:"status,omitempty"`
	Timestamp  string          `json:"timestamp,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
}

// MessageContent represents the message field in stream-json events
type MessageContent struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // Can be string or array
}

// ContentBlock represents a single content block in Claude's output
type ContentBlock struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Name string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// StreamJSONWatcher watches Claude's stream-json output and emits structured events
type StreamJSONWatcher struct {
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	callback func(StreamEvent)
	stop     chan struct{}
	once     sync.Once
}

// StreamEvent represents a parsed event from the stream-json output
type StreamEvent struct {
	Type            string   // "message", "tool_use", "tool_result", "status_change", "permission_prompt"
	Role            string   // "user" or "assistant"
	Text            string   // Text content (for messages)
	ToolName        string   // Tool name (for tool_use)
	ToolInput       string   // Tool input (for tool_use)
	ToolOutput      string   // Tool output (for tool_result)
	Error           string   // Error message
	StatusChange    *AgentStatus
	IsPermissionPrompt bool
}

// NewStreamJSONWatcher creates a watcher for Claude's stream-json output
func NewStreamJSONWatcher(cmd *exec.Cmd, callback func(StreamEvent)) *StreamJSONWatcher {
	return &StreamJSONWatcher{
		cmd:      cmd,
		callback: callback,
		stop:     make(chan struct{}),
	}
}

// Start begins watching the stream-json output
func (w *StreamJSONWatcher) Start() error {
	stdout, err := w.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	w.stdout = stdout

	go w.loop()
	return nil
}

// Stop stops the watcher
func (w *StreamJSONWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

func (w *StreamJSONWatcher) loop() {
	scanner := bufio.NewScanner(w.stdout)
	for scanner.Scan() {
		select {
		case <-w.stop:
			return
		default:
		}

		line := scanner.Bytes()
		if ev, ok := w.parseLine(line); ok {
			w.callback(ev)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[StreamJSON] Scanner error: %v", err)
	}
}

func (w *StreamJSONWatcher) parseLine(data []byte) (StreamEvent, bool) {
	var event StreamJSONEvent
	if err := json.Unmarshal(data, &event); err != nil {
		// Not valid JSON, might be regular output
		return StreamEvent{}, false
	}

	switch event.Type {
	case "init":
		// Initialization event, contains session info
		return StreamEvent{Type: "init"}, true

	case "message":
		return w.parseMessageEvent(event)

	case "user":
		return w.parseUserEvent(event)

	case "tool_use":
		return StreamEvent{
			Type:     "tool_use",
			ToolName: event.Name,
			ToolInput: string(event.Input),
			StatusChange: statusPtr(StatusWorking),
		}, true

	case "tool_result":
		return StreamEvent{
			Type:       "tool_result",
			ToolOutput: string(event.Output),
			Error:      event.Error,
		}, true

	case "result":
		return StreamEvent{
			Type:         "result",
			StatusChange: statusPtr(StatusStandby),
		}, true

	case "permission_prompt":
		return StreamEvent{
			Type:               "permission_prompt",
			IsPermissionPrompt: true,
		}, true

	default:
		// Unknown event type, skip
		return StreamEvent{}, false
	}
}

func (w *StreamJSONWatcher) parseMessageEvent(event StreamJSONEvent) (StreamEvent, bool) {
	if event.Message == nil {
		return StreamEvent{}, false
	}

	ev := StreamEvent{
		Type: "message",
		Role: event.Message.Role,
	}

	// Content can be string or array of content blocks
	var contentStr string
	if err := json.Unmarshal(event.Message.Content, &contentStr); err == nil {
		// Simple string content
		ev.Text = contentStr
	} else {
		// Array of content blocks
		var blocks []ContentBlock
		if err := json.Unmarshal(event.Message.Content, &blocks); err == nil {
			hasToolUse := false
			for _, block := range blocks {
				switch block.Type {
				case "text":
					ev.Text += block.Text
				case "tool_use":
					hasToolUse = true
					ev.ToolName = block.Name
					if block.Input != nil {
						ev.ToolInput = string(block.Input)
					}
				}
			}
			if hasToolUse {
				ev.StatusChange = statusPtr(StatusWorking)
			} else if ev.Text != "" {
				ev.StatusChange = statusPtr(StatusStandby)
			}
		}
	}

	return ev, true
}

func (w *StreamJSONWatcher) parseUserEvent(event StreamJSONEvent) (StreamEvent, bool) {
	// User message events
	return StreamEvent{
		Type: "message",
		Role: "user",
		Text: string(event.Content),
	}, true
}

func statusPtr(s AgentStatus) *AgentStatus {
	return &s
}

// WaitForReady waits for the stream-json output to be ready (init event received)
func (w *StreamJSONWatcher) WaitForReady(timeout time.Duration) bool {
	ready := make(chan struct{})
	originalCallback := w.callback

	w.callback = func(ev StreamEvent) {
		if ev.Type == "init" {
			close(ready)
		}
		originalCallback(ev)
	}

	select {
	case <-ready:
		return true
	case <-time.After(timeout):
		return false
	case <-w.stop:
		return false
	}
}
