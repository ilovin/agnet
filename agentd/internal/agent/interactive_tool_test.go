package agent

import (
	"encoding/json"
	"testing"
)

// TestParseInteractiveToolUse_AskUserQuestion tests multi and single question variants.
func TestParseInteractiveToolUse_AskUserQuestion(t *testing.T) {
	t.Run("multi question with options", func(t *testing.T) {
		input := json.RawMessage(`{
			"questions": [
				{
					"question": "Which framework?",
					"header": "Choose a framework",
					"multi_select": false,
					"options": [
						{"label": "React", "description": "UI library"},
						{"label": "Vue", "description": "Progressive framework", "preview": "preview text"}
					]
				},
				{
					"question": "Add TypeScript?",
					"multi_select": true,
					"options": [
						{"label": "Yes"},
						{"label": "No"}
					]
				}
			]
		}`)
		kind, payload, ok := ParseInteractiveToolUse("AskUserQuestion", "toolu_01ABC", input)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if kind != KindAskUserQuestion {
			t.Fatalf("expected kind=%q, got %q", KindAskUserQuestion, kind)
		}
		p, ok := payload.(*AskUserQuestionPayload)
		if !ok {
			t.Fatalf("expected *AskUserQuestionPayload, got %T", payload)
		}
		if p.ToolUseID != "toolu_01ABC" {
			t.Errorf("tool_use_id: got %q, want %q", p.ToolUseID, "toolu_01ABC")
		}
		if len(p.Questions) != 2 {
			t.Fatalf("expected 2 questions, got %d", len(p.Questions))
		}
		q0 := p.Questions[0]
		if q0.Question != "Which framework?" {
			t.Errorf("q0.Question: %q", q0.Question)
		}
		if q0.Header != "Choose a framework" {
			t.Errorf("q0.Header: %q", q0.Header)
		}
		if q0.MultiSelect != false {
			t.Error("q0.MultiSelect should be false")
		}
		if len(q0.Options) != 2 {
			t.Fatalf("q0.Options: expected 2, got %d", len(q0.Options))
		}
		if q0.Options[1].Preview != "preview text" {
			t.Errorf("option preview: %q", q0.Options[1].Preview)
		}
		q1 := p.Questions[1]
		if q1.MultiSelect != true {
			t.Error("q1.MultiSelect should be true")
		}
	})

	t.Run("single question no header", func(t *testing.T) {
		input := json.RawMessage(`{
			"questions": [
				{
					"question": "Continue?",
					"multi_select": false,
					"options": [{"label": "Yes"}, {"label": "No"}]
				}
			]
		}`)
		kind, payload, ok := ParseInteractiveToolUse("AskUserQuestion", "toolu_002", input)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if kind != KindAskUserQuestion {
			t.Fatalf("kind: got %q", kind)
		}
		p := payload.(*AskUserQuestionPayload)
		if len(p.Questions) != 1 {
			t.Fatalf("expected 1 question")
		}
		if p.Questions[0].Header != "" {
			t.Errorf("expected empty header")
		}
	})
}

// TestParseInteractiveToolUse_ExitPlanMode tests plan extraction.
func TestParseInteractiveToolUse_ExitPlanMode(t *testing.T) {
	t.Run("with plan", func(t *testing.T) {
		input := json.RawMessage(`{"plan": "Step 1: do X\nStep 2: do Y"}`)
		kind, payload, ok := ParseInteractiveToolUse("ExitPlanMode", "toolu_plan01", input)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if kind != KindExitPlanMode {
			t.Fatalf("kind: got %q, want %q", kind, KindExitPlanMode)
		}
		p, ok := payload.(*ExitPlanModePayload)
		if !ok {
			t.Fatalf("expected *ExitPlanModePayload, got %T", payload)
		}
		if p.ToolUseID != "toolu_plan01" {
			t.Errorf("tool_use_id: %q", p.ToolUseID)
		}
		if p.Plan != "Step 1: do X\nStep 2: do Y" {
			t.Errorf("plan: %q", p.Plan)
		}
	})

	t.Run("empty plan", func(t *testing.T) {
		input := json.RawMessage(`{"plan": ""}`)
		kind, payload, ok := ParseInteractiveToolUse("ExitPlanMode", "toolu_empty", input)
		if !ok {
			t.Fatal("expected ok=true even with empty plan")
		}
		if kind != KindExitPlanMode {
			t.Fatalf("kind: %q", kind)
		}
		p := payload.(*ExitPlanModePayload)
		if p.Plan != "" {
			t.Errorf("expected empty plan, got %q", p.Plan)
		}
	})
}

// TestParseInteractiveToolUse_NotMatched verifies ordinary tools are not intercepted.
func TestParseInteractiveToolUse_NotMatched(t *testing.T) {
	tools := []struct {
		name  string
		input string
	}{
		{"Bash", `{"command": "go test ./..."}`},
		{"Read", `{"file_path": "/foo/bar.go"}`},
		{"Write", `{"file_path": "/foo/bar.go", "content": "hello"}`},
		{"Edit", `{"file_path": "/foo/bar.go", "old_string": "a", "new_string": "b"}`},
		{"Agent", `{"prompt": "do something"}`},
	}
	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := ParseInteractiveToolUse(tt.name, "toolu_xyz", json.RawMessage(tt.input))
			if ok {
				t.Errorf("tool %q should NOT match interactive tools", tt.name)
			}
		})
	}
}

// TestParseInteractiveToolUse_Malformed checks robustness against bad input.
func TestParseInteractiveToolUse_Malformed(t *testing.T) {
	t.Run("AskUserQuestion nil input", func(t *testing.T) {
		_, _, ok := ParseInteractiveToolUse("AskUserQuestion", "id1", nil)
		if ok {
			t.Error("nil input should return ok=false")
		}
	})

	t.Run("AskUserQuestion empty input", func(t *testing.T) {
		_, _, ok := ParseInteractiveToolUse("AskUserQuestion", "id2", json.RawMessage{})
		if ok {
			t.Error("empty input should return ok=false")
		}
	})

	t.Run("AskUserQuestion invalid JSON", func(t *testing.T) {
		_, _, ok := ParseInteractiveToolUse("AskUserQuestion", "id3", json.RawMessage(`{not valid`))
		if ok {
			t.Error("invalid JSON should return ok=false")
		}
	})

	t.Run("AskUserQuestion empty questions array", func(t *testing.T) {
		_, _, ok := ParseInteractiveToolUse("AskUserQuestion", "id4", json.RawMessage(`{"questions": []}`))
		if ok {
			t.Error("empty questions array should return ok=false")
		}
	})

	t.Run("ExitPlanMode nil input", func(t *testing.T) {
		_, _, ok := ParseInteractiveToolUse("ExitPlanMode", "id5", nil)
		if ok {
			t.Error("nil input should return ok=false")
		}
	})

	t.Run("ExitPlanMode invalid JSON", func(t *testing.T) {
		_, _, ok := ParseInteractiveToolUse("ExitPlanMode", "id6", json.RawMessage(`!!invalid`))
		if ok {
			t.Error("invalid JSON should return ok=false")
		}
	})

	t.Run("does not panic on unexpected types", func(t *testing.T) {
		// Should not panic even with unexpected input shape
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panicked: %v", r)
			}
		}()
		ParseInteractiveToolUse("AskUserQuestion", "", json.RawMessage(`{"questions": "not_an_array"}`))
		ParseInteractiveToolUse("ExitPlanMode", "", json.RawMessage(`{"plan": 42}`))
	})
}
