package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterParentAgentsKeepsOnlyParents(t *testing.T) {
	input := []ProcessInfo{
		{PID: 100, PPID: 1, Provider: "claude", WorkDir: "/repo"},
		{PID: 101, PPID: 100, Provider: "claude", WorkDir: "/repo"},
		{PID: 102, PPID: 101, Provider: "claude", WorkDir: "/repo"},
		{PID: 200, PPID: 1, Provider: "opencode", WorkDir: "/repo"},
		{PID: 201, PPID: 200, Provider: "opencode", WorkDir: "/repo"},
		{PID: 300, PPID: 1, Provider: "claude", WorkDir: "/other"},
	}

	got := filterParentAgents(input)
	if len(got) != 3 {
		t.Fatalf("expected 3 parent agents, got %d: %#v", len(got), got)
	}

	pids := map[int]bool{}
	for _, proc := range got {
		pids[proc.PID] = true
	}

	for _, pid := range []int{100, 200, 300} {
		if !pids[pid] {
			t.Fatalf("expected pid %d to be kept, got %#v", pid, got)
		}
	}
	for _, pid := range []int{101, 102, 201} {
		if pids[pid] {
			t.Fatalf("expected child pid %d to be filtered, got %#v", pid, got)
		}
	}
}

func TestFinalizeProcessScanAddsLiveClaudeSessionInfo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(home, ".claude", "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "sessions", "123.json"), []byte(`{"sessionId":"sess-live"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(projectDir, "sess-live.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := finalizeProcessScan([]ProcessInfo{{
		PID:      123,
		PPID:     1,
		Provider: "claude",
		WorkDir:  "/repo",
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 process, got %d", len(got))
	}
	if got[0].SessionID != "sess-live" {
		t.Fatalf("expected session id sess-live, got %q", got[0].SessionID)
	}
	if got[0].SessionFile != sessionFile {
		t.Fatalf("expected session file %q, got %q", sessionFile, got[0].SessionFile)
	}
}

func TestFinalizeProcessScanKeepsClaudeWithoutLiveSessionMapping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Process without session mapping should still be visible (attachable but not resumable)
	got := finalizeProcessScan([]ProcessInfo{{
		PID:      999,
		PPID:     1,
		Provider: "claude",
		WorkDir:  "/repo",
	}})
	if len(got) != 1 {
		t.Fatalf("expected unmapped claude process to be kept, got %#v", got)
	}
	if got[0].SessionID != "" || got[0].SessionFile != "" {
		t.Fatalf("expected empty session info for unmapped process, got sessionID=%q, sessionFile=%q", got[0].SessionID, got[0].SessionFile)
	}
}

func TestResolveTmuxTargetFromPaneListMatchesTTY(t *testing.T) {
	output := "/dev/ttys001\tmain\tmain:0.0\n/dev/ttys002\tdev\tdev:1.2\n"

	target, session := resolveTmuxTargetFromPaneList(output, "ttys002")
	if target != "dev:1.2" {
		t.Fatalf("expected target dev:1.2, got %q", target)
	}
	if session != "dev" {
		t.Fatalf("expected session dev, got %q", session)
	}
}

func TestProcessInfoAttachRoutingMetadata(t *testing.T) {
	t.Run("tmux is writable", func(t *testing.T) {
		proc := ProcessInfo{Provider: "claude", Terminal: "/dev/ttys002", TmuxTarget: "dev:1.2"}
		if got := proc.AttachMode(); got != AttachModeTmux {
			t.Fatalf("expected attach mode %q, got %q", AttachModeTmux, got)
		}
		if proc.AttachReadOnly() {
			t.Fatal("expected tmux attach to be writable")
		}
		if got := proc.AttachReadOnlyReason(); got != "" {
			t.Fatalf("expected empty read-only reason, got %q", got)
		}
	})

	t.Run("claude tty without tmux is read-only", func(t *testing.T) {
		proc := ProcessInfo{Provider: "claude", Terminal: "/dev/ttys002"}
		if got := proc.AttachMode(); got != AttachModeWatcher {
			t.Fatalf("expected attach mode %q, got %q", AttachModeWatcher, got)
		}
		if !proc.AttachReadOnly() {
			t.Fatal("expected watcher attach to be read-only")
		}
		if got := proc.AttachReadOnlyReason(); got == "" {
			t.Fatal("expected non-empty read-only reason")
		}
	})
}
