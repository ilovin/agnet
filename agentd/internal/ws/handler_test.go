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
	"github.com/phone-talk/agentd/internal/watcher"
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

func TestResolveLaunchCodexSession(t *testing.T) {
	provider, cmd, args, _ := testSvc.ResolveLaunch("codex", "", nil, "sess_codex", "", "")
	if provider != "codex" {
		t.Fatalf("provider = %q, want codex", provider)
	}
	if filepath.Base(cmd) != "codex" {
		t.Fatalf("cmd = %q, want basename codex", cmd)
	}
	want := []string{"resume", "sess_codex"}
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

// TestUserMessageBroadcastIncludesNodeIDAndSessionID verifies the canonical
// broadcastData built for the four conversation.send code paths (tmux PTY /
// generic PTY / claude restart / fresh claude start) carries both the
// nodeId and the agent's current resume sessionId. This is the agentd half
// of the hotfix for the "messages disappear after a Hermes /clear" bug:
// the Flutter app keys its conversation cache on (nodeId, agentId, sessionId)
// so omitting either field causes pushed messages to land in the wrong
// cache bucket and never render.
func TestUserMessageBroadcastIncludesNodeIDAndSessionID(t *testing.T) {
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

	const wantSession = "session-msg-123"
	if err := mgr.UpdateResumeSessionID(ag.ID, wantSession); err != nil {
		t.Fatalf("UpdateResumeSessionID: %v", err)
	}

	const wantNode = "testnode"
	srv := New(mgr, "testtoken", wantNode)
	h := &handler{server: srv, service: NewAgentService()}

	// Single canonical builder must be used by every conversation.send path so
	// no path can drift away from including (nodeId, sessionId, seq).
	data := h.buildUserMessageBroadcast(ag, "hello", []string{"/tmp/a.png"}, 1, 42)

	if got, _ := data["nodeId"].(string); got != wantNode {
		t.Fatalf("broadcast nodeId = %q, want %q", got, wantNode)
	}
	if got, _ := data["sessionId"].(string); got != wantSession {
		t.Fatalf("broadcast sessionId = %q, want %q", got, wantSession)
	}
	if got, _ := data["agentId"].(string); got != ag.ID {
		t.Fatalf("broadcast agentId = %q, want %q", got, ag.ID)
	}
	if got, _ := data["role"].(string); got != "user" {
		t.Fatalf("broadcast role = %q, want %q", got, "user")
	}
	if got, _ := data["text"].(string); got != "hello" {
		t.Fatalf("broadcast text = %q, want %q", got, "hello")
	}
	if got, _ := data["imageCount"].(int); got != 1 {
		t.Fatalf("broadcast imageCount = %v, want 1", data["imageCount"])
	}
	imgs, ok := data["images"].([]string)
	if !ok || len(imgs) != 1 || imgs[0] != "/tmp/a.png" {
		t.Fatalf("broadcast images = %v, want [/tmp/a.png]", data["images"])
	}
	if _, hasTs := data["timestamp"]; !hasTs {
		t.Fatalf("broadcast missing timestamp: %v", data)
	}
	if got, _ := data["seq"].(uint64); got != 42 {
		t.Fatalf("broadcast seq = %v, want 42", data["seq"])
	}
}

// TestUserMessageBroadcastOmitsImagesWhenEmpty ensures the canonical
// builder does not put a stale "images" entry on the broadcast when no
// images were attached (the original 4 paths only set the key when
// imagePaths was non-empty; we keep that contract).
func TestUserMessageBroadcastOmitsImagesWhenEmpty(t *testing.T) {
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

	srv := New(mgr, "testtoken", "n1")
	h := &handler{server: srv, service: NewAgentService()}

	data := h.buildUserMessageBroadcast(ag, "ping", nil, 0, 7)

	if _, has := data["images"]; has {
		t.Fatalf("broadcast should omit images when none were attached, got %v", data["images"])
	}
	if got, _ := data["nodeId"].(string); got != "n1" {
		t.Fatalf("nodeId = %q, want n1", got)
	}
	if got, _ := data["seq"].(uint64); got != 7 {
		t.Fatalf("seq = %v, want 7", data["seq"])
	}
}

// TestUserMessageBroadcastOmitsSeqWhenZero ensures the canonical builder does
// not emit a placeholder zero seq — the frontend dedups by seq, and a stream
// of 0-seq broadcasts would collide.
func TestUserMessageBroadcastOmitsSeqWhenZero(t *testing.T) {
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
	srv := New(mgr, "testtoken", "n1")
	h := &handler{server: srv, service: NewAgentService()}

	data := h.buildUserMessageBroadcast(ag, "ping", nil, 0, 0)
	if _, has := data["seq"]; has {
		t.Fatalf("broadcast should omit seq when zero, got %v", data["seq"])
	}
}

// TestAssistantMessageBroadcastIncludesSessionID verifies the end-to-end
// "agent fires output → ws server broadcasts conversation.message" pipeline
// stamps the agent's resume sessionId onto the broadcast params. This is the
// regression test for the symptom "app shows stale message (seq 242) while
// agentd DB has seq 250": when sessionId is missing from the broadcast,
// conversation_provider.dart routes the event into the (nodeId, agentId, "")
// bucket instead of the live (nodeId, agentId, sessionId) bucket and the
// rendered transcript stops advancing.
func TestAssistantMessageBroadcastIncludesSessionID(t *testing.T) {
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

	const wantSession = "session-assistant-broadcast-123"
	if err := mgr.UpdateResumeSessionID(ag.ID, wantSession); err != nil {
		t.Fatalf("UpdateResumeSessionID: %v", err)
	}

	const wantNode = "testnode-assistant"
	srv := New(mgr, "testtoken", wantNode)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer testtoken"}}
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Wait briefly for the server to register the client before driving the
	// watcher callback (otherwise the broadcast lands before the client is in
	// the map and we time out).
	deadline := time.Now().Add(2 * time.Second)
	for srv.ClientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// Drive the production watcher → onOutput → broadcast pipeline by firing a
	// synthetic ConversationEvent through the same callback the JSONL watcher
	// uses in production.
	cb := mgr.MakeWatcherCallback(ag.ID, ag)
	cb(watcher.ConversationEvent{
		Role: "assistant",
		Text: "hello after /clear",
	})

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg["method"] != "conversation.message" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			t.Fatalf("conversation.message missing params: %v", msg)
		}
		gotSession, _ := params["sessionId"].(string)
		if gotSession != wantSession {
			t.Fatalf("conversation.message sessionId: got %q want %q (full params: %v)", gotSession, wantSession, params)
		}
		if got, _ := params["nodeId"].(string); got != wantNode {
			t.Fatalf("conversation.message nodeId: got %q want %q", got, wantNode)
		}
		if got, _ := params["agentId"].(string); got != ag.ID {
			t.Fatalf("conversation.message agentId: got %q want %q", got, ag.ID)
		}
		return
	}
}

// TestAssistantMessageUpdateBroadcastIncludesSessionID is the streaming
// counterpart of TestAssistantMessageBroadcastIncludesSessionID. The
// conversation.message_update event (opencode-style streaming where text
// grows under a stable msg_id) must also carry sessionId, otherwise the
// frontend's _handleMessageUpdate lookup in the wrong cache bucket leaves
// the streaming bubble frozen at its first chunk.
func TestAssistantMessageUpdateBroadcastIncludesSessionID(t *testing.T) {
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

	const wantSession = "session-stream-update-456"
	if err := mgr.UpdateResumeSessionID(ag.ID, wantSession); err != nil {
		t.Fatalf("UpdateResumeSessionID: %v", err)
	}

	srv := New(mgr, "testtoken", "n-stream")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer testtoken"}}
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for srv.ClientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	cb := mgr.MakeWatcherCallback(ag.ID, ag)
	// First fire creates the message under msg_id; second fire grows the text
	// and triggers the _update path which the server forwards as
	// conversation.message_update.
	cb(watcher.ConversationEvent{Role: "assistant", Text: "chunk1", MsgID: "msg-update-1"})
	cb(watcher.ConversationEvent{Role: "assistant", Text: "chunk1 chunk2", MsgID: "msg-update-1"})

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	sawUpdate := false
	for !sawUpdate {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg["method"] != "conversation.message_update" {
			continue
		}
		sawUpdate = true
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			t.Fatalf("conversation.message_update missing params: %v", msg)
		}
		gotSession, _ := params["sessionId"].(string)
		if gotSession != wantSession {
			t.Fatalf("conversation.message_update sessionId: got %q want %q (full params: %v)", gotSession, wantSession, params)
		}
	}
}
