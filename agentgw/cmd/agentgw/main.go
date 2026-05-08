package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	"github.com/phone-talk/agentgw/internal/upgrade"
	"github.com/phone-talk/agentgw/internal/ws"
	"github.com/skip2/go-qrcode"
)

var activeTunnel *tunnel.Client
var currentTunnelURL string
var currentTunnelToken string
var currentRealityCfg *tunnel.RealityConfig

// Version is set at build time via -ldflags "-X main.Version=<version>".
var Version = "v0.1.0"

// Domain defaults are injected at build time via -ldflags.
// Subdomains are automatically derived from the root domain.
var DefaultHubDomain = "ilovin.xyz"
var DefaultAPIDomain = ""
var DefaultDownloadDomain = ""

type tunnelStatusBody struct {
	Connected               bool      `json:"connected"`
	ConnectedAt             time.Time `json:"connectedAt"`
	LastHandshakeDurationMs int64     `json:"lastHandshakeDurationMs"`
	LastHandshakeAt         time.Time `json:"lastHandshakeAt"`
	LastCommunicationAt     time.Time `json:"lastCommunicationAt"`
	LastDisconnectedAt      time.Time `json:"lastDisconnectedAt"`
	LastError               string    `json:"lastError"`
}

var configLoadPath string

var statusHTTPGet = func(url, token string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func restartTunnel(url, token, localAddr, localToken string) {
	currentTunnelURL = url
	currentTunnelToken = token
	if activeTunnel != nil {
		activeTunnel.Stop()
	}
	if url != "" {
		activeTunnel = tunnel.NewClient(url, token, localAddr, localToken)
		if currentRealityCfg != nil {
			activeTunnel.SetReality(currentRealityCfg)
		}
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
		defaultHub := os.Getenv("AGENTGW_HUB")
		if defaultHub == "" && DefaultHubDomain != "" {
			defaultHub = "https://" + DefaultHubDomain
		}
		hubBase := fs.String("hub", defaultHub, "tunnelhub public base URL, e.g. https://domain (env AGENTGW_HUB)")
		appURL := fs.String("app-url", os.Getenv("AGENTGW_APP_URL"), "app-facing hub URL for QR code, if different from tunnel-url (env AGENTGW_APP_URL)")
		realityPub := fs.String("reality-pub", os.Getenv("AGENTGW_REALITY_PUB"), "REALITY public key base64 (env AGENTGW_REALITY_PUB)")
		realitySID := fs.String("reality-sid", os.Getenv("AGENTGW_REALITY_SID"), "REALITY short ID hex (env AGENTGW_REALITY_SID)")
		realitySNI := fs.String("reality-sni", os.Getenv("AGENTGW_REALITY_SNI"), "REALITY server name (env AGENTGW_REALITY_SNI)")
		showQR := fs.Bool("qr", false, "print connection QR code to terminal after startup")
		fs.Parse(args[1:])
		var rcfg *tunnel.RealityConfig
		if *realityPub != "" && *realitySNI != "" {
			rcfg = &tunnel.RealityConfig{
				PublicKey:  *realityPub,
				ShortId:    *realitySID,
				ServerName: *realitySNI,
			}
		}
		runServer(*tunnelURL, *tunnelToken, *hubBase, *appURL, rcfg, *showQR)
	case "qr":
		fs := flag.NewFlagSet("qr", flag.ExitOnError)
		tunnelURL := fs.String("tunnel-url", os.Getenv("AGENTGW_TUNNEL_URL"), "tunnel hub URL (env AGENTGW_TUNNEL_URL)")
		tunnelToken := fs.String("tunnel-token", os.Getenv("AGENTGW_TUNNEL_TOKEN"), "tunnel auth token (env AGENTGW_TUNNEL_TOKEN)")
		defaultAppURL := os.Getenv("AGENTGW_APP_URL")
		if defaultAppURL == "" && DefaultHubDomain != "" {
			defaultAppURL = "https://" + DefaultHubDomain
		}
		appURL := fs.String("app-url", defaultAppURL, "app-facing hub URL for QR code, if different from tunnel-url (env AGENTGW_APP_URL)")
		fs.Parse(args[1:])
		printQRFromConfig(*tunnelURL, *tunnelToken, *appURL)
	case "login":
		fs := flag.NewFlagSet("login", flag.ExitOnError)
		defaultHub := os.Getenv("AGENTGW_HUB")
		if defaultHub == "" && DefaultHubDomain != "" {
			defaultHub = "https://" + DefaultHubDomain
		}
		hubURL := fs.String("hub", defaultHub, "tunnelhub URL for registration (env AGENTGW_HUB)")
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
		defaultHub := os.Getenv("AGENTGW_HUB")
		if defaultHub == "" && DefaultHubDomain != "" {
			defaultHub = "https://" + DefaultHubDomain
		}
		hubURL := fs.String("hub", defaultHub, "tunnelhub URL for unregistration (env AGENTGW_HUB)")
		fs.Parse(args[1:])
		if err := oauth.DoLocalLogout(*hubURL); err != nil {
			log.Fatalf("logout failed: %v", err)
		}
		fmt.Println("Unregistered successfully.")
	case "status":
		showStatus()
	case "version":
		fmt.Println("agentgw " + Version)
	case "rotate-token":
		var token string
		if len(args) > 1 {
			token = args[1]
		}
		rotateToken(token)
	case "help", "--help", "-h":
		showHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(1)
	}
}

func showHelp() {
	hubExample := "https://ilovin.xyz"
	if DefaultHubDomain != "" {
		hubExample = "https://" + DefaultHubDomain
	}
	fmt.Printf(`Usage: agentgw <start|login|logout|qr|status|version|rotate-token|help>

Commands:
  start         Start the gateway server
  login         Register with tunnelhub and save credentials locally
  logout        Unregister from tunnelhub and delete local credentials
  qr            Print connection QR code to terminal now
  status        Show gateway status (node list, connections, uptime)
  rotate-token  Generate a new token and sync it to all agentd configs
                Usage: agentgw rotate-token [EXISTING_TOKEN]
                If EXISTING_TOKEN is provided, use it instead of generating a new one.
                Useful when tunnelhub provides a token after registration.
  version       Show version
  help          Show this help message

Start flags:
  --hub string           Tunnelhub public base URL, use fixed entry (recommend https://domain, env AGENTGW_HUB)
  --tunnel-url string    Full tunnel hub URL, overrides --hub (env AGENTGW_TUNNEL_URL)
  --tunnel-token string  Tunnel auth token, overrides saved credentials (env AGENTGW_TUNNEL_TOKEN)
  --app-url string       App-facing hub URL for QR code (env AGENTGW_APP_URL)
  --reality-pub string   REALITY public key base64 (env AGENTGW_REALITY_PUB)
  --reality-sid string   REALITY short ID hex (env AGENTGW_REALITY_SID)
  --reality-sni string   REALITY server name / SNI (env AGENTGW_REALITY_SNI)
  --qr                   Print connection QR code to terminal after startup

Login flags:
  --hub string           Tunnelhub base URL for registration (env AGENTGW_HUB)

Quick start:
  agentgw login --hub %s
  agentgw start --hub %s --qr

REALITY mode (anti-detection):
  agentgw start --hub https://domain:443 \
    --reality-pub <base64-key> --reality-sid <hex-id> --reality-sni www.bing.com --qr

Environment:
  AGENTGW_HUB           Tunnelhub base URL (used by both login and start)
  AGENTGW_TUNNEL_URL    Full tunnel hub URL (overrides AGENTGW_HUB)
  AGENTGW_APP_URL       App-facing hub URL for QR code
  AGENTGW_REALITY_PUB   REALITY public key (base64)
  AGENTGW_REALITY_SID   REALITY short ID (hex)
  AGENTGW_REALITY_SNI   REALITY server name for TLS SNI
`, hubExample, hubExample)
}

func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func updateAgentdToken(path, token string) error {
	script := fmt.Sprintf(`import json,os;path='%s';cfg={'port':7373,'data_dir':os.path.expanduser('~/.agentd/data')};os.path.exists(path) and [cfg.update(json.load(open(path)))];cfg['token']='%s';json.dump(cfg,open(path,'w'),indent=2);open(path,'a').write('\n')`, path, token)
	cmd := exec.Command("python3", "-c", script)
	return cmd.Run()
}

func sshUpdateAgentdToken(host string, port int, keyPath, token string) error {
	script := fmt.Sprintf(`python3 -c "import json,os;path=os.path.expanduser('~/.agentd/config.json');cfg={'port':7373,'data_dir':os.path.expanduser('~/.agentd/data')};os.path.exists(path) and [cfg.update(json.load(open(path)))];cfg['token']='%s';json.dump(cfg,open(path,'w'),indent=2);open(path,'a').write('\n')"`, token)
	args := []string{"-o", "ConnectTimeout=5", "-p", strconv.Itoa(port)}
	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}
	args = append(args, host, script)
	cmd := exec.Command("ssh", args...)
	return cmd.Run()
}

func rotateToken(existingToken string) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("get home dir: %v", err)
	}

	cfgPath := home + "/.agentgw/config.json"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	newToken := existingToken
	if newToken == "" {
		newToken = generateToken()
		fmt.Printf("Generated new token: %s\n", newToken)
	} else {
		fmt.Printf("Using provided token: %s\n", newToken)
	}

	// Update config
	cfg.Token = newToken
	for i := range cfg.Nodes {
		cfg.Nodes[i].Token = newToken
	}

	// Save config
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Fatalf("marshal config: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		log.Fatalf("write config: %v", err)
	}

	fmt.Println("Updated agentgw config")

	// Update local agentd config
	localAgentdConfig := home + "/.agentd/config.json"
	if err := updateAgentdToken(localAgentdConfig, newToken); err != nil {
		log.Printf("warn: failed to update local agentd token: %v", err)
	} else {
		fmt.Println("Updated local agentd config")
	}

	// Update remote agentd configs
	for _, n := range cfg.Nodes {
		if n.Host == "localhost" || n.Host == "127.0.0.1" {
			continue
		}
		target := n.Host
		if n.SSHAlias != "" {
			target = n.SSHAlias
		}
		port := n.SSHPort
		if port == 0 {
			port = 22
		}
		if err := sshUpdateAgentdToken(target, port, n.SSHKeyPath, newToken); err != nil {
			log.Printf("warn: failed to update remote agentd token for %s: %v", target, err)
		} else {
			fmt.Printf("Updated remote agentd config for %s\n", target)
		}
	}

	fmt.Println("\nToken rotated. Restart services to apply:")
	fmt.Println("  ./scripts/install.sh restart")
}

// staticHandler wraps a http.FileServer and serves static files.
// For SPA (Single Page Application) support, non-existent paths fall back to index.html.
func staticHandler(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || path == "/index.html" {
			indexPath := filepath.Join(root, "index.html")
			if data, err := os.ReadFile(indexPath); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(data)
				return
			}
		}
		// SPA fallback: serve index.html for non-existent paths (Flutter Web deep links)
		cleanPath := filepath.Clean(path)
		if cleanPath == "." || cleanPath == "/" {
			cleanPath = "index.html"
		} else if strings.HasPrefix(cleanPath, "/") {
			cleanPath = cleanPath[1:]
		}
		filePath := filepath.Join(root, cleanPath)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			indexPath := filepath.Join(root, "index.html")
			if data, err := os.ReadFile(indexPath); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(data)
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

// withCORS wraps an http.HandlerFunc and adds CORS headers for cross-origin requests.
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// registerQRHandler registers GET /qr.png which returns a PNG QR code image.
// Query param ?type=local generates a QR for ws://<localIP>:<port>/ws|<token>.
// Query param ?type=remote generates a QR for the remote tunnel URL.
func registerQRHandler(mux *http.ServeMux, port int, token string, tunnelURL, tunnelToken, appURL, userID string) {
	mux.HandleFunc("/qr.png", withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		qrType := r.URL.Query().Get("type")
		var content string
		switch qrType {
		case "local":
			localIP := getLocalIP()
			if localIP == "" {
				http.Error(w, "unable to determine local IP", http.StatusInternalServerError)
				return
			}
			content = fmt.Sprintf("ws://%s:%d/ws|%s", localIP, port, token)
		case "remote":
			if tunnelURL == "" {
				http.Error(w, "tunnel not configured", http.StatusNotFound)
				return
			}
			remoteURL := buildRemoteQRURL(tunnelURL, appURL, userID)
			if remoteURL == "" {
				http.Error(w, "unable to build remote URL", http.StatusInternalServerError)
				return
			}
			content = remoteURL + "|" + tunnelToken
		default:
			http.Error(w, "invalid type parameter", http.StatusBadRequest)
			return
		}
		qr, err := qrcode.New(content, qrcode.Medium)
		if err != nil {
			http.Error(w, "failed to generate QR code", http.StatusInternalServerError)
			return
		}
		pngBytes, err := qr.PNG(256)
		if err != nil {
			http.Error(w, "failed to encode QR code", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
}

func runServer(tunnelURLFlag, tunnelTokenFlag, hubBaseFlag, appURLFlag string, realityCfg *tunnel.RealityConfig, showQR bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("get home dir: %v", err)
	}

	cfgPath := home + "/.agentgw/config.json"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Use config.json as the unified nodes store when nodes_file is not explicitly set.
	nodesFile := cfg.NodesFile
	if nodesFile == "" {
		nodesFile = cfgPath
	}
	store := nodecfg.New(nodesFile)

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

	// Merge static nodes from config.json with persisted nodes, deduplicating by host/alias.
	allEntries := dedupNodeEntries(append(entries, cfg.Nodes...))
	mgr.LoadAll(allEntries)

	srv := ws.New(mgr, cfg.Token)

	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := listenWithInherit(addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	gatewayRestartFn := func() error {
		return restartWithListener(ln)
	}
	srv.SetGatewayRestartFunc(gatewayRestartFn)

	if cfg.Upgrade.ManifestURL != "" {
		srv.SetUpgradeService(&upgrade.Service{
			ManifestURL: cfg.Upgrade.ManifestURL,
			Manager:     mgr,
			RestartFn:   gatewayRestartFn,
			NowVersion: func() string {
				return Version
			},
			Executable: os.Executable,
		})
		log.Printf("package upgrade enabled with manifest: %s", cfg.Upgrade.ManifestURL)
	}

	mgr.OnEvent(func(nodeID string, ev map[string]any) {
		method, _ := ev["method"].(string)
		params := ev["params"]
		srv.Broadcast(ws.RPCEvent{JSONRPC: "2.0", Method: method, Params: params})
	})

	mgr.ConnectAll()
	mgr.StartHealthCheck()

	// Start reverse tunnel client if configured.
	// Tunnel token is embedded into the URL query so that URL + token
	// travel together as a single unit (e.g. ?token=xxx).
	tunnelURL := tunnelURLFlag
	if tunnelURL == "" {
		tunnelURL = os.Getenv("AGENTGW_TUNNEL_URL")
	}

	// Resolve tunnel credentials: prefer local_auth.json, then oauth.json, then config.json tunnel section.
	var localAuth *oauth.LocalAuth
	if la, err := oauth.LoadLocalAuth(oauth.LocalAuthFilePath()); err == nil {
		localAuth = la
	}

	// Resolve tunnel token from any available source.
	tunnelToken := ""
	if localAuth != nil && localAuth.Token != "" {
		tunnelToken = localAuth.Token
	} else if lr, err := oauth.LoadLoginResult(oauth.TokenFilePath()); err == nil && lr.AccessToken != "" {
		tunnelToken = lr.AccessToken
	} else {
		// Fallback to config.json tunnel section
		tunnelToken, _ = readTunnelAuthFromConfig(cfgPath)
	}

	// Resolve tunnel userId from any available source.
	tunnelUserID := ""
	if localAuth != nil && localAuth.UserID != "" {
		tunnelUserID = localAuth.UserID
	} else {
		_, tunnelUserID = readTunnelAuthFromConfig(cfgPath)
	}

	// Auto-construct tunnel URL from --hub if --tunnel-url not given.
	// We only need a hub base and a valid token; local_auth.json is not required.
	hubBase := hubBaseFlag
	if hubBase == "" {
		hubBase = os.Getenv("AGENTGW_HUB")
	}
	if tunnelURL == "" && hubBase != "" && tunnelToken != "" {
		tunnelURL = strings.TrimRight(hubBase, "/")
		log.Printf("[agentgw] constructed tunnel URL: %s", tunnelURL)
	}

	// Also allow hub_url from config to trigger tunnel when no --hub flag is given.
	if tunnelURL == "" && cfg.Tunnel.HubURL != "" && tunnelToken != "" {
		tunnelURL = strings.TrimRight(cfg.Tunnel.HubURL, "/")
		log.Printf("[agentgw] constructed tunnel URL from config: %s", tunnelURL)
	}

	if tunnelURL != "" {
		if normalized, err := normalizePublicHubURL(tunnelURL); err == nil {
			tunnelURL = normalized
			log.Printf("[agentgw] normalized tunnel URL: %s", tunnelURL)
		} else {
			log.Printf("[agentgw] tunnel URL normalization skipped: %v", err)
		}
	}

	// Auto-construct app URL from hub base if not given.
	if appURLFlag == "" && hubBase != "" {
		appURLFlag = strings.TrimRight(hubBase, "/")
		log.Printf("[agentgw] constructed app URL: %s", appURLFlag)
	}
	if appURLFlag == "" && cfg.Tunnel.AppURL != "" {
		appURLFlag = strings.TrimRight(cfg.Tunnel.AppURL, "/")
		log.Printf("[agentgw] constructed app URL from config: %s", appURLFlag)
	}
	if appURLFlag == "" {
		appURLFlag = tunnelURL
	}
	if appURLFlag != "" {
		if normalized, err := normalizePublicHubURL(appURLFlag); err == nil {
			appURLFlag = normalized
			log.Printf("[agentgw] normalized app URL: %s", appURLFlag)
		} else {
			log.Printf("[agentgw] app URL normalization skipped: %v", err)
		}
	}

	localAddr := fmt.Sprintf("localhost:%d", cfg.Port)
	if realityCfg != nil {
		currentRealityCfg = realityCfg
		log.Printf("[agentgw] REALITY enabled (sni=%s)", realityCfg.ServerName)
	}
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

	// Status endpoint for gw status command.
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		nodes := mgr.List()
		nodeStats := make([]map[string]any, 0, len(nodes))
		statusCounts := map[string]int{"disconnected": 0, "connecting": 0, "connected": 0, "deploying": 0, "error": 0}
		for _, n := range nodes {
			statusCounts[string(n.GetStatus())]++
			nodeStats = append(nodeStats, map[string]any{
				"id":       n.ID,
				"name":     n.Name,
				"host":     n.Host,
				"location": n.DisplayLocation(),
				"status":   string(n.GetStatus()),
			})
		}
		tunnelStatus := map[string]any(nil)
		if currentTunnelURL != "" {
			tunnelStatus = map[string]any{"connected": false}
			if activeTunnel != nil {
				s := activeTunnel.Status()
				tunnelStatus = map[string]any{
					"connected":               s.Connected,
					"connectedAt":             s.ConnectedAt,
					"lastHandshakeDurationMs": s.LastHandshakeDuration.Milliseconds(),
					"lastHandshakeAt":         s.LastHandshakeAt,
					"lastCommunicationAt":     s.LastCommunicationAt,
					"lastDisconnectedAt":      s.LastDisconnectedAt,
					"lastError":               s.LastError,
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"version":       Version,
			"uptimeSeconds": int(srv.Uptime().Seconds()),
			"port":          cfg.Port,
			"clientCount":   srv.ClientCount(),
			"nodeCount":     len(nodes),
			"statusCounts":  statusCounts,
			"nodes":         nodeStats,
			"tunnelUrl":     currentTunnelURL,
			"tunnelStatus":  tunnelStatus,
		})
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

	userID := tunnelUserID

	// Register QR image endpoint for portal.
	registerQRHandler(http.DefaultServeMux, cfg.Port, cfg.Token, tunnelURL, tunnelToken, appURLFlag, userID)

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

func dedupNodeEntries(entries []nodecfg.NodeEntry) []nodecfg.NodeEntry {
	seenHost := make(map[string]bool)
	seenAlias := make(map[string]bool)
	result := make([]nodecfg.NodeEntry, 0, len(entries))
	for _, e := range entries {
		if e.Host != "" && seenHost[e.Host] {
			continue
		}
		if e.SSHAlias != "" && seenAlias[e.SSHAlias] {
			continue
		}
		if e.Host != "" {
			seenHost[e.Host] = true
		}
		if e.SSHAlias != "" {
			seenAlias[e.SSHAlias] = true
		}
		result = append(result, e)
	}
	return result
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

func normalizePublicHubURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Scheme == "ws" {
		u.Scheme = "http"
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("missing hostname")
	}
	u.User = nil
	u.Host = host
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
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

	// Strip port so the app connects on default HTTPS port (443) through Caddy,
	// not directly to the tunnel port (e.g. 8443).
	host := u.Hostname()

	return fmt.Sprintf("%s://%s/ws/%s", scheme, host, userID)
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
	cfgPath := home + "/.agentgw/config.json"
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
	_, userID := readTunnelAuthFromConfig(cfgPath)
	if userID == "" {
		if la, err := oauth.LoadLocalAuth(oauth.LocalAuthFilePath()); err == nil {
			userID = la.UserID
		}
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

func showStatus() {
	cfgPath := configLoadPath
	if cfgPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("get home dir: %v", err)
		}
		cfgPath = home + "/.agentgw/config.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	url := fmt.Sprintf("http://localhost:%d/status", cfg.Port)
	bodyBytes, statusCode, err := statusHTTPGet(url, cfg.Token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentgw is not running (could not connect to port %d)\n", cfg.Port)
		os.Exit(1)
	}
	if statusCode == http.StatusUnauthorized {
		fmt.Fprintln(os.Stderr, "unauthorized: token mismatch")
		os.Exit(1)
	}
	if statusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "agentgw returned status %d\n", statusCode)
		os.Exit(1)
	}

	var body struct {
		Version       string         `json:"version"`
		UptimeSeconds int            `json:"uptimeSeconds"`
		Port          int            `json:"port"`
		ClientCount   int            `json:"clientCount"`
		NodeCount     int            `json:"nodeCount"`
		StatusCounts  map[string]int `json:"statusCounts"`
		Nodes         []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Host     string `json:"host"`
			Location string `json:"location"`
			Status   string `json:"status"`
		} `json:"nodes"`
		TunnelURL    string            `json:"tunnelUrl"`
		TunnelStatus *tunnelStatusBody `json:"tunnelStatus"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		log.Fatalf("decode response: %v", err)
	}

	fmt.Printf("agentgw %s (port %d)\n", body.Version, body.Port)
	fmt.Printf("uptime:   %s\n", formatDuration(time.Duration(body.UptimeSeconds)*time.Second))
	fmt.Printf("clients:  %d\n", body.ClientCount)
	fmt.Printf("nodes:    %d total", body.NodeCount)
	if body.NodeCount > 0 {
		parts := []string{}
		for _, s := range []string{"connected", "connecting", "disconnected", "error", "deploying"} {
			if c := body.StatusCounts[s]; c > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", s, c))
			}
		}
		fmt.Printf(" (%s)\n", strings.Join(parts, ", "))
	} else {
		fmt.Println()
	}
	if body.TunnelURL != "" {
		fmt.Printf("tunnel:   %s\n", body.TunnelURL)
		if body.TunnelStatus != nil {
			state := "disconnected"
			if body.TunnelStatus.Connected {
				state = "connected"
			}
			fmt.Printf("state:    %s\n", state)
			if body.TunnelStatus.LastHandshakeDurationMs > 0 {
				fmt.Printf("handshake:%dms\n", body.TunnelStatus.LastHandshakeDurationMs)
			}
			if !body.TunnelStatus.LastCommunicationAt.IsZero() {
				fmt.Printf("last comm:%s\n", body.TunnelStatus.LastCommunicationAt.Format(time.RFC3339))
			}
			if !body.TunnelStatus.ConnectedAt.IsZero() {
				fmt.Printf("connected:%s\n", body.TunnelStatus.ConnectedAt.Format(time.RFC3339))
			}
			if !body.TunnelStatus.LastDisconnectedAt.IsZero() {
				fmt.Printf("last disc:%s\n", body.TunnelStatus.LastDisconnectedAt.Format(time.RFC3339))
			}
			if body.TunnelStatus.LastError != "" {
				fmt.Printf("last err: %s\n", body.TunnelStatus.LastError)
			}
		}
	}
	if len(body.Nodes) > 0 {
		fmt.Println()
		fmt.Println("Nodes:")
		for _, n := range body.Nodes {
			fmt.Printf("  %-12s %-20s %-15s %s\n", n.ID[:8]+"...", n.Name, n.Location, n.Status)
		}
	}
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
