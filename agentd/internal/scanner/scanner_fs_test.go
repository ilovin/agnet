package scanner

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFindClaudeSessionInfoWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	home := "/home/test"
	fs.SetHome(home)

	workDir := "/repo"
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	tasksDir := filepath.Join(home, ".claude", "tasks")

	fs.MkdirAll(projectDir, 0o755)
	fs.MkdirAll(tasksDir, 0o755)

	// Create a session .jsonl file
	fs.WriteFile(filepath.Join(projectDir, "sess-live.jsonl"), []byte("{}\n"), 0o644)

	// Create a task dir with a file
	taskDir := filepath.Join(tasksDir, "sess-live")
	fs.MkdirAll(taskDir, 0o755)
	fs.WriteFile(filepath.Join(taskDir, ".highwatermark"), []byte("1"), 0o644)

	// Simulate /proc/PID/cmdline showing "claude"
	pid := 1234
	fs.WriteFile(filepath.Join("/proc", "1234", "cmdline"), []byte("claude\x00"), 0o644)

	sessionID, sessionFile := findClaudeSessionInfoWithFS(fs, pid, workDir, "")
	if sessionID != "sess-live" {
		t.Fatalf("expected session id sess-live, got %q", sessionID)
	}
	wantFile := filepath.Join(projectDir, "sess-live.jsonl")
	if sessionFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, sessionFile)
	}
}

func TestFindClaudeSessionInfoPrefersTaskFDWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	home := "/home/test"
	fs.SetHome(home)

	workDir := "/repo"
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	tasksDir := filepath.Join(home, ".claude", "tasks")

	fs.MkdirAll(projectDir, 0o755)
	fs.MkdirAll(tasksDir, 0o755)

	// Create two session files
	fs.WriteFile(filepath.Join(projectDir, "sess-old.jsonl"), []byte("{}\n"), 0o644)
	fs.WriteFile(filepath.Join(projectDir, "sess-live.jsonl"), []byte("{}\n"), 0o644)

	// Create task dirs for both sessions
	fs.MkdirAll(filepath.Join(tasksDir, "sess-old"), 0o755)
	fs.MkdirAll(filepath.Join(tasksDir, "sess-live"), 0o755)
	fs.WriteFile(filepath.Join(tasksDir, "sess-live", "task.json"), []byte(`{}`), 0o644)

	// Simulate /proc/PID/fd with a symlink to the live task dir
	pid := 1234
	fs.WriteFile(filepath.Join("/proc", "1234", "cmdline"), []byte("claude\x00"), 0o644)
	fs.MkdirAll(filepath.Join("/proc", "1234", "fd"), 0o755)
	fs.Symlink(filepath.Join(tasksDir, "sess-live"), filepath.Join("/proc", "1234", "fd", "3"))

	sessionID, sessionFile := findClaudeSessionInfoWithFS(fs, pid, workDir, "")
	if sessionID != "sess-live" {
		t.Fatalf("expected session id sess-live, got %q", sessionID)
	}
	wantFile := filepath.Join(projectDir, "sess-live.jsonl")
	if sessionFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, sessionFile)
	}
}

func TestFindClaudeSessionInfoReturnsEmptyWhenNoJSONLWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	home := "/home/test"
	fs.SetHome(home)

	workDir := "/repo"
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))

	fs.MkdirAll(projectDir, 0o755)

	pid := 1234
	fs.WriteFile(filepath.Join("/proc", "1234", "cmdline"), []byte("claude\x00"), 0o644)

	sessionID, sessionFile := findClaudeSessionInfoWithFS(fs, pid, workDir, "")
	if sessionID != "" {
		t.Fatalf("expected empty session id, got %q", sessionID)
	}
	if sessionFile != "" {
		t.Fatalf("expected empty session file, got %q", sessionFile)
	}
}

func TestFindOpenCodeSessionInfoWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	home := "/home/test"
	fs.SetHome(home)

	workDir := "/repo"
	storageDir := filepath.Join(home, ".local", "share", "opencode", "storage", "session")

	fs.MkdirAll(storageDir, 0o755)

	// Create a session file with matching workDir
	sessionContent := `{"workDir":"/repo"}`
	fs.WriteFile(filepath.Join(storageDir, "abc123.json"), []byte(sessionContent), 0o644)

	// Simulate /proc/PID/cmdline showing "opencode"
	pid := 5678
	fs.WriteFile(filepath.Join("/proc", "5678", "cmdline"), []byte("opencode\x00"), 0o644)

	sessionID, sessionFile := findOpenCodeSessionInfoWithFS(fs, pid, workDir)
	if sessionID != "abc123" {
		t.Fatalf("expected session id abc123, got %q", sessionID)
	}
	wantFile := filepath.Join(storageDir, "abc123.json")
	if sessionFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, sessionFile)
	}
}

func TestFindOpenCodeSessionInfoFallbackLatestWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	home := "/home/test"
	fs.SetHome(home)

	workDir := "/nonexistent"
	storageDir := filepath.Join(home, ".local", "share", "opencode", "storage", "session")

	fs.MkdirAll(storageDir, 0o755)

	// Create two session files, old and new
	fs.WriteFile(filepath.Join(storageDir, "old.json"), []byte(`{"workDir":"/other"}`), 0o644)
	fs.WriteFile(filepath.Join(storageDir, "new.json"), []byte(`{"workDir":"/another"}`), 0o644)
	fs.SetModTime(filepath.Join(storageDir, "new.json"), time.Now().Add(time.Hour))

	pid := 5678
	fs.WriteFile(filepath.Join("/proc", "5678", "cmdline"), []byte("opencode\x00"), 0o644)

	sessionID, sessionFile := findOpenCodeSessionInfoWithFS(fs, pid, workDir)
	if sessionID != "new" {
		t.Fatalf("expected session id new, got %q", sessionID)
	}
	wantFile := filepath.Join(storageDir, "new.json")
	if sessionFile != wantFile {
		t.Fatalf("expected session file %q, got %q", wantFile, sessionFile)
	}
}

func TestScanLinuxWithMemFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping Linux scan test on non-Linux platform")
	}
	fs := NewMemFileSystem()

	// Set up /proc with fake processes
	fs.MkdirAll(filepath.Join("/proc", "1"), 0o755)
	fs.MkdirAll(filepath.Join("/proc", "100", "fd"), 0o755)
	fs.MkdirAll(filepath.Join("/proc", "101", "fd"), 0o755)

	// Process 100 = claude
	fs.WriteFile(filepath.Join("/proc", "100", "cmdline"), []byte("claude\x00--resume\x00"), 0o644)
	fs.WriteFile(filepath.Join("/proc", "100", "status"), []byte("PPid:\t1\n"), 0o644)
	fs.Symlink("/repo", filepath.Join("/proc", "100", "cwd"))
	fs.Symlink("/dev/pts/0", filepath.Join("/proc", "100", "fd", "0"))

	// Process 101 = opencode
	fs.WriteFile(filepath.Join("/proc", "101", "cmdline"), []byte("opencode\x00"), 0o644)
	fs.WriteFile(filepath.Join("/proc", "101", "status"), []byte("PPid:\t1\n"), 0o644)
	fs.Symlink("/other", filepath.Join("/proc", "101", "cwd"))

	// Set up home with session files
	home := "/home/test"
	fs.SetHome(home)
	claudeProjectDir := filepath.Join(home, ".claude", "projects", projectDirName("/repo"))
	fs.MkdirAll(claudeProjectDir, 0o755)
	fs.WriteFile(filepath.Join(claudeProjectDir, "sess-claude.jsonl"), []byte("{}\n"), 0o644)

	opencodeStorageDir := filepath.Join(home, ".local", "share", "opencode", "storage", "session")
	fs.MkdirAll(opencodeStorageDir, 0o755)
	fs.WriteFile(filepath.Join(opencodeStorageDir, "sess-opencode.json"), []byte(`{"workDir":"/other"}`), 0o644)

	s := New()
	results, err := s.ScanWithFS(fs)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 processes, got %d: %+v", len(results), results)
	}

	providers := map[string]bool{}
	for _, r := range results {
		providers[r.Provider] = true
	}
	if !providers["claude"] {
		t.Fatal("expected claude process")
	}
	if !providers["opencode"] {
		t.Fatal("expected opencode process")
	}
}

func TestIsClaudeProcessWithFS(t *testing.T) {
	fs := NewMemFileSystem()
	fs.WriteFile(filepath.Join("/proc", "100", "cmdline"), []byte("claude\x00"), 0o644)
	if !isClaudeProcessWithFS(fs, 100) {
		t.Fatal("expected pid 100 to be claude process")
	}
}

func TestIsOpenCodeProcessWithFS(t *testing.T) {
	fs := NewMemFileSystem()
	fs.WriteFile(filepath.Join("/proc", "200", "cmdline"), []byte("opencode\x00"), 0o644)
	if !isOpenCodeProcessWithFS(fs, 200) {
		t.Fatal("expected pid 200 to be opencode process")
	}
}

func TestListSessionCandidatesWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	dir := "/projects"
	fs.MkdirAll(dir, 0o755)
	fs.WriteFile(filepath.Join(dir, "sess1.jsonl"), []byte(`{"timestamp":"2024-01-01T00:00:00Z"}`+"\n"), 0o644)
	fs.WriteFile(filepath.Join(dir, "sess2.jsonl"), []byte(`{"timestamp":"2024-01-02T00:00:00Z"}`+"\n"), 0o644)
	fs.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0o644)

	candidates := listSessionCandidatesWithFS(fs, dir)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestGetLastActivityTimeWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	path := "/test.jsonl"
	fs.WriteFile(path, []byte(`{"timestamp":"2024-06-15T12:30:00Z"}`+"\n"), 0o644)

	tm := getLastActivityTimeWithFS(fs, path)
	want := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	if !tm.Equal(want) {
		t.Fatalf("expected time %v, got %v", want, tm)
	}
}

func TestFinalizeProcessScanWithMemFS(t *testing.T) {
	fs := NewMemFileSystem()
	home := "/home/test"
	fs.SetHome(home)

	workDir := "/repo"
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	fs.MkdirAll(projectDir, 0o755)
	fs.WriteFile(filepath.Join(projectDir, "sess-live.jsonl"), []byte("{}\n"), 0o644)

	pid := 1234
	fs.WriteFile(filepath.Join("/proc", "1234", "cmdline"), []byte("claude\x00"), 0o644)

	processes := []ProcessInfo{{
		PID:      pid,
		PPID:     1,
		Provider: "claude",
		WorkDir:  workDir,
	}}

	result := finalizeProcessScanWithFS(fs, processes)
	if len(result) != 1 {
		t.Fatalf("expected 1 process, got %d", len(result))
	}
	if result[0].SessionID != "sess-live" {
		t.Fatalf("expected session id sess-live, got %q", result[0].SessionID)
	}
}
