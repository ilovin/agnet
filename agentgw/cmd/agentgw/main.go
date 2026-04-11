package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// staticHandler wraps a http.FileServer and handles token auth for index.html
func staticHandler(root string, token string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For index.html, check token
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			if !checkToken(r, token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		fs.ServeHTTP(w, r)
	})
}

func checkToken(r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	queryToken := r.URL.Query().Get("token")
	headerToken := strings.TrimPrefix(auth, "Bearer ")
	t := headerToken
	if t == "" {
		t = queryToken
	}
	return t == token
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
	mgr := node.NewManager(store, nil)

	entries, err := store.Load()
	if err != nil {
		log.Printf("warn: could not load nodes: %v", err)
	}
	mgr.LoadAll(entries)

	srv := ws.New(mgr, cfg.Token)

	mgr.OnEvent(func(nodeID string, ev map[string]any) {
		method, _ := ev["method"].(string)
		params := ev["params"]
		srv.Broadcast(ws.RPCEvent{JSONRPC: "2.0", Method: method, Params: params})
	})

	mgr.ConnectAll()

	// Find static directory (check multiple locations)
	staticDir := findStaticDir()
	if staticDir != "" {
		log.Printf("Serving static files from: %s", staticDir)
		http.Handle("/", staticHandler(staticDir, cfg.Token))
	} else {
		log.Printf("Warning: static directory not found")
		http.Handle("/", srv)
	}
	http.Handle("/ws", srv)

	// APK download endpoint for dev updates
	apkCandidates := func() []string {
		candidates := []string{"agentapp.apk", "./agentapp.apk", "../agentapp.apk"}
		if ex, err := os.Executable(); err == nil {
			exDir := filepath.Dir(ex)
			candidates = append([]string{
				filepath.Join(exDir, "agentapp.apk"),
				filepath.Join(exDir, "..", "agentapp.apk"),
			}, candidates...)
		}
		return candidates
	}

	http.HandleFunc("/apk", func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		for _, path := range apkCandidates() {
			if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
				w.Header().Set("Content-Type", "application/vnd.android.package-archive")
				w.Header().Set("Content-Disposition", "attachment; filename=agentapp.apk")
				http.ServeFile(w, r, path)
				return
			}
		}
		http.Error(w, "APK not found", http.StatusNotFound)
	})

	http.HandleFunc("/apk/version", func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		for _, path := range apkCandidates() {
			if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"version":    fi.ModTime().Unix(),
					"size":       fi.Size(),
					"modifiedAt": fi.ModTime().Format(time.RFC3339),
				})
				return
			}
		}
		http.Error(w, "APK not found", http.StatusNotFound)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	tokenPreview := cfg.Token
	if len(tokenPreview) > 8 {
		tokenPreview = tokenPreview[:8]
	}
	log.Printf("agentgw listening on %s (token: %s...)", addr, tokenPreview)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func findStaticDir() string {
	// Check multiple locations for static directory
	candidates := []string{
		"static",
		"./static",
		"../static",
	}

	// Get executable directory
	ex, err := os.Executable()
	if err == nil {
		exDir := filepath.Dir(ex)
		candidates = append([]string{
			filepath.Join(exDir, "static"),
			filepath.Join(exDir, "..", "static"),
		}, candidates...)
	}

	for _, dir := range candidates {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			// Check for index.html
			if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
				return dir
			}
		}
	}
	return ""
}
