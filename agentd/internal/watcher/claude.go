package watcher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AgentStatus string

const (
	StatusWorking AgentStatus = "working"
	StatusStandby AgentStatus = "standby"
)

// ConversationEvent represents a parsed line from the Claude JSONL session file.
type ConversationEvent struct {
	Role         string       // "user" or "assistant"
	Text         string       // combined text content
	ToolSummary  string       // human-readable tool call summary (e.g. "Bash: go test ./...")
	StatusChange *AgentStatus // non-nil when this line changes agent status
}

// ClaudeWatcher tails a Claude Code JSONL session file and emits ConversationEvents.
type ClaudeWatcher struct {
	path     string
	callback func(ConversationEvent)
	stop     chan struct{}
	once     sync.Once
	offset   int64
}

func NewClaudeWatcher(path string, callback func(ConversationEvent)) *ClaudeWatcher {
	return &ClaudeWatcher{path: path, callback: callback, stop: make(chan struct{})}
}

func (w *ClaudeWatcher) Start() error {
	// Parse existing content first
	if err := w.poll(); err != nil && !os.IsNotExist(err) {
		return err
	}
	go w.loop()
	return nil
}

func (w *ClaudeWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

func (w *ClaudeWatcher) loop() {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			_ = w.poll()
		}
	}
}

func (w *ClaudeWatcher) poll() error {
	f, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Detect file truncation (e.g. from context compaction):
	// if the file is now smaller than our saved offset, reset to 0.
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() < w.offset {
		w.offset = 0
	}

	if _, err := f.Seek(w.offset, io.SeekStart); err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line size
	for scanner.Scan() {
		line := scanner.Bytes()
		if ev, ok := parseLine(line); ok {
			w.callback(ev)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Get the actual file position after scanning to avoid newline-encoding assumptions
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	w.offset = pos
	return nil
}

// claudeLine is the minimal structure we need from Claude's JSONL output.
type claudeLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content interface{} `json:"content"` // Can be string or array
	} `json:"message"`
}

// buildToolSummary generates a human-readable summary for a tool_use block.
func buildToolSummary(name string, input map[string]interface{}) string {
	switch name {
	case "Glob":
		pattern, _ := input["pattern"].(string)
		path, _ := input["path"].(string)
		if path != "" {
			return fmt.Sprintf("Glob %s in %s", pattern, path)
		}
		return fmt.Sprintf("Glob %s", pattern)
	case "Grep":
		pattern, _ := input["pattern"].(string)
		glob, _ := input["glob"].(string)
		if glob != "" {
			return fmt.Sprintf("Grep /%s/ %s", pattern, glob)
		}
		return fmt.Sprintf("Grep /%s/", pattern)
	case "Read":
		filePath, _ := input["file_path"].(string)
		base := filepath.Base(filePath)
		offset, hasOffset := input["offset"]
		limit, hasLimit := input["limit"]
		if hasOffset || hasLimit {
			offsetStr := fmt.Sprintf("%v", offset)
			limitStr := fmt.Sprintf("%v", limit)
			return fmt.Sprintf("Read %s:%s-%s", base, offsetStr, limitStr)
		}
		return fmt.Sprintf("Read %s", base)
	case "Bash":
		cmd, _ := input["command"].(string)
		cmd = strings.TrimSpace(cmd)
		if len(cmd) > 60 {
			cmd = cmd[:60]
		}
		return cmd
	case "Edit":
		filePath, _ := input["file_path"].(string)
		return fmt.Sprintf("Edit %s", filepath.Base(filePath))
	case "Write":
		filePath, _ := input["file_path"].(string)
		return fmt.Sprintf("Write %s", filepath.Base(filePath))
	default:
		return ""
	}
}

func parseLine(data []byte) (ConversationEvent, bool) {
	var line claudeLine
	if err := json.Unmarshal(data, &line); err != nil {
		return ConversationEvent{}, false
	}
	// Only process user and assistant messages
	if line.Type != "user" && line.Type != "assistant" {
		return ConversationEvent{}, false
	}

	ev := ConversationEvent{Role: line.Message.Role}

	// Content can be either a string or an array of content blocks
	switch content := line.Message.Content.(type) {
	case string:
		// Simple text content
		ev.Text = content
	case []interface{}:
		// Array of content blocks (text, tool_use, etc.)
		hasToolUse := false
		isTextStop := false
		for _, item := range content {
			if block, ok := item.(map[string]interface{}); ok {
				blockType, _ := block["type"].(string)
				switch blockType {
				case "text":
					if text, ok := block["text"].(string); ok {
						ev.Text += text
						isTextStop = true
					}
				case "tool_use":
					hasToolUse = true
					if name, ok := block["name"].(string); ok {
						input, _ := block["input"].(map[string]interface{})
						if input == nil {
							input = map[string]interface{}{}
						}
						summary := buildToolSummary(name, input)
						if summary != "" {
							ev.Text += fmt.Sprintf("[%s: %s]", name, summary)
							if ev.ToolSummary == "" {
								ev.ToolSummary = summary
							}
						} else {
							cmd, _ := input["command"].(string)
							if cmd != "" {
								ev.Text += fmt.Sprintf("[%s: %s]", name, cmd)
							} else {
								ev.Text += fmt.Sprintf("[%s]", name)
							}
						}
					}
				}
			}
		}
		// Status change detection
		if line.Type == "assistant" {
			if hasToolUse {
				s := StatusWorking
				ev.StatusChange = &s
			} else if isTextStop {
				s := StatusStandby
				ev.StatusChange = &s
			}
		}
	default:
		// Unknown content type, skip
		return ConversationEvent{}, false
	}

	return ev, true
}
