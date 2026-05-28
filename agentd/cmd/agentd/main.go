package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/config"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/ws"
)

const version = "agentd v1.0.0"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: agentd <start|status|version>")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "start":
		runServer()
	case "status":
		printStatus()
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func loadConfig() (*config.Config, string) {
	var cfgPath string
	if envPath := os.Getenv("AGENTD_CONFIG"); envPath != "" {
		cfgPath = envPath
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("get home dir: %v", err)
		}
		cfgPath = filepath.Join(home, ".agentd", "config.json")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	return cfg, cfgPath
}

func printStatus() {
	cfg, _ := loadConfig()
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/status", cfg.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd is not running (could not connect to port %d): %v\n", cfg.Port, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "agentd status error: %s\n%s\n", resp.Status, string(body))
		os.Exit(1)
	}

	var st statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		fmt.Fprintf(os.Stderr, "failed to decode status: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Version:        %s\n", st.Version)
	fmt.Printf("Uptime:         %s\n", formatDuration(time.Since(st.Uptime)))
	fmt.Printf("GW Connections: %d\n", st.GWConnections)
	fmt.Println()
	if len(st.Sessions) == 0 {
		fmt.Println("No active sessions.")
		return
	}
	fmt.Printf("%-36s %-20s %-24s %-10s %-8s %-12s %-20s\n",
		"ID", "Name", "SessionID", "Provider", "Status", "PID", "Last Updated")
	fmt.Println(strings.Repeat("-", 132))
	for _, se := range st.Sessions {
		name := se.Name
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		sessionID := se.SessionID
		if len(sessionID) > 24 {
			sessionID = sessionID[:21] + "..."
		}
		if sessionID == "" {
			sessionID = "N/A"
		}
		last := se.LastUpdated
		if last == "" {
			last = "N/A"
		}
		fmt.Printf("%-36s %-20s %-24s %-10s %-8s %-12d %-20s\n",
			se.ID, name, sessionID, se.Provider, se.Status, se.PID, last)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

type statusResponse struct {
	Version       string        `json:"version"`
	Uptime        time.Time     `json:"uptime"`
	GWConnections int           `json:"gwConnections"`
	Sessions      []sessionInfo `json:"sessions"`
}

type sessionInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SessionID    string `json:"sessionId"`
	Provider     string `json:"provider"`
	Status       string `json:"status"`
	PID          int    `json:"pid"`
	RuntimeState string `json:"runtimeState"`
	SessionState string `json:"sessionState"`
	LastUpdated  string `json:"lastUpdated"`
}

func runServer() {
	cfg, _ := loadConfig()

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
	if err := mgr.LoadFromStore(); err != nil {
		log.Printf("warning: failed to load agents from store: %v", err)
	}

	// Auto-attach runs in the background so the HTTP server can start
	// accepting connections immediately.
	go mgr.AutoAttachExisting()
	go mgr.PeriodicScanAndAttach()

	// Periodic goroutine/thread diagnostics
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			n, _ := runtime.ThreadCreateProfile(nil)
			g := runtime.NumGoroutine()
			log.Printf("[Runtime] goroutines=%d threads_created=%d", g, n)
			if g > 500 || n > 200 {
				buf := make([]byte, 1<<20) // 1MB
				sz := runtime.Stack(buf, true)
				log.Printf("[Runtime] stack snapshot (goroutines=%d threads=%d):\n%s", g, n, string(buf[:min(sz, len(buf))]))
			}
		}
	}()

	startTime := time.Now()

	http.HandleFunc("/debug/stacks", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 2<<20) // 2MB
		sz := runtime.Stack(buf, true)
		w.Header().Set("Content-Type", "text/plain")
		w.Write(buf[:sz])
	})

	srv := ws.New(mgr, cfg.Token, cfg.NodeID)

	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		agents := mgr.List()
		sessions := make([]sessionInfo, 0, len(agents))
		for _, ag := range agents {
			derived := mgr.DeriveAgentState(ag.ID)
			resumeID, _ := mgr.GetResumeSessionID(ag.ID)
			lastT, _ := s.LastConversationEventTime(ag.ID)
			lastStr := ""
			if !lastT.IsZero() {
				lastStr = lastT.Format("2006-01-02 15:04:05")
			}
			sessions = append(sessions, sessionInfo{
				ID:           ag.ID,
				Name:         ag.Name,
				SessionID:    resumeID,
				Provider:     ag.Provider,
				Status:       string(ag.Status()),
				PID:          ag.PID,
				RuntimeState: derived.RuntimeState,
				SessionState: derived.SessionState,
				LastUpdated:  lastStr,
			})
		}

		resp := statusResponse{
			Version:       version,
			Uptime:        startTime,
			GWConnections: srv.ClientCount(),
			Sessions:      sessions,
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	http.Handle("/ws", srv)
	tokenPreview := cfg.Token
	if len(tokenPreview) > 8 {
		tokenPreview = tokenPreview[:8]
	}
	log.Printf("agentd listening on %s (token: %s...)", addr, tokenPreview)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
