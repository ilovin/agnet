package scanner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// TestFindClaudeSessionInfoPrefersClaudePIDSessionMap exercises the
// `~/.claude/sessions/<PID>.json` ground truth that claude itself maintains.
// This file is the only authoritative PID -> sessionId mapping on macOS where
// task-fd discovery via lsof is unreliable (claude opens-writes-closes its
// task files without keeping fds around). Without this hook the resolver falls
// back to mtime + contentMatch, which can pick a stale jsonl with similar
// content fingerprints and bounce between candidates between scans.
func TestFindClaudeSessionInfoPrefersClaudePIDSessionMap(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(home, ".claude", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Two candidate jsonls with similar mtimes. Without the pid map the resolver
	// is forced to pick one heuristically; we want the truth file to win
	// regardless of which heuristic would otherwise apply.
	staleSession := "stale-content-match"
	liveSession := "live-truth"
	now := time.Now()
	for i, sid := range []string{staleSession, liveSession} {
		p := filepath.Join(projectDir, sid+".jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Make the stale file MORE RECENT so any mtime-based fallback would
		// prefer it. The pid map must override this.
		mt := now.Add(time.Duration(-i) * time.Second)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	// Write the authoritative pid -> sessionId map (mirrors what claude writes
	// to ~/.claude/sessions/<PID>.json on every status update).
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ownPID := os.Getpid()
	pidMap := filepath.Join(sessionsDir, strconv.Itoa(ownPID)+".json")
	pidMapBody := `{"pid":` + strconv.Itoa(ownPID) + `,"sessionId":"` + liveSession + `","cwd":"` + workDir + `","status":"busy"}`
	if err := os.WriteFile(pidMap, []byte(pidMapBody), 0o644); err != nil {
		t.Fatal(err)
	}

	gotID, gotFile := findClaudeSessionInfo(ownPID, workDir, "")
	if gotID != liveSession {
		t.Fatalf("expected session id %q from pid map, got %q (resolver fell back to heuristic)", liveSession, gotID)
	}
	wantFile := filepath.Join(projectDir, liveSession+".jsonl")
	if gotFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, gotFile)
	}
}

// TestFindClaudeSessionInfoFallsBackWhenPIDMapAbsent ensures the new pid-map
// step is purely additive: if the file is missing the resolver still works via
// the existing task-fd / mtime / contentMatch chain.
func TestFindClaudeSessionInfoFallsBackWhenPIDMapAbsent(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(home, ".claude", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	sid := "only-session"
	p := filepath.Join(projectDir, sid+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gotID, _ := findClaudeSessionInfo(os.Getpid(), workDir, "")
	if gotID != sid {
		t.Fatalf("expected fallback resolver to return %q, got %q", sid, gotID)
	}
}

// TestFindClaudeSessionInfoIgnoresPIDMapForOtherPID guards against blindly
// trusting any sessions/*.json file: the resolver must only honor the entry
// keyed by the requested PID. Otherwise a stale entry from a long-dead process
// could leak into another agent's lookup.
func TestFindClaudeSessionInfoIgnoresPIDMapForOtherPID(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(home, ".claude", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	realSession := "real-session"
	p := filepath.Join(projectDir, realSession+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Map for an unrelated PID should be ignored.
	otherPID := os.Getpid() + 1
	otherFile := filepath.Join(sessionsDir, strconv.Itoa(otherPID)+".json")
	body := `{"pid":` + strconv.Itoa(otherPID) + `,"sessionId":"some-other-session"}`
	if err := os.WriteFile(otherFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	gotID, _ := findClaudeSessionInfo(os.Getpid(), workDir, "")
	if gotID != realSession {
		t.Fatalf("expected fallback to %q (ignoring other-pid map), got %q", realSession, gotID)
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

func TestAttachReadOnlyReason_HermesNoTmux(t *testing.T) {
	proc := ProcessInfo{Provider: "hermes"}
	reason := proc.AttachReadOnlyReason()
	if reason == "" {
		t.Fatal("expected non-empty read-only reason for hermes without tmux")
	}
	if !strings.Contains(reason, "no tmux pane") {
		t.Fatalf("expected reason to mention 'no tmux pane', got %q", reason)
	}
}

func TestAttachReadOnlyReason_HermesTmux(t *testing.T) {
	proc := ProcessInfo{Provider: "hermes", TmuxTarget: "sess:0.1"}
	if got := proc.AttachReadOnlyReason(); got != "" {
		t.Fatalf("expected empty read-only reason for hermes with tmux, got %q", got)
	}
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
		{"native codex binary", "/usr/local/bin/codex", nil, "codex"},
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

func TestFindCodexSessionInfoByWorkDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(home, ".codex", "sessions", "2026", "05", "30")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "rollout-2026-05-30T12-26-57-019e7722-9bb3-7733-bb07-94fe9d0809a5.jsonl")
	line := `{"timestamp":"2026-05-30T04:27:15.964Z","type":"session_meta","payload":{"id":"019e7722-9bb3-7733-bb07-94fe9d0809a5","cwd":"` + workDir + `"}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionID, gotFile := findCodexSessionInfo(1234, workDir)
	if sessionID != "019e7722-9bb3-7733-bb07-94fe9d0809a5" {
		t.Fatalf("session id = %q, want %q", sessionID, "019e7722-9bb3-7733-bb07-94fe9d0809a5")
	}
	if gotFile != sessionFile {
		t.Fatalf("session file = %q, want %q", gotFile, sessionFile)
	}
}

func TestFindClaudeSessionInfoReturnsEmptyWhenMultipleCandidatesAndNoConfidentMatch(t *testing.T) {
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

	baseTime := time.Now()
	for i := 0; i < 2; i++ {
		sid := "sess-" + string(rune('a'+i))
		jsonlPath := filepath.Join(projectDir, sid+".jsonl")
		if err := os.WriteFile(jsonlPath, []byte("{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"unlikely fingerprint text\"}]}}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(jsonlPath, baseTime.Add(time.Duration(i)*time.Second), baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	gotSessionID, gotSessionFile := findClaudeSessionInfo(os.Getpid(), workDir, "")
	if gotSessionID != "" || gotSessionFile != "" {
		t.Fatalf("expected empty result when no confident match and no task-fd hint, got sessionID=%q sessionFile=%q", gotSessionID, gotSessionFile)
	}
}

func TestContentMatchSessionExpandsCandidatesBeyondTopFive(t *testing.T) {
	dir := t.TempDir()
	var candidates []SessionCandidate

	for i := 0; i < 6; i++ {
		sid := "sess-" + string(rune('a'+i))
		jsonlPath := filepath.Join(dir, sid+".jsonl")
		text := "common text"
		if i == 5 {
			text = "unique expansion fingerprint target"
		}
		line := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"" + text + "\"}]}}\n"
		if err := os.WriteFile(jsonlPath, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, SessionCandidate{SessionID: sid, JSONLPath: jsonlPath, LastActivity: time.Now().Add(-time.Duration(i) * time.Second)})
	}

	matched := contentMatchSession("", candidates, nil)
	if matched != nil {
		t.Fatalf("expected nil without tmux target, got %+v", matched)
	}
}

func TestExtractFingerprintsIncludesToolUseOnlyMessages(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "sess-tool-only.jsonl")
	line := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Bash\",\"input\":{\"command\":\"go test ./internal/scanner -run TestFoo -count=1\"}}]}}\n"
	if err := os.WriteFile(jsonlPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	fps := extractFingerprints(jsonlPath, 20)
	if len(fps) == 0 {
		t.Fatalf("expected tool_use-only message to produce fingerprints, got none")
	}

	foundToolName := false
	for _, fp := range fps {
		if fp == "bash" || fp == "tool bash" {
			foundToolName = true
			break
		}
	}
	if !foundToolName {
		t.Fatalf("expected tool name fingerprint in %v", fps)
	}
}

func TestContentMatchSessionByPaneTextRejectsLowScore(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "sess-low.jsonl")
	line := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"single weak token\"}]}}\n"
	if err := os.WriteFile(jsonlPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates := []SessionCandidate{{SessionID: "sess-low", JSONLPath: jsonlPath, LastActivity: time.Now()}}

	matched := contentMatchSessionByPaneText("single weak token", candidates, nil)
	if matched != nil {
		t.Fatalf("expected low-score match to be rejected, got %+v", matched)
	}
}

func TestContentMatchSessionByPaneTextRejectsAmbiguousTopScores(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "sess-left.jsonl")
	rightPath := filepath.Join(dir, "sess-right.jsonl")

	left := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma\"}]}}\n"
	right := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta delta\"}]}}\n"
	if err := os.WriteFile(leftPath, []byte(left), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(right), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates := []SessionCandidate{
		{SessionID: "sess-left", JSONLPath: leftPath, LastActivity: time.Now()},
		{SessionID: "sess-right", JSONLPath: rightPath, LastActivity: time.Now().Add(-time.Second)},
	}

	pane := "deploy alpha beta"
	matched := contentMatchSessionByPaneText(pane, candidates, nil)
	if matched != nil {
		t.Fatalf("expected ambiguous top scores to be rejected, got %+v", matched)
	}
}

func TestContentMatchSessionByPaneTextAcceptsClearWinner(t *testing.T) {
	dir := t.TempDir()
	winPath := filepath.Join(dir, "sess-win.jsonl")
	losePath := filepath.Join(dir, "sess-lose.jsonl")

	winner := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon\"}]}}\n"
	loser := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"noise only\"}]}}\n"
	if err := os.WriteFile(winPath, []byte(winner), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(losePath, []byte(loser), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates := []SessionCandidate{
		{SessionID: "sess-win", JSONLPath: winPath, LastActivity: time.Now()},
		{SessionID: "sess-lose", JSONLPath: losePath, LastActivity: time.Now().Add(-time.Second)},
	}

	pane := "deploy alpha beta gamma epsilon"
	matched := contentMatchSessionByPaneText(pane, candidates, nil)
	if matched == nil || matched.SessionID != "sess-win" {
		t.Fatalf("expected sess-win clear match, got %+v", matched)
	}
}

func TestCleanTUITextPreservesUnicode(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"需要把 hermes client 的 URL 指向正确的地址", "需要把 hermes client 的 url 指向正确的地址"},
		{"有 2 个测试失败", "有 2 个测试失败"},
		{"为什么捕获的panel就那么点信息", "为什么捕获的panel就那么点信息"},
		{"根据这个 PRD 的开发路径", "根据这个 prd 的开发路径"},
		{"Bash(go test ./...) の結果", "bash go test の結果"},
		{"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt", "bypass permissions on shift tab to cycle esc to interrupt"},
	}

	for _, c := range cases {
		got := cleanTUIText(c.input)
		if got != c.expected {
			t.Errorf("cleanTUIText(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestContentMatchSessionByPaneTextWithChinese(t *testing.T) {
	dir := t.TempDir()

	leftPath := filepath.Join(dir, "sess-cn-left.jsonl")
	rightPath := filepath.Join(dir, "sess-cn-right.jsonl")

	left := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"需要把 hermes client 的 URL 指向正确的地址，然后检查端口是否开放\"}]}}\n"
	right := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"为什么捕获的panel就那么点信息，可能和contentmatch有关\"}]}}\n"
	if err := os.WriteFile(leftPath, []byte(left), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(right), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates := []SessionCandidate{
		{SessionID: "sess-cn-left", JSONLPath: leftPath, LastActivity: time.Now()},
		{SessionID: "sess-cn-right", JSONLPath: rightPath, LastActivity: time.Now().Add(-time.Second)},
	}

	// Pane content contains Chinese text matching sess-cn-left
	pane := "需要把 hermes client 的 URL 指向正确的地址"
	matched := contentMatchSessionByPaneText(pane, candidates, nil)
	if matched == nil || matched.SessionID != "sess-cn-left" {
		t.Fatalf("expected sess-cn-left clear match, got %+v", matched)
	}
}

func TestContentMatchSessionByPaneTextWithMixedCJKAndToolUse(t *testing.T) {
	dir := t.TempDir()

	leftPath := filepath.Join(dir, "sess-cjk-tool.jsonl")
	left := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Bash\",\"input\":{\"command\":\"echo 你好世界 这是测试命令\"}}]}}\n"
	if err := os.WriteFile(leftPath, []byte(left), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates := []SessionCandidate{
		{SessionID: "sess-cjk-tool", JSONLPath: leftPath, LastActivity: time.Now()},
	}

	pane := "在终端中执行 echo 你好世界 这是测试命令"
	matched := contentMatchSessionByPaneText(pane, candidates, nil)
	if matched == nil || matched.SessionID != "sess-cjk-tool" {
		t.Fatalf("expected sess-cjk-tool match for CJK tool_use fingerprint, got %+v", matched)
	}
}

func TestFilterByPaneActivityDoesNotHardPruneOutsideTolerance(t *testing.T) {
	paneActivity := time.Now()
	inTolerance := SessionCandidate{SessionID: "near", LastActivity: paneActivity.Add(-2 * time.Minute)}
	outOfTolerance := SessionCandidate{SessionID: "far", LastActivity: paneActivity.Add(-20 * time.Minute)}

	got := filterByPaneActivity([]SessionCandidate{inTolerance, outOfTolerance}, &paneActivity, 5*time.Minute)
	if len(got) != 2 {
		t.Fatalf("expected no hard prune (2 candidates), got %d", len(got))
	}
}

func TestContentMatchCache_HitReturnsSameResult(t *testing.T) {
	clearContentMatchCache()
	clearFingerprintsCache()

	dir := t.TempDir()
	winPath := filepath.Join(dir, "sess-win.jsonl")
	losePath := filepath.Join(dir, "sess-lose.jsonl")

	winner := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon\"}]}}\n"
	loser := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"noise only\"}]}}\n"
	if err := os.WriteFile(winPath, []byte(winner), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(losePath, []byte(loser), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates := []SessionCandidate{
		{SessionID: "sess-win", JSONLPath: winPath, LastActivity: time.Now()},
		{SessionID: "sess-lose", JSONLPath: losePath, LastActivity: time.Now().Add(-time.Second)},
	}

	pane := "deploy alpha beta gamma epsilon"

	// First call: scoring runs, cache populates.
	first := contentMatchSessionWithCache("tmux-target-A", pane, candidates, nil)
	if first == nil || first.SessionID != "sess-win" {
		t.Fatalf("expected first call to return sess-win, got %+v", first)
	}

	calls := atomicLoadFingerprintCallCount()

	// Second call with same key: should hit cache and not invoke extractFingerprints.
	second := contentMatchSessionWithCache("tmux-target-A", pane, candidates, nil)
	if second == nil || second.SessionID != "sess-win" {
		t.Fatalf("expected second call to return cached sess-win, got %+v", second)
	}
	if atomicLoadFingerprintCallCount() != calls {
		t.Fatalf("expected cache hit (no new extractFingerprints calls); before=%d after=%d", calls, atomicLoadFingerprintCallCount())
	}
}

func TestContentMatchCache_TTLExpires(t *testing.T) {
	clearContentMatchCache()
	clearFingerprintsCache()

	prevTTL := contentMatchCacheTTL
	contentMatchCacheTTL = 50 * time.Millisecond
	defer func() { contentMatchCacheTTL = prevTTL }()

	dir := t.TempDir()
	winPath := filepath.Join(dir, "sess-win.jsonl")
	winner := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon\"}]}}\n"
	if err := os.WriteFile(winPath, []byte(winner), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates := []SessionCandidate{
		{SessionID: "sess-win", JSONLPath: winPath, LastActivity: time.Now()},
	}
	pane := "deploy alpha beta gamma epsilon"

	first := contentMatchSessionWithCache("tmux-target-TTL", pane, candidates, nil)
	if first == nil {
		t.Fatalf("expected first call to populate cache")
	}

	// Force fingerprint cache invalidation so we can detect a recompute by call count.
	clearFingerprintsCache()
	callsBefore := atomicLoadFingerprintCallCount()

	time.Sleep(80 * time.Millisecond)

	second := contentMatchSessionWithCache("tmux-target-TTL", pane, candidates, nil)
	if second == nil {
		t.Fatalf("expected post-TTL call to recompute and return result")
	}
	if atomicLoadFingerprintCallCount() == callsBefore {
		t.Fatalf("expected extractFingerprints to be called again after TTL expiry")
	}
}

func TestContentMatchCache_InvalidatesOnCandidateChange(t *testing.T) {
	clearContentMatchCache()
	clearFingerprintsCache()

	dir := t.TempDir()
	aPath := filepath.Join(dir, "sess-a.jsonl")
	bPath := filepath.Join(dir, "sess-b.jsonl")
	a := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon\"}]}}\n"
	b := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"another distinct fingerprint set\"}]}}\n"
	if err := os.WriteFile(aPath, []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}

	pane := "deploy alpha beta gamma epsilon"

	cands1 := []SessionCandidate{
		{SessionID: "sess-a", JSONLPath: aPath, LastActivity: time.Now()},
	}
	cands2 := []SessionCandidate{
		{SessionID: "sess-a", JSONLPath: aPath, LastActivity: time.Now()},
		{SessionID: "sess-b", JSONLPath: bPath, LastActivity: time.Now()},
	}

	_ = contentMatchSessionWithCache("tmux-target-INV", pane, cands1, nil)
	callsBefore := atomicLoadFingerprintCallCount()

	// Different candidate set -> different key -> must recompute.
	_ = contentMatchSessionWithCache("tmux-target-INV", pane, cands2, nil)
	if atomicLoadFingerprintCallCount() == callsBefore {
		t.Fatalf("expected extractFingerprints to be called for changed candidate set")
	}
}

func TestExtractFingerprintsCache_HitOnSameMtime(t *testing.T) {
	clearFingerprintsCache()

	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "sess-cache.jsonl")
	line := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"some fingerprint content here\"}]}}\n"
	if err := os.WriteFile(jsonlPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	first := extractFingerprints(jsonlPath, 20)
	if len(first) == 0 {
		t.Fatalf("expected first call to return fingerprints")
	}
	callsBefore := atomicLoadFingerprintCallCount()

	second := extractFingerprints(jsonlPath, 20)
	if len(second) != len(first) {
		t.Fatalf("expected cached fingerprints to match first call: first=%d second=%d", len(first), len(second))
	}
	if atomicLoadFingerprintCallCount() != callsBefore {
		t.Fatalf("expected fingerprints cache hit (no recompute); before=%d after=%d", callsBefore, atomicLoadFingerprintCallCount())
	}
}

// TestContentMatchFuzzyScores verifies that fuzzy scoring produces meaningfully
// different scores between a fingerprint set that broadly matches the pane text
// (CJK + noise) and one that does not. This guards against the old substring
// approach where a single token could swing the integer hit count by 1 and flip
// the winner.
func TestContentMatchFuzzyScores(t *testing.T) {
	// paneText simulates a Chinese+English+terminal-decorated tmux capture.
	// It contains the deploy/部署 phrase plus noise like timestamps and
	// truncated escape sequences.
	pane := cleanTUIText("⏵⏵ 用户要求 deploy 到生产环境 的 staging 镜像 [12:34:56] hermes client 已就绪")

	// signal: fingerprints that strongly correspond to pane content.
	signal := []string{"deploy", "staging", "hermes client", "用户要求", "生产环境"}
	// noise: fingerprints that don't correspond to pane content.
	noise := []string{"completely unrelated", "alpha bravo", "qwerty xyzzy", "无关紧要", "另一个会话"}

	signalScore := matchScoreFuzzy(pane, signal)
	noiseScore := matchScoreFuzzy(pane, noise)

	if signalScore <= noiseScore {
		t.Fatalf("expected signal score (%v) > noise score (%v)", signalScore, noiseScore)
	}
	// Signal should be at least 30% above noise to be considered "confident"
	// (matches contentMatchMinMarginRatio).
	if (signalScore - noiseScore) < 0.30*signalScore {
		t.Fatalf("expected confident margin: signal=%v noise=%v relMargin=%v",
			signalScore, noiseScore, (signalScore-noiseScore)/signalScore)
	}
}

// TestContentMatchTieBreaksByActivity verifies that when two candidates score
// identically (e.g. similar jsonl content), the candidate with the more recent
// LastActivity is chosen. Without this tie-break, slice ordering is undefined
// and the chosen winner can flip between scans causing oscillation.
func TestContentMatchTieBreaksByActivity(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "sess-older.jsonl")
	newer := filepath.Join(dir, "sess-newer.jsonl")

	// Identical content -> identical fingerprint scores.
	body := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon\"}]}}\n"
	if err := os.WriteFile(older, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// Pass `older` first in slice order; activity tie-break must still pick `newer`.
	candidates := []SessionCandidate{
		{SessionID: "sess-older", JSONLPath: older, LastActivity: now.Add(-10 * time.Minute)},
		{SessionID: "sess-newer", JSONLPath: newer, LastActivity: now},
	}

	pane := "deploy alpha beta gamma epsilon"
	matched := contentMatchSessionByPaneText(pane, candidates, nil)
	if matched == nil {
		t.Fatalf("expected a match for tied scores via tie-break, got nil")
	}
	if matched.SessionID != "sess-newer" {
		t.Fatalf("expected newer session to win tie-break, got %s", matched.SessionID)
	}
}

// TestContentMatchWeakResultShortTTL verifies that a weak (low-margin) match
// is cached with a shorter TTL so the system can re-evaluate sooner. A strong
// match keeps the standard TTL for performance.
func TestContentMatchWeakResultShortTTL(t *testing.T) {
	clearContentMatchCache()
	clearFingerprintsCache()

	prevStrong := contentMatchCacheTTL
	prevWeak := contentMatchWeakCacheTTL
	contentMatchCacheTTL = 30 * time.Second
	contentMatchWeakCacheTTL = 5 * time.Second
	defer func() {
		contentMatchCacheTTL = prevStrong
		contentMatchWeakCacheTTL = prevWeak
	}()

	dir := t.TempDir()

	// Strong scenario: winner content uniquely matches pane; loser has totally
	// disjoint content -> margin ~ 1.0 -> strong TTL.
	strongWin := filepath.Join(dir, "sess-strong-win.jsonl")
	strongLose := filepath.Join(dir, "sess-strong-lose.jsonl")
	winner := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon zeta omega lambda\"}]}}\n"
	totallyDisjoint := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"unrelated content xyzzy plugh frobnicate\"}]}}\n"
	if err := os.WriteFile(strongWin, []byte(winner), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strongLose, []byte(totallyDisjoint), 0o644); err != nil {
		t.Fatal(err)
	}

	strongCands := []SessionCandidate{
		{SessionID: "sess-strong-win", JSONLPath: strongWin, LastActivity: time.Now()},
		{SessionID: "sess-strong-lose", JSONLPath: strongLose, LastActivity: time.Now().Add(-time.Second)},
	}
	strongPane := "deploy alpha beta gamma epsilon zeta omega lambda"
	matched := contentMatchSessionWithCache("tmux-target-strong", strongPane, strongCands, nil)
	if matched == nil || matched.SessionID != "sess-strong-win" {
		t.Fatalf("expected strong match to pick sess-strong-win, got %+v", matched)
	}

	// Inspect the cached entry's TTL: strong match should use the long TTL.
	contentMatchCacheMu.Lock()
	var strongExpiry time.Time
	for k, v := range contentMatchCache {
		if strings.HasPrefix(k, "tmux-target-strong|") {
			strongExpiry = v.expiry
			break
		}
	}
	contentMatchCacheMu.Unlock()
	if strongExpiry.IsZero() {
		t.Fatalf("expected strong-match cache entry to be present")
	}
	strongTTL := time.Until(strongExpiry)
	if strongTTL < 20*time.Second {
		t.Fatalf("expected strong-match TTL ~30s, got %v", strongTTL)
	}

	// Weak scenario: two candidates whose fingerprints overlap heavily with
	// the pane -> margin small but above the min threshold -> weak TTL.
	clearContentMatchCache()
	weakA := filepath.Join(dir, "sess-weak-a.jsonl")
	weakB := filepath.Join(dir, "sess-weak-b.jsonl")
	weakABody := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon zeta\"}]}}\n"
	weakBBody := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma noise tail\"}]}}\n"
	if err := os.WriteFile(weakA, []byte(weakABody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weakB, []byte(weakBBody), 0o644); err != nil {
		t.Fatal(err)
	}
	weakCands := []SessionCandidate{
		{SessionID: "sess-weak-a", JSONLPath: weakA, LastActivity: time.Now()},
		{SessionID: "sess-weak-b", JSONLPath: weakB, LastActivity: time.Now().Add(-time.Second)},
	}
	weakPane := "deploy alpha beta gamma epsilon zeta omega lambda"
	_ = contentMatchSessionWithCache("tmux-target-weak", weakPane, weakCands, nil)

	contentMatchCacheMu.Lock()
	var weakExpiry time.Time
	for k, v := range contentMatchCache {
		if strings.HasPrefix(k, "tmux-target-weak|") {
			weakExpiry = v.expiry
			break
		}
	}
	contentMatchCacheMu.Unlock()
	if weakExpiry.IsZero() {
		t.Fatalf("expected weak-match cache entry to be present")
	}
	weakTTL := time.Until(weakExpiry)
	if weakTTL > 10*time.Second {
		t.Fatalf("expected weak-match TTL ~5s, got %v", weakTTL)
	}
}

// TestClearContentMatchCacheForRemovesTarget verifies that
// ClearContentMatchCacheFor only invalidates entries for the specified tmux
// target so that stale matches are recomputed on the next scan, while other
// targets remain cached.
func TestClearContentMatchCacheForRemovesTarget(t *testing.T) {
	clearContentMatchCache()
	clearFingerprintsCache()

	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "sess.jsonl")
	body := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"deploy alpha beta gamma epsilon\"}]}}\n"
	if err := os.WriteFile(jsonlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates := []SessionCandidate{
		{SessionID: "sess", JSONLPath: jsonlPath, LastActivity: time.Now()},
	}
	pane := "deploy alpha beta gamma epsilon"

	// Populate cache for two different tmux targets.
	_ = contentMatchSessionWithCache("tmux-target-A", pane, candidates, nil)
	_ = contentMatchSessionWithCache("tmux-target-B", pane, candidates, nil)

	contentMatchCacheMu.Lock()
	beforeCount := len(contentMatchCache)
	contentMatchCacheMu.Unlock()
	if beforeCount < 2 {
		t.Fatalf("expected 2 cache entries after populate, got %d", beforeCount)
	}

	ClearContentMatchCacheFor("tmux-target-A")

	contentMatchCacheMu.Lock()
	defer contentMatchCacheMu.Unlock()
	for k := range contentMatchCache {
		if strings.HasPrefix(k, "tmux-target-A|") {
			t.Fatalf("expected target-A entries to be cleared, but found key=%s", k)
		}
	}
	hasB := false
	for k := range contentMatchCache {
		if strings.HasPrefix(k, "tmux-target-B|") {
			hasB = true
			break
		}
	}
	if !hasB {
		t.Fatalf("expected target-B entries to remain after clearing target-A")
	}
}

func TestExtractFingerprintsCache_InvalidatesOnMtime(t *testing.T) {
	clearFingerprintsCache()

	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "sess-mtime.jsonl")
	line := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"first content fingerprint\"}]}}\n"
	if err := os.WriteFile(jsonlPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = extractFingerprints(jsonlPath, 20)
	callsBefore := atomicLoadFingerprintCallCount()

	// Modify mtime by writing fresh content with a slight delay (filesystem mtime resolution).
	time.Sleep(20 * time.Millisecond)
	newLine := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"different content fingerprint after change\"}]}}\n"
	if err := os.WriteFile(jsonlPath, []byte(newLine), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bump mtime explicitly to be safe across filesystems.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(jsonlPath, future, future); err != nil {
		t.Fatal(err)
	}

	_ = extractFingerprints(jsonlPath, 20)
	if atomicLoadFingerprintCallCount() == callsBefore {
		t.Fatalf("expected fingerprints recompute after mtime change")
	}
}
