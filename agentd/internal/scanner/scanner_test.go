package scanner

import (
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

func TestProjectDirNameDotPrefixDir(t *testing.T) {
	got := projectDirName("/ephstorage/geo_front/.workspace/argus_post_integration")
	want := "-ephstorage-geo-front--workspace-argus-post-integration"
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

	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
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

func TestFinalizeProcessScanKeepsHermesGatewayRunArgs(t *testing.T) {
	got := finalizeProcessScan([]ProcessInfo{{
		PID:      321,
		PPID:     1,
		Provider: "hermes",
		Cmd:      "hermes",
		Args:     []string{"gateway", "run"},
		WorkDir:  "/repo",
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 hermes process, got %d", len(got))
	}
	if got[0].Provider != "hermes" {
		t.Fatalf("expected provider hermes, got %q", got[0].Provider)
	}
	if len(got[0].Args) < 2 || got[0].Args[0] != "gateway" || got[0].Args[1] != "run" {
		t.Fatalf("expected hermes args to keep gateway run, got %#v", got[0].Args)
	}
}

func TestFinalizeProcessScanMapsHermesSessionIDFromArgs(t *testing.T) {
	got := finalizeProcessScan([]ProcessInfo{{
		PID:      654,
		PPID:     1,
		Provider: "hermes",
		Cmd:      "hermes",
		Args:     []string{"gateway", "run", "--session", "sess-hermes-1"},
		WorkDir:  "/repo",
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 hermes process, got %d", len(got))
	}
	if got[0].SessionID != "sess-hermes-1" {
		t.Fatalf("expected hermes session id sess-hermes-1, got %q", got[0].SessionID)
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

func TestFindClaudeSessionInfoReturnsEmptyWhenNoJSONL(t *testing.T) {
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

	ownPID := os.Getpid()

	// No .jsonl file exists in projectDir - should return empty
	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotSessionID != "" {
		t.Fatalf("expected empty session id when no .jsonl exists, got %q", gotSessionID)
	}
	if gotSessionFile != "" {
		t.Fatalf("expected empty session file when no .jsonl exists, got %q", gotSessionFile)
	}
}

func TestFindClaudeSessionInfoReturnsEmptyWithoutResume(t *testing.T) {
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

	ownPID := os.Getpid()

	// No .jsonl file exists and no task fd links (claude started without --resume)
	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotSessionID != "" {
		t.Fatalf("expected empty session ID when no .jsonl exists, got %q", gotSessionID)
	}
	if gotSessionFile != "" {
		t.Fatalf("expected empty session file when no .jsonl exists, got %q", gotSessionFile)
	}
}

func TestFindClaudeSessionInfoDoesNotMutateProjectDir(t *testing.T) {
	// Setup: Create a temp filesystem with two mock user home dirs.
	// We override homeBaseDir so the root scan loop reads from our temp dir
	// instead of the real /home.
	origHomeBaseDir := homeBaseDir
	defer func() { homeBaseDir = origHomeBaseDir }()

	mockHomeBase := t.TempDir()
	homeBaseDir = mockHomeBase

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create userA with a session file but no task fd links
	userAHome := filepath.Join(mockHomeBase, "userA")
	userAProjectDir := filepath.Join(userAHome, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(userAProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userAProjectDir, "sessionA.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	userATasksDir := filepath.Join(userAHome, ".claude", "tasks")
	if err := os.MkdirAll(userATasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create userB with an empty project dir (no sessions)
	userBHome := filepath.Join(mockHomeBase, "userB")
	userBProjectDir := filepath.Join(userBHome, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(userBProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userBTasksDir := filepath.Join(userBHome, ".claude", "tasks")
	if err := os.MkdirAll(userBTasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Set HOME to userA so the initial projectDir points to userA
	t.Setenv("HOME", userAHome)

	ownPID := os.Getpid()

	// The bug: after the root scan loop, projectDir would point to userB's dir
	// (the last user in the loop) because it was mutated in-place.
	// listSessionCandidates would then scan userB's dir (empty) and return empty.
	// After fix: projectDir should still point to userA's dir, and the session
	// file should be found.
	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotSessionID != "sessionA" {
		t.Fatalf("expected session id sessionA, got %q", gotSessionID)
	}
	wantFile := filepath.Join(userAProjectDir, "sessionA.jsonl")
	if gotSessionFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, gotSessionFile)
	}
}

func TestFindClaudeSessionInfoReturnsEmptyWithNonUUIDResumeNoJSONL(t *testing.T) {
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

	ownPID := os.Getpid()

	// Non-UUID session name but no .jsonl file - should still return empty
	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotSessionID != "" {
		t.Fatalf("expected empty session ID when no .jsonl exists, got %q", gotSessionID)
	}
	if gotSessionFile != "" {
		t.Fatalf("expected empty session file when no .jsonl exists, got %q", gotSessionFile)
	}
}

// TestFindClaudeSessionInfoGlobalFallback tests the global fallback when
// projectDirName(workDir) does not match the actual directory name on disk
// (e.g., /proc/PID/cwd resolves ".workspace" as "workspace").
func TestFindClaudeSessionInfoGlobalFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Simulate workDir as reported by /proc/PID/cwd (dots stripped from hidden dirs)
	workDir := "/ephstorage/geo_front/workspace/argus_post_integration"

	// The actual project directory on disk retains the dot
	actualProjectDirName := "-ephstorage-geo-front--workspace-argus-post-integration"
	actualProjectDir := filepath.Join(home, ".claude", "projects", actualProjectDirName)
	if err := os.MkdirAll(actualProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a session file in the actual directory
	baseTime := time.Now()
	sessionFile := filepath.Join(actualProjectDir, "sess-fallback.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"timestamp":"`+baseTime.Format(time.RFC3339)+`"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sessionFile, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}

	// No task fd links (no tasks dir / open handles)
	ownPID := os.Getpid()

	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotSessionID != "sess-fallback" {
		t.Fatalf("expected session id sess-fallback, got %q", gotSessionID)
	}
	if gotSessionFile != sessionFile {
		t.Fatalf("expected session file %q, got %q", sessionFile, gotSessionFile)
	}
}

// TestFindClaudeSessionInfoGlobalFallbackPrefersTaskSession tests that when
// both a global fallback candidate and a task-fd session exist, the task-fd
// session is preferred (because it is more specific).
func TestFindClaudeSessionInfoGlobalFallbackPrefersTaskSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := "/ephstorage/geo_front/workspace/argus_post_integration"
	actualProjectDirName := "-ephstorage-geo-front--workspace-argus-post-integration"
	actualProjectDir := filepath.Join(home, ".claude", "projects", actualProjectDirName)
	if err := os.MkdirAll(actualProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tasksDir := filepath.Join(home, ".claude", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	baseTime := time.Now()

	// Session A: from global fallback (older)
	sessionAFile := filepath.Join(actualProjectDir, "sess-a.jsonl")
	if err := os.WriteFile(sessionAFile, []byte(`{"timestamp":"`+baseTime.Add(-time.Hour).Format(time.RFC3339)+`"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sessionAFile, baseTime.Add(-time.Hour), baseTime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Session B: has an open task dir (newer, should be preferred)
	sessionBFile := filepath.Join(actualProjectDir, "sess-b.jsonl")
	if err := os.WriteFile(sessionBFile, []byte(`{"timestamp":"`+baseTime.Format(time.RFC3339)+`"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sessionBFile, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}

	taskDir := filepath.Join(tasksDir, "sess-b")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, ".highwatermark"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveTask := filepath.Join(taskDir, "42.json")
	if err := os.WriteFile(liveTask, []byte(`{"id":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(liveTask, baseTime.Add(10*time.Second), baseTime.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}

	dirHandle, err := os.Open(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	defer dirHandle.Close()

	ownPID := os.Getpid()

	gotSessionID, gotSessionFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotSessionID != "sess-b" {
		t.Fatalf("expected session id sess-b (task-fd preferred), got %q", gotSessionID)
	}
	if gotSessionFile != sessionBFile {
		t.Fatalf("expected session file %q, got %q", sessionBFile, gotSessionFile)
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

func TestIsClaudeSubagentArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "plain interactive", args: []string{"--dangerously-skip-permissions"}, want: false},
		{name: "print mode", args: []string{"-p", "hello"}, want: true},
		{name: "output format separate flag", args: []string{"--output-format", "stream-json"}, want: true},
		{name: "output format equals", args: []string{"--output-format=stream-json"}, want: true},
		{name: "similar token not match", args: []string{"-path", "foo"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isClaudeSubagentArgs(tt.args)
			if got != tt.want {
				t.Fatalf("isClaudeSubagentArgs(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want string
	}{
		{"native claude binary", "/usr/local/bin/claude", nil, "claude"},
		{"native opencode binary", "/usr/local/bin/opencode", nil, "opencode"},
		{"node wrapper with opencode.js", "/usr/bin/node", []string{"/path/to/opencode.js"}, "opencode"},
		{"node wrapper with opencode in path", "/usr/bin/node", []string{"/home/user/.nvm/versions/node/v20/opencode/bin/opencode"}, "opencode"},
		{"nodejs wrapper", "/usr/bin/nodejs", []string{"/opt/opencode/dist/index.js"}, "opencode"},
		{"node without opencode", "/usr/bin/node", []string{"server.js"}, ""},
		{"node with unrelated arg", "/usr/bin/node", []string{"--max-old-space-size=4096", "app.js"}, ""},
		{"random binary", "/usr/bin/python3", []string{"script.py"}, ""},
		{"claude-code binary", "/usr/local/bin/claude-code", nil, "claude"},
		{"node wrapper case insensitive", "/usr/bin/node", []string{"/path/to/OpenCode.js"}, "opencode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectProvider(tt.cmd, tt.args)
			if got != tt.want {
				t.Fatalf("detectProvider(%q, %v) = %q, want %q", tt.cmd, tt.args, got, tt.want)
			}
		})
	}
}
