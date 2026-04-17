package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/phone-talk/tunnelhub/internal/hub"
	"github.com/phone-talk/tunnelhub/internal/sso"
)

func parseUsers(s string) map[string]string {
	users := make(map[string]string)
	s = strings.TrimSpace(s)
	if s == "" {
		return users
	}
	pairs := strings.Split(s, ";")
	for _, p := range pairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 2 {
			users[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return users
}

func main() {
	users := parseUsers(os.Getenv("USERS"))
	if len(users) == 0 {
		secret := os.Getenv("TUNNEL_SECRET")
		if secret == "" {
			secret = "dev-secret-change-me"
			log.Println("Warning: using default TUNNEL_SECRET=dev-secret-change-me")
		}
		users = map[string]string{"default": secret}
	}

	var validator *sso.Validator
	if os.Getenv("OPENSSO_PROFILE_URL") != "" {
		validator = sso.NewValidator()
		log.Println("[tunnelhub] OpenSSO validation enabled")
	}

	h := hub.New(users, validator)

	// agentgw local outbound connections
	http.HandleFunc("/tunnel/register", h.RegisterTunnel)

	// agentapp inbound connections
	http.HandleFunc("/ws/", h.BridgeApp)

	// health probe for orchestrators
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "7374"
	}
	addr := ":" + port
	log.Printf("tunnelhub listening on %s", addr)
	log.Fatalf("serve: %v", http.ListenAndServe(addr, nil))
}
