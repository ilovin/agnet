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
		fmt.Fprintln(os.Stderr, "Usage: agentd <start|version>")
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
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("get home dir: %v", err)
	}
	cfgPath := filepath.Join(home, ".agentd", "config.yaml")
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
	tokenPreview := cfg.Token
	if len(tokenPreview) > 8 {
		tokenPreview = tokenPreview[:8]
	}
	log.Printf("agentd listening on %s (token: %s...)", addr, tokenPreview)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
