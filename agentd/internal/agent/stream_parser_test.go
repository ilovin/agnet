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
	summary := sp.BuildToolInputSummary("Bash", []byte{})
	if summary != "" {
		t.Fatalf("expected empty, got %q", summary)
	}
}
