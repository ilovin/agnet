package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/phone-talk/agentgw/internal/config"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/oauth"
	"github.com/phone-talk/agentgw/internal/tunnel"
	"github.com/phone-talk/agentgw/internal/ws"
	"github.com/skip2/go-qrcode"
)

var activeTunnel *tunnel.Client
var currentTunnelURL string
var currentTunnelToken string

func restartTunnel(url, token, localAddr, localToken string) {
	currentTunnelURL = url
	currentTunnelToken = token
	if activeTunnel != nil {
		activeTunnel.Stop()
	}
	if url != "" {
		activeTunnel = tunnel.NewClient(url, token, localAddr, localToken)
		go activeTunnel.Start()
		log.Printf("[agentgw] tunnel client started: %s -> %s", url, localAddr)
	} else {
		activeTunnel = nil
		log.Printf("[agentgw] tunnel client stopped")
	}
}

func main() {
	args := os.Args[1:]

	// If the first arg looks like a flag, treat it as implicit "start".
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		args = append([]string{"start"}, args...)
	}

	if len(args) < 1 {
		showHelp()
		os.Exit(1)
	}

	switch args[0] {
	case "start":
		fs := flag.NewFlagSet("start", flag.ExitOnError)
		tunnelURL := fs.String("tunnel-url", os.Getenv("AGENTGW_TUNNEL_URL"), "tunnel hub URL (env AGENTGW_TUNNEL_URL)")
		tunnelToken := fs.String("tunnel-token", os.Getenv("AGENTGW_TUNNEL_TOKEN"), "tunnel auth token (env AGENTGW_TUNNEL_TOKEN)")
		hubBase := fs.String("hub", os.Getenv("AGENTGW_HUB"), "tunnelhub base URL, e.g. wss://domain:8443 (env AGENTGW_HUB)")
		appURL := fs.String("app-url", os.Getenv("AGENTGW_APP_URL"), "app-facing hub URL for QR code, if different from tunnel-url (env AGENTGW_APP_URL)")
		showQR := fs.Bool("qr", false, "print connection QR code to terminal after startup")
		fs.Parse(args[1:])
		runServer(*tunnelURL, *tunnelToken, *hubBase, *appURL, *showQR)
	case "qr":
		fs := flag.NewFlagSet("qr", flag.ExitOnError)
		tunnelURL := fs.String("tunnel-url", os.Getenv("AGENTGW_TUNNEL_URL"), "tunnel hub URL (env AGENTGW_TUNNEL_URL)")
		tunnelToken := fs.String("tunnel-token", os.Getenv("AGENTGW_TUNNEL_TOKEN"), "tunnel auth token (env AGENTGW_TUNNEL_TOKEN)")
		appURL := fs.String("app-url", os.Getenv("AGENTGW_APP_URL"), "app-facing hub URL for QR code, if different from tunnel-url (env AGENTGW_APP_URL)")
		fs.Parse(args[1:])
		printQRFromConfig(*tunnelURL, *tunnelToken, *appURL)
	case "login":
		fs := flag.NewFlagSet("login", flag.ExitOnError)
		hubURL := fs.String("hub", os.Getenv("AGENTGW_TUNNEL_URL"), "tunnelhub URL for registration (env AGENTGW_TUNNEL_URL)")
		fs.Parse(args[1:])
		if os.Getenv("OPENSSO_CLIENT_ID") != "" {
			cfg := oauth.DefaultConfig()
			result, err := oauth.DoLogin(cfg)
			if err != nil {
				log.Fatalf("login failed: %v", err)
			}
			path := oauth.TokenFilePath()
			if err := result.Save(path); err != nil {
				log.Fatalf("save token: %v", err)
			}
			fmt.Printf("Login successful! userId=%s\n", result.UserID)
			fmt.Printf("Token saved to: %s\n", path)
		} else {
			auth, err := oauth.DoLocalLogin(*hubURL)
			if err != nil {
				log.Fatalf("login failed: %v", err)
			}
			fmt.Printf("\nRegistered successfully!\n")
			fmt.Printf("  userId: %s\n", auth.UserID)
			fmt.Printf("  token:  %s\n", auth.Token)
			fmt.Printf("Saved to: %s\n", oauth.LocalAuthFilePath())
			fmt.Println("\nCredentials will be used automatically by 'agentgw start'.")
		}
	case "logout":
		fs := flag.NewFlagSet("logout", flag.ExitOnError)
		hubURL := fs.String("hub", os.Getenv("AGENTGW_TUNNEL_URL"), "tunnelhub URL for unregistration (env AGENTGW_TUNNEL_URL)")
		fs.Parse(args[1:])
		if err := oauth.DoLocalLogout(*hubURL); err != nil {
			log.Fatalf("logout failed: %v", err)
		}
		fmt.Println("Unregistered successfully.")
	case "version":
		fmt.Println("agentgw v0.1.0")
	case "help", "--help", "-h":
		showHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(1)
	}
}

func showHelp() {
	fmt.Print(`Usage: agentgw <start|login|logout|qr|version|help>

Commands:
  start    Start the gateway server
  login    Register with tunnelhub and save credentials locally
  logout   Unregister from tunnelhub and delete local credentials
  qr       Print connection QR code to terminal now
  version  Show version
  help     Show this help message

Start flags:
  --hub string           Tunnelhub base URL, auto-constructs tunnel-url from saved credentials (env AGENTGW_HUB)
  --tunnel-url string    Full tunnel hub URL, overrides --hub (env AGENTGW_TUNNEL_URL)
  --tunnel-token string  Tunnel auth token, overrides saved credentials (env AGENTGW_TUNNEL_TOKEN)
  --app-url string       App-facing hub URL for QR code (env AGENTGW_APP_URL)
  --qr                   Print connection QR code to terminal after startup

Login flags:
  --hub string           Tunnelhub base URL for registration (env AGENTGW_TUNNEL_URL)

Quick start:
  agentgw login --hub wss://domain:8443
  agentgw start --hub wss://domain:8443 --qr

Environment:
  AGENTGW_HUB           Tunnelhub base URL (used by both login and start)
  AGENTGW_TUNNEL_URL    Full tunnel hub URL (overrides AGENTGW_HUB)
  AGENTGW_TUNNEL_TOKEN  Tunnel auth token
  AGENTGW_APP_URL       App-facing hub URL (e.g. wss://domain:8443/ws for SNI bypass)
`)
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

func runServer(tunnelURLFlag, tunnelTokenFlag, hubBaseFlag, appURLFlag string, showQR bool) {
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

	// Start reverse tunnel client if configured.
	// Tunnel token is embedded into the URL query so that URL + token
	// travel together as a single unit (e.g. ?token=xxx).
	tunnelURL := tunnelURLFlag
	if tunnelURL == "" {
		tunnelURL = os.Getenv("AGENTGW_TUNNEL_URL")
	}

	// Auto-load credentials from local_auth.json for --hub mode
	var localAuth *oauth.LocalAuth
	if la, err := oauth.LoadLocalAuth(oauth.LocalAuthFilePath()); err == nil {
		localAuth = la
	}

	// Resolve tunnel token
	tunnelToken := ""
	if localAuth != nil && localAuth.Token != "" {
		tunnelToken = localAuth.Token
	} else if lr, err := oauth.LoadLoginResult(oauth.TokenFilePath()); err == nil && lr.AccessToken != "" {
		tunnelToken = lr.AccessToken
	}

	// Auto-construct tunnel URL from --hub + userId if --tunnel-url not given
	hubBase := hubBaseFlag
	if hubBase == "" {
		hubBase = os.Getenv("AGENTGW_HUB")
	}
	if tunnelURL == "" && hubBase != "" && localAuth != nil {
		tunnelURL = strings.TrimRight(hubBase, "/") + "/api/v1/stream"
		log.Printf("[agentgw] constructed tunnel URL: %s", tunnelURL)
	}

	// Auto-construct app URL from hub base if not given
	if appURLFlag == "" && hubBase != "" {
		appURLFlag = strings.TrimRight(hubBase, "/") + "/api/v1/ws"
		log.Printf("[agentgw] constructed app URL: %s", appURLFlag)
	}

	localAddr := fmt.Sprintf("localhost:%d", cfg.Port)
	if tunnelURL != "" {
		restartTunnel(tunnelURL, tunnelToken, localAddr, cfg.Token)
		currentTunnelToken = tunnelToken
	}

	// Admin endpoint to get or hot-update tunnel URL without restarting agentgw.
	http.HandleFunc("/config/tunnel", func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"tunnelUrl": currentTunnelURL, "tunnelToken": currentTunnelToken})
		case http.MethodPost:
			var req struct {
				URL   string `json:"url"`
				Token string `json:"token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			restartTunnel(req.URL, req.Token, localAddr, cfg.Token)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": req.URL})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

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

	userID := ""
	if localAuth != nil {
		userID = localAuth.UserID
	}
	if showQR {
		printQRCode(cfg.Port, cfg.Token, tunnelURL, tunnelToken, appURLFlag, userID)
	}

	// Handle on-demand QR requests via SIGUSR1.
	go handleQRSignals(cfg.Port, cfg.Token, tunnelURL, tunnelToken, appURLFlag, userID)

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

func buildRemoteQRURL(tunnelURL, appURL, userID string) string {
	if tunnelURL == "" {
		return ""
	}

	urlToParse := appURL
	if urlToParse == "" {
		urlToParse = tunnelURL
	}
	u, err := url.Parse(urlToParse)
	if err != nil {
		return ""
	}

	if userID == "" {
		userID = "default"
	}

	scheme := "wss"
	if u.Scheme == "ws" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/api/v1/ws/%s", scheme, u.Host, userID)
}

func printQRCode(port int, token, tunnelURL, tunnelToken, appURL, userID string) {
	localIP := getLocalIP()
	if localIP == "" {
		fmt.Println("Unable to determine local IP address")
		return
	}
	localURL := fmt.Sprintf("ws://%s:%d/ws|%s", localIP, port, token)
	urls := []string{localURL}
	if tunnelURL != "" && tunnelToken != "" {
		if remoteURL := buildRemoteQRURL(tunnelURL, appURL, userID); remoteURL != "" {
			urls = append(urls, remoteURL+"|"+tunnelToken)
		}
	}
	for i, qrURL := range urls {
		label := "Local"
		if i > 0 {
			label = "Remote"
		}
		qr, err := qrcode.New(qrURL, qrcode.Medium)
		if err == nil {
			fmt.Printf("\n[%s] Scan QR code to connect:\n", label)
			fmt.Println(qr.ToSmallString(false))
			fmt.Printf("URL: %s\n", qrURL)
		} else {
			log.Printf("failed to generate QR for %s: %v", label, err)
		}
	}
	fmt.Println()
}

func printQRFromConfig(tunnelURL, tunnelToken, appURL string) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("get home dir: %v", err)
	}
	cfgPath := home + "/.agentgw/config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if tunnelURL == "" || tunnelToken == "" {
		url, tok := fetchTunnelConfigFromRunningServer(cfg.Port, cfg.Token)
		if tunnelURL == "" {
			tunnelURL = url
		}
		if tunnelToken == "" {
			tunnelToken = tok
		}
	}
	userID := ""
	if la, err := oauth.LoadLocalAuth(oauth.LocalAuthFilePath()); err == nil {
		userID = la.UserID
	}
	printQRCode(cfg.Port, cfg.Token, tunnelURL, tunnelToken, appURL, userID)
}

func fetchTunnelConfigFromRunningServer(port int, token string) (string, string) {
	url := fmt.Sprintf("http://localhost:%d/config/tunnel", port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var body struct {
		TunnelURL   string `json:"tunnelUrl"`
		TunnelToken string `json:"tunnelToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", ""
	}
	return body.TunnelURL, body.TunnelToken
}

func handleQRSignals(port int, token, tunnelURL, tunnelToken, appURL, userID string) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	for range ch {
		printQRCode(port, token, tunnelURL, tunnelToken, appURL, userID)
	}
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
