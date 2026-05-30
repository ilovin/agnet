package provider

import "testing"

func TestSessionLifecyclePolicyFor(t *testing.T) {
	opencode := SessionLifecyclePolicyFor("opencode")
	if !opencode.ReloadHistory || !opencode.EmitClearedSessionID {
		t.Fatalf("opencode policy mismatch: %+v", opencode)
	}

	claude := SessionLifecyclePolicyFor("claude")
	if claude.EmitClearedSessionID {
		t.Fatalf("claude should not emit cleared session id by default")
	}

	custom := SessionLifecyclePolicyFor("custom")
	if !custom.ClearPersistedEvents || !custom.ResetEventBuffer || !custom.EmitClearedEvent {
		t.Fatalf("custom default policy should be safe-cleanup: %+v", custom)
	}
}
