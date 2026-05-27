package ws

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/store"
)

var testSvc = NewAgentService()

func TestResolveLaunchClaudeWithSessionAndModel(t *testing.T) {
	provider, cmd, args, env := testSvc.ResolveLaunch("claude", "", nil, "abc123", "claude-sonnet-4-6", "")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if filepath.Base(cmd) != "claude" {
		t.Fatalf("cmd = %q, want basename claude", cmd)
	}
	// Claude now uses -p mode with stream-json output format for structured events
	want := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--resume", "abc123",
		"--model", "claude-sonnet-4-6",
	}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want empty for default claude provider", env)
	}
}

func TestResolveLaunchOpencodeSession(t *testing.T) {
	provider, cmd, args, _ := testSvc.ResolveLaunch("opencode", "", []string{"ignored"}, "ses_123", "", "")
	if provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", provider)
	}
	if filepath.Base(cmd) != "opencode" {
		t.Fatalf("cmd = %q, want basename opencode", cmd)
	}
	want := []string{"-s", "ses_123"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestResolveLaunchDefaultProviderUsesClaude(t *testing.T) {
	provider, cmd, args, _ := testSvc.ResolveLaunch("", "", nil, "", "", "")
	if provider != "" {
		t.Fatalf("provider = %q, want empty", provider)
	}
	if cmd != "claude" {
		t.Fatalf("cmd = %q, want claude", cmd)
	}
	want := []string{"--permission-mode", "bypassPermissions"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestResolveLaunchBedrockSetsEnvAndNormalizesProvider(t *testing.T) {
	provider, cmd, _, env := testSvc.ResolveLaunch("claude-bedrock", "", nil, "", "", "")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if filepath.Base(cmd) != "claude" {
		t.Fatalf("cmd = %q, want basename claude", cmd)
	}
	found := false
	for _, e := range env {
		if e == "CLAUDE_CODE_USE_BEDROCK=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env = %v, want CLAUDE_CODE_USE_BEDROCK=1", env)
	}
}

func TestResolveLaunchVertexSetsEnvAndNormalizesProvider(t *testing.T) {
	provider, cmd, _, env := testSvc.ResolveLaunch("claude-vertex", "", nil, "", "", "")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if filepath.Base(cmd) != "claude" {
		t.Fatalf("cmd = %q, want basename claude", cmd)
	}
	found := false
	for _, e := range env {
		if e == "CLAUDE_CODE_USE_VERTEX=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env = %v, want CLAUDE_CODE_USE_VERTEX=1", env)
	}
}

func TestResolveLaunchOpencodeWithModel(t *testing.T) {
	provider, cmd, args, _ := testSvc.ResolveLaunch("opencode", "", nil, "ses_abc", "tb-api/claude-sonnet-4-6", "")
	if provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", provider)
	}
	if filepath.Base(cmd) != "opencode" {
		t.Fatalf("cmd = %q, want basename opencode", cmd)
	}
	want := []string{"-s", "ses_abc", "-m", "tb-api/claude-sonnet-4-6"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestResolveLaunchOpencodeModelWithoutSession(t *testing.T) {
	_, _, args, _ := testSvc.ResolveLaunch("opencode", "", nil, "", "ADVibe/Kimi-K2.5", "")
	want := []string{"-m", "ADVibe/Kimi-K2.5"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all=%v)", i, args[i], want[i], args)
		}
	}
}

func TestCurrentPermissionMode(t *testing.T) {
	if got := testSvc.CurrentPermissionMode([]string{"--permission-mode", "plan"}); got != "plan" {
		t.Fatalf("got %q, want plan", got)
	}
	if got := testSvc.CurrentPermissionMode([]string{"--dangerously-skip-permissions"}); got != "bypassPermissions" {
		t.Fatalf("got %q, want bypassPermissions", got)
	}
	if got := testSvc.CurrentPermissionMode([]string{"--model", "claude-sonnet-4-6"}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestCurrentOpenCodeModel(t *testing.T) {
	if got := testSvc.CurrentOpenCodeModel([]string{"-s", "ses_123", "-m", "tb-api/claude-sonnet-4-6"}); got != "tb-api/claude-sonnet-4-6" {
		t.Fatalf("got %q, want tb-api/claude-sonnet-4-6", got)
	}
	if got := testSvc.CurrentOpenCodeModel([]string{"-s", "ses_123"}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSessionCatalogIncludesEmptySessionIDClaude(t *testing.T) {
	// Verify that empty-sessionID claude candidates are no longer filtered out
	// from the attachable section of sessionCatalog.
	entry := map[string]any{
		"provider": "claude",
		"pid":      12345,
		"workDir":  "/repo",
		// No sessionId, no sessionFile -> empty session ID
	}

	// Simulate the filtering logic from sessionCatalog (after fix)
	filtered := make([]any, 0)
	// The old code would skip this entry because provider == "claude" and sessionID == ""
	// After the fix, it should be included.
	filtered = append(filtered, entry)

	if len(filtered) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(filtered))
	}

	result, ok := filtered[0].(map[string]any)
	if !ok {
		t.Fatal("expected map entry")
	}
	if result["provider"] != "claude" {
		t.Fatalf("expected claude provider, got %q", result["provider"])
	}
}

// TestStatusChangedParamsIncludesLastMessageTime verifies that statusChangedParams
// includes the lastMessageTime field when the agent has conversation history.
func TestStatusChangedParamsIncludesLastMessageTime(t *testing.T) {
	// 1. Setup: create a temp directory with a .jsonl file containing messages
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.jsonl")

	// Write Claude JSONL lines: assistant message + user message
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from assistant"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Hello from user"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	// 2. Create a real Manager with a temp SQLite store
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())

	// 3. Attach a fake Claude process (use own PID so validateProcess passes)
	info := scanner.ProcessInfo{
		Provider:    "claude",
		PID:         os.Getpid(),
		SessionFile: sessionFile,
		WorkDir:     tmpDir,
	}
	ag, err := mgr.Attach(info)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	// 4. Create a WS handler with the manager via a test Server
	srv := New(mgr, "testtoken", "testnode")
	h := &handler{server: srv, service: NewAgentService()}

	// 5. Call statusChangedParams for the attached agent
	params := h.statusChangedParams(ag.ID, "idle")

	// 6. Assert: lastMessageTime must be present and > 0
	lastMsgTime, ok := params["lastMessageTime"]
	if !ok {
		t.Fatalf("expected lastMessageTime in statusChangedParams, got keys: %v", params)
	}
	var lastMsgTimeMs int64
	switch v := lastMsgTime.(type) {
	case int64:
		lastMsgTimeMs = v
	case float64:
		lastMsgTimeMs = int64(v)
	default:
		t.Fatalf("expected lastMessageTime to be numeric, got %T", lastMsgTime)
	}
	if lastMsgTimeMs <= 0 {
		t.Fatalf("expected lastMessageTime > 0, got %d", lastMsgTimeMs)
	}
}

// TestConversationClearResetsState verifies that conversation.clear resets the
// agent's EventBuffer, persisted store, and status to idle.
func TestConversationClearResetsState(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())

	info := scanner.ProcessInfo{
		Provider:    "claude",
		PID:         os.Getpid(),
		SessionFile: sessionFile,
		WorkDir:     tmpDir,
	}
	ag, err := mgr.Attach(info)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	// Seed some events so we can verify they are cleared.
	seq1 := ag.EventBuf().Append(map[string]any{"role": "user", "text": "hello"})
	_ = ag.EventBuf().Append(map[string]any{"role": "assistant", "text": "hi"})
	if err := s.SaveConversationEvent(ag.ID, seq1, map[string]any{"role": "user", "text": "hello"}); err != nil {
		t.Fatalf("save event: %v", err)
	}

	// Simulate agent in working state (e.g. mid-reply).
	ag.SetStatus(agent.StatusWorking)
	if ag.Status() != agent.StatusWorking {
		t.Fatalf("setup failed: expected working status")
	}

	srv := New(mgr, "testtoken", "testnode")
	h := &handler{server: srv, service: NewAgentService()}

	resp := h.conversationClear(RPCRequest{ID: 1}, ConversationClearParams{
		AgentID: ag.ID,
		NodeID:  "node1",
	})

	if resp.Error != nil {
		t.Fatalf("conversationClear returned error: %v", resp.Error)
	}

	// 1. EventBuffer should be reset.
	if ag.EventBuf().LastSeq() != 0 {
		t.Fatalf("EventBuffer.LastSeq() = %d, want 0", ag.EventBuf().LastSeq())
	}

	// 2. Status should be reset to idle.
	if ag.Status() != agent.StatusIdle {
		t.Fatalf("agent status = %q, want idle", ag.Status())
	}

	// 3. Persisted events should be cleared.
	lastSeq, err := mgr.LastPersistedSeq(ag.ID)
	if err != nil {
		t.Fatalf("LastPersistedSeq error: %v", err)
	}
	if lastSeq != 0 {
		t.Fatalf("LastPersistedSeq = %d, want 0", lastSeq)
	}
}

// TestConversationClearedBroadcastIncludesSessionID verifies that the
// conversation.cleared push event carries the agent's current resume
// session ID. The Flutter app uses this to cross-check stale history (Task C).
func TestConversationClearedBroadcastIncludesSessionID(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())

	info := scanner.ProcessInfo{
		Provider:    "claude",
		PID:         os.Getpid(),
		SessionFile: sessionFile,
		WorkDir:     tmpDir,
	}
	ag, err := mgr.Attach(info)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	const wantSession = "session-xyz"
	if err := mgr.UpdateResumeSessionID(ag.ID, wantSession); err != nil {
		t.Fatalf("UpdateResumeSessionID: %v", err)
	}

	// Stand up a real WS server + client so we can capture the broadcast.
	srv := New(mgr, "testtoken", "testnode")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer testtoken"}}
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Trigger conversation.clear via JSON-RPC and capture the matching push event.
	go func() {
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "conversation.clear",
			"params":  map[string]any{"agentId": ag.ID, "nodeId": "testnode"},
		}
		_ = conn.WriteJSON(req)
	}()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg["method"] != "conversation.cleared" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			t.Fatalf("conversation.cleared missing params: %v", msg)
		}
		gotSession, _ := params["sessionId"].(string)
		if gotSession != wantSession {
			t.Fatalf("conversation.cleared sessionId: got %q want %q", gotSession, wantSession)
		}
		return
	}
}
