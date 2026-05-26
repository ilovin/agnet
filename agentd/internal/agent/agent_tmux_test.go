package agent

import (
	"strings"
	"sync"
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

// TestSendTmuxLiteralChunksLargePayload verifies that sendTmuxLiteral splits
// payloads larger than tmuxSendKeysMaxChunk into multiple `tmux send-keys -l`
// invocations. Plan §M4 §2.4: large messages must not exceed tmux ARG_MAX.
func TestSendTmuxLiteralChunksLargePayload(t *testing.T) {
	const totalSize = 8 * 1024 // 8KB
	payload := strings.Repeat("x", totalSize)
	expectedChunks := totalSize / tmuxSendKeysMaxChunk
	if totalSize%tmuxSendKeysMaxChunk != 0 {
		expectedChunks++
	}

	var (
		mu         sync.Mutex
		calls      int
		seen       []string
		gotTargets []string
	)
	restore := SetTmuxSendKeysFuncForTest(func(args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		// args layout: ["-t", target, "-l", chunk]
		if len(args) != 4 {
			t.Errorf("unexpected args layout: %v", args)
			return nil, nil
		}
		gotTargets = append(gotTargets, args[1])
		seen = append(seen, args[3])
		return nil, nil
	})
	defer restore()

	if err := sendTmuxLiteral("sess:1.0", payload); err != nil {
		t.Fatalf("sendTmuxLiteral: %v", err)
	}

	if calls != expectedChunks {
		t.Fatalf("expected %d send-keys calls for %d-byte payload, got %d",
			expectedChunks, totalSize, calls)
	}
	got := strings.Join(seen, "")
	if got != payload {
		t.Fatalf("reassembled chunks length=%d, want %d", len(got), len(payload))
	}
	for _, tgt := range gotTargets {
		if tgt != "sess:1.0" {
			t.Fatalf("target = %q, want sess:1.0", tgt)
		}
	}
	for i, c := range seen {
		if i < expectedChunks-1 && len(c) != tmuxSendKeysMaxChunk {
			t.Errorf("chunk %d has size %d, want %d", i, len(c), tmuxSendKeysMaxChunk)
		}
	}
}

// TestSendTmuxLiteralSmallPayloadSingleCall verifies the no-chunking happy path
// for normal-sized messages.
func TestSendTmuxLiteralSmallPayloadSingleCall(t *testing.T) {
	var calls int
	restore := SetTmuxSendKeysFuncForTest(func(args ...string) ([]byte, error) {
		calls++
		return nil, nil
	})
	defer restore()

	if err := sendTmuxLiteral("sess:1.0", "hello"); err != nil {
		t.Fatalf("sendTmuxLiteral: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 send-keys call for short message, got %d", calls)
	}
}

// TestValidateNonShellPaneRejectsShells verifies the M4 §2.2 guard fires for
// each shell in the blacklist (case-insensitive).
func TestValidateNonShellPaneRejectsShells(t *testing.T) {
	cases := []string{"bash", "sh", "zsh", "fish", "dash", "ksh", "BASH", "Zsh"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			restore := SetTmuxDisplayMessageFuncForTest(func(target, format string) (string, error) {
				return name, nil
			})
			defer restore()
			err := ValidateNonShellPane("sess:1.0")
			if err == nil {
				t.Fatalf("expected error for shell %q, got nil", name)
			}
			if !strings.Contains(err.Error(), "shell") {
				t.Fatalf("error %q does not mention shell", err)
			}
		})
	}
}

// TestValidateNonShellPaneAcceptsNonShell verifies validation passes when the
// pane's foreground process is the CLI (or its interpreter, e.g. python3).
// Recon §3 documents that hermes shows up as "python3" via tmux's
// pane_current_command, so the validation must accept that name.
func TestValidateNonShellPaneAcceptsNonShell(t *testing.T) {
	cases := []string{"python3", "hermes", "claude", "node", "go"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			restore := SetTmuxDisplayMessageFuncForTest(func(target, format string) (string, error) {
				return name, nil
			})
			defer restore()
			if err := ValidateNonShellPane("sess:1.0"); err != nil {
				t.Fatalf("expected nil for non-shell %q, got %v", name, err)
			}
		})
	}
}

// TestValidateNonShellPaneEmptyOutputErrors ensures we treat an empty
// pane_current_command as failure (cannot verify CLI is running) rather than
// accepting it.
func TestValidateNonShellPaneEmptyOutputErrors(t *testing.T) {
	restore := SetTmuxDisplayMessageFuncForTest(func(target, format string) (string, error) {
		return "", nil
	})
	defer restore()
	if err := ValidateNonShellPane("sess:1.0"); err == nil {
		t.Fatalf("expected error on empty pane_current_command")
	}
}

// TestValidateNonShellPaneEmptyTargetErrors guards against accidentally
// passing an empty tmux target.
func TestValidateNonShellPaneEmptyTargetErrors(t *testing.T) {
	if err := ValidateNonShellPane(""); err == nil {
		t.Fatalf("expected error on empty target")
	}
}

// TestAgentBeginEndSendTracksFlag verifies the BeginSend/EndSend/IsSending
// triple wired for hermes session-switch suppression. Plan §M4 §2.1.
func TestAgentBeginEndSendTracksFlag(t *testing.T) {
	ag := NewTestAgent("hermes-test", "hermes")
	if ag.IsSending() {
		t.Fatalf("freshly-constructed agent should not be sending")
	}
	ag.BeginSend()
	if !ag.IsSending() {
		t.Fatalf("IsSending should be true after BeginSend")
	}
	ag.EndSend()
	if ag.IsSending() {
		t.Fatalf("IsSending should be false after EndSend")
	}
}
