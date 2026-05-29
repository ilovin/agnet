package watcher

import (
	"testing"
)

func TestOpenCodeDBHistoryWithReasoning(t *testing.T) {
	dbPath := FindOpenCodeDB()
	if dbPath == "" {
		t.Skip("No opencode DB found")
	}

	sessionID := "ses_1fd655113ffeiSdqZldHDmO3gj"

	events, err := OpenCodeDBHistory(sessionID)
	if err != nil {
		t.Fatalf("OpenCodeDBHistory failed: %v", err)
	}

	t.Logf("Total events: %d", len(events))
	if len(events) == 0 {
		t.Skip("No events found for this session")
	}

	// #77 roundtrip: a real session that contains reasoning + tool parts must
	// surface kind=thinking and kind=tool_use events (not just plain text).
	var thinking, toolUse, plain int
	for _, e := range events {
		switch e.Kind {
		case "thinking":
			thinking++
		case "tool_use":
			toolUse++
		case "":
			plain++
		}
	}
	t.Logf("thinking=%d tool_use=%d plain/text=%d", thinking, toolUse, plain)
	if thinking == 0 {
		t.Errorf("expected >=1 kind=thinking event from a reasoning-heavy session, got 0")
	}
	if toolUse == 0 {
		t.Errorf("expected >=1 kind=tool_use event from a tool-heavy session, got 0")
	}
	// tool_use events must carry a display ToolName and "[Tool...]" text.
	for _, e := range events {
		if e.Kind == "tool_use" {
			if e.ToolName == "" {
				t.Errorf("tool_use event missing ToolName: %+v", e)
			}
			if len(e.Text) == 0 || e.Text[0] != '[' {
				t.Errorf("tool_use event text should be a [Tool: ...] summary, got %q", e.Text)
			}
			break
		}
	}
	// Sub-event MsgIDs (reasoning/tool) must be distinct from the message text
	// MsgID so the app does not collapse them via update-by-msgId.
	seen := map[string]int{}
	for _, e := range events {
		if e.Kind == "thinking" || e.Kind == "tool_use" {
			seen[e.MsgID]++
		}
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("sub-event MsgID %q reused %d times (must be unique per part)", id, n)
		}
	}
}
