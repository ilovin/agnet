package agent_test

import (
	"testing"

	"github.com/phone-talk/agentd/internal/agent"
)

func TestTryParseStreamJSON_ValidEvent(t *testing.T) {
	sp := agent.NewStreamParser()
	line := `{"type":"assistant","role":"assistant","content":[{"type":"text","text":"hello"}]}`
	ev := sp.TryParseStreamJSON(line)
	if ev == nil {
		t.Fatal("expected parsed event, got nil")
	}
	if ev.Type != "assistant" {
		t.Fatalf("expected type=assistant, got %q", ev.Type)
	}
}

func TestTryParseStreamJSON_InvalidJSON(t *testing.T) {
	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON("not json")
	if ev != nil {
		t.Fatal("expected nil for non-json")
	}
}

func TestTryParseStreamJSON_UnknownType(t *testing.T) {
	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"unknown_thing"}`)
	if ev != nil {
		t.Fatal("expected nil for unknown type")
	}
}

func TestTryParseStreamJSON_AnthropicStreamingEvents(t *testing.T) {
	sp := agent.NewStreamParser()

	tests := []struct {
		name     string
		line     string
		wantType string
	}{
		{
			name:     "message_start",
			line:     `{"type":"message_start","message":{"id":"msg_123","role":"assistant"}}`,
			wantType: "message_start",
		},
		{
			name:     "content_block_start tool_use",
			line:     `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash","input":{}}}`,
			wantType: "content_block_start",
		},
		{
			name:     "content_block_delta text",
			line:     `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			wantType: "content_block_delta",
		},
		{
			name:     "content_block_stop",
			line:     `{"type":"content_block_stop","index":0}`,
			wantType: "content_block_stop",
		},
		{
			name:     "message_stop",
			line:     `{"type":"message_stop"}`,
			wantType: "message_stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := sp.TryParseStreamJSON(tt.line)
			if ev == nil {
				t.Fatalf("expected parsed event, got nil")
			}
			if ev.Type != tt.wantType {
				t.Fatalf("expected type=%q, got %q", tt.wantType, ev.Type)
			}
		})
	}
}

func TestTryParseStreamJSON_NonObject(t *testing.T) {
	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`[1,2,3]`)
	if ev != nil {
		t.Fatal("expected nil for array")
	}
}

func TestBuildToolResultSummary_Bash(t *testing.T) {
	sp := agent.NewStreamParser()
	output := []byte("line1\nline2\nline3\nline4\nline5\nline6\n")
	summary := sp.BuildToolResultSummary("Bash", output)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if summary == "(no output)" {
		t.Fatal("expected content, got no output")
	}
}

func TestBuildToolResultSummary_Grep(t *testing.T) {
	sp := agent.NewStreamParser()
	output := []byte("match1\nmatch2\nmatch3\n")
	summary := sp.BuildToolResultSummary("Grep", output)
	if summary != "3 matches" {
		t.Fatalf("expected '3 matches', got %q", summary)
	}
}

func TestBuildToolResultSummary_Read(t *testing.T) {
	sp := agent.NewStreamParser()
	output := []byte("line1\nline2\n")
	summary := sp.BuildToolResultSummary("Read", output)
	if summary != "2 lines" {
		t.Fatalf("expected '2 lines', got %q", summary)
	}
}

func TestBuildToolResultSummary_Empty(t *testing.T) {
	sp := agent.NewStreamParser()
	summary := sp.BuildToolResultSummary("Bash", []byte(""))
	if summary != "(no output)" {
		t.Fatalf("expected '(no output)', got %q", summary)
	}
}

func TestBuildToolInputSummary_Bash(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"command":"ls -la"}`)
	summary := sp.BuildToolInputSummary("Bash", input)
	if summary != "ls -la" {
		t.Fatalf("expected 'ls -la', got %q", summary)
	}
}

func TestBuildToolInputSummary_Read(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"file_path":"/etc/passwd"}`)
	summary := sp.BuildToolInputSummary("Read", input)
	if summary != "/etc/passwd" {
		t.Fatalf("expected '/etc/passwd', got %q", summary)
	}
}

func TestBuildToolInputSummary_Empty(t *testing.T) {
	sp := agent.NewStreamParser()
	// Even when input is empty (e.g. a streaming `content_block_start` for a
	// tool_use whose deltas have not arrived yet), we must return a non-empty
	// summary so callers don't emit a bare `[ToolName]` card.
	summary := sp.BuildToolInputSummary("Bash", []byte{})
	if summary == "" {
		t.Fatalf("expected non-empty fallback for empty input, got empty")
	}
}

func TestBuildToolInputSummary_TaskUpdate_FullArgs(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"taskId":"63","status":"completed"}`)
	got := sp.BuildToolInputSummary("TaskUpdate", input)
	if got != "#63 -> completed" {
		t.Fatalf("expected '#63 -> completed', got %q", got)
	}
}

func TestBuildToolInputSummary_TaskUpdate_OnlyTaskId(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"taskId":"63","status":""}`)
	got := sp.BuildToolInputSummary("TaskUpdate", input)
	if got != "#63" {
		t.Fatalf("expected '#63', got %q", got)
	}
}

func TestBuildToolInputSummary_TaskUpdate_OnlyStatus(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"taskId":"","status":"completed"}`)
	got := sp.BuildToolInputSummary("TaskUpdate", input)
	if got != "completed" {
		t.Fatalf("expected 'completed', got %q", got)
	}
}

func TestBuildToolInputSummary_TaskUpdate_EmptyArgs(t *testing.T) {
	sp := agent.NewStreamParser()
	// Streaming content_block_start case: input is `{}`. Must still produce
	// a non-empty summary so the activity card has a meaningful title.
	got := sp.BuildToolInputSummary("TaskUpdate", []byte(`{}`))
	if got == "" {
		t.Fatalf("expected non-empty summary for TaskUpdate with empty input, got empty")
	}
}

func TestBuildToolInputSummary_Agent_Description(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"description":"Find Python bugs","prompt":"long prompt..."}`)
	got := sp.BuildToolInputSummary("Agent", input)
	if got != "Find Python bugs" {
		t.Fatalf("expected 'Find Python bugs', got %q", got)
	}
}

func TestBuildToolInputSummary_Agent_PromptOnly(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"description":"","prompt":"do thing"}`)
	got := sp.BuildToolInputSummary("Agent", input)
	if got != "do thing" {
		t.Fatalf("expected 'do thing', got %q", got)
	}
}

func TestBuildToolInputSummary_Agent_SubagentTypeFallback(t *testing.T) {
	sp := agent.NewStreamParser()
	input := []byte(`{"description":"","prompt":"","subagent_type":"general-purpose"}`)
	got := sp.BuildToolInputSummary("Agent", input)
	if got != "general-purpose" {
		t.Fatalf("expected 'general-purpose', got %q", got)
	}
}

func TestBuildToolInputSummary_Agent_EmptyArgs(t *testing.T) {
	sp := agent.NewStreamParser()
	// Streaming early-emit case: input is `{}`. Must produce a non-empty
	// summary; otherwise the activity card has no detail.
	got := sp.BuildToolInputSummary("Agent", []byte(`{}`))
	if got == "" {
		t.Fatalf("expected non-empty summary for Agent with empty input, got empty")
	}
}

func TestBuildToolInputSummary_UnknownTool_NonEmpty(t *testing.T) {
	sp := agent.NewStreamParser()
	// Even an unknown tool name with empty input should produce a non-empty
	// fallback so the card stays meaningful.
	got := sp.BuildToolInputSummary("MysteryTool", []byte(`{}`))
	if got == "" {
		t.Fatalf("expected non-empty fallback, got empty")
	}
}

func TestBuildToolInputSummary_UnknownTool_FirstStringField(t *testing.T) {
	sp := agent.NewStreamParser()
	// Generic fallback should pick the first non-empty string value when no
	// specific case matches.
	input := []byte(`{"foo":"hello"}`)
	got := sp.BuildToolInputSummary("MysteryTool", input)
	if got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
}
