package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeWatcherDetectsMessages(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "abc123.jsonl")

	// Pre-write a user message line
	line1 := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line1), 0644); err != nil {
		t.Fatal(err)
	}

	events := make(chan ConversationEvent, 10)
	w := NewClaudeWatcher(sessionFile, func(e ConversationEvent) {
		events <- e
	})

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Append an assistant message (no stop_reason = still streaming)
	line2 := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}` + "\n"
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(line2)
	f.Close()

	// Expect two events: one for existing line, one for new line
	got := collectEvents(events, 2, 3*time.Second)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(got))
	}
	if got[0].Role != "user" {
		t.Errorf("expected first event role=user, got %q", got[0].Role)
	}
	if got[1].Role != "assistant" {
		t.Errorf("expected second event role=assistant, got %q", got[1].Role)
	}
	if got[1].Text != "hi there" {
		t.Errorf("expected text 'hi there', got %q", got[1].Text)
	}
}

func TestClaudeWatcherDetectsWorking(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "xyz.jsonl")
	os.WriteFile(sessionFile, []byte{}, 0644)

	statuses := make(chan AgentStatus, 10)
	w := NewClaudeWatcher(sessionFile, func(e ConversationEvent) {
		if e.StatusChange != nil {
			statuses <- *e.StatusChange
		}
	})
	w.Start()
	defer w.Stop()

	// tool_use line → Working
	toolLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}` + "\n"
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(toolLine)
	f.Close()

	got := collectStatuses(statuses, 1, 5*time.Second)
	if len(got) == 0 {
		t.Fatal("expected a status change event")
	}
	if got[0] != StatusWorking {
		t.Errorf("expected StatusWorking, got %v", got[0])
	}
}

func TestClaudeWatcherStreamingTextIsWorking(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "stream.jsonl")
	os.WriteFile(sessionFile, []byte{}, 0644)

	statuses := make(chan AgentStatus, 10)
	w := NewClaudeWatcher(sessionFile, func(e ConversationEvent) {
		if e.StatusChange != nil {
			statuses <- *e.StatusChange
		}
	})
	w.Start()
	defer w.Stop()

	// Text without stop_reason → still streaming → Working
	streamLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"✳ Generating…"}]}}` + "\n"
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(streamLine)
	f.Close()

	got := collectStatuses(statuses, 1, 5*time.Second)
	if len(got) == 0 {
		t.Fatal("expected a status change for streaming text")
	}
	if got[0] != StatusWorking {
		t.Errorf("expected StatusWorking for streaming text, got %v", got[0])
	}

	// Text with stop_reason "end_turn" → Standby
	endLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}` + "\n"
	f2, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f2.WriteString(endLine)
	f2.Close()

	got2 := collectStatuses(statuses, 1, 5*time.Second)
	if len(got2) == 0 {
		t.Fatal("expected a status change for end_turn")
	}
	if got2[0] != StatusStandby {
		t.Errorf("expected StatusStandby for end_turn, got %v", got2[0])
	}
}

func collectEvents(ch <-chan ConversationEvent, count int, timeout time.Duration) []ConversationEvent {
	var out []ConversationEvent
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			out = append(out, e)
			if len(out) >= count {
				return out
			}
		case <-deadline:
			return out
		}
	}
}

func collectStatuses(ch <-chan AgentStatus, count int, timeout time.Duration) []AgentStatus {
	var out []AgentStatus
	deadline := time.After(timeout)
	for {
		select {
		case s := <-ch:
			out = append(out, s)
			if len(out) >= count {
				return out
			}
		case <-deadline:
			return out
		}
	}
}

func TestClaudeWatcherStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "test.jsonl")
	os.WriteFile(sessionFile, []byte{}, 0644)

	w := NewClaudeWatcher(sessionFile, func(e ConversationEvent) {})
	w.Start()

	// Calling Stop twice must not panic
	w.Stop()
	w.Stop() // should be a no-op, not a panic
}

func TestClaudeWatcherRefreshPrefersCurrentTaskSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	tasksDir := filepath.Join(home, ".claude", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks dir: %v", err)
	}

	live := filepath.Join(projectDir, "sess-live.jsonl")
	archived := filepath.Join(projectDir, "sess-archived.jsonl")
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write live session: %v", err)
	}
	if err := os.WriteFile(archived, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write archived session: %v", err)
	}
	baseTime := time.Now()
	if err := os.Chtimes(live, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes live: %v", err)
	}
	if err := os.Chtimes(archived, baseTime.Add(time.Second), baseTime.Add(time.Second)); err != nil {
		t.Fatalf("chtimes archived: %v", err)
	}

	taskDir := filepath.Join(tasksDir, "sess-live")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, ".highwatermark"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write task marker: %v", err)
	}
	dirHandle, err := os.Open(taskDir)
	if err != nil {
		t.Fatalf("open task dir: %v", err)
	}
	defer dirHandle.Close()

	w := NewClaudeWatcher(live, func(ConversationEvent) {})
	w.SetWorkDir(workDir)
	w.SetPID(os.Getpid())
	w.refreshSessionFile()

	if w.path != live {
		t.Fatalf("expected watcher to stay on %s, got %s", live, w.path)
	}
}

func TestClaudeWatcherRefreshNoCrossTalk(t *testing.T) {
	// Two watchers in the same project dir must NOT hop to each other's session
	// when there is no PID map.
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	sessA := filepath.Join(projectDir, "sess-a.jsonl")
	sessB := filepath.Join(projectDir, "sess-b.jsonl")
	if err := os.WriteFile(sessA, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write sessA: %v", err)
	}
	if err := os.WriteFile(sessB, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write sessB: %v", err)
	}
	// Make sessB newer so the old mtime fallback would pick it
	baseTime := time.Now()
	if err := os.Chtimes(sessA, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes sessA: %v", err)
	}
	if err := os.Chtimes(sessB, baseTime.Add(time.Second), baseTime.Add(time.Second)); err != nil {
		t.Fatalf("chtimes sessB: %v", err)
	}

	// Create two watchers without PID maps (simulating attached history sessions)
	wA := NewClaudeWatcher(sessA, func(ConversationEvent) {})
	wA.SetWorkDir(workDir)
	wB := NewClaudeWatcher(sessB, func(ConversationEvent) {})
	wB.SetWorkDir(workDir)

	wA.refreshSessionFile()
	wB.refreshSessionFile()

	if wA.path != sessA {
		t.Errorf("watcher A should stay on sessA, got %s", wA.path)
	}
	if wB.path != sessB {
		t.Errorf("watcher B should stay on sessB, got %s", wB.path)
	}
}

func TestClaudeWatcherRefreshPrefersMostActiveOpenTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	projectDir := filepath.Join(home, ".claude", "projects", projectDirName(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	tasksDir := filepath.Join(home, ".claude", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks dir: %v", err)
	}

	sessions := []string{"sess-old", "sess-mid", "sess-live"}
	baseTime := time.Now()
	var openDirs []*os.File
	for i, sid := range sessions {
		sessionFile := filepath.Join(projectDir, sid+".jsonl")
		if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write session file: %v", err)
		}
		if err := os.Chtimes(sessionFile, baseTime.Add(time.Duration(i)*time.Second), baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("chtimes session file: %v", err)
		}

		taskDir := filepath.Join(tasksDir, sid)
		if err := os.MkdirAll(taskDir, 0o755); err != nil {
			t.Fatalf("mkdir task dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(taskDir, ".highwatermark"), []byte("1"), 0o644); err != nil {
			t.Fatalf("write highwatermark: %v", err)
		}
		if err := os.Chtimes(filepath.Join(taskDir, ".highwatermark"), baseTime.Add(time.Duration(i)*time.Second), baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("chtimes highwatermark: %v", err)
		}
		if sid == "sess-live" {
			liveTask := filepath.Join(taskDir, "26.json")
			if err := os.WriteFile(liveTask, []byte(`{"id":26}`), 0o644); err != nil {
				t.Fatalf("write live task json: %v", err)
			}
			if err := os.Chtimes(liveTask, baseTime.Add(10*time.Second), baseTime.Add(10*time.Second)); err != nil {
				t.Fatalf("chtimes live task json: %v", err)
			}
		}
		dirHandle, err := os.Open(taskDir)
		if err != nil {
			t.Fatalf("open task dir: %v", err)
		}
		openDirs = append(openDirs, dirHandle)
	}
	defer func() {
		for _, dirHandle := range openDirs {
			dirHandle.Close()
		}
	}()

	w := NewClaudeWatcher(filepath.Join(projectDir, "sess-old.jsonl"), func(ConversationEvent) {})
	w.SetWorkDir(workDir)
	w.SetPID(os.Getpid())
	if sessionID := taskSessionID(tasksDir, filepath.Join("/private", tasksDir, "sess-live", "26.json")); sessionID != "sess-live" {
		t.Fatalf("expected taskSessionID to normalize /private path, got %q", sessionID)
	}

	want := filepath.Join(projectDir, "sess-live.jsonl")
	if got := w.findSessionFromTasks(); got != want {
		t.Fatalf("expected watcher to find most active open task %s, got %s", want, got)
	}
}
