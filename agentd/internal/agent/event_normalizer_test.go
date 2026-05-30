package agent

import (
	"encoding/json"
	"testing"

	"github.com/phone-talk/agentd/internal/watcher"
)

func TestNormalizeWatcherEvent_Text(t *testing.T) {
	ev := watcher.ConversationEvent{
		Role: "assistant",
		Text: "hello",
		Kind: "thinking",
	}
	got, ok := NormalizeWatcherEvent(ev, "sess-1")
	if !ok {
		t.Fatalf("expected normalize ok")
	}
	if got["sessionId"] != "sess-1" {
		t.Fatalf("expected sessionId injected")
	}
	if got["kind"] != "thinking" {
		t.Fatalf("expected kind=thinking, got %#v", got["kind"])
	}
}

func TestNormalizeWatcherEvent_InteractiveTool(t *testing.T) {
	input := map[string]any{
		"questions": []map[string]any{
			{
				"question": "继续吗？",
				"options": []map[string]any{
					{"label": "是"},
				},
			},
		},
	}
	raw, _ := json.Marshal(input)
	ev := watcher.ConversationEvent{
		ToolUseName:  "AskUserQuestion",
		ToolUseID:    "toolu_1",
		ToolUseInput: raw,
	}
	got, ok := NormalizeWatcherEvent(ev, "sess-2")
	if !ok {
		t.Fatalf("expected interactive event to normalize")
	}
	if got["kind"] != KindAskUserQuestion {
		t.Fatalf("expected kind=%s, got %#v", KindAskUserQuestion, got["kind"])
	}
	if _, ok := got["askUserQuestion"]; !ok {
		t.Fatalf("expected askUserQuestion payload")
	}
}
