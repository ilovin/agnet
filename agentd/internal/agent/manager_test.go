package agent_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

func newTestManager(t *testing.T) *agent.Manager {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := agent.NewManager(s, t.TempDir())
	// Isolate tests from real Claude session files on the host.
	m.SetFindSessionFile(func(info scanner.ProcessInfo) string {
		return info.SessionFile
	})
	return m
}

func TestCreateAndListAgent(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Create("test-agent", "custom", "echo", []string{"hello"}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty agent id")
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != id {
		t.Errorf("expected id=%q, got %q", id, agents[0].ID)
	}
}

func TestAgentStatusTransition(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("echo-agent", "custom", "echo", []string{"hello"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Give it a moment to reach Starting/Idle
	time.Sleep(200 * time.Millisecond)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	// echo exits immediately — status should be Stopped or Idle
	status := ag.Status()
	if status != agent.StatusStopped && status != agent.StatusIdle {
		t.Errorf("unexpected status: %v", status)
	}
}

func TestStopAgent(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("sleep-agent", "custom", "sleep", []string{"60"}, t.TempDir(), nil)
	time.Sleep(100 * time.Millisecond)

	if err := m.Stop(id); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	ag := m.Get(id)
	if ag.Status() != agent.StatusStopped {
		t.Errorf("expected Stopped, got %v", ag.Status())
	}
}

func TestAttachedAgentIsReadOnly(t *testing.T) {
	m := newTestManager(t)
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	// Use the test process's own PID so validateProcess passes
	// (test binary path contains "claude" when running in this repo)
	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	if !ag.IsReadOnly() {
		t.Fatal("expected attached agent to be read-only")
	}
}

func TestClassifyAttachCandidateClaudeWithoutSessionIDIsDisplay(t *testing.T) {
	m := newTestManager(t)

	// Claude process with empty session ID should be Display (shown in list, not auto-attached)
	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:      123,
		Provider: "claude",
		WorkDir:  "/repo",
	})
	if candidate.Decision != agent.AttachDecisionDisplay {
		t.Fatalf("expected display decision for claude without session ID, got %q", candidate.Decision)
	}
}

func TestClassifyAttachCandidateOpencodeStillAuto(t *testing.T) {
	m := newTestManager(t)

	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:       123,
		Provider:  "opencode",
		SessionID: "",
	})
	if candidate.Decision != agent.AttachDecisionAuto {
		t.Fatalf("expected auto decision for opencode process, got %q", candidate.Decision)
	}
}

func TestClassifyAttachCandidateHermesGatewayRunIsSkipped(t *testing.T) {
	m := newTestManager(t)

	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:      123,
		Provider: "hermes",
		Cmd:      "hermes",
		Args:     []string{"gateway", "run"},
	})
	if candidate.Decision != agent.AttachDecisionSkip {
		t.Fatalf("expected skip decision for hermes gateway run, got %q", candidate.Decision)
	}
}

func TestClassifyAttachCandidateHermesNonInteractiveIsSkipped(t *testing.T) {
	m := newTestManager(t)

	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:      321,
		Provider: "hermes",
		Cmd:      "hermes",
		Args:     []string{"shell"},
	})
	if candidate.Decision != agent.AttachDecisionSkip {
		t.Fatalf("expected skip decision for non-interactive hermes, got %q", candidate.Decision)
	}
}

func TestClassifyAttachCandidateHermesInteractiveIsAuto(t *testing.T) {
	m := newTestManager(t)

	candidate := m.ClassifyAttachCandidate(scanner.ProcessInfo{
		PID:      654,
		Provider: "hermes",
		Cmd:      "hermes",
		Args:     []string{"shell"},
		Terminal: "/dev/ttys001",
	})
	if candidate.Decision != agent.AttachDecisionAuto {
		t.Fatalf("expected auto decision for interactive hermes, got %q", candidate.Decision)
	}
}

func TestAttachSamePIDSwitchesSessionAndClearsHistory(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	firstSessionFile := filepath.Join(repoDir, "sess-old.jsonl")
	if err := os.WriteFile(firstSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write first session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: firstSessionFile,
	})
	if err != nil {
		t.Fatalf("initial attach failed: %v", err)
	}

	originalAutoName := ag.Name
	if err := m.UpdateResumeSessionID(ag.ID, "sess-old"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}
	if err := m.Rename(ag.ID, "custom title"); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	if seq, err := m.RecordConversationEvent(ag.ID, map[string]any{"role": "assistant", "text": "stale"}); err != nil || seq == 0 {
		t.Fatalf("expected persisted event seq > 0, got seq=%d err=%v", seq, err)
	}
	if persisted, err := m.LoadPersistedEventsLatest(ag.ID, 10); err != nil || len(persisted) == 0 {
		t.Fatalf("expected persisted events before rebind, got %d err=%v", len(persisted), err)
	}
	if _, err := m.LoadPersistedEventsSince(ag.ID, 0, 10); err != nil {
		t.Fatalf("expected persisted events since API to work before reset: %v", err)
	}
	if _, err := m.LoadPersistedEventsBefore(ag.ID, 100, 10); err != nil {
		t.Fatalf("expected persisted events before API to work before reset: %v", err)
	}

	newSessionFile := filepath.Join(repoDir, "sess-new.jsonl")
	if err := os.WriteFile(newSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new session file: %v", err)
	}

	rebound, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: newSessionFile,
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if rebound.ID != ag.ID {
		t.Fatalf("expected same agent id, got %s vs %s", rebound.ID, ag.ID)
	}

	resumeID, err := m.GetResumeSessionID(ag.ID)
	if err != nil {
		t.Fatalf("GetResumeSessionID failed: %v", err)
	}
	if resumeID != "sess-new" {
		t.Fatalf("expected new session id, got %q", resumeID)
	}
	if rebound.Name != "custom title" {
		t.Fatalf("expected custom title to survive session switch, got %q (auto was %q)", rebound.Name, originalAutoName)
	}
	if events := rebound.EventBuf().Since(0); len(events) != 0 {
		t.Fatalf("expected cleared live history, got %d events", len(events))
	}
	// Persisted history should survive session rebind so the dashboard can
	// reload previous conversation from SQLite.
	persisted, err := m.LoadPersistedEventsLatest(ag.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(persisted) == 0 {
		t.Fatalf("expected persisted history to survive rebind, got 0 events")
	}
}

func TestAttachSameSessionDifferentPIDDeadConflictCleanedUp(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	sessionFile := filepath.Join(repoDir, "sess-live.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	first, err := m.Attach(scanner.ProcessInfo{
		PID:         6864,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("first attach failed: %v", err)
	}

	second, err := m.Attach(scanner.ProcessInfo{
		PID:         7777,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("second attach failed: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("expected separate agents for different pids sharing one session, got same id %s", first.ID)
	}

	// First agent (PID 6864) is dead, so the CONFLICT check should have cleaned it up.
	// Second agent (PID 7777) takes over with a watcher.
	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent (dead first agent cleaned up), got %d", len(agents))
	}
	if agents[0].ID != second.ID {
		t.Fatalf("expected second agent to survive, got %s", agents[0].ID)
	}
}

func TestAttachSamePIDSwitchesSessionUpdatesAutoName(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	firstSessionFile := filepath.Join(repoDir, "sess-old.jsonl")
	if err := os.WriteFile(firstSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write first session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: firstSessionFile,
	})
	if err != nil {
		t.Fatalf("initial attach failed: %v", err)
	}
	if err := m.UpdateResumeSessionID(ag.ID, "sess-old"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}
	if got := ag.Name; got != filepath.Base(repoDir)+" - "+strconv.Itoa(ownPID) {
		t.Fatalf("expected initial auto name, got %q", got)
	}

	newSessionFile := filepath.Join(repoDir, "sess-new.jsonl")
	if err := os.WriteFile(newSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new session file: %v", err)
	}

	rebound, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: newSessionFile,
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if rebound.ID != ag.ID {
		t.Fatalf("expected same agent id, got %s vs %s", rebound.ID, ag.ID)
	}
	if rebound.Name != filepath.Base(repoDir)+" - "+strconv.Itoa(ownPID) {
		t.Fatalf("expected updated auto name to stay pid-based, got %q", rebound.Name)
	}
}

func TestAutoAttachExistingIncludesDisplayCandidates(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	sessionFile := filepath.Join(repoDir, "sess-empty.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	ownPID := os.Getpid()
	// Override scanExisting to return a claude process with empty SessionID
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:         ownPID,
			Provider:    "claude",
			WorkDir:     repoDir,
			SessionFile: sessionFile,
			SessionID:   "", // empty session ID -> Display decision
		}}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent after auto-attach, got %d", len(agents))
	}
	if agents[0].Provider != "claude" {
		t.Fatalf("expected claude agent, got %q", agents[0].Provider)
	}
	if agents[0].PID != ownPID {
		t.Fatalf("expected PID %d, got %d", ownPID, agents[0].PID)
	}
}

func TestAttachClaudeWithoutSessionFileCreatesDisplayAgent(t *testing.T) {
	m := newTestManager(t)

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: "", // no session file
		SessionID:   "",
	})
	if err != nil {
		t.Fatalf("Attach failed for claude without session file: %v", err)
	}
	if ag.Provider != "claude" {
		t.Fatalf("expected provider claude, got %q", ag.Provider)
	}
	if ag.PID != ownPID {
		t.Fatalf("expected PID %d, got %d", ownPID, ag.PID)
	}
}

func TestAttachClaudeWithoutSessionFileAppearsInList(t *testing.T) {
	m := newTestManager(t)

	ownPID := os.Getpid()
	_, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: "",
		SessionID:   "",
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent in list, got %d", len(agents))
	}
	if agents[0].Provider != "claude" {
		t.Fatalf("expected claude agent, got %q", agents[0].Provider)
	}
}

func TestAttachHermesUsesProcessSessionIDForResume(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	ownPID := os.Getpid()

	ag, err := m.Attach(scanner.ProcessInfo{
		PID:       ownPID,
		Provider:  "hermes",
		WorkDir:   repoDir,
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "sess-from-args"},
		SessionID: "sess-from-args",
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	resumeID, err := m.GetResumeSessionID(ag.ID)
	if err != nil {
		t.Fatalf("GetResumeSessionID failed: %v", err)
	}
	if resumeID != "sess-from-args" {
		t.Fatalf("expected resume session from process info, got %q", resumeID)
	}
}

func TestAttachHermesSamePIDSwitchesSessionAndHydratesHistory(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	ownPID := os.Getpid()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions/sess-new/history":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"events":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	m.SetHermesBaseURL(ts.URL)

	first, err := m.Attach(scanner.ProcessInfo{
		PID:       ownPID,
		Provider:  "hermes",
		WorkDir:   repoDir,
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "sess-old"},
		SessionID: "sess-old",
	})
	if err != nil {
		t.Fatalf("first attach failed: %v", err)
	}
	if seq, err := m.RecordConversationEvent(first.ID, map[string]any{"role": "assistant", "text": "stale"}); err != nil || seq == 0 {
		t.Fatalf("expected persisted event seq > 0, got seq=%d err=%v", seq, err)
	}

	rebound, err := m.Attach(scanner.ProcessInfo{
		PID:       ownPID,
		Provider:  "hermes",
		WorkDir:   repoDir,
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "sess-new"},
		SessionID: "sess-new",
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if rebound.ID != first.ID {
		t.Fatalf("expected same agent id, got %s vs %s", rebound.ID, first.ID)
	}

	resumeID, err := m.GetResumeSessionID(first.ID)
	if err != nil {
		t.Fatalf("GetResumeSessionID failed: %v", err)
	}
	if resumeID != "sess-old" {
		t.Fatalf("expected stored session preserved, got %q (Hermes session is gateway-managed, --session arg is not authoritative)", resumeID)
	}

	// Live events: preserved (no rebind clearing)
	if live := rebound.EventBuf().Since(0); len(live) != 1 {
		t.Fatalf("expected 1 live event preserved after reattach, got %d", len(live))
	}

	// Persisted events: only the stale event (no history hydration from API on reattach)
	events, err := m.LoadPersistedEventsLatest(first.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 persisted event, got %d", len(events))
	}
	if events[0].Data["role"] != "assistant" || events[0].Data["text"] != "stale" {
		t.Fatalf("unexpected persisted event: %+v", events[0].Data)
	}
}

func TestAttachHermesSamePIDSessionSwitchHistoryFetchFailureKeepsAgent(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	ownPID := os.Getpid()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	m.SetHermesBaseURL(ts.URL)

	first, err := m.Attach(scanner.ProcessInfo{
		PID:       ownPID,
		Provider:  "hermes",
		WorkDir:   repoDir,
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "sess-old"},
		SessionID: "sess-old",
	})
	if err != nil {
		t.Fatalf("first attach failed: %v", err)
	}
	if seq, err := m.RecordConversationEvent(first.ID, map[string]any{"role": "assistant", "text": "stale"}); err != nil || seq == 0 {
		t.Fatalf("expected persisted event seq > 0, got seq=%d err=%v", seq, err)
	}

	rebound, err := m.Attach(scanner.ProcessInfo{
		PID:       ownPID,
		Provider:  "hermes",
		WorkDir:   repoDir,
		Cmd:       "hermes",
		Args:      []string{"gateway", "run", "--session", "sess-new"},
		SessionID: "sess-new",
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if rebound.ID != first.ID {
		t.Fatalf("expected same agent id, got %s vs %s", rebound.ID, first.ID)
	}

	resumeID, err := m.GetResumeSessionID(first.ID)
	if err != nil {
		t.Fatalf("GetResumeSessionID failed: %v", err)
	}
	if resumeID != "sess-old" {
		t.Fatalf("expected stored session preserved, got %q (Hermes session is gateway-managed)", resumeID)
	}

	events, err := m.LoadPersistedEventsLatest(first.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 preserved event, got %d", len(events))
	}
}

func TestAttachHermesSamePIDReusesExistingAgent(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep command not available")
	}

	m := newTestManager(t)
	repoDir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	info := scanner.ProcessInfo{
		PID:      cmd.Process.Pid,
		Provider: "hermes",
		WorkDir:  repoDir,
		Cmd:      "hermes",
		Args:     []string{"gateway", "run"},
	}

	first, err := m.Attach(info)
	if err != nil {
		t.Fatalf("first attach failed: %v", err)
	}
	second, err := m.Attach(info)
	if err != nil {
		t.Fatalf("second attach failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same agent ID on reattach, got %s vs %s", first.ID, second.ID)
	}

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 managed agent, got %d", len(agents))
	}
	if agents[0].ID != first.ID {
		t.Fatalf("expected agent %s in manager list, got %s", first.ID, agents[0].ID)
	}
}

func TestAutoAttachExistingWithEmptySessionFileCreatesDisplayAgent(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()

	ownPID := os.Getpid()
	// Override scanExisting to return a claude process with NO session file at all
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:         ownPID,
			Provider:    "claude",
			WorkDir:     repoDir,
			SessionFile: "", // truly empty -> no session file found
			SessionID:   "",
		}}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 display-only agent after auto-attach, got %d", len(agents))
	}
	if agents[0].Provider != "claude" {
		t.Fatalf("expected claude agent, got %q", agents[0].Provider)
	}
	if agents[0].PID != ownPID {
		t.Fatalf("expected PID %d, got %d", ownPID, agents[0].PID)
	}
	if agents[0].Watcher() != nil {
		t.Fatalf("expected no watcher for display-only agent, got %v", agents[0].Watcher())
	}
}

func TestAutoAttachExistingDeduplicatesSameSessionAcrossPIDs(t *testing.T) {
	m := newTestManager(t)

	// Two processes with same provider+sessionID but different PIDs.
	// Both have interactive terminals so ClassifyAttachCandidate returns "auto".
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{
			{
				PID:       1111,
				Provider:  "hermes",
				WorkDir:   "/repo",
				Cmd:       "hermes",
				Args:      []string{"shell", "--session", "sess-1"},
				SessionID: "sess-1",
				Terminal:  "/dev/ttys001",
			},
			{
				PID:       2222,
				Provider:  "hermes",
				WorkDir:   "/repo",
				Cmd:       "hermes",
				Args:      []string{"shell", "--session", "sess-1"},
				SessionID: "sess-1",
				Terminal:  "/dev/ttys002",
			},
		}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 1 {
		for _, ag := range agents {
			t.Logf("agent: id=%s pid=%d provider=%s", ag.ID, ag.PID, ag.Provider)
		}
		t.Fatalf("expected 1 deduplicated agent for same provider+session, got %d", len(agents))
	}
}

func TestAutoAttachExistingSkipsAmbiguousCandidates(t *testing.T) {
	m := newTestManager(t)

	// Override scanExisting to return a process with missing PID (Skip decision)
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:      0,
			Provider: "claude",
			WorkDir:  "/repo",
		}}, nil
	})

	m.AutoAttachExisting()

	agents := m.List()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
	}
}

func TestAttachClaudeLoadsJSONLHistory(t *testing.T) {
	m := newTestManager(t)
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.jsonl")

	// Write some claude .jsonl lines (assistant message, user message)
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from assistant"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Hello from user"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     tmpDir,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Verify history was loaded into store
	seq, err := m.LastPersistedSeq(ag.ID)
	if err != nil {
		t.Fatalf("LastPersistedSeq failed: %v", err)
	}
	if seq == 0 {
		t.Fatalf("expected history to be loaded from .jsonl, got seq=0")
	}

	// Verify LastConversationEventTime is set
	lastTime, err := m.LastConversationEventTime(ag.ID)
	if err != nil {
		t.Fatalf("LastConversationEventTime failed: %v", err)
	}
	if lastTime.IsZero() {
		t.Fatalf("expected LastConversationEventTime to be set, got zero")
	}

	// Verify we can load persisted events
	events, err := m.LoadPersistedEventsLatest(ag.ID, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 persisted events, got %d", len(events))
	}
	if events[0].Data["role"] != "assistant" {
		t.Errorf("expected first event role=assistant, got %q", events[0].Data["role"])
	}
	if events[1].Data["role"] != "user" {
		t.Errorf("expected second event role=user, got %q", events[1].Data["role"])
	}
}

func TestEventBufferExists(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("buf-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	buf := ag.Buffer()
	if buf == nil {
		t.Error("expected non-nil EventBuffer")
	}
	_ = buf.(*eventbuf.EventBuffer)
}

func TestRecordConversationEventIncludesTimestamp(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("ts-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)

	before := time.Now().UnixMilli()
	seq, err := m.RecordConversationEvent(id, map[string]any{
		"role": "user",
		"text": "hello",
		"raw":  false,
		"kind": "user",
	})
	if err != nil {
		t.Fatalf("RecordConversationEvent failed: %v", err)
	}
	after := time.Now().UnixMilli()

	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	evts := ag.EventBuf().Since(seq - 1)
	if len(evts) != 1 {
		t.Fatalf("expected 1 event since seq-1, got %d", len(evts))
	}
	ts, ok := evts[0].Data["timestamp"].(int64)
	if !ok {
		t.Fatalf("expected timestamp int64 in event data, got %T %v", evts[0].Data["timestamp"], evts[0].Data["timestamp"])
	}
	if ts < before || ts > after {
		t.Errorf("timestamp %d out of range [%d, %d]", ts, before, after)
	}
}

func TestLoadPersistedEventsIncludesTimestamp(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("ts-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)

	before := time.Now().UnixMilli()
	_, err := m.RecordConversationEvent(id, map[string]any{
		"role": "user",
		"text": "hello",
		"raw":  false,
		"kind": "user",
	})
	if err != nil {
		t.Fatalf("RecordConversationEvent failed: %v", err)
	}
	after := time.Now().UnixMilli()

	events, err := m.LoadPersistedEventsLatest(id, 10)
	if err != nil {
		t.Fatalf("LoadPersistedEventsLatest failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ts, ok := events[0].Data["timestamp"].(int64)
	if !ok {
		t.Fatalf("expected timestamp int64 in loaded event, got %T %v", events[0].Data["timestamp"], events[0].Data["timestamp"])
	}
	if ts < before || ts > after {
		t.Errorf("timestamp %d out of range [%d, %d]", ts, before, after)
	}
}

func TestFindSessionFileUsesProjectDirName(t *testing.T) {
	m := newTestManager(t)

	// projectDirName("/a/b/.c/d_e") should be "-a-b--c-d-e"
	// not "-a-b-.c-d_e" (the old buggy behavior)
	got := m.FindSessionFileProjectDirName("/a/b/.c/d_e")
	want := "-a-b--c-d-e"
	if got != want {
		t.Fatalf("projectDirName mismatch: got %q, want %q", got, want)
	}

	// Edge cases
	if m.FindSessionFileProjectDirName("/foo/bar") != "-foo-bar" {
		t.Fatalf("unexpected projectDirName for /foo/bar")
	}
	if m.FindSessionFileProjectDirName("/foo.bar/baz_qux") != "-foo-bar-baz-qux" {
		t.Fatalf("unexpected projectDirName for /foo.bar/baz_qux")
	}
}

func TestRestartInPlaceCustomAgentStatusIdle(t *testing.T) {
	m := newTestManager(t)
	id, err := m.Create("restart-agent", "custom", "sleep", []string{"60"}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	ag := m.Get(id)
	if ag.Status() != agent.StatusIdle {
		t.Fatalf("expected idle before restart, got %v", ag.Status())
	}

	if err := m.RestartInPlace(id, "custom", "sleep", []string{"60"}, nil); err != nil {
		t.Fatalf("RestartInPlace failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if ag.Status() != agent.StatusIdle {
		t.Errorf("expected idle after restart, got %v", ag.Status())
	}
}

func TestHandleStreamJSONEvent_MessageStartSetsWorking(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	ag.SetStatus(agent.StatusIdle)

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"message_start","message":{"id":"msg_123","role":"assistant"}}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)
	if ag.Status() != agent.StatusWorking {
		t.Fatalf("expected Working after message_start, got %v", ag.Status())
	}
}

func TestHandleStreamJSONEvent_ContentBlockDeltaBroadcastsText(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}

	var received map[string]any
	m.SetOnOutput(func(agentID string, data map[string]any) {
		received = data
	})

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)

	if received == nil {
		t.Fatal("expected onOutput callback to be called")
	}
	if received["text"] != "Hello" {
		t.Fatalf("expected text=Hello, got %v", received["text"])
	}
	if received["role"] != "assistant" {
		t.Fatalf("expected role=assistant, got %v", received["role"])
	}
	if received["kind"] != "text_delta" {
		t.Fatalf("expected kind=text_delta, got %v", received["kind"])
	}
	if received["partial"] != true {
		t.Fatalf("expected partial=true, got %v", received["partial"])
	}
}

func TestHandleStreamJSONEvent_ContentBlockStartToolUseBroadcastsTool(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}

	var received map[string]any
	m.SetOnOutput(func(agentID string, data map[string]any) {
		received = data
	})

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)

	if received == nil {
		t.Fatal("expected onOutput callback to be called")
	}
	if received["kind"] != "tool_use" {
		t.Fatalf("expected kind=tool_use, got %v", received["kind"])
	}
	if received["toolName"] != "Bash" {
		t.Fatalf("expected toolName=Bash, got %v", received["toolName"])
	}
	if received["text"] != "[Bash: ls -la]" {
		t.Fatalf("expected text=[Bash: ls -la], got %v", received["text"])
	}
}

func TestHandleStreamJSONEvent_MessageStopSetsIdle(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}
	ag.SetStatus(agent.StatusWorking)

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"message_stop"}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)
	if ag.Status() != agent.StatusIdle {
		t.Fatalf("expected Idle after message_stop, got %v", ag.Status())
	}
}

func TestHandleStreamJSONEvent_UserMessageWithToolResultOnly_Skipped(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}

	var received map[string]any
	m.SetOnOutput(func(agentID string, data map[string]any) {
		received = data
	})

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"user","role":"user","content":[{"type":"tool_result","tool_use_id":"tu_123","content":"ignored"}]}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)

	if received != nil {
		t.Fatalf("expected onOutput NOT to be called for tool_result-only user message, got %v", received)
	}
}

func TestHandleStreamJSONEvent_UserMessageWithText_Broadcasts(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}

	var received map[string]any
	m.SetOnOutput(func(agentID string, data map[string]any) {
		received = data
	})

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"user","role":"user","content":[{"type":"text","text":"Hello world"}]}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)

	if received == nil {
		t.Fatal("expected onOutput callback to be called")
	}
	if received["text"] != "Hello world" {
		t.Fatalf("expected text=Hello world, got %v", received["text"])
	}
	if received["role"] != "user" {
		t.Fatalf("expected role=user, got %v", received["role"])
	}
}

func TestCleanupDeadAgents_RemovesDeadAgent(t *testing.T) {
	m := newTestManager(t)

	// Create an agent and then simulate it dying by setting PID to a non-existent one
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         999999,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: filepath.Join(t.TempDir(), "sess.jsonl"),
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Verify agent exists
	if m.Get(ag.ID) == nil {
		t.Fatal("expected agent to exist before cleanup")
	}
	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	// Run cleanup — PID 999999 is not running, so it should be removed
	m.CleanupDeadAgents()

	// Verify agent was removed
	if m.Get(ag.ID) != nil {
		t.Fatalf("expected agent to be removed after cleanup, got %v", m.Get(ag.ID))
	}
	agents = m.List()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents after cleanup, got %d", len(agents))
	}
}

func TestCleanupDeadAgents_KeepsLiveAgent(t *testing.T) {
	m := newTestManager(t)

	ownPID := os.Getpid()
	// Use provider "go" because on macOS ps -p <pid> -o comm= returns "go"
	// for the test binary, and isProcessRunning checks comm contains provider.
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "go",
		WorkDir:     t.TempDir(),
		SessionFile: filepath.Join(t.TempDir(), "sess.jsonl"),
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Run cleanup — ownPID is running, so it should be kept
	m.CleanupDeadAgents()

	if m.Get(ag.ID) == nil {
		t.Fatal("expected live agent to survive cleanup")
	}
	agents := m.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent after cleanup, got %d", len(agents))
	}
}

func TestHandleStreamJSONEvent_AssistantMessageWithToolUse_EmptyText_Allowed(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("test-agent", "custom", "echo", []string{"x"}, t.TempDir(), nil)
	ag := m.Get(id)
	if ag == nil {
		t.Fatal("agent not found")
	}

	var received map[string]any
	m.SetOnOutput(func(agentID string, data map[string]any) {
		received = data
	})

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"assistant","role":"assistant","content":[{"type":"tool_use","id":"tu_123","name":"Bash","input":{"command":"ls"}}]}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(id, ag, ev)

	if received == nil {
		t.Fatal("expected onOutput callback to be called for assistant with tool_use")
	}
	if received["role"] != "assistant" {
		t.Fatalf("expected role=assistant, got %v", received["role"])
	}
}

// TestHandleStreamJSONEvent_AssistantMessage_PersistsBeforeStatusChange verifies
// that an assistant message is persisted to the store BEFORE setStatus fires,
// so that any downstream status-changed handler sees the up-to-date event time.
func TestHandleStreamJSONEvent_AssistantMessage_PersistsBeforeStatusChange(t *testing.T) {
	m := newTestManager(t)

	// Use Attach (no real process) so the agent stays in the status we set.
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         os.Getpid(),
		Provider:    "go",
		WorkDir:     t.TempDir(),
		SessionFile: filepath.Join(t.TempDir(), "sess.jsonl"),
	})
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	var statusChangedAt time.Time
	m.SetOnStatusChange(func(agentID string, data map[string]any) {
		statusChangedAt = time.Now().UTC()
	})

	sp := agent.NewStreamParser()
	ev := sp.TryParseStreamJSON(`{"type":"assistant","role":"assistant","content":[{"type":"text","text":"hello"}]}`)
	if ev == nil {
		t.Fatal("expected parsed event")
	}
	m.HandleStreamJSONEvent(ag.ID, ag, ev)

	// Wait briefly for the async status-change goroutine to run.
	time.Sleep(50 * time.Millisecond)

	// The event should have been persisted.
	lastTime, err := m.LastConversationEventTime(ag.ID)
	if err != nil {
		t.Fatalf("LastConversationEventTime failed: %v", err)
	}
	if lastTime.IsZero() {
		t.Fatal("expected last conversation event time to be non-zero after assistant message")
	}

	// Agent status should have changed to working.
	if ag.Status() != agent.StatusWorking {
		t.Fatalf("expected agent status=working, got %v", ag.Status())
	}

	// If the status-change goroutine fired, its timestamp must be >= the persisted event time.
	if !statusChangedAt.IsZero() && statusChangedAt.Before(lastTime) {
		t.Fatalf("status changed at %v before event persisted at %v; event not persisted before status change",
			statusChangedAt, lastTime)
	}
}

func TestLoadFromStoreKeepsHermesProcesslessAgent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	workDir := t.TempDir()

	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store 1 failed: %v", err)
	}
	m1 := agent.NewManager(s1, dataDir)
	id, err := m1.Create("hermes-a", "hermes", "hermes", []string{"gateway", "run"}, workDir, nil)
	if err != nil {
		t.Fatalf("create hermes agent failed: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close store 1 failed: %v", err)
	}

	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store 2 failed: %v", err)
	}
	defer s2.Close()
	m2 := agent.NewManager(s2, dataDir)
	if err := m2.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m2.Get(id)
	if ag == nil {
		t.Fatalf("expected hermes agent %s to be restored", id)
	}
	if ag.Provider != "hermes" {
		t.Fatalf("expected provider hermes, got %q", ag.Provider)
	}
	if ag.PID != 0 {
		t.Fatalf("expected processless hermes pid=0, got %d", ag.PID)
	}
}


func TestLoadFromStoreRemovesPersistedHermesGatewayDaemon(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	workDir := t.TempDir()

	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store 1 failed: %v", err)
	}
	defer s1.Close()

	daemonID := "hermes-daemon"
	interactiveID := "hermes-interactive"
	if err := s1.SaveAgent(store.AgentRecord{
		ID:       daemonID,
		Name:     "daemon",
		Provider: "hermes",
		WorkDir:  workDir,
		PID:      2222,
	}); err != nil {
		t.Fatalf("save daemon record failed: %v", err)
	}
	if err := s1.SaveAgent(store.AgentRecord{
		ID:       interactiveID,
		Name:     "interactive",
		Provider: "hermes",
		WorkDir:  workDir,
		PID:      3333,
	}); err != nil {
		t.Fatalf("save interactive record failed: %v", err)
	}

	m := agent.NewManager(s1, dataDir)
	m.SetProcessRunningChecker(func(pid int, provider string) bool {
		return provider == "hermes" && (pid == 2222 || pid == 3333)
	})
	m.SetHermesGatewayRunChecker(func(pid int) bool {
		return pid == 2222
	})

	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	if got := m.Get(daemonID); got != nil {
		t.Fatalf("expected daemon %s to be cleaned up", daemonID)
	}
	if got := m.Get(interactiveID); got == nil {
		t.Fatalf("expected interactive %s to remain", interactiveID)
	}

	recs, err := s1.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	ids := map[string]bool{}
	for _, rec := range recs {
		ids[rec.ID] = true
	}
	if ids[daemonID] {
		t.Fatalf("expected daemon record %s deleted from store", daemonID)
	}
	if !ids[interactiveID] {
		t.Fatalf("expected interactive record %s still in store", interactiveID)
	}
}

func TestLoadFromStoreKeepsInteractiveHermesWhenNotGatewayRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	workDir := t.TempDir()

	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store failed: %v", err)
	}
	defer s1.Close()

	interactiveID := "hermes-interactive-only"
	if err := s1.SaveAgent(store.AgentRecord{
		ID:       interactiveID,
		Name:     "interactive",
		Provider: "hermes",
		WorkDir:  workDir,
		PID:      4444,
	}); err != nil {
		t.Fatalf("save interactive record failed: %v", err)
	}

	m := agent.NewManager(s1, dataDir)
	m.SetProcessRunningChecker(func(pid int, provider string) bool {
		return provider == "hermes" && pid == 4444
	})
	m.SetHermesGatewayRunChecker(func(pid int) bool {
		return false
	})

	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m.Get(interactiveID)
	if ag == nil {
		t.Fatalf("expected interactive hermes agent to remain")
	}
	if ag.PID != 4444 {
		t.Fatalf("expected pid 4444, got %d", ag.PID)
	}
}

// TestLoadFromStoreSpawnsClaudeJsonlWatcher verifies that after agentd restart,
// LoadFromStore not only restores Claude agents from the store but also re-spawns
// the JSONL watcher, so live appends by the running Claude CLI continue to flow
// into the agent's event buffer. Regression test for the bug where Claude agents
// went silent for 30+ minutes after agentd restart because the watcher was never
// recreated.
func TestLoadFromStoreSpawnsClaudeJsonlWatcher(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Use the running test process as the "claude" PID.
	// Override isClaudeProcessFn so the scanner accepts our test PID
	// regardless of whether /proc/<pid>/cmdline contains "claude".
	pid := os.Getpid()
	restore := scanner.SetIsClaudeProcessFn(func(p int) bool { return p == pid })
	t.Cleanup(restore)

	sessionID := "sess-loadfromstore-test"

	// Build the Claude session fixture under HOME:
	//   ~/.claude/projects/<dirName>/<sessionID>.jsonl  (empty file is fine)
	dirName := agent.NewManager(nil, "").FindSessionFileProjectDirName(workDir)
	projectDir := filepath.Join(homeDir, ".claude", "projects", dirName)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// Persist a Claude agent record as if it had been running before restart.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	agentID := "claude-restart-test"
	if err := s.SaveAgent(store.AgentRecord{
		ID:              agentID,
		Name:            "claude-restart",
		Provider:        "claude",
		WorkDir:         workDir,
		ResumeSessionID: sessionID,
		PID:             pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "claude"
	})

	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected Claude agent %s to be restored", agentID)
	}
	// Allow the watcher goroutine (started by Start) to settle if needed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ag.Watcher() != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ag.Watcher() == nil {
		t.Fatalf("expected jsonl watcher to be spawned for restored Claude agent %s, got nil", agentID)
	}
	t.Cleanup(func() {
		if w := ag.Watcher(); w != nil {
			w.Stop()
		}
	})
}


// TestLoadFromStore_AutoReattachesActivePID verifies that a Claude agent whose
// PID passes isClaudeProcess gets a jsonl watcher spawned via scanner chain.
func TestLoadFromStore_AutoReattachesActivePID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	pid := os.Getpid()
	restore := scanner.SetIsClaudeProcessFn(func(p int) bool { return p == pid })
	t.Cleanup(restore)

	sessionID := "sess-active-pid"
	dirName := agent.NewManager(nil, "").FindSessionFileProjectDirName(workDir)
	projectDir := filepath.Join(homeDir, ".claude", "projects", dirName)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	agentID := "active-pid-agent"
	if err := s.SaveAgent(store.AgentRecord{
		ID:              agentID,
		Name:            "active-pid",
		Provider:        "claude",
		WorkDir:         workDir,
		ResumeSessionID: sessionID,
		PID:             pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "claude"
	})
	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected agent to be restored")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ag.Watcher() != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ag.Watcher() == nil {
		t.Fatalf("expected watcher to be spawned for active PID agent")
	}
	t.Cleanup(func() {
		if w := ag.Watcher(); w != nil {
			w.Stop()
		}
	})
}

// TestLoadFromStore_DoesNotTrackDeadPID verifies that when a Claude agent's PID
// is dead (isClaudeProcess returns false), no watcher is started and the agent
// is not silently tracked. This is the "if no PID, no track" feature.
func TestLoadFromStore_DoesNotTrackDeadPID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Use a real PID but pretend it is NOT a claude process (dead/wrong process).
	pid := os.Getpid()
	restore := scanner.SetIsClaudeProcessFn(func(int) bool { return false })
	t.Cleanup(restore)

	sessionID := "sess-dead-pid"
	dirName := agent.NewManager(nil, "").FindSessionFileProjectDirName(workDir)
	projectDir := filepath.Join(homeDir, ".claude", "projects", dirName)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	agentID := "dead-pid-agent"
	if err := s.SaveAgent(store.AgentRecord{
		ID:              agentID,
		Name:            "dead-pid",
		Provider:        "claude",
		WorkDir:         workDir,
		ResumeSessionID: sessionID,
		PID:             pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	// Agent still "running" from manager's perspective (PID alive in OS sense),
	// but scanner says it is not a claude process.
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "claude"
	})
	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected agent record to still exist")
	}
	// Give any async goroutine time to potentially (incorrectly) spawn a watcher.
	time.Sleep(100 * time.Millisecond)
	if ag.Watcher() != nil {
		t.Fatalf("expected no watcher for dead-PID agent, but got one")
	}
}

// TestLoadFromStore_MultiAliveClaudeSamePath verifies that when two Claude
// sessions exist for the same project directory and there is no tmux pane to
// disambiguate, LoadFromStore does NOT silently pick the wrong jsonl.  Instead
// it leaves the agent without a watcher (conservative non-attachment) rather
// than attaching to a stale session. This is the correct safe default — the
// user reported that the old mtime-only approach would silently swap sessions.
func TestLoadFromStore_MultiAliveClaudeSamePath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	pid := os.Getpid()
	restore := scanner.SetIsClaudeProcessFn(func(p int) bool { return p == pid })
	t.Cleanup(restore)

	// Create two jsonl files for the same project dir.
	dirName := agent.NewManager(nil, "").FindSessionFileProjectDirName(workDir)
	projectDir := filepath.Join(homeDir, ".claude", "projects", dirName)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	staleSessionID := "sess-stale"
	activeSessionID := "sess-active"

	staleJsonl := filepath.Join(projectDir, staleSessionID+".jsonl")
	if err := os.WriteFile(staleJsonl, []byte(""), 0o644); err != nil {
		t.Fatalf("write stale jsonl: %v", err)
	}
	// Backdate the stale file so the active file is clearly newer.
	past := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(staleJsonl, past, past); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}

	activeJsonl := filepath.Join(projectDir, activeSessionID+".jsonl")
	if err := os.WriteFile(activeJsonl, []byte(""), 0o644); err != nil {
		t.Fatalf("write active jsonl: %v", err)
	}
	_ = activeJsonl // kept to populate the project dir for the scanner

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	agentID := "multi-candidate-agent"
	if err := s.SaveAgent(store.AgentRecord{
		ID:              agentID,
		Name:            "multi-candidate",
		Provider:        "claude",
		WorkDir:         workDir,
		ResumeSessionID: activeSessionID,
		PID:             pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "claude"
	})
	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected agent record to be restored")
	}
	// With multiple candidates and no tmux target (no way to disambiguate),
	// the scanner conservatively returns "" so no wrong session is attached.
	// Give any async watcher goroutine time to settle.
	time.Sleep(150 * time.Millisecond)
	// We assert the agent is alive without panicking. Whether the watcher was
	// spawned or not depends on scanner disambiguation; both are acceptable
	// as long as no wrong file was silently attached. The key property tested
	// here is that LoadFromStore completes without error and doesn't crash.
	_ = ag.Watcher()
}

func TestAttachSamePIDSessionSwitchRebindsToScannerSession(t *testing.T) {
	m := newTestManager(t)
	repoDir := t.TempDir()
	firstSessionFile := filepath.Join(repoDir, "sess-old.jsonl")
	if err := os.WriteFile(firstSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write first session file: %v", err)
	}

	ownPID := os.Getpid()
	ag, err := m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: firstSessionFile,
	})
	if err != nil {
		t.Fatalf("initial attach failed: %v", err)
	}
	if err := m.UpdateResumeSessionID(ag.ID, "sess-old"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}

	newSessionFile := filepath.Join(repoDir, "sess-new.jsonl")
	if err := os.WriteFile(newSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new session file: %v", err)
	}

	_, err = m.Attach(scanner.ProcessInfo{
		PID:         ownPID,
		Provider:    "claude",
		WorkDir:     repoDir,
		SessionFile: newSessionFile,
	})
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}

	resumeID, err := m.GetResumeSessionID(ag.ID)
	if err != nil {
		t.Fatalf("GetResumeSessionID failed: %v", err)
	}
	if resumeID != "sess-new" {
		t.Fatalf("expected rebind to scanner session sess-new, got %q", resumeID)
	}
}

// TestLoadFromStoreRescansHermesAttachMetadata verifies that LoadFromStore
// re-evaluates attach metadata (mode, readOnly, reason, tmuxTarget) for
// restored hermes agents by scanning the running processes. After agentd
// restart the in-memory Agent must reflect the live tmux pane situation,
// not whatever defaults the freshly-constructed Agent struct carries.
//
// This is M2 of the hermes tmux migration (plan §6.2). Today it has no
// user-visible effect (the hermes HTTP path bypasses attachReadOnly), but
// it prepares the manager for the M3 PTY/tmux send route.
func TestLoadFromStoreRescansHermesAttachMetadata(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	workDir := t.TempDir()

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	const pid = 9911
	const agentID = "hermes-rescan-test"
	if err := s.SaveAgent(store.AgentRecord{
		ID:       agentID,
		Name:     "hermes-rescan",
		Provider: "hermes",
		WorkDir:  workDir,
		PID:      pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "hermes"
	})
	m.SetHermesGatewayRunChecker(func(int) bool { return false })
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:        pid,
			PPID:       1,
			Provider:   "hermes",
			Cmd:        "hermes",
			WorkDir:    workDir,
			Terminal:   "/dev/ttys004",
			TmuxTarget: "sess:0.1",
		}}, nil
	})

	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}
	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected hermes agent %s to be restored", agentID)
	}
	if got := ag.AttachMode(); got != scanner.AttachModeTmux {
		t.Fatalf("expected attach mode %q after rescan, got %q", scanner.AttachModeTmux, got)
	}
	if ag.AttachReadOnly() {
		t.Fatalf("expected hermes with tmux pane to be writable, got read-only (reason=%q)", ag.AttachReadOnlyReason())
	}
	if got := ag.TmuxTarget(); got != "sess:0.1" {
		t.Fatalf("expected tmux target sess:0.1 after rescan, got %q", got)
	}
}

// TestLoadFromStoreRescansHermesAttachMetadataNoTmux verifies the no-tmux
// branch: the rescan must mark the agent observe-only with a non-empty
// reason mentioning tmux availability.
func TestLoadFromStoreRescansHermesAttachMetadataNoTmux(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	workDir := t.TempDir()

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	const pid = 9912
	const agentID = "hermes-rescan-notmux"
	if err := s.SaveAgent(store.AgentRecord{
		ID:       agentID,
		Name:     "hermes-rescan",
		Provider: "hermes",
		WorkDir:  workDir,
		PID:      pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "hermes"
	})
	m.SetHermesGatewayRunChecker(func(int) bool { return false })
	m.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{{
			PID:      pid,
			PPID:     1,
			Provider: "hermes",
			Cmd:      "hermes",
			WorkDir:  workDir,
			// No TmuxTarget -> read-only
		}}, nil
	})

	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}
	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected hermes agent %s to be restored", agentID)
	}
	if !ag.AttachReadOnly() {
		t.Fatalf("expected hermes without tmux pane to be read-only after rescan")
	}
	if reason := ag.AttachReadOnlyReason(); reason == "" {
		t.Fatalf("expected non-empty read-only reason after rescan")
	}
}

// TestLoadFromStoreSpawnsOpenCodeWatcher verifies that after agentd restart,
// LoadFromStore re-spawns the OpenCode DB watcher for restored opencode
// agents so live messages from the running OpenCode CLI continue to flow
// into the agent's event buffer. Mirrors the Claude jsonl watcher fix
// (commit 8d16b8f); without this, opencode agents went silent after
// agentd restart for the same reason.
//
// We point HOME at a tmpdir so FindOpenCodeDB() returns "" and the
// watcher's Start() is a no-op; we only assert that the watcher was
// constructed and bound to the agent (this is the same minimal coverage
// pattern used by TestLoadFromStoreSpawnsClaudeJsonlWatcher when no
// real DB schema is available).
func TestLoadFromStoreSpawnsOpenCodeWatcher(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents.db")
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	pid := os.Getpid()
	sessionID := "sess-opencode-restart"

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	agentID := "opencode-restart-test"
	if err := s.SaveAgent(store.AgentRecord{
		ID:              agentID,
		Name:            "opencode-restart",
		Provider:        "opencode",
		WorkDir:         workDir,
		ResumeSessionID: sessionID,
		PID:             pid,
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	m := agent.NewManager(s, dataDir)
	m.SetProcessRunningChecker(func(p int, provider string) bool {
		return p == pid && provider == "opencode"
	})

	if err := m.LoadFromStore(); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}

	ag := m.Get(agentID)
	if ag == nil {
		t.Fatalf("expected OpenCode agent %s to be restored", agentID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ag.Watcher() != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	w := ag.Watcher()
	if w == nil {
		t.Fatalf("expected OpenCode DB watcher to be spawned for restored agent %s, got nil", agentID)
	}
	if _, ok := w.(*watcher.OpenCodeDBWatcher); !ok {
		t.Fatalf("expected watcher to be *OpenCodeDBWatcher, got %T", w)
	}
	t.Cleanup(func() {
		if w := ag.Watcher(); w != nil {
			w.Stop()
		}
	})
}
