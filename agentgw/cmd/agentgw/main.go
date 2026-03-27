package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/phone-talk/agentgw/internal/config"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/ws"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: agentgw <start|version>")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "start":
		runServer()
	case "version":
		fmt.Println("agentgw v0.1.0")
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

	cfgPath := home + "/.agentgw/config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store := nodecfg.New(cfg.NodesFile)
	mgr := node.NewManager(store, nil) // agentd embed wired at build time

	// Load persisted nodes in batch (avoids N redundant file writes)
	entries, err := store.Load()
	if err != nil {
		log.Printf("warn: could not load nodes: %v", err)
	}
	mgr.LoadAll(entries)

	srv := ws.New(mgr, cfg.Token)

	// Wire event forwarding: agentd push events → broadcast to all App clients
	mgr.OnEvent(func(nodeID string, ev map[string]any) {
		method, _ := ev["method"].(string)
		params := ev["params"]
		srv.Broadcast(ws.RPCEvent{JSONRPC: "2.0", Method: method, Params: params})
	})

	// Connect all loaded nodes in background
	mgr.ConnectAll()

	addr := fmt.Sprintf(":%d", cfg.Port)
	http.Handle("/ws", srv)
	http.Handle("/", srv)

	tokenPreview := cfg.Token
	if len(tokenPreview) > 8 {
		tokenPreview = tokenPreview[:8]
	}
	log.Printf("agentgw listening on %s (token: %s...)", addr, tokenPreview)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
