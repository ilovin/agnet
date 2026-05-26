package watcher

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// findHermesStateDBFunc allows tests to inject a custom DB locator.
var findHermesStateDBFunc = findHermesStateDB

// hermesDBWatcherInterval is the polling interval for HermesDBWatcher.
// Exposed as a var so tests can shorten it.
var hermesDBWatcherInterval = 3 * time.Second

// SetFindHermesStateDBForTest replaces the function used to locate the Hermes
// state.db. Returns a restore function callers should defer.
//
// This exists so integration tests in other packages (agent, ws) can inject a
// temporary sqlite DB. It is the only supported override path; production
// code must not rely on it.
func SetFindHermesStateDBForTest(fn func() string) (restore func()) {
	orig := findHermesStateDBFunc
	findHermesStateDBFunc = fn
	return func() { findHermesStateDBFunc = orig }
}

// SetHermesDBWatcherIntervalForTest shortens the polling interval used by
// HermesDBWatcher.loop. Returns a restore function callers should defer.
func SetHermesDBWatcherIntervalForTest(d time.Duration) (restore func()) {
	orig := hermesDBWatcherInterval
	hermesDBWatcherInterval = d
	return func() { hermesDBWatcherInterval = orig }
}

// HermesStateDBHistory loads all conversation events from the Hermes state.db
// for the most recently active session. Returns events, the session ID, and any error.
func HermesStateDBHistory() ([]ConversationEvent, string, error) {
	dbPath := findHermesStateDBFunc()
	if dbPath == "" {
		return nil, "", nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return nil, "", err
	}
	defer db.Close()

	// Find the session with the most recent message (Hermes reuses the same
	// session ID across daily resets, so ORDER BY started_at is wrong).
	var sessionID string
	err = db.QueryRow(`SELECT s.id FROM sessions s JOIN messages m ON s.id=m.session_id GROUP BY s.id ORDER BY MAX(m.timestamp) DESC LIMIT 1`).Scan(&sessionID)
	if err != nil {
		return nil, "", nil
	}

	// Load all messages for that session
	rows, err := db.Query(`SELECT role, content, timestamp FROM messages WHERE session_id=? ORDER BY timestamp ASC`, sessionID)
	if err != nil {
		return nil, sessionID, err
	}
	defer rows.Close()

	var events []ConversationEvent
	for rows.Next() {
		var role, content string
		var timestamp string
		if err := rows.Scan(&role, &content, &timestamp); err != nil {
			continue
		}
		events = append(events, ConversationEvent{
			Role: role,
			Text: content,
		})
	}

	log.Printf("[HermesDB] Loaded %d events for session %s", len(events), sessionID)
	return events, sessionID, nil
}

// HermesStateDBLoadSession loads all messages for a specific Hermes session ID.
// Returns an empty slice (no error) if the DB is missing or the session has
// no messages, so callers can use it without special-casing absence.
func HermesStateDBLoadSession(sessionID string) ([]ConversationEvent, error) {
	dbPath := findHermesStateDBFunc()
	if dbPath == "" {
		return nil, nil
	}
	if sessionID == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT role, content, timestamp FROM messages WHERE session_id=? ORDER BY timestamp ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ConversationEvent
	for rows.Next() {
		var role, content, timestamp string
		if err := rows.Scan(&role, &content, &timestamp); err != nil {
			continue
		}
		events = append(events, ConversationEvent{
			Role: role,
			Text: content,
		})
	}
	return events, nil
}

func findHermesStateDB() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".hermes", "state.db"),
	}
	// Also check common home directories for remote users
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates,
					filepath.Join("/home", e.Name(), ".hermes", "state.db"))
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// HermesDBWatcher polls the Hermes state.db on a fixed interval and emits
// ConversationEvents for new messages, plus an OnSessionSwitch callback when
// Hermes' "current" session (the one with the latest message timestamp) flips
// to a different ID — which is how Hermes signals a /clear-equivalent reset.
//
// Notes on scope (M1+M2):
//   - This watcher only detects switches; it does not itself perform any
//     manager-side bookkeeping (resume-id rewrite, history flush, etc.). The
//     onSwitch callback is injected by the caller (Manager) and is responsible
//     for that.
//   - We assume one Hermes process per host. Multi-Hermes is out of scope.
type HermesDBWatcher struct {
	dbPath  string
	agentID string // for logging only

	callback func(event ConversationEvent)
	onSwitch func(newSessionID string)

	// isSending, when set and returning true, suppresses OnSessionSwitch
	// firings: the caller (Manager) is currently driving a request and a
	// chunk.Done will arrive shortly with the authoritative session id, so
	// the watcher should not pre-empt it. New-message emission within the
	// same session is unaffected.
	isSending func() bool

	// pid is the OS pid of the hermes CLI process this watcher is attached
	// to. When > 0, each poll() probes it via probeProcessFunc; on first
	// detection of process death, onProcessDead fires (sync.Once) and the
	// poll loop exits. Plan §M4 §2.3.
	pid             int
	onProcessDead   func()
	processDeadOnce sync.Once

	stop chan struct{}
	once sync.Once

	mu           sync.Mutex
	sessionID    string
	lastTS       string // last emitted message timestamp (RFC3339-ish string from messages.timestamp)
	skipExisting bool
	seeded       bool // true once we've initialised lastTS for the current session
}

// NewHermesDBWatcher returns a HermesDBWatcher bound to the given initial
// session id. callback receives ConversationEvents for newly-appeared messages.
func NewHermesDBWatcher(agentID, initialSessionID string, callback func(ConversationEvent)) *HermesDBWatcher {
	return &HermesDBWatcher{
		dbPath:    findHermesStateDBFunc(),
		agentID:   agentID,
		sessionID: initialSessionID,
		callback:  callback,
		stop:      make(chan struct{}),
	}
}

// SetSkipExisting tells the watcher not to emit events that exist in the DB
// at the time Start runs — useful when the caller has already loaded history
// via HermesStateDBLoadSession and just wants live tail behaviour.
func (w *HermesDBWatcher) SetSkipExisting(skip bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.skipExisting = skip
}

// OnSessionSwitch registers a callback that fires when Hermes' active session
// (latest-message-timestamp) becomes different from the one this watcher was
// constructed with (or last told about via SetSessionID).
func (w *HermesDBWatcher) OnSessionSwitch(fn func(newSessionID string)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onSwitch = fn
}

// SetSendingChecker injects a predicate the watcher consults before firing
// OnSessionSwitch. While the predicate returns true, session-switch detection
// is suppressed (but in-session new-message emission still runs). This avoids
// races with chunk.Done in hermesSend (see plan §3.6).
//
// Pass nil to remove the checker.
func (w *HermesDBWatcher) SetSendingChecker(fn func() bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.isSending = fn
}

// probeProcessFunc reports whether the given pid is alive. Replaceable for
// tests via SetProbeProcessFuncForTest. Default uses syscall.Kill(pid, 0):
// returns nil if the process exists and we have permission to signal it,
// returns ESRCH if the process is gone. We only treat ESRCH (or any non-nil
// error) as "dead" — permission errors (EPERM) imply the process is alive.
var probeProcessFunc = defaultProbeProcess

func defaultProbeProcess(pid int) bool {
	if pid <= 0 {
		return true
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means process exists but we lack signal permission — still alive.
	if err == syscall.EPERM {
		return true
	}
	return false
}

// SetProbeProcessFuncForTest replaces probeProcessFunc for the duration of a
// test. Returns a restore function callers should defer.
func SetProbeProcessFuncForTest(fn func(pid int) bool) (restore func()) {
	orig := probeProcessFunc
	probeProcessFunc = fn
	return func() { probeProcessFunc = orig }
}

// SetPID records the OS pid of the hermes CLI process this watcher should
// monitor. When set (>0), each poll() probes the pid; on death, onProcessDead
// fires once and the loop exits. Plan §M4 §2.3.
func (w *HermesDBWatcher) SetPID(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pid = pid
}

// SetOnProcessDead registers a callback that fires at most once when the
// monitored hermes pid is detected as dead. Plan §M4 §2.3.
func (w *HermesDBWatcher) SetOnProcessDead(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onProcessDead = fn
}

// SetSessionID updates the session this watcher is tracking. Used after a
// chunk.Done that established a fresh session id; we reset lastTS to the
// latest message timestamp of the new session so the next poll doesn't
// re-emit chunk.Done's already-shipped messages.
func (w *HermesDBWatcher) SetSessionID(sessionID string) {
	w.mu.Lock()
	w.sessionID = sessionID
	w.lastTS = w.latestTimestampForSessionLocked(sessionID)
	w.seeded = true
	w.mu.Unlock()
}

// Start launches the polling goroutine. If no Hermes DB is found, Start is a
// no-op (returns nil) — same convention as OpenCodeDBWatcher.
func (w *HermesDBWatcher) Start() error {
	if w.dbPath == "" {
		return nil
	}
	go w.loop()
	return nil
}

// Stop signals the polling goroutine to exit. Safe to call multiple times.
func (w *HermesDBWatcher) Stop() {
	w.once.Do(func() {
		close(w.stop)
	})
}

func (w *HermesDBWatcher) loop() {
	ticker := time.NewTicker(hermesDBWatcherInterval)
	defer ticker.Stop()
	// Run an immediate first poll so skipExisting / lastTS seeding doesn't
	// have to wait a whole tick.
	if w.checkProcessAlive() {
		w.poll()
	} else {
		return
	}
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			if !w.checkProcessAlive() {
				return
			}
			w.poll()
		}
	}
}

// checkProcessAlive probes the monitored hermes pid (if any). Returns true if
// the process is alive (or no pid is set). On first observation of death, fires
// onProcessDead exactly once via sync.Once and returns false so the caller can
// exit the poll loop. Plan §M4 §2.3.
func (w *HermesDBWatcher) checkProcessAlive() bool {
	w.mu.Lock()
	pid := w.pid
	cb := w.onProcessDead
	w.mu.Unlock()
	if pid <= 0 {
		return true
	}
	if probeProcessFunc(pid) {
		return true
	}
	log.Printf("[HermesDB][agent=%s] hermes pid %d gone; firing onProcessDead and stopping watcher", w.agentID, pid)
	if cb != nil {
		w.processDeadOnce.Do(cb)
	}
	return false
}

// poll executes one polling cycle. Concurrency-safe: only the goroutine
// started by Start invokes it, but it shares state with public setters via
// w.mu.
func (w *HermesDBWatcher) poll() {
	db, err := sql.Open("sqlite", w.dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return
	}
	defer db.Close()

	var latestSessionID, latestTS string
	err = db.QueryRow(`SELECT s.id, MAX(m.timestamp)
		FROM sessions s JOIN messages m ON s.id=m.session_id
		GROUP BY s.id ORDER BY MAX(m.timestamp) DESC LIMIT 1`).Scan(&latestSessionID, &latestTS)
	if err != nil {
		return
	}

	w.mu.Lock()
	currentSession := w.sessionID
	skipExisting := w.skipExisting
	seeded := w.seeded
	lastTS := w.lastTS
	onSwitch := w.onSwitch
	isSending := w.isSending

	if currentSession == "" {
		// Watcher was constructed without an initial session id (e.g. agentd
		// LoadFromStore where ResumeSessionID was empty, or attach paths
		// where Hermes hasn't surfaced its session yet). Without a current
		// session, the latestSessionID!=currentSession branch below would
		// fire OnSessionSwitch on every single poll — wrong, because there
		// is no "previous" session to switch *from*. Instead, silently seed
		// to the latest session id observed in state.db and let the normal
		// in-session new-message branch take over from the next tick.
		// Production regression: without this seed, hermes replies after
		// agentd restart never reach EventBuf and the app sees no response.
		w.sessionID = latestSessionID
		w.lastTS = latestTS
		w.seeded = true
		w.mu.Unlock()
		_ = skipExisting
		return
	}

	if latestSessionID != currentSession {
		// Session switch detected. If a request is currently in flight,
		// suppress the switch — chunk.Done will follow with the authoritative
		// session id and call SetSessionID. We also do not fall through to the
		// in-session emit branch because lastTS belongs to the *old* session
		// and any rows for the new session would be wrongly attributed.
		w.mu.Unlock()
		if isSending != nil && isSending() {
			log.Printf("[HermesDB][agent=%s] session switch %s->%s suppressed: send in flight",
				w.agentID, currentSession, latestSessionID)
			return
		}
		if onSwitch != nil {
			onSwitch(latestSessionID)
		}
		return
	}

	// Same session. Seed lastTS on first poll so we don't emit historical
	// content that the caller already loaded via HermesStateDBLoadSession.
	if !seeded {
		// Always seed to current latest, regardless of skipExisting — when
		// skipExisting is false we still expect the caller to have flushed
		// history; the live-emit guard is "timestamp > lastTS".
		w.lastTS = latestTS
		w.seeded = true
		w.mu.Unlock()
		_ = skipExisting // currently both branches behave the same; kept for clarity
		return
	}

	if latestTS <= lastTS {
		// No new messages.
		w.mu.Unlock()
		return
	}

	// New messages exist for the current session. Load them and emit those
	// strictly newer than lastTS.
	w.mu.Unlock()

	rows, err := db.Query(`SELECT role, content, timestamp FROM messages
		WHERE session_id=? AND timestamp>?
		ORDER BY timestamp ASC`, currentSession, lastTS)
	if err != nil {
		return
	}
	defer rows.Close()

	var newEvents []ConversationEvent
	var newLast string
	for rows.Next() {
		var role, content, ts string
		if err := rows.Scan(&role, &content, &ts); err != nil {
			continue
		}
		newEvents = append(newEvents, ConversationEvent{
			Role: role,
			Text: content,
		})
		newLast = ts
	}

	if len(newEvents) == 0 {
		return
	}

	for _, ev := range newEvents {
		if w.callback != nil {
			w.callback(ev)
		}
	}

	w.mu.Lock()
	// Only advance lastTS if the session hasn't been swapped out from
	// underneath us during the emit loop.
	if w.sessionID == currentSession && newLast > w.lastTS {
		w.lastTS = newLast
	}
	w.mu.Unlock()
}

// latestTimestampForSessionLocked must be called with w.mu held. It opens a
// short-lived connection to the DB and returns the latest message timestamp
// for the given session, or "" on error / no rows.
func (w *HermesDBWatcher) latestTimestampForSessionLocked(sessionID string) string {
	if w.dbPath == "" || sessionID == "" {
		return ""
	}
	db, err := sql.Open("sqlite", w.dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return ""
	}
	defer db.Close()
	var ts sql.NullString
	if err := db.QueryRow(`SELECT MAX(timestamp) FROM messages WHERE session_id=?`, sessionID).Scan(&ts); err != nil {
		return ""
	}
	if !ts.Valid {
		return ""
	}
	return ts.String
}
