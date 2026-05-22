package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	agentpty "github.com/phone-talk/agentd/internal/pty"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/watcher"
)

// ProcessManager handles agent process lifecycle: spawning, stopping,
// session discovery, and output reading.
type ProcessManager struct {
	store                 *store.Store
	parser                *StreamParser
	dataDir               string
	handleStreamJSONEvent func(agentID string, ag *Agent, ev *StreamJSONEvent)
	makeWatcherCallback   func(agentID string, ag *Agent) func(watcher.ConversationEvent)
}

// NewProcessManager creates a new ProcessManager.
func NewProcessManager(s *store.Store, parser *StreamParser, dataDir string) *ProcessManager {
	return &ProcessManager{
		store:   s,
		parser:  parser,
		dataDir: dataDir,
	}
}

// SetHandleStreamJSONEvent registers the callback for stream-json events.
func (pm *ProcessManager) SetHandleStreamJSONEvent(fn func(agentID string, ag *Agent, ev *StreamJSONEvent)) {
	pm.handleStreamJSONEvent = fn
}

// SetMakeWatcherCallback registers the callback for watcher creation.
func (pm *ProcessManager) SetMakeWatcherCallback(fn func(agentID string, ag *Agent) func(watcher.ConversationEvent)) {
	pm.makeWatcherCallback = fn
}

// Create spawns a new agent process. The agent must already be registered
// in the Manager before calling Create.
func (pm *ProcessManager) Create(id string, ag *Agent, env []string) error {
	provider := ag.Provider
	workDir := ag.WorkDir
	cmd := ag.Cmd
	args := ag.Args

	ag.setStatus(StatusStarting)

	// For Claude with -p mode, don't start process on initial creation
	// because -p requires stdin input. Process will be started on first message.
	isClaudePrintMode := provider == "claude" && containsString(args, "-p")
	if isClaudePrintMode {
		ag.setStatus(StatusIdle)
		log.Printf("[Create] Agent %s created in idle mode (Claude -p, will start on first message)", id)
		return nil
	}

	if provider == "hermes" {
		ag.setStatus(StatusIdle)
		log.Printf("[Create] Agent %s created in idle mode (Hermes HTTP API)", id)
		return nil
	}

	var p *agentpty.Process
	var err error
	if provider == "claude" {
		p, err = agentpty.SpawnPipes(cmd, args, workDir, env)
	} else {
		p, err = agentpty.Spawn(cmd, args, workDir, env)
	}
	if err != nil {
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("spawn: %w", err)
	}
	ag.setProcess(p)
	ag.setStatus(StatusIdle)
	log.Printf("[Create] Agent %s started with pid=%d status=%s", id, p.Pid(), ag.Status())

	// Update store with PID
	_ = pm.store.SaveAgent(store.AgentRecord{
		ID:       id,
		Name:     ag.Name,
		Provider: provider,
		WorkDir:  workDir,
		PID:      p.Pid(),
	})

	if provider != "claude" {
		go pm.startSessionWatcher(id, ag, p.Pid(), workDir)
		go pm.readPTYForPermissionPrompts(id, ag, provider, p)
	} else {
		log.Printf("[Create] Starting Claude in -p mode, agent %s, pid %d", id, p.Pid())
		go pm.readPipeOutputAndWait(id, ag, p, false)
	}

	return nil
}

// RestartInPlace restarts an agent process in place.
func (pm *ProcessManager) RestartInPlace(id string, ag *Agent, provider, cmd string, args []string, env []string) error {
	if provider == "hermes" {
		ag.kill()
		ag.setProcess(nil)
		ag.setWatcher(nil)
		ag.mu.Lock()
		ag.Provider = provider
		ag.Cmd = cmd
		ag.Args = append([]string{}, args...)
		ag.mu.Unlock()
		ag.setStatus(StatusIdle)
		log.Printf("[Restart] Agent %s restarted in idle mode (Hermes HTTP API)", id)
		return nil
	}

	ag.kill()
	ag.setProcess(nil)
	ag.setWatcher(nil)
	ag.setStatus(StatusStarting)

	ag.mu.Lock()
	ag.Provider = provider
	ag.Cmd = cmd
	ag.Args = append([]string{}, args...)
	workDir := ag.WorkDir
	ag.mu.Unlock()

	var (
		p   *agentpty.Process
		err error
	)
	if provider == "claude" {
		p, err = agentpty.SpawnPipes(cmd, args, workDir, env)
	} else {
		p, err = agentpty.Spawn(cmd, args, workDir, env)
	}
	if err != nil {
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("spawn: %w", err)
	}

	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	if provider != "claude" {
		go pm.startSessionWatcher(id, ag, p.Pid(), workDir)
		go pm.readPTYForPermissionPrompts(id, ag, provider, p)
	} else {
		log.Printf("[Restart] Starting Claude in -p mode, agent %s, pid %d", id, p.Pid())
		go pm.readPipeOutputAndWait(id, ag, p, false)
	}

	return nil
}

// StartInPlaceWithMessage starts a fresh agent with a message written to stdin.
func (pm *ProcessManager) StartInPlaceWithMessage(id string, ag *Agent, provider, cmd string, args []string, env []string, message string) error {
	if provider == "hermes" {
		ag.setStatus(StatusIdle)
		log.Printf("[Start] Agent %s started in idle mode (Hermes HTTP API)", id)
		return nil
	}

	ag.setStatus(StatusStarting)

	ag.mu.Lock()
	workDir := ag.WorkDir
	ag.mu.Unlock()

	var (
		p   *agentpty.Process
		err error
	)
	if provider == "claude" {
		p, err = agentpty.SpawnPipes(cmd, args, workDir, env)
	} else {
		p, err = agentpty.Spawn(cmd, args, workDir, env)
	}
	if err != nil {
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("spawn: %w", err)
	}

	// Write message to stdin
	if _, err := p.Write([]byte(message + "\n")); err != nil {
		p.Kill()
		ag.setStatus(StatusCrashed)
		return fmt.Errorf("write message: %w", err)
	}
	p.CloseStdin()

	ag.setProcess(p)
	ag.setStatus(StatusIdle)

	if provider == "claude" {
		log.Printf("[Start] Starting Claude in -p mode, agent %s, pid %d", id, p.Pid())
		go pm.readPipeOutputAndWait(id, ag, p, false)
	}

	return nil
}

// Stop kills the agent process.
func (pm *ProcessManager) Stop(ag *Agent) {
	ag.kill()
	ag.setStatus(StatusStopped)
}

// Remove cleans up the agent's process resources.
func (pm *ProcessManager) Remove(id string, ag *Agent) error {
	pm.Stop(ag)
	// Clean up persisted images for this agent
	imgDir := filepath.Join(pm.dataDir, "images", id)
	if err := os.RemoveAll(imgDir); err != nil {
		log.Printf("[Remove] failed to clean image dir for %s: %v", id, err)
	}
	return pm.store.DeleteAgent(id)
}

// readPipeOutputAndWait reads stream-json output from pipe and waits for process exit.
// Used for Claude -p mode where the process exits after each response.
func (pm *ProcessManager) readPipeOutputAndWait(agentID string, ag *Agent, p *agentpty.Process, initial bool) {
	defer func() {
		if ag.Process() == p {
			ag.setProcess(nil)
			log.Printf("[Pipe] Agent %s process exited, initial=%v, current status=%s", agentID, initial, ag.Status())
			if !initial {
				cur := ag.Status()
				if cur != StatusStopped && cur != StatusCrashed {
					ag.setStatus(StatusIdle)
					log.Printf("[Pipe] Agent %s process exited normally, status stays idle", agentID)
				} else {
					log.Printf("[Pipe] Agent %s status is %s, keeping it", agentID, cur)
				}
			}
		}
	}()
	log.Printf("[Pipe] Started reading output for agent %s (initial=%v)", agentID, initial)

	buf := make([]byte, 4096)
	var lineBuffer strings.Builder
	var fullText strings.Builder

	for {
		n, err := p.Read(buf)
		if n > 0 {
			text := string(buf[:n])
			lineBuffer.WriteString(text)

			content := lineBuffer.String()
			lines := strings.Split(content, "\n")
			lineBuffer.Reset()
			if len(lines) > 0 && !strings.HasSuffix(content, "\n") {
				lineBuffer.WriteString(lines[len(lines)-1])
				lines = lines[:len(lines)-1]
			}

			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				if ev := pm.tryParseStreamJSON(line); ev != nil {
					log.Printf("[StreamJSON] Parsed event type=%s for agent %s", ev.Type, agentID)
					if pm.handleStreamJSONEvent != nil {
						pm.handleStreamJSONEvent(agentID, ag, ev)
					}
					if ev.Type == "assistant" && ev.Content != nil {
						var text string
						var contentArr []struct {
							Type string `json:"type"`
							Text string `json:"text,omitempty"`
						}
						if err := json.Unmarshal(ev.Content, &text); err == nil {
							fullText.WriteString(text)
						} else if err := json.Unmarshal(ev.Content, &contentArr); err == nil {
							for _, block := range contentArr {
								if block.Type == "text" {
									fullText.WriteString(block.Text)
								}
							}
						}
					}
				} else {
					if len(line) > 0 {
						preview := line
						if len(preview) > 80 {
							preview = preview[:80]
						}
						log.Printf("[StreamJSON] Failed to parse line: %s...", preview)
						fullText.WriteString(line)
						fullText.WriteString("\n")
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[Pipe] Read error for agent %s: %v", agentID, err)
			}
			break
		}
	}

	remaining := strings.TrimSpace(lineBuffer.String())
	if remaining != "" {
		if ev := pm.tryParseStreamJSON(remaining); ev != nil {
			if pm.handleStreamJSONEvent != nil {
				pm.handleStreamJSONEvent(agentID, ag, ev)
			}
		} else {
			fullText.WriteString(remaining)
		}
	}

	if err := p.Wait(); err != nil {
		log.Printf("[Process] Agent %s exited with error: %v", agentID, err)
	} else {
		log.Printf("[Process] Agent %s completed successfully", agentID)
	}
	log.Printf("[Pipe] Finished reading output for agent %s, captured %d chars", agentID, fullText.Len())
}

// readPTYForPermissionPrompts reads PTY output only for permission prompt detection.
func (pm *ProcessManager) readPTYForPermissionPrompts(agentID string, ag *Agent, provider string, p *agentpty.Process) {
	defer func() {
		if ag.Process() == p {
			ag.setProcess(nil)
			ag.setStatus(StatusStopped)
		}
	}()

	buf := make([]byte, 4096)
	var lineBuffer strings.Builder
	var lastAutoResolveTime time.Time
	const autoResolveCooldown = 2 * time.Second

	for {
		n, err := p.Read(buf)
		if n > 0 {
			text := string(buf[:n])

			if provider == "claude" {
				if sessionID := maybeExtractSessionIDFromRaw(strings.TrimSpace(text)); sessionID != "" {
					if err := pm.store.UpdateResumeSessionID(agentID, sessionID); err != nil {
						log.Printf("update resume session from stream for %s: %v", agentID, err)
					}
				}
			}

			if time.Since(lastAutoResolveTime) > autoResolveCooldown && detectPermissionPrompt(text) {
				log.Printf("[Permission] Detected prompt for agent %s", agentID)
				ag.setPermissionPromptActive(true)
				if err := ag.WriteInput("\t\r\r"); err == nil {
					log.Printf("[Permission] Auto-resolved prompt for agent %s", agentID)
					ag.SetPermissionPromptActive(false)
					lastAutoResolveTime = time.Now()
				} else {
					log.Printf("[Permission] Auto-resolve failed for agent %s: %v", agentID, err)
				}
			}

			if provider == "claude" {
				lineBuffer.WriteString(text)
				content := lineBuffer.String()
				lines := strings.Split(content, "\n")
				lineBuffer.Reset()
				if len(lines) > 0 && !strings.HasSuffix(content, "\n") {
					lineBuffer.WriteString(lines[len(lines)-1])
					lines = lines[:len(lines)-1]
				}
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					if ev := pm.tryParseStreamJSON(line); ev != nil {
						if pm.handleStreamJSONEvent != nil {
							pm.handleStreamJSONEvent(agentID, ag, ev)
						}
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[PTY] Read error for agent %s: %v", agentID, err)
			}
			break
		}
	}

	if provider == "claude" {
		remaining := strings.TrimSpace(lineBuffer.String())
		if remaining != "" {
			if ev := pm.tryParseStreamJSON(remaining); ev != nil {
				if pm.handleStreamJSONEvent != nil {
					pm.handleStreamJSONEvent(agentID, ag, ev)
				}
			}
		}
	}
}

// startSessionWatcher tries to find the session file for a newly created agent
// and starts the appropriate watcher.
func (pm *ProcessManager) startSessionWatcher(agentID string, ag *Agent, pid int, workDir string) {
	log.Printf("[Watcher] Starting session watcher for agent %s (PID %d)", agentID, pid)

	if ag.Provider == "opencode" {
		sessionID := pm.findOpenCodeSessionID(pid)
		if sessionID != "" {
			if pm.makeWatcherCallback == nil {
				return
			}
			cb := pm.makeWatcherCallback(agentID, ag)
			w := watcher.NewOpenCodeDBWatcher(sessionID, cb)
			if err := w.Start(); err != nil {
				log.Printf("[Watcher] OpenCode DB watcher start failed for agent %s: %v", agentID, err)
				return
			}
			ag.setWatcher(w)
			log.Printf("[Watcher] Started OpenCode DB watcher for agent %s (session %s)", agentID, sessionID)
			return
		}
	}

	var sessionFile string
	retryCount := 0
	maxRetries := 300

	for sessionFile == "" && retryCount < maxRetries {
		if ag.Status() == StatusStopped {
			log.Printf("[Watcher] Agent %s stopped, aborting watcher", agentID)
			return
		}

		sessionFile = pm.findSessionFile(pid, workDir)
		if sessionFile == "" {
			retryCount++
			if retryCount%10 == 0 {
				log.Printf("[Watcher] Still looking for session file for agent %s (retry %d)", agentID, retryCount)
			}
			time.Sleep(1 * time.Second)
		}
	}

	if sessionFile == "" {
		log.Printf("[Watcher] Could not find session file for agent %s (PID %d) after %d retries", agentID, pid, maxRetries)
		return
	}
	log.Printf("[Watcher] Found session file for agent %s: %s", agentID, sessionFile)

	if pm.makeWatcherCallback == nil {
		return
	}
	cb := pm.makeWatcherCallback(agentID, ag)
	w := watcher.NewClaudeWatcher(sessionFile, cb)
	w.SetWorkDir(workDir)
	w.SetPID(pid)
	w.SetTmuxTarget(ag.TmuxTarget())
	if err := w.Start(); err != nil {
		log.Printf("[Watcher] Watcher start failed for agent %s: %v", agentID, err)
		return
	}
	ag.setWatcher(w)
	log.Printf("[Watcher] Started session watcher for agent %s", agentID)
}

// findSessionFile attempts to find the Claude JSONL session file for a given PID.
func (pm *ProcessManager) findSessionFile(pid int, workDir string) string {
	for _, home := range allClaudeHomeDirs() {
		sessionsDir := filepath.Join(home, ".claude", "sessions")
		pidFile := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", pid))

		if _, err := os.Stat(pidFile); err == nil {
			data, err := os.ReadFile(pidFile)
			if err == nil {
				var pidInfo struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(data, &pidInfo); err == nil && pidInfo.SessionID != "" {
					projectsBase := filepath.Join(home, ".claude", "projects")
					entries, _ := os.ReadDir(projectsBase)
					for _, entry := range entries {
						if entry.IsDir() {
							jsonlPath := filepath.Join(projectsBase, entry.Name(), pidInfo.SessionID+".jsonl")
							if _, err := os.Stat(jsonlPath); err == nil {
								return jsonlPath
							}
						}
					}
				}
			}
		}

		dirName := strings.ReplaceAll(workDir, "/", "-")
		if dirName == "" || dirName == "-" {
			dirName = "-"
		}

		projectsDir := filepath.Join(home, ".claude", "projects", dirName)
		entries, err := os.ReadDir(projectsDir)
		if err != nil {
			continue
		}

		cutoff := time.Now().Add(-5 * time.Minute)
		var latest string
		var latestTime time.Time
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".jsonl") {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(cutoff) && info.ModTime().After(latestTime) {
					latestTime = info.ModTime()
					latest = filepath.Join(projectsDir, entry.Name())
				}
			}
		}
		if latest != "" {
			return latest
		}
	}

	return ""
}

// findOpenCodeSessionID queries the opencode SQLite DB to find the most recent
// session for the given PID's working directory.
func (pm *ProcessManager) findOpenCodeSessionID(pid int) string {
	cwd, err := getWorkingDirectory(pid)
	if err != nil {
		return ""
	}

	dbPath := watcher.FindOpenCodeDB()
	if dbPath == "" {
		return ""
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return ""
	}
	defer db.Close()

	var sessionID string
	err = db.QueryRow(`
		SELECT id FROM session
		WHERE directory = ?
		ORDER BY time_updated DESC
		LIMIT 1`, cwd).Scan(&sessionID)
	if err != nil {
		return ""
	}
	return sessionID
}

func (pm *ProcessManager) tryParseStreamJSON(text string) *StreamJSONEvent {
	return pm.parser.TryParseStreamJSON(text)
}
