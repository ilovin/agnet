package watcher

import (
	"bufio"
	"encoding/json"
	"os"
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
	StatusChange *AgentStatus // non-nil when this line changes agent status
}

// ClaudeWatcher tails a Claude Code JSONL session file and emits ConversationEvents.
type ClaudeWatcher struct {
	path     string
	callback func(ConversationEvent)
	stop     chan struct{}
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
	close(w.stop)
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

	if _, err := f.Seek(w.offset, 0); err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		w.offset += int64(len(line)) + 1 // +1 for newline
		if ev, ok := parseLine(line); ok {
			w.callback(ev)
		}
	}
	return scanner.Err()
}

// claudeLine is the minimal structure we need from Claude's JSONL output.
type claudeLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
	} `json:"message"`
}

func parseLine(data []byte) (ConversationEvent, bool) {
	var line claudeLine
	if err := json.Unmarshal(data, &line); err != nil {
		return ConversationEvent{}, false
	}
	if line.Type != "user" && line.Type != "assistant" {
		return ConversationEvent{}, false
	}

	ev := ConversationEvent{Role: line.Message.Role}
	hasToolUse := false
	isTextStop := false

	for _, c := range line.Message.Content {
		switch c.Type {
		case "text":
			ev.Text += c.Text
			isTextStop = true
		case "tool_use":
			hasToolUse = true
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

	return ev, true
}
