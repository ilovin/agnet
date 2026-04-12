package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// OpenCodeEvent represents a parsed event from opencode export
type OpenCodeEvent struct {
	Info  OpenCodeMessageInfo   `json:"info"`
	Parts []OpenCodeMessagePart `json:"parts"`
}

type OpenCodeMessageInfo struct {
	Role    string `json:"role"`
	Agent   string `json:"agent"`
	Model   OpenCodeModel `json:"model"`
}

type OpenCodeModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type OpenCodeMessagePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ExportOpenCodeSession exports an OpenCode session using the opencode CLI
func ExportOpenCodeSession(sessionID string) ([]ConversationEvent, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}

	// Use temp file to avoid pipe buffer issues with large exports
	tmpFile := "/tmp/opencode_export_" + sessionID + ".json"

	// Run opencode export command with output redirected to file
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf("opencode export %s > %s 2>&1", sessionID, tmpFile))
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opencode export failed: %w", err)
	}

	// Read the exported file
	readCmd := exec.Command("cat", tmpFile)
	output, err := readCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read export file: %w", err)
	}

	outputStr := string(output)
	log.Printf("[OpenCode Export] Session %s: output length %d bytes", sessionID, len(outputStr))

	// Find the start of JSON (first '{' character)
	// Skip the "Exporting session: ..." line
	jsonStart := strings.Index(outputStr, "{")
	if jsonStart == -1 {
		return nil, fmt.Errorf("no JSON found in export output")
	}

	jsonData := outputStr[jsonStart:]

	var exportData struct {
		Info     json.RawMessage `json:"info"`
		Messages []OpenCodeEvent `json:"messages"`
	}

	if err := json.Unmarshal([]byte(jsonData), &exportData); err != nil {
		return nil, fmt.Errorf("parse export JSON: %w", err)
	}

	// Convert to ConversationEvent
	events := make([]ConversationEvent, 0, len(exportData.Messages))
	for _, msg := range exportData.Messages {
		// Extract text from parts
		var text string
		for _, part := range msg.Parts {
			if part.Type == "text" {
				text += part.Text
			}
		}

		if text != "" {
			events = append(events, ConversationEvent{
				Role: msg.Info.Role,
				Text: text,
			})
		}
	}

	return events, nil
}

// OpenCodeSessionHistory loads historical events from an OpenCode session
func OpenCodeSessionHistory(sessionID string) ([]ConversationEvent, error) {
	return ExportOpenCodeSession(sessionID)
}
