package agent_test

import (
	"testing"

	"github.com/phone-talk/agentd/internal/agent"
)

func TestCleanPermissionText(t *testing.T) {
	pr := agent.NewPermissionResolver()
	text := "\x1b[31m\x1b[1mPermission\x1b[0m required"
	cleaned := pr.CleanPermissionText(text)
	if cleaned != "Permission required" {
		t.Fatalf("expected 'Permission required', got %q", cleaned)
	}
}

func TestDetectPermissionPrompt_Bypass(t *testing.T) {
	pr := agent.NewPermissionResolver()
	if !pr.DetectPermissionPrompt("bypass permissions on this") {
		t.Fatal("expected true for bypass+permission")
	}
}

func TestDetectPermissionPrompt_ShiftTab(t *testing.T) {
	pr := agent.NewPermissionResolver()
	if !pr.DetectPermissionPrompt("shift+tab to cycle") {
		t.Fatal("expected true for shift+tab+cycle")
	}
}

func TestDetectPermissionPrompt_NoMatch(t *testing.T) {
	pr := agent.NewPermissionResolver()
	if pr.DetectPermissionPrompt("hello world") {
		t.Fatal("expected false for unrelated text")
	}
}

func TestDetectPermissionPrompt_LegacyPattern(t *testing.T) {
	pr := agent.NewPermissionResolver()
	if !pr.DetectPermissionPrompt("⏵⏵ bypass") {
		t.Fatal("expected true for legacy pattern")
	}
}

func TestMaybeExtractSessionIDFromRaw(t *testing.T) {
	pr := agent.NewPermissionResolver()
	id := pr.MaybeExtractSessionIDFromRaw(`{"session_id":"sess-123"}`)
	if id != "sess-123" {
		t.Fatalf("expected sess-123, got %q", id)
	}
}

func TestMaybeExtractSessionIDFromRaw_Empty(t *testing.T) {
	pr := agent.NewPermissionResolver()
	id := pr.MaybeExtractSessionIDFromRaw("")
	if id != "" {
		t.Fatalf("expected empty, got %q", id)
	}
}

func TestMaybeExtractSessionIDFromRaw_Nested(t *testing.T) {
	pr := agent.NewPermissionResolver()
	id := pr.MaybeExtractSessionIDFromRaw(`{"message":{"sessionId":"nested-456"}}`)
	if id != "nested-456" {
		t.Fatalf("expected nested-456, got %q", id)
	}
}
