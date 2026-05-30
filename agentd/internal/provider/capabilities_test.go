package provider

import "testing"

func TestCapabilitiesForKnownProviders(t *testing.T) {
	codex := CapabilitiesFor("codex")
	if !codex.RequiresTmuxForegroundValidation {
		t.Fatalf("expected codex to require tmux foreground validation")
	}
	if codex.SendMode != SendModeTMUX {
		t.Fatalf("expected codex send mode tmux, got %q", codex.SendMode)
	}

	opencode := CapabilitiesFor("opencode")
	if opencode.SupportsImageAttachment {
		t.Fatalf("expected opencode to reject image attachments")
	}
	if opencode.SendMode != SendModeResumeCmd {
		t.Fatalf("expected opencode send mode resume_cmd, got %q", opencode.SendMode)
	}
}

func TestCapabilitiesForUnknownProvider(t *testing.T) {
	got := CapabilitiesFor("custom-provider")
	if got.Name != "custom-provider" {
		t.Fatalf("unexpected name %q", got.Name)
	}
	if got.SendMode != SendModePTY {
		t.Fatalf("expected default send mode pty, got %q", got.SendMode)
	}
	if !got.SupportsImageAttachment {
		t.Fatalf("default provider should support image attachments")
	}
}
