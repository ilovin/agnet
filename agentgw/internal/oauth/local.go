package oauth

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalAuth holds user-generated credentials for LAN/simple mode.
type LocalAuth struct {
	UserID string `json:"userId"`
	Token  string `json:"token"`
}

// LocalAuthFilePath returns the path where local auth is persisted.
func LocalAuthFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentgw", "local_auth.json")
}

// Save persists the local auth to disk.
func (a *LocalAuth) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(a)
}

// LoadLocalAuth reads the persisted local auth.
func LoadLocalAuth(path string) (*LocalAuth, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var a LocalAuth
	if err := json.NewDecoder(f).Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}

// RandomToken generates a secure random hex token.
func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// DoLocalLogin registers with tunnelhub if hubURL is provided, otherwise generates local credentials.
// userId defaults to the output of `whoami`.
//
// Idempotent: if local_auth.json already contains the same userId, it is returned
// immediately without contacting the hub again. If the hub reports the userId is
// already registered but we have no local record, the user is prompted to retry
// with a different name.
func DoLocalLogin(hubURL string) (*LocalAuth, error) {
	userID := whoami()

	for {
		fmt.Printf("Enter userId (default: %s): ", userID)
		var input string
		fmt.Scanln(&input)
		if input != "" {
			userID = input
		}

		// Idempotency: reuse local registration for the same userId
		path := LocalAuthFilePath()
		if existing, err := LoadLocalAuth(path); err == nil && existing.UserID == userID {
			return existing, nil
		}

		var token string
		if hubURL != "" {
			t, err := registerWithHub(hubURL, userID)
			if err != nil {
				if strings.Contains(err.Error(), "already registered") {
					fmt.Printf("userId %q already registered on hub. Try a different one? (y/n): ", userID)
					var retry string
					fmt.Scanln(&retry)
					if strings.ToLower(retry) == "y" || strings.ToLower(retry) == "yes" {
						continue
					}
					// User wants to keep this userId — allow manual token recovery
					fmt.Printf("Enter the existing token for %q (or press Enter to skip): ", userID)
					var existingToken string
					fmt.Scanln(&existingToken)
					if existingToken != "" {
						auth := &LocalAuth{UserID: userID, Token: existingToken}
						if err := auth.Save(path); err != nil {
							return nil, fmt.Errorf("save local auth: %w", err)
						}
						return auth, nil
					}
				}
				return nil, err
			}
			token = t
		} else {
			token = RandomToken()
		}

		auth := &LocalAuth{UserID: userID, Token: token}
		if err := auth.Save(path); err != nil {
			return nil, fmt.Errorf("save local auth: %w", err)
		}
		return auth, nil
	}
}

func whoami() string {
	out, err := exec.Command("whoami").Output()
	if err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "default"
}

// DoLocalLogout interactively unregisters a user from tunnelhub and deletes local_auth.json.
// GW must pass token verification to operate — the token is required and sent to the hub
// for validation (unless the hub is running in local/super-admin mode).
func DoLocalLogout(hubURL string) error {
	fmt.Printf("Enter userId to unregister (default: %s): ", whoami())
	var userID string
	fmt.Scanln(&userID)
	if userID == "" {
		userID = whoami()
	}

	fmt.Print("Enter token: ")
	var token string
	fmt.Scanln(&token)
	if token == "" {
		return fmt.Errorf("token is required")
	}

	if hubURL != "" {
		if err := unregisterFromHub(hubURL, userID, token); err != nil {
			return fmt.Errorf("hub unregistration failed: %w", err)
		}
		fmt.Println("Hub unregistration succeeded.")
	}

	// Also delete local auth if it matches the unregistered user.
	path := LocalAuthFilePath()
	if auth, err := LoadLocalAuth(path); err == nil && auth.UserID == userID {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove local auth: %w", err)
		}
		fmt.Printf("Removed local auth for user=%s\n", userID)
	}
	return nil
}

func unregisterFromHub(hubURL, userID, token string) error {
	base := hubURL
	base = strings.Replace(base, "wss://", "https://", 1)
	base = strings.Replace(base, "ws://", "http://", 1)
	if idx := strings.Index(base, "/tunnel"); idx != -1 {
		base = base[:idx]
	}
	if idx := strings.Index(base, "?"); idx != -1 {
		base = base[:idx]
	}
	unregisterURL := base + "/unregister"

	body, _ := json.Marshal(map[string]string{"userId": userID, "token": token})
	req, err := http.NewRequest("POST", unregisterURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("unregister request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unregister failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func registerWithHub(hubURL, userID string) (string, error) {
	// hubURL is like wss://domain:8443/api/v1/stream
	// We need to POST to https://domain:8443/register
	base := hubURL
	base = strings.Replace(base, "wss://", "https://", 1)
	base = strings.Replace(base, "ws://", "http://", 1)
	// Strip path and query
	if idx := strings.Index(base, "/api/"); idx != -1 {
		base = base[:idx]
	}
	if idx := strings.Index(base, "?"); idx != -1 {
		base = base[:idx]
	}
	registerURL := base + "/register"

	body, _ := json.Marshal(map[string]string{"userId": userID})
	resp, err := http.Post(registerURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("register request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return "", fmt.Errorf("userId %q already registered on hub", userID)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("register failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse register response: %w", err)
	}
	return result.Token, nil
}
