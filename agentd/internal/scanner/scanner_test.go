package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectDirNameMatchesClaudeNaming(t *testing.T) {
	got := projectDirName("/Users/fengming.xie/Documents/project/phone_talk/")
	want := "-Users-fengming-xie-Documents-project-phone-talk"
	if got != want {
		t.Fatalf("expected project dir %q, got %q", want, got)
	}
}

func TestFindClaudeSessionInfoPrefersMostActiveOpenTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasksDir := filepath.Join(home, ".claude", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessions := []string{"sess-old", "sess-live"}
	baseTime := time.Now()
	var openDirs []*os.File
	for i, sid := range sessions {
		sessionFile := filepath.Join(projectDir, sid+".jsonl")
		if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(sessionFile, baseTime.Add(time.Duration(i)*time.Second), baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
		taskDir := filepath.Join(tasksDir, sid)
		if err := os.MkdirAll(taskDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(taskDir, ".highwatermark"), []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if sid == "sess-live" {
			liveTask := filepath.Join(taskDir, "26.json")
			if err := os.WriteFile(liveTask, []byte(`{"id":26}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(liveTask, baseTime.Add(10*time.Second), baseTime.Add(10*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		dirHandle, err := os.Open(taskDir)
		if err != nil {
			t.Fatal(err)
		}
		openDirs = append(openDirs, dirHandle)
	}
	defer func() {
		for _, dirHandle := range openDirs {
			dirHandle.Close()
		}
	}()

	ownPID := os.Getpid()
	if err := os.WriteFile(filepath.Join(sessionsDir, fmt.Sprintf("%d.json", ownPID)), []byte(`{"sessionId":"sess-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir)
	if gotSessionID != "sess-live" {
		t.Fatalf("expected session id sess-live, got %q", gotSessionID)
	}
	wantFile := filepath.Join(projectDir, "sess-live.jsonl")
	if gotSessionFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, gotSessionFile)
	}
}

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

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasksDir := filepath.Join(home, ".claude", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ownPID := os.Getpid()
	sessionFile := filepath.Join(projectDir, "sess-live.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(tasksDir, "sess-live")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, ".highwatermark"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirHandle, err := os.Open(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	defer dirHandle.Close()

	got := finalizeProcessScan([]ProcessInfo{{
		PID:      ownPID,
		PPID:     1,
		Provider: "claude",
		WorkDir:  workDir,
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
