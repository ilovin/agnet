# agentd MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `agentd` — a Go daemon that runs on remote machines, manages Claude Code agent processes via PTY, parses conversation output from JSONL files, and exposes a WebSocket JSON-RPC 2.0 API for monitoring and control.

**Architecture:** agentd is a single Go binary with no runtime dependencies. It spawns AI agent processes (starting with Claude Code) as PTY children, watches their JSONL session files for conversation events, maintains an in-memory EventBuffer per agent for reconnect replay, and serves a WebSocket API over port 7373. Authentication uses a static bearer token in the config file.

**Tech Stack:** Go 1.22+, `github.com/gorilla/websocket`, `github.com/creack/pty`, `github.com/mattn/go-sqlite3`, `gopkg.in/yaml.v3`, standard library `fsnotify` alternative via polling.

---

## File Structure

```
phone-talk/
└── agentd/
    ├── cmd/
    │   └── agentd/
    │       └── main.go              # CLI entrypoint: start/stop/status subcommands
    ├── internal/
    │   ├── config/
    │   │   └── config.go            # Load ~/.agentd/config.yaml (token, port, data_dir)
    │   ├── agent/
    │   │   ├── agent.go             # Agent struct + state machine (Created→Starting→Idle⇄Working→Stopped→Crashed)
    │   │   ├── manager.go           # AgentManager: create/stop/restart/list agents
    │   │   └── provider.go          # Provider interface + ClaudeCode implementation
    │   ├── pty/
    │   │   └── pty.go               # PTY spawn/write/kill wrapper around creack/pty
    │   ├── watcher/
    │   │   └── claude.go            # ClaudeWatcher: poll JSONL file, emit ConversationEvents
    │   ├── eventbuf/
    │   │   └── eventbuf.go          # EventBuffer: ring buffer with monotonic seq, thread-safe
    │   ├── store/
    │   │   └── store.go             # SQLite: persist agent metadata (id, provider, cwd, name, resumeSessionId)
    │   └── ws/
    │       ├── server.go            # WebSocket server: upgrade, auth, dispatch JSON-RPC
    │       ├── handler.go           # JSON-RPC method handlers (agent.*, conversation.*)
    │       └── types.go             # JSON-RPC request/response/event types
    ├── go.mod
    └── go.sum
```

---

## Task 1: Go Module + Config

**Files:**
- Create: `agentd/go.mod`
- Create: `agentd/internal/config/config.go`
- Create: `agentd/internal/config/config_test.go`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
mkdir -p agentd/cmd/agentd agentd/internal/{config,agent,pty,watcher,eventbuf,store,ws}
cd agentd
go mod init github.com/phone-talk/agentd
go get github.com/gorilla/websocket@v1.5.3
go get github.com/creack/pty@v1.1.21
go get github.com/mattn/go-sqlite3@v1.14.22
go get gopkg.in/yaml.v3@v3.0.1
```

Expected: `go.mod` and `go.sum` created.

- [ ] **Step 2: Write config test**

Create `agentd/internal/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentd/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	cfg, err := config.Load(filepath.Join(tmp, "config.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 7373 {
		t.Errorf("expected port 7373, got %d", cfg.Port)
	}
	if cfg.Token == "" {
		t.Error("expected non-empty default token")
	}
	if cfg.DataDir == "" {
		t.Error("expected non-empty data dir")
	}
}

func TestLoadFromFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	content := `port: 9999
token: "mytoken"
data_dir: "/tmp/agentd-data"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("expected port 9999, got %d", cfg.Port)
	}
	if cfg.Token != "mytoken" {
		t.Errorf("expected token 'mytoken', got %q", cfg.Token)
	}
}
```

- [ ] **Step 3: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/config/... -v
```

Expected: compile error — package not found.

- [ ] **Step 4: Implement config**

Create `agentd/internal/config/config.go`:

```go
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port    int    `yaml:"port"`
	Token   string `yaml:"token"`
	DataDir string `yaml:"data_dir"`
}

// Load reads config from path. If the file doesn't exist, returns defaults and
// writes the defaults to path so the user can edit it.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Port:    7373,
		DataDir: filepath.Join(os.Getenv("HOME"), ".agentd", "data"),
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg.Token = randomToken()
		if err2 := os.MkdirAll(filepath.Dir(path), 0700); err2 != nil {
			return nil, fmt.Errorf("mkdir config dir: %w", err2)
		}
		out, _ := yaml.Marshal(cfg)
		_ = os.WriteFile(path, out, 0600)
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Token == "" {
		cfg.Token = randomToken()
	}
	return cfg, nil
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 5: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/config/... -v
```

Expected:
```
--- PASS: TestLoadDefaults (0.00s)
--- PASS: TestLoadFromFile (0.00s)
PASS
```

- [ ] **Step 6: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/
git commit -m "feat(agentd): initialize go module and config loader"
```

---

## Task 2: EventBuffer

**Files:**
- Create: `agentd/internal/eventbuf/eventbuf.go`
- Create: `agentd/internal/eventbuf/eventbuf_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/eventbuf/eventbuf_test.go`:

```go
package eventbuf_test

import (
	"testing"

	"github.com/phone-talk/agentd/internal/eventbuf"
)

func TestAppendAndSince(t *testing.T) {
	buf := eventbuf.New(100)

	buf.Append(map[string]any{"type": "a"})
	buf.Append(map[string]any{"type": "b"})
	buf.Append(map[string]any{"type": "c"})

	events := buf.Since(0)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Seq != 1 {
		t.Errorf("expected seq 1, got %d", events[0].Seq)
	}
	if events[2].Seq != 3 {
		t.Errorf("expected seq 3, got %d", events[2].Seq)
	}

	// Since(2) should return only event 3
	partial := buf.Since(2)
	if len(partial) != 1 {
		t.Fatalf("expected 1 event after seq 2, got %d", len(partial))
	}
	if partial[0].Seq != 3 {
		t.Errorf("expected seq 3, got %d", partial[0].Seq)
	}
}

func TestCapEviction(t *testing.T) {
	buf := eventbuf.New(3)
	for i := 0; i < 5; i++ {
		buf.Append(map[string]any{"i": i})
	}
	// Only last 3 should be retained
	events := buf.Since(0)
	if len(events) != 3 {
		t.Fatalf("expected 3 events after eviction, got %d", len(events))
	}
	if events[0].Seq != 3 {
		t.Errorf("expected oldest retained seq=3, got %d", events[0].Seq)
	}
}

func TestSinceReturnsEmpty(t *testing.T) {
	buf := eventbuf.New(100)
	buf.Append(map[string]any{"type": "x"})
	events := buf.Since(1) // already have seq 1, nothing newer
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/eventbuf/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement EventBuffer**

Create `agentd/internal/eventbuf/eventbuf.go`:

```go
package eventbuf

import "sync"

// Event is a single buffered event with a monotonic sequence number.
type Event struct {
	Seq  uint64         `json:"seq"`
	Data map[string]any `json:"data"`
}

// EventBuffer is a capped circular buffer of Events, safe for concurrent use.
// Uses head/tail indices for O(1) append instead of O(n) shift.
type EventBuffer struct {
	mu    sync.Mutex
	cap   int
	seq   uint64
	buf   []Event
	head  int // index of oldest element
	count int // number of elements currently in buffer
}

// New creates an EventBuffer with the given capacity.
func New(cap int) *EventBuffer {
	return &EventBuffer{cap: cap, buf: make([]Event, cap)}
}

// Append adds data to the buffer, evicting the oldest entry if at capacity. O(1).
func (eb *EventBuffer) Append(data map[string]any) uint64 {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.seq++
	e := Event{Seq: eb.seq, Data: data}
	if eb.count < eb.cap {
		// Buffer not yet full: write at head+count
		eb.buf[(eb.head+eb.count)%eb.cap] = e
		eb.count++
	} else {
		// Buffer full: overwrite oldest at head, advance head
		eb.buf[eb.head] = e
		eb.head = (eb.head + 1) % eb.cap
	}
	return eb.seq
}

// Since returns all events with Seq > afterSeq, in order.
func (eb *EventBuffer) Since(afterSeq uint64) []Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	var result []Event
	for i := 0; i < eb.count; i++ {
		e := eb.buf[(eb.head+i)%eb.cap]
		if e.Seq > afterSeq {
			result = append(result, e)
		}
	}
	return result
}

// LastSeq returns the highest sequence number appended so far.
func (eb *EventBuffer) LastSeq() uint64 {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	return eb.seq
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/eventbuf/... -v
```

Expected:
```
--- PASS: TestAppendAndSince (0.00s)
--- PASS: TestCapEviction (0.00s)
--- PASS: TestSinceReturnsEmpty (0.00s)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/internal/eventbuf/
git commit -m "feat(agentd): add EventBuffer with cap eviction and Since replay"
```

---

## Task 3: PTY Wrapper

**Files:**
- Create: `agentd/internal/pty/pty.go`
- Create: `agentd/internal/pty/pty_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/pty/pty_test.go`:

```go
package pty_test

import (
	"strings"
	"testing"
	"time"

	agentpty "github.com/phone-talk/agentd/internal/pty"
)

func TestSpawnAndRead(t *testing.T) {
	p, err := agentpty.Spawn("echo", []string{"hello agentd"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	defer p.Kill()

	// Collect output for up to 2 seconds
	out := collectOutput(p, 2*time.Second)
	if !strings.Contains(out, "hello agentd") {
		t.Errorf("expected 'hello agentd' in output, got: %q", out)
	}
}

func TestKill(t *testing.T) {
	p, err := agentpty.Spawn("sleep", []string{"60"}, "/tmp", nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Errorf("Kill failed: %v", err)
	}
	// Wait should return (process ended)
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Wait did not return after Kill")
	}
}

func collectOutput(p *agentpty.Process, timeout time.Duration) string {
	var sb strings.Builder
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		p.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := p.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/pty/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement PTY wrapper**

Create `agentd/internal/pty/pty.go`:

```go
package pty

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

// Process wraps a PTY-attached child process.
type Process struct {
	cmd  *exec.Cmd
	ptmx *os.File
}

// Spawn starts cmd with args in workDir, attached to a PTY. env is merged with os.Environ().
func Spawn(cmd string, args []string, workDir string, env []string) (*Process, error) {
	c := exec.Command(cmd, args...)
	c.Dir = workDir
	c.Env = append(os.Environ(), env...)

	ptmx, err := pty.Start(c)
	if err != nil {
		return nil, fmt.Errorf("pty.Start: %w", err)
	}
	return &Process{cmd: c, ptmx: ptmx}, nil
}

// Read reads raw PTY output bytes.
func (p *Process) Read(buf []byte) (int, error) {
	return p.ptmx.Read(buf)
}

// Write sends input bytes to the PTY (as if typed by the user).
func (p *Process) Write(data []byte) (int, error) {
	return p.ptmx.Write(data)
}

// SetReadDeadline sets a deadline on the underlying PTY file.
func (p *Process) SetReadDeadline(t time.Time) {
	_ = p.ptmx.SetReadDeadline(t)
}

// Kill sends SIGKILL to the child process and closes the PTY.
// Process is killed first to avoid SIGHUP-induced zombie from closing ptmx early.
func (p *Process) Kill() error {
	var err error
	if p.cmd.Process != nil {
		err = p.cmd.Process.Kill()
	}
	_ = p.ptmx.Close()
	return err
}

// Wait waits for the child process to exit.
func (p *Process) Wait() error {
	return p.cmd.Wait()
}

// Pid returns the child process PID.
func (p *Process) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/pty/... -v -timeout 15s
```

Expected:
```
--- PASS: TestSpawnAndRead (0.xx s)
--- PASS: TestKill (0.xx s)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/internal/pty/
git commit -m "feat(agentd): add PTY process wrapper"
```

---

## Task 4: Claude JSONL Watcher

**Files:**
- Create: `agentd/internal/watcher/claude.go`
- Create: `agentd/internal/watcher/claude_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/watcher/claude_test.go`:

```go
package watcher_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/watcher"
)

func TestClaudeWatcherDetectsMessages(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "abc123.jsonl")

	// Pre-write a user message line
	line1 := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line1), 0644); err != nil {
		t.Fatal(err)
	}

	events := make(chan watcher.ConversationEvent, 10)
	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {
		events <- e
	})

	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Append an assistant message
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

	statuses := make(chan watcher.AgentStatus, 10)
	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {
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

	got := collectStatuses(statuses, 1, 2*time.Second)
	if len(got) == 0 {
		t.Fatal("expected a status change event")
	}
	if got[0] != watcher.StatusWorking {
		t.Errorf("expected StatusWorking, got %v", got[0])
	}
}

func collectEvents(ch <-chan watcher.ConversationEvent, count int, timeout time.Duration) []watcher.ConversationEvent {
	var out []watcher.ConversationEvent
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

func collectStatuses(ch <-chan watcher.AgentStatus, count int, timeout time.Duration) []watcher.AgentStatus {
	var out []watcher.AgentStatus
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
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/watcher/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement ClaudeWatcher**

Create `agentd/internal/watcher/claude.go`:

```go
package watcher

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

type AgentStatus string

const (
	StatusWorking AgentStatus = "working"
	StatusStandby AgentStatus = "standby"
)

// ConversationEvent represents a parsed line from the Claude JSONL session file.
type ConversationEvent struct {
	Role         string      // "user" or "assistant"
	Text         string      // combined text content
	StatusChange *AgentStatus // non-nil when this line changes agent status
}

// ClaudeWatcher tails a Claude Code JSONL session file and emits ConversationEvents.
type ClaudeWatcher struct {
	path     string
	callback func(ConversationEvent)
	stop     chan struct{}
	offset   int64
}

func NewClaudeWatcher(path string, callback func(ConversationEvent)) *ClaudeWatcher {
	return &ClaudeWatcher{path: path, callback: callback, stop: make(chan struct{})}
}

func (w *ClaudeWatcher) Start() error {
	// Parse existing content first
	if err := w.poll(); err != nil && !os.IsNotExist(err) {
		return err
	}
	go w.loop()
	return nil
}

func (w *ClaudeWatcher) Stop() {
	close(w.stop)
}

func (w *ClaudeWatcher) loop() {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			_ = w.poll()
		}
	}
}

func (w *ClaudeWatcher) poll() error {
	f, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(w.offset, 0); err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		w.offset += int64(len(line)) + 1 // +1 for newline
		if ev, ok := parseLine(line); ok {
			w.callback(ev)
		}
	}
	return scanner.Err()
}

// claudeLine is the minimal structure we need from Claude's JSONL output.
type claudeLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
	} `json:"message"`
}

func parseLine(data []byte) (ConversationEvent, bool) {
	var line claudeLine
	if err := json.Unmarshal(data, &line); err != nil {
		return ConversationEvent{}, false
	}
	if line.Type != "user" && line.Type != "assistant" {
		return ConversationEvent{}, false
	}

	ev := ConversationEvent{Role: line.Message.Role}
	hasToolUse := false
	isTextStop := false

	for _, c := range line.Message.Content {
		switch c.Type {
		case "text":
			ev.Text += c.Text
			isTextStop = true
		case "tool_use":
			hasToolUse = true
		}
	}

	// Status change detection (mirrors OpenCove's TurnStateWatcher logic)
	if line.Type == "assistant" {
		if hasToolUse {
			s := StatusWorking
			ev.StatusChange = &s
		} else if isTextStop {
			s := StatusStandby
			ev.StatusChange = &s
		}
	}

	return ev, true
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/watcher/... -v -timeout 15s
```

Expected:
```
--- PASS: TestClaudeWatcherDetectsMessages (x.xxs)
--- PASS: TestClaudeWatcherDetectsWorking (x.xxs)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/internal/watcher/
git commit -m "feat(agentd): add Claude JSONL watcher with status detection"
```

---

## Task 5: SQLite Store

**Files:**
- Create: `agentd/internal/store/store.go`
- Create: `agentd/internal/store/store_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/store/store_test.go`:

```go
package store_test

import (
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentd/internal/store"
)

func TestSaveAndLoad(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ag := store.AgentRecord{
		ID:              "agent-1",
		Name:            "my claude",
		Provider:        "claude-code",
		WorkDir:         "/tmp/proj",
		ResumeSessionID: "",
	}
	if err := s.SaveAgent(ag); err != nil {
		t.Fatalf("SaveAgent failed: %v", err)
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != "agent-1" {
		t.Errorf("expected id=agent-1, got %q", agents[0].ID)
	}
}

func TestUpdateResumeSessionID(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ag := store.AgentRecord{ID: "agent-2", Name: "x", Provider: "claude-code", WorkDir: "/tmp"}
	s.SaveAgent(ag)

	if err := s.UpdateResumeSessionID("agent-2", "sess-abc"); err != nil {
		t.Fatalf("UpdateResumeSessionID failed: %v", err)
	}

	agents, _ := s.ListAgents()
	if agents[0].ResumeSessionID != "sess-abc" {
		t.Errorf("expected sess-abc, got %q", agents[0].ResumeSessionID)
	}
}

func TestDeleteAgent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.SaveAgent(store.AgentRecord{ID: "del-1", Name: "x", Provider: "claude-code", WorkDir: "/tmp"})
	s.DeleteAgent("del-1")

	agents, _ := s.ListAgents()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after delete, got %d", len(agents))
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/store/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement store**

Create `agentd/internal/store/store.go`:

```go
package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// AgentRecord is a persisted agent entry.
type AgentRecord struct {
	ID              string
	Name            string
	Provider        string
	WorkDir         string
	ResumeSessionID string
}

// Store wraps a SQLite database for agent metadata.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agents (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		provider TEXT NOT NULL,
		work_dir TEXT NOT NULL,
		resume_session_id TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveAgent(r AgentRecord) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO agents (id, name, provider, work_dir, resume_session_id) VALUES (?,?,?,?,?)`,
		r.ID, r.Name, r.Provider, r.WorkDir, r.ResumeSessionID,
	)
	return err
}

func (s *Store) ListAgents() ([]AgentRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, provider, work_dir, resume_session_id FROM agents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRecord
	for rows.Next() {
		var r AgentRecord
		if err := rows.Scan(&r.ID, &r.Name, &r.Provider, &r.WorkDir, &r.ResumeSessionID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateResumeSessionID(id, sessionID string) error {
	_, err := s.db.Exec(`UPDATE agents SET resume_session_id=? WHERE id=?`, sessionID, id)
	return err
}

func (s *Store) DeleteAgent(id string) error {
	_, err := s.db.Exec(`DELETE FROM agents WHERE id=?`, id)
	return err
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/store/... -v
```

Expected:
```
--- PASS: TestSaveAndLoad (0.00s)
--- PASS: TestUpdateResumeSessionID (0.00s)
--- PASS: TestDeleteAgent (0.00s)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/internal/store/
git commit -m "feat(agentd): add SQLite store for agent metadata"
```

---

## Task 6: Agent + Provider

**Files:**
- Create: `agentd/internal/agent/provider.go`
- Create: `agentd/internal/agent/agent.go`
- Create: `agentd/internal/agent/manager.go`
- Create: `agentd/internal/agent/manager_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/agent/manager_test.go`:

```go
package agent_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/store"
)

func newTestManager(t *testing.T) *agent.Manager {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return agent.NewManager(s, t.TempDir())
}

func TestCreateAndListAgent(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Create("test-agent", "echo", []string{"hello"}, t.TempDir())
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
	id, err := m.Create("echo-agent", "echo", []string{"hello"}, t.TempDir())
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
	id, _ := m.Create("sleep-agent", "sleep", []string{"60"}, t.TempDir())
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

func TestEventBufferExists(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Create("buf-agent", "echo", []string{"x"}, t.TempDir())
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
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/agent/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement provider.go**

Create `agentd/internal/agent/provider.go`:

```go
package agent

// Provider describes how to launch and interact with a specific AI agent CLI.
type Provider interface {
	// Command returns the executable and args to launch the agent.
	Command(workDir string, resumeSessionID string) (cmd string, args []string)
	// SessionFilePath returns the path to the JSONL session file for the given workDir,
	// or "" if this provider doesn't use JSONL files.
	SessionFilePath(workDir string, sessionID string) string
}

// EchoProvider is a test provider that just runs a given command (used in tests).
type EchoProvider struct {
	Cmd  string
	Args []string
}

func (e *EchoProvider) Command(_ string, _ string) (string, []string) {
	return e.Cmd, e.Args
}

func (e *EchoProvider) SessionFilePath(_ string, _ string) string { return "" }

// ClaudeCodeProvider launches `claude` and watches JSONL files.
type ClaudeCodeProvider struct{}

func (c *ClaudeCodeProvider) Command(workDir string, resumeSessionID string) (string, []string) {
	args := []string{"--dangerously-skip-permissions"}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	return "claude", args
}

func (c *ClaudeCodeProvider) SessionFilePath(workDir string, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	// Claude stores sessions at ~/.claude/projects/<escaped-cwd>/<sessionID>.jsonl
	// We watch the directory; the manager discovers the file after session starts.
	return ""
}
```

- [ ] **Step 4: Implement agent.go**

Create `agentd/internal/agent/agent.go`:

```go
package agent

import (
	"fmt"
	"sync"

	"github.com/phone-talk/agentd/internal/eventbuf"
	agentpty "github.com/phone-talk/agentd/internal/pty"
)

type Status string

const (
	StatusCreated  Status = "created"
	StatusStarting Status = "starting"
	StatusIdle     Status = "idle"
	StatusWorking  Status = "working"
	StatusStopped  Status = "stopped"
	StatusCrashed  Status = "crashed"
)

// Agent represents a single managed AI agent process.
type Agent struct {
	ID       string
	Name     string
	Provider string
	WorkDir  string
	Cmd      string   // original command used to spawn this agent
	Args     []string // original args used to spawn this agent

	mu      sync.RWMutex
	status  Status
	process *agentpty.Process
	buf     *eventbuf.EventBuffer
}

func newAgent(id, name, provider, workDir, cmd string, args []string) *Agent {
	return &Agent{
		ID:       id,
		Name:     name,
		Provider: provider,
		WorkDir:  workDir,
		Cmd:      cmd,
		Args:     args,
		status:   StatusCreated,
		buf:      eventbuf.New(10000),
	}
}

func (a *Agent) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Agent) setStatus(s Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = s
}

// Buffer returns the EventBuffer for this agent (typed as interface{} to avoid import cycle in tests).
func (a *Agent) Buffer() interface{} {
	return a.buf
}

// EventBuf returns the typed EventBuffer.
func (a *Agent) EventBuf() *eventbuf.EventBuffer {
	return a.buf
}

func (a *Agent) setProcess(p *agentpty.Process) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.process = p
}

func (a *Agent) kill() {
	a.mu.RLock()
	p := a.process
	a.mu.RUnlock()
	if p != nil {
		_ = p.Kill()
	}
}

// WriteInput sends text to the agent's PTY stdin (as if typed by the user).
func (a *Agent) WriteInput(text string) error {
	a.mu.RLock()
	p := a.process
	a.mu.RUnlock()
	if p == nil {
		return fmt.Errorf("agent process not running")
	}
	_, err := p.Write([]byte(text))
	return err
}
```

- [ ] **Step 5: Implement manager.go**

Create `agentd/internal/agent/manager.go`:

```go
package agent

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/store"
)

// Manager creates, tracks, and controls Agent instances.
type Manager struct {
	mu      sync.RWMutex
	agents  map[string]*Agent
	store   *store.Store
	dataDir string
}

func NewManager(s *store.Store, dataDir string) *Manager {
	return &Manager{
		agents:  make(map[string]*Agent),
		store:   s,
		dataDir: dataDir,
	}
}

// Create spawns a new agent process using the given command/args (provider-resolved by caller).
func (m *Manager) Create(name, cmd string, args []string, workDir string) (string, error) {
	id := uuid.New().String()
	ag := newAgent(id, name, "custom", workDir, cmd, args)

	m.mu.Lock()
	m.agents[id] = ag
	m.mu.Unlock()

	// Persist metadata
	_ = m.store.SaveAgent(store.AgentRecord{
		ID: id, Name: name, Provider: "custom", WorkDir: workDir,
	})

	ag.setStatus(StatusStarting)

	p, err := agentpty.Spawn(cmd, args, workDir, nil)
	if err != nil {
		ag.setStatus(StatusCrashed)
		return id, fmt.Errorf("spawn: %w", err)
	}
	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	// Watch for process exit
	go func() {
		_ = p.Wait()
		ag.setStatus(StatusStopped)
	}()

	return id, nil
}

// List returns a snapshot of all agents.
func (m *Manager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Agent, 0, len(m.agents))
	for _, ag := range m.agents {
		out = append(out, ag)
	}
	return out
}

// Get returns an agent by ID or nil.
func (m *Manager) Get(id string) *Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agents[id]
}

// Stop kills the agent process.
func (m *Manager) Stop(id string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}
	ag.kill()
	ag.setStatus(StatusStopped)
	return nil
}

// Remove stops and removes the agent from tracking.
func (m *Manager) Remove(id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.agents, id)
	m.mu.Unlock()
	return m.store.DeleteAgent(id)
}
```

- [ ] **Step 6: Add uuid dependency**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go get github.com/google/uuid@v1.6.0
```

- [ ] **Step 7: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/agent/... -v -timeout 15s
```

Expected:
```
--- PASS: TestCreateAndListAgent (0.xx s)
--- PASS: TestAgentStatusTransition (0.xx s)
--- PASS: TestStopAgent (0.xx s)
--- PASS: TestEventBufferExists (0.xx s)
PASS
```

- [ ] **Step 8: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/internal/agent/
git commit -m "feat(agentd): add Agent, Manager, and Provider abstractions"
```

---

## Task 7: WebSocket Server (JSON-RPC types + auth)

**Files:**
- Create: `agentd/internal/ws/types.go`
- Create: `agentd/internal/ws/server.go`
- Create: `agentd/internal/ws/handler.go`
- Create: `agentd/internal/ws/server_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/ws/server_test.go`:

```go
package ws_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/ws"
)

func newTestServer(t *testing.T) (*httptest.Server, *ws.Server) {
	t.Helper()
	s, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	mgr := agent.NewManager(s, t.TempDir())
	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); s.Close() })
	return ts, srv
}

func dialWS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func rpc(conn *websocket.Conn, method string, params any) map[string]any {
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	_ = conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp map[string]any
	_ = conn.ReadJSON(&resp)
	return resp
}

func TestAuthRejectsInvalidToken(t *testing.T) {
	ts, _ := newTestServer(t)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer wrongtoken"}}
	_, resp, _ := websocket.DefaultDialer.Dial(u, header)
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got: %v", resp)
	}
}

func TestAgentList(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.list", nil)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result := resp["result"]
	b, _ := json.Marshal(result)
	var agents []any
	json.Unmarshal(b, &agents)
	if agents == nil {
		// empty list is also valid
		agents = []any{}
	}
	_ = agents // just checking no error
}

func TestAgentCreateAndList(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.create", map[string]any{
		"name":    "test",
		"cmd":     "echo",
		"args":    []string{"hello"},
		"workDir": t.TempDir(),
	})
	if resp["error"] != nil {
		t.Fatalf("create error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp["result"])
	}
	id, ok := result["id"].(string)
	if !ok || id == "" {
		t.Errorf("expected non-empty id in result")
	}

	// List should now have 1 agent
	listResp := rpc(conn, "agent.list", nil)
	b, _ := json.Marshal(listResp["result"])
	var agents []map[string]any
	json.Unmarshal(b, &agents)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

func TestConversationSend(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	// Create a long-running agent (sleep) so we can send to it
	workDir := t.TempDir()
	createResp := rpc(conn, "agent.create", map[string]any{
		"name": "cat-agent", "cmd": "cat", "args": []string{},
		"workDir": workDir,
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}
	result := createResp["result"].(map[string]any)
	agentID := result["id"].(string)

	// Send a message
	sendResp := rpc(conn, "conversation.send", map[string]any{
		"agentId": agentID,
		"message": "hello agent",
	})
	if sendResp["error"] != nil {
		t.Fatalf("send error: %v", sendResp["error"])
	}

	// Send to non-existent agent should fail
	errResp := rpc(conn, "conversation.send", map[string]any{
		"agentId": "nonexistent",
		"message": "hello",
	})
	if errResp["error"] == nil {
		t.Error("expected error for non-existent agent")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/ws/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement types.go**

Create `agentd/internal/ws/types.go`:

```go
package ws

// RPCRequest is an incoming JSON-RPC 2.0 request.
type RPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// RPCResponse is an outgoing JSON-RPC 2.0 response.
type RPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCEvent is a server-pushed event (no ID, no result/error).
type RPCEvent struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

func okResp(id any, result any) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id any, code int, msg string) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func event(method string, params any) RPCEvent {
	return RPCEvent{JSONRPC: "2.0", Method: method, Params: params}
}
```

- [ ] **Step 4: Implement server.go**

Create `agentd/internal/ws/server.go`:

```go
package ws

import (
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server is the WebSocket HTTP handler.
type Server struct {
	manager *agent.Manager
	token   string

	mu      sync.RWMutex
	clients map[*websocket.Conn]struct{}
}

func New(mgr *agent.Manager, token string) *Server {
	return &Server{
		manager: mgr,
		token:   token,
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth: accept either Authorization header or ?token= query param
	// (Flutter mobile clients can't set custom WS headers)
	auth := r.Header.Get("Authorization")
	queryToken := r.URL.Query().Get("token")
	headerToken := strings.TrimPrefix(auth, "Bearer ")
	token := headerToken
	if token == "" {
		token = queryToken
	}
	if token != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	s.mu.Lock()
	s.clients[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	h := &handler{server: s, conn: conn}
	h.loop()
}

// broadcast sends an event to all connected clients.
func (s *Server) broadcast(ev RPCEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn := range s.clients {
		_ = conn.WriteJSON(ev)
	}
}
```

- [ ] **Step 5: Implement handler.go**

Create `agentd/internal/ws/handler.go`:

```go
package ws

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

type handler struct {
	server *Server
	conn   *websocket.Conn
}

func (h *handler) loop() {
	for {
		_, msg, err := h.conn.ReadMessage()
		if err != nil {
			return
		}
		var req RPCRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = h.conn.WriteJSON(errResp(nil, -32700, "parse error"))
			continue
		}
		resp := h.dispatch(req)
		if err := h.conn.WriteJSON(resp); err != nil {
			log.Printf("ws write: %v", err)
			return
		}
	}
}

func (h *handler) dispatch(req RPCRequest) RPCResponse {
	switch req.Method {
	case "agent.list":
		return h.agentList(req)
	case "agent.create":
		return h.agentCreate(req)
	case "agent.stop":
		return h.agentStop(req)
	case "agent.restart":
		return h.agentRestart(req)
	case "conversation.send":
		return h.conversationSend(req)
	case "conversation.history":
		return h.conversationHistory(req)
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (h *handler) agentList(req RPCRequest) RPCResponse {
	agents := h.server.manager.List()
	type agentInfo struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		WorkDir  string `json:"workDir"`
		Status   string `json:"status"`
	}
	result := make([]agentInfo, 0, len(agents))
	for _, ag := range agents {
		result = append(result, agentInfo{
			ID: ag.ID, Name: ag.Name, Provider: ag.Provider,
			WorkDir: ag.WorkDir, Status: string(ag.Status()),
		})
	}
	return okResp(req.ID, result)
}

func (h *handler) agentCreate(req RPCRequest) RPCResponse {
	name, _ := req.Params["name"].(string)
	cmd, _ := req.Params["cmd"].(string)
	workDir, _ := req.Params["workDir"].(string)

	var args []string
	if raw, ok := req.Params["args"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &args)
	}

	if cmd == "" {
		cmd = "claude"
		args = []string{"--dangerously-skip-permissions"}
	}
	if workDir == "" {
		workDir = "/tmp"
	}

	id, err := h.server.manager.Create(name, cmd, args, workDir)
	if err != nil {
		return errResp(req.ID, -32000, err.Error())
	}

	// Broadcast status event
	h.server.broadcast(event("agent.status_changed", map[string]any{
		"agentId": id, "status": "idle",
	}))

	return okResp(req.ID, map[string]any{"id": id})
}

func (h *handler) agentStop(req RPCRequest) RPCResponse {
	id, _ := req.Params["agentId"].(string)
	if err := h.server.manager.Stop(id); err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	h.server.broadcast(event("agent.status_changed", map[string]any{
		"agentId": id, "status": "stopped",
	}))
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) agentRestart(req RPCRequest) RPCResponse {
	id, _ := req.Params["agentId"].(string)
	ag := h.server.manager.Get(id)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	// Stop then recreate with same params (uses stored cmd/args)
	_ = h.server.manager.Stop(id)
	newID, err := h.server.manager.Create(ag.Name, ag.Cmd, ag.Args, ag.WorkDir)
	if err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	return okResp(req.ID, map[string]any{"id": newID})
}

func (h *handler) conversationSend(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	message, _ := req.Params["message"].(string)
	if message == "" {
		return errResp(req.ID, -32602, "message is required")
	}
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	if err := ag.WriteInput(message + "\n"); err != nil {
		return errResp(req.ID, -32000, "write to agent: "+err.Error())
	}
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) conversationHistory(req RPCRequest) RPCResponse {
	agentID, _ := req.Params["agentId"].(string)
	var afterSeq uint64
	if v, ok := req.Params["cursor"].(float64); ok {
		afterSeq = uint64(v)
	}
	ag := h.server.manager.Get(agentID)
	if ag == nil {
		return errResp(req.ID, -32000, "agent not found")
	}
	events := ag.EventBuf().Since(afterSeq)
	return okResp(req.ID, map[string]any{
		"events":  events,
		"lastSeq": ag.EventBuf().LastSeq(),
	})
}
```

- [ ] **Step 6: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/ws/... -v -timeout 15s
```

Expected:
```
--- PASS: TestAuthRejectsInvalidToken (0.xx s)
--- PASS: TestAgentList (0.xx s)
--- PASS: TestAgentCreateAndList (0.xx s)
PASS
```

- [ ] **Step 7: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/internal/ws/
git commit -m "feat(agentd): add WebSocket JSON-RPC server with auth"
```

---

## Task 8: CLI Entrypoint + Integration Test

**Files:**
- Create: `agentd/cmd/agentd/main.go`
- Create: `agentd/integration_test.go`

- [ ] **Step 1: Implement main.go**

Create `agentd/cmd/agentd/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/config"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/ws"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: agentd <start|status|version>")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "start":
		runServer()
	case "version":
		fmt.Println("agentd v0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServer() {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".agentd", "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "agents.db")
	s, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer s.Close()

	mgr := agent.NewManager(s, cfg.DataDir)
	srv := ws.New(mgr, cfg.Token)

	addr := fmt.Sprintf(":%d", cfg.Port)
	http.Handle("/ws", srv)
	log.Printf("agentd listening on %s (token: %s...)", addr, cfg.Token[:8])
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
```

- [ ] **Step 2: Build and verify compilation**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go build ./cmd/agentd/
```

Expected: `agentd` binary produced with no errors.

- [ ] **Step 3: Write integration test**

Create `agentd/integration_test.go`:

```go
//go:build integration

package agentd_test

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/ws"
)

// Integration test: real server, real echo process, end-to-end JSON-RPC
func TestEndToEnd(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())
	srv := ws.New(mgr, "e2etoken")

	ts := httptest.NewServer(srv)
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u, map[string][]string{
		"Authorization": {"Bearer e2etoken"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Create an echo agent
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"method": "agent.create",
		"params": map[string]any{
			"name": "e2e-echo", "cmd": "echo",
			"args": []string{"integration test ok"}, "workDir": t.TempDir(),
		},
	}
	conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var resp map[string]any
	conn.ReadJSON(&resp)
	if resp["error"] != nil {
		t.Fatalf("create error: %v", resp["error"])
	}

	// List and verify
	conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "agent.list", "params": nil})
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var listResp map[string]any
	conn.ReadJSON(&listResp)
	t.Logf("agent.list response: %v", listResp)
}
```

- [ ] **Step 4: Run unit tests (all packages)**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./... -v -timeout 30s
```

Expected: all unit tests PASS (integration test is gated by build tag, not run).

- [ ] **Step 5: Run integration test**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test -tags integration ./... -v -timeout 30s
```

Expected: `TestEndToEnd` PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/cmd/ agentd/integration_test.go
git commit -m "feat(agentd): add CLI entrypoint and integration test"
```

---

## Task 9: Wire Claude Watcher into Agent Lifecycle

**Files:**
- Modify: `agentd/internal/agent/manager.go`
- Modify: `agentd/internal/agent/agent.go`
- Create: `agentd/internal/agent/claude_integration_test.go`

- [ ] **Step 1: Write failing test**

Create `agentd/internal/agent/claude_integration_test.go`:

```go
//go:build integration

package agent_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// Tests that when a JSONL file is written, the agent's EventBuffer gets populated.
func TestClaudeWatcherIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	s, _ := store.Open(filepath.Join(tmpDir, "t.db"))
	defer s.Close()
	mgr := agent.NewManager(s, tmpDir)

	sessionFile := filepath.Join(tmpDir, "session.jsonl")
	os.WriteFile(sessionFile, []byte{}, 0644)

	id, err := mgr.CreateWithWatcher("watcher-agent", "echo", []string{"x"}, tmpDir, sessionFile)
	if err != nil {
		t.Fatalf("CreateWithWatcher: %v", err)
	}

	// Write a message line to the session file
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done!"}]}}` + "\n"
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(line)
	f.Close()

	// Wait for watcher to pick it up
	time.Sleep(600 * time.Millisecond)

	ag := mgr.Get(id)
	events := ag.EventBuf().Since(0)
	if len(events) == 0 {
		t.Error("expected events in buffer after watcher write")
	}
	_ = watcher.StatusStandby // verify import
}
```

- [ ] **Step 2: Add `AppendEvent` and `setWatcher` to agent.go**

Edit `agentd/internal/agent/agent.go` — replace the entire file with this updated version that adds the `watcher` field, `setWatcher`, and `AppendEvent` methods:

```go
package agent

import (
	"fmt"
	"sync"

	"github.com/phone-talk/agentd/internal/eventbuf"
	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/watcher"
)

type Status string

const (
	StatusCreated  Status = "created"
	StatusStarting Status = "starting"
	StatusIdle     Status = "idle"
	StatusWorking  Status = "working"
	StatusStopped  Status = "stopped"
	StatusCrashed  Status = "crashed"
)

// Agent represents a single managed AI agent process.
type Agent struct {
	ID       string
	Name     string
	Provider string
	WorkDir  string
	Cmd      string   // original command used to spawn this agent
	Args     []string // original args used to spawn this agent

	mu      sync.RWMutex
	status  Status
	process *agentpty.Process
	buf     *eventbuf.EventBuffer
	w       *watcher.ClaudeWatcher
}

func newAgent(id, name, provider, workDir, cmd string, args []string) *Agent {
	return &Agent{
		ID:       id,
		Name:     name,
		Provider: provider,
		WorkDir:  workDir,
		Cmd:      cmd,
		Args:     args,
		status:   StatusCreated,
		buf:      eventbuf.New(10000),
	}
}

func (a *Agent) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Agent) setStatus(s Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = s
}

// Buffer returns the EventBuffer for this agent (typed as interface{} to avoid import cycle in tests).
func (a *Agent) Buffer() interface{} {
	return a.buf
}

// EventBuf returns the typed EventBuffer.
func (a *Agent) EventBuf() *eventbuf.EventBuffer {
	return a.buf
}

// AppendEvent adds a conversation event to this agent's EventBuffer.
func (a *Agent) AppendEvent(data map[string]any) {
	a.buf.Append(data)
}

func (a *Agent) setProcess(p *agentpty.Process) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.process = p
}

func (a *Agent) setWatcher(w *watcher.ClaudeWatcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.w = w
}

func (a *Agent) kill() {
	a.mu.RLock()
	p := a.process
	w := a.w
	a.mu.RUnlock()
	if w != nil {
		w.Stop()
	}
	if p != nil {
		_ = p.Kill()
	}
}

// WriteInput sends text to the agent's PTY stdin (as if typed by the user).
func (a *Agent) WriteInput(text string) error {
	a.mu.RLock()
	p := a.process
	a.mu.RUnlock()
	if p == nil {
		return fmt.Errorf("agent process not running")
	}
	_, err := p.Write([]byte(text))
	return err
}
```

- [ ] **Step 3: Add `CreateWithWatcher` to manager.go**

Edit `agentd/internal/agent/manager.go` — replace the entire file with this updated version:

```go
package agent

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// Manager creates, tracks, and controls Agent instances.
type Manager struct {
	mu      sync.RWMutex
	agents  map[string]*Agent
	store   *store.Store
	dataDir string
}

func NewManager(s *store.Store, dataDir string) *Manager {
	return &Manager{
		agents:  make(map[string]*Agent),
		store:   s,
		dataDir: dataDir,
	}
}

// Create spawns a new agent process using the given command/args (provider-resolved by caller).
func (m *Manager) Create(name, cmd string, args []string, workDir string) (string, error) {
	id := uuid.New().String()
	ag := newAgent(id, name, "custom", workDir, cmd, args)

	m.mu.Lock()
	m.agents[id] = ag
	m.mu.Unlock()

	// Persist metadata
	_ = m.store.SaveAgent(store.AgentRecord{
		ID: id, Name: name, Provider: "custom", WorkDir: workDir,
	})

	ag.setStatus(StatusStarting)

	p, err := agentpty.Spawn(cmd, args, workDir, nil)
	if err != nil {
		ag.setStatus(StatusCrashed)
		return id, fmt.Errorf("spawn: %w", err)
	}
	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	// Watch for process exit
	go func() {
		_ = p.Wait()
		ag.setStatus(StatusStopped)
	}()

	return id, nil
}

// CreateWithWatcher spawns an agent and starts a ClaudeWatcher on sessionFile.
// Use this for Claude Code agents where the JSONL session file path is known at launch.
func (m *Manager) CreateWithWatcher(name, cmd string, args []string, workDir, sessionFile string) (string, error) {
	id, err := m.Create(name, cmd, args, workDir)
	if err != nil {
		return id, err
	}
	ag := m.Get(id)
	if ag == nil {
		return id, nil
	}

	w := watcher.NewClaudeWatcher(sessionFile, func(e watcher.ConversationEvent) {
		data := map[string]any{
			"role": e.Role,
			"text": e.Text,
		}
		if e.StatusChange != nil {
			data["statusChange"] = string(*e.StatusChange)
			if *e.StatusChange == watcher.StatusWorking {
				ag.setStatus(StatusWorking)
			} else {
				ag.setStatus(StatusIdle)
			}
		}
		ag.AppendEvent(data)
	})

	if err := w.Start(); err != nil {
		return id, fmt.Errorf("watcher start: %w", err)
	}
	ag.setWatcher(w)
	return id, nil
}

// List returns a snapshot of all agents.
func (m *Manager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Agent, 0, len(m.agents))
	for _, ag := range m.agents {
		out = append(out, ag)
	}
	return out
}

// Get returns an agent by ID or nil.
func (m *Manager) Get(id string) *Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agents[id]
}

// Stop kills the agent process.
func (m *Manager) Stop(id string) error {
	ag := m.Get(id)
	if ag == nil {
		return fmt.Errorf("agent %q not found", id)
	}
	ag.kill()
	ag.setStatus(StatusStopped)
	return nil
}

// Remove stops and removes the agent from tracking.
func (m *Manager) Remove(id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.agents, id)
	m.mu.Unlock()
	return m.store.DeleteAgent(id)
}
```

- [ ] **Step 4: Run unit tests to verify no regressions**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./internal/agent/... -v -timeout 15s
```

Expected: all existing agent tests still PASS.

- [ ] **Step 5: Run integration test**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test -tags integration ./internal/agent/... -v -timeout 15s -run TestClaudeWatcherIntegration
```

Expected: PASS.

- [ ] **Step 6: Run all tests**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentd
go test ./... -v -timeout 30s
go test -tags integration ./... -v -timeout 30s
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentd/
git commit -m "feat(agentd): wire ClaudeWatcher into agent lifecycle for event buffering"
```

---

## Self-Review Notes

**Spec coverage check:**
- ✅ WebSocket API (agent.list, agent.create, agent.stop, agent.restart, conversation.history, conversation.send) — Task 7
- ✅ Agent state machine (Created→Starting→Idle⇄Working→Stopped→Crashed) — Task 6
- ✅ Claude JSONL watcher + TurnState detection — Tasks 4 & 9
- ✅ EventBuffer with replay (Since/lastSeq), O(1) circular buffer — Task 2
- ✅ PTY process management (correct kill order: process first, then ptmx) — Task 3
- ✅ SQLite persistence — Task 5
- ✅ Static token auth — Task 7
- ✅ Config file (token, port, data_dir) — Task 1
- ✅ CLI entrypoint (agentd start) — Task 8
- ✅ Agent stores cmd/args for restart — Task 6 (agent.go stores Cmd/Args, agentRestart uses them)

**Type consistency check:**
- `EventBuffer.Since(afterSeq uint64)` used consistently in eventbuf.go, agent.go, handler.go ✅
- `AgentStatus` (watcher package) vs `Status` (agent package) — distinct types, no collision ✅
- `AgentRecord` fields match between store.go and manager.go ✅
- `RPCRequest.Params` is `map[string]any` — handler.go accesses via type assertions ✅
- `Agent.Cmd/Args` stored at creation, reused by agentRestart ✅
