package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// staticHandler wraps a http.FileServer and serves static files without auth.
func staticHandler(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// Load agentd binary for remote deploy
	var agentdBin []byte
	for _, p := range agentdBinCandidates() {
		if data, err := os.ReadFile(p); err == nil {
			agentdBin = data
			log.Printf("Loaded agentd binary from: %s (%d bytes)", p, len(data))
			break
		}
	}

	mgr := node.NewManager(store, agentdBin)

	entries, err := store.Load()
	if err != nil {
		log.Printf("warn: could not load nodes: %v", err)
	}
	mgr.LoadAll(entries)

	srv := ws.New(mgr, cfg.Token)

	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := listenWithInherit(addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv.SetGatewayRestartFunc(func() error {
		return restartWithListener(ln)
	})

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
		http.Handle("/", staticHandler(staticDir))
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

	tokenPreview := cfg.Token
	if len(tokenPreview) > 8 {
		tokenPreview = tokenPreview[:8]
	}
	log.Printf("agentgw listening on %s (token: %s...)", addr, tokenPreview)
	err = http.Serve(ln, nil)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatalf("serve: %v", err)
	}

	// Graceful shutdown: keep existing connections alive for a while.
	log.Printf("agentgw listener closed, draining connections...")
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// listenWithInherit creates a TCP listener, reusing a file descriptor passed
// by a parent process during hot restart if available.
func listenWithInherit(addr string) (net.Listener, error) {
	if fdStr := os.Getenv("AGENTGW_INHERIT_FD"); fdStr != "" {
		fd, err := strconv.Atoi(fdStr)
		if err == nil {
			file := os.NewFile(uintptr(fd), "inherited-listener")
			if file != nil {
				ln, err := net.FileListener(file)
				file.Close()
				if err == nil {
					return ln, nil
				}
				log.Printf("failed to inherit listener: %v", err)
			}
		}
		os.Unsetenv("AGENTGW_INHERIT_FD")
	}
	return net.Listen("tcp", addr)
}

// restartWithListener starts a new agentgw process inheriting the current
// TCP listener so the port remains bound without interruption.
func restartWithListener(ln net.Listener) error {
	ex, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}

	tcpLn, ok := ln.(*net.TCPListener)
	if !ok {
		return fmt.Errorf("listener is not TCP")
	}

	f, err := tcpLn.File()
	if err != nil {
		return fmt.Errorf("get listener file: %w", err)
	}

	cmd := exec.Command(ex, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "AGENTGW_INHERIT_FD=3")
	cmd.ExtraFiles = []*os.File{f}

	if err := cmd.Start(); err != nil {
		f.Close()
		return fmt.Errorf("start child: %w", err)
	}

	// Close the listener so http.Serve returns in this process.
	// Existing connections remain alive because they have their own FDs.
	ln.Close()
	return nil
}

func agentdBinCandidates() []string {
	candidates := []string{"agentd-linux", "./agentd-linux", "../agentd/agentd-linux"}
	if ex, err := os.Executable(); err == nil {
		exDir := filepath.Dir(ex)
		candidates = append([]string{
			filepath.Join(exDir, "agentd-linux"),
			filepath.Join(exDir, "..", "agentd", "agentd-linux"),
		}, candidates...)
	}
	return candidates
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
