package agent

import (
	"strings"
	"testing"
)

// TestWriteInputHermesTmuxRoutesThroughSendKeys is the M3 contract test at
// the agent layer: a hermes-provider agent with attachMode=tmux + tmuxTarget
// set must route WriteInput through the generic tmux send-keys path, exactly
// like Claude tmux-attached agents.
//
// Plan: docs/plans/p-hermes-tmux-migration.md §M3.
func TestWriteInputHermesTmuxRoutesThroughSendKeys(t *testing.T) {
	ag := NewTestAgent("hermes-test", "hermes")
	ag.SetAttachInputRoute("tmux", false, "", "sess:1.1")

	var captured []string
	restore := SetTmuxSendKeysFuncForTest(func(args ...string) ([]byte, error) {
		captured = append(captured, strings.Join(args, " "))
		return nil, nil
	})
	defer restore()

	if err := ag.WriteInput("hello\n"); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}

	if len(captured) == 0 {
		t.Fatalf("expected at least one tmux send-keys call, got 0")
	}
	// The literal text and Enter key are issued as separate send-keys calls
	// because sendTmuxInput translates "\n" to a Key send.
	if !strings.Contains(captured[0], "-t sess:1.1") {
		t.Fatalf("first call missing target: %q", captured[0])
	}
	if !strings.Contains(captured[0], "-l hello") {
		t.Fatalf("first call missing literal payload: %q", captured[0])
	}
	enterSeen := false
	for _, c := range captured {
		if strings.HasSuffix(c, " Enter") {
			enterSeen = true
			break
		}
	}
	if !enterSeen {
		t.Fatalf("expected an Enter key send (\\n -> Enter), got calls=%v", captured)
	}
}
