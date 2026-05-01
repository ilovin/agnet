package watcher

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseLineLocalCommandStdout(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"local_command","content":"<local-command-stdout>Status dialog dismissed</local-command-stdout>"}`)
	ev, ok := parseLine(line)
	if !ok {
		t.Fatal("expected local_command stdout line to be parsed")
	}
	if ev.Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", ev.Role)
	}
	if ev.Text != "Status dialog dismissed" {
		t.Fatalf("expected parsed stdout text, got %q", ev.Text)
	}
	if ev.StatusChange == nil || *ev.StatusChange != StatusStandby {
		t.Fatalf("expected standby status change, got %#v", ev.StatusChange)
	}
}

func TestParseLineLocalCommandEmptyStdoutFallback(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"local_command","content":"<local-command-stdout></local-command-stdout>"}`)
	ev, ok := parseLine(line)
	if !ok {
		t.Fatal("expected local_command empty stdout line to be parsed")
	}
	if ev.Text != "Local command completed" {
		t.Fatalf("expected fallback text, got %q", ev.Text)
	}
	if ev.StatusChange == nil || *ev.StatusChange != StatusStandby {
		t.Fatalf("expected standby status change, got %#v", ev.StatusChange)
	}
}

func TestParseLineLocalCommandStderr(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"local_command","content":"<local-command-stderr>Error: failed</local-command-stderr>"}`)
	ev, ok := parseLine(line)
	if !ok {
		t.Fatal("expected local_command stderr line to be parsed")
	}
	if ev.Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", ev.Role)
	}
	if ev.Text != "Error: failed" {
		t.Fatalf("expected parsed stderr text, got %q", ev.Text)
	}
	if ev.StatusChange == nil || *ev.StatusChange != StatusStandby {
		t.Fatalf("expected standby status change, got %#v", ev.StatusChange)
	}
}

func TestParseLineLocalCommandMetaIgnored(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"local_command","content":"<command-name>/status</command-name><command-message>status</command-message>"}`)
	_, ok := parseLine(line)
	if ok {
		t.Fatal("expected command metadata-only local_command line to be ignored")
	}
}

func TestParseLineLocalCommandMultilineStdout(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"local_command","content":"<local-command-stdout>line1\nline2</local-command-stdout>"}`)
	ev, ok := parseLine(line)
	if !ok {
		t.Fatal("expected multiline stdout line to be parsed")
	}
	if !strings.Contains(ev.Text, "line1") || !strings.Contains(ev.Text, "line2") {
		t.Fatalf("expected multiline text preserved, got %q", ev.Text)
	}
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

func TestContentMatchFromCandidatesRequiresConfidence(t *testing.T) {
	origCapture := capturePaneContentFunc
	origExtract := extractFingerprintsFunc
	t.Cleanup(func() {
		capturePaneContentFunc = origCapture
		extractFingerprintsFunc = origExtract
	})

	capturePaneContentFunc = func(string) (string, error) {
		return "This pane shows alpha beta gamma and some extra text", nil
	}
	extractFingerprintsFunc = func(path string, _ int) []string {
		switch path {
		case "/tmp/a.jsonl":
			return []string{"alpha", "beta", "gamma"}
		case "/tmp/b.jsonl":
			return []string{"alpha", "beta"}
		default:
			return nil
		}
	}

	matched := contentMatchFromCandidates("0:1.2", []sessionCandidate{
		{jsonlPath: "/tmp/a.jsonl"},
		{jsonlPath: "/tmp/b.jsonl"},
	}, 5)
	if matched != "" {
		t.Fatalf("expected ambiguous match to be rejected, got %s", matched)
	}
}

func TestContentMatchFromCandidatesAcceptsStrongWinner(t *testing.T) {
	origCapture := capturePaneContentFunc
	origExtract := extractFingerprintsFunc
	t.Cleanup(func() {
		capturePaneContentFunc = origCapture
		extractFingerprintsFunc = origExtract
	})

	capturePaneContentFunc = func(string) (string, error) {
		return "pane text includes alpha beta gamma delta", nil
	}
	extractFingerprintsFunc = func(path string, _ int) []string {
		switch path {
		case "/tmp/a.jsonl":
			return []string{"alpha", "beta", "gamma", "delta"}
		case "/tmp/b.jsonl":
			return []string{"alpha", "beta"}
		default:
			return nil
		}
	}

	matched := contentMatchFromCandidates("0:1.2", []sessionCandidate{
		{jsonlPath: "/tmp/a.jsonl"},
		{jsonlPath: "/tmp/b.jsonl"},
	}, 5)
	if matched != "/tmp/a.jsonl" {
		t.Fatalf("expected strong winner /tmp/a.jsonl, got %s", matched)
	}
}

func TestClaudeWatcherRefreshKeepsCurrentOnAmbiguousContentMatch(t *testing.T) {
	origCapture := capturePaneContentFunc
	origExtract := extractFingerprintsFunc
	t.Cleanup(func() {
		capturePaneContentFunc = origCapture
		extractFingerprintsFunc = origExtract
	})

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
	baseTime := time.Now()
	if err := os.Chtimes(sessA, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes sessA: %v", err)
	}
	if err := os.Chtimes(sessB, baseTime.Add(time.Second), baseTime.Add(time.Second)); err != nil {
		t.Fatalf("chtimes sessB: %v", err)
	}

	capturePaneContentFunc = func(string) (string, error) {
		return "pane has alpha and beta", nil
	}
	extractFingerprintsFunc = func(path string, _ int) []string {
		switch path {
		case sessA:
			return []string{"alpha", "beta", "gamma"}
		case sessB:
			return []string{"alpha", "beta"}
		default:
			return nil
		}
	}

	w := NewClaudeWatcher(sessA, func(ConversationEvent) {})
	w.SetWorkDir(workDir)
	w.SetTmuxTarget("0:1.2")
	w.refreshSessionFile()

	if w.path != sessA {
		t.Fatalf("expected watcher to keep current session on ambiguous match, got %s", w.path)
	}
}

func TestClaudeWatcherRefreshKeepsStaleSession(t *testing.T) {
	// A session may be idle for hours and then resumed the next day.
	// Time-based staleness checks are unreliable — the watcher must keep
	// the current session as long as the file still exists, regardless of
	// how much newer other candidates are.
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

	stale := filepath.Join(projectDir, "sess-stale.jsonl")
	fresh := filepath.Join(projectDir, "sess-fresh.jsonl")
	if err := os.WriteFile(stale, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := os.WriteFile(fresh, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	now := time.Now()
	// stale is 31 minutes old, fresh is current
	if err := os.Chtimes(stale, now.Add(-31*time.Minute), now.Add(-31*time.Minute)); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatalf("chtimes fresh: %v", err)
	}

	w := NewClaudeWatcher(stale, func(ConversationEvent) {})
	w.SetWorkDir(workDir)
	w.SetPID(1)
	w.refreshSessionFile()

	if w.path != stale {
		t.Fatalf("expected watcher to keep stale session, got %s", w.path)
	}
}

func TestClaudeWatcherSkipExisting(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "existing.jsonl")

	// Pre-write two lines
	content := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"world"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var count int
	w := NewClaudeWatcher(sessionFile, func(e ConversationEvent) {
		count++
	})
	w.SetSkipExisting(true)

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Existing content should be skipped
	if count != 0 {
		t.Fatalf("expected 0 events when skipExisting=true, got %d", count)
	}

	// Append a new line — it should be detected by the loop
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"new"}]}}` + "\n")
	f.Close()

	// Wait for the loop to poll
	time.Sleep(3 * time.Second)

	if count != 1 {
		t.Fatalf("expected 1 new event after append, got %d", count)
	}
}

func TestClaudeWatcherRefreshSwitchesAfterClear(t *testing.T) {
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

	oldSess := filepath.Join(projectDir, "sess-old.jsonl")
	newSess := filepath.Join(projectDir, "sess-new.jsonl")

	oldTime := time.Now().Add(-10 * time.Minute)
	if err := os.WriteFile(oldSess, []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"old"}]}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write old session: %v", err)
	}
	if err := os.Chtimes(oldSess, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	newTime := time.Now()
	newLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"new"}]}}` + "\n"
	if err := os.WriteFile(newSess, []byte(newLine), 0o644); err != nil {
		t.Fatalf("write new session: %v", err)
	}
	if err := os.Chtimes(newSess, newTime, newTime); err != nil {
		t.Fatalf("chtimes new: %v", err)
	}

	origGetPaneActivity := getPaneActivityFunc
	getPaneActivityFunc = func(string) *time.Time {
		t := time.Now()
		return &t
	}
	defer func() { getPaneActivityFunc = origGetPaneActivity }()

	w := NewClaudeWatcher(oldSess, func(ConversationEvent) {})
	w.SetWorkDir(workDir)
	w.SetTmuxTarget("mock:0.0")
	w.SetPID(999999)
	w.lastSwitchAt = time.Time{}

	w.refreshSessionFile()

	if w.path != newSess {
		t.Fatalf("expected watcher to switch from %s to %s, got %s", oldSess, newSess, w.path)
	}
}

func TestClaudeWatcherNoRefreshOnFirstPoll(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "single.jsonl")
	os.WriteFile(sessionFile, []byte("{}\n"), 0644)

	w := NewClaudeWatcher(sessionFile, func(ConversationEvent) {})
	w.SetWorkDir(dir)
	w.SetPID(1)
	w.SetSkipExisting(true)

	// Manually call poll (which Start() does) — hasPolled should be false
	// so refreshSessionFile is NOT triggered even though count==0
	w.poll()

	// If refreshSessionFile had run with an empty project dir, it might
	// switch to a different file or panic. The fact that w.path is unchanged
	// confirms refreshSessionFile was skipped.
	if w.path != sessionFile {
		t.Fatalf("expected path unchanged on first poll, got %s", w.path)
	}
}

func TestClaudeWatcherNoCrossProcessSwitch(t *testing.T) {
	// When PID fd shows multiple sessions (old fd not closed after /clear),
	// restrict candidates to ONLY those fd-backed sessions. Do NOT scan the
	// whole project dir, which would include other processes' sessions.
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

	ownOld := filepath.Join(projectDir, "sess-own-old.jsonl")
	ownNew := filepath.Join(projectDir, "sess-own-new.jsonl")
	other  := filepath.Join(projectDir, "sess-other.jsonl")

	now := time.Now()
	oldTime := now.Add(-10 * time.Minute)
	newTime := now.Add(-1 * time.Minute)
	otherTime := now // other is also recent, to simulate an active unrelated session

	// Write own-old with a timestamp line so getLastActivityTimeFromJSONL picks it up
	oldLine := `{"timestamp":"` + oldTime.Format(time.RFC3339) + `"}` + "\n"
	if err := os.WriteFile(ownOld, []byte(oldLine), 0o644); err != nil {
		t.Fatalf("write ownOld: %v", err)
	}
	if err := os.Chtimes(ownOld, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes ownOld: %v", err)
	}

	newLine := `{"timestamp":"` + newTime.Format(time.RFC3339) + `"}` + "\n"
	if err := os.WriteFile(ownNew, []byte(newLine), 0o644); err != nil {
		t.Fatalf("write ownNew: %v", err)
	}
	if err := os.Chtimes(ownNew, newTime, newTime); err != nil {
		t.Fatalf("chtimes ownNew: %v", err)
	}

	otherLine := `{"timestamp":"` + otherTime.Format(time.RFC3339) + `"}` + "\n"
	if err := os.WriteFile(other, []byte(otherLine), 0o644); err != nil {
		t.Fatalf("write other: %v", err)
	}
	if err := os.Chtimes(other, otherTime, otherTime); err != nil {
		t.Fatalf("chtimes other: %v", err)
	}

	w := NewClaudeWatcher(ownOld, func(ConversationEvent) {})
	w.SetWorkDir(workDir)
	w.SetPID(999999)
	w.lastSwitchAt = time.Time{}

	// Mock findSessionIDsFromTasks to return both own sessions (simulating old fd not closed)
	w.findSessionIDsFromTasksFunc = func(tasksDir string) []string {
		return []string{"sess-own-old", "sess-own-new"}
	}

	w.refreshSessionFile()

	if w.path != ownNew {
		t.Fatalf("expected watcher to switch to %s, got %s", ownNew, w.path)
	}
}
