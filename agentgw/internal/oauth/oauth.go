package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	AuthorizeURL string
	TokenURL     string
	ProfileURL   string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

func DefaultConfig() Config {
	return Config{
		AuthorizeURL: getEnv("OPENSSO_AUTHORIZE_URL", "https://signin.nio.com/oauth2/authorize"),
		TokenURL:     getEnv("OPENSSO_TOKEN_URL", "https://signin.nio.com/oauth2/accessToken"),
		ProfileURL:   getEnv("OPENSSO_PROFILE_URL", "https://signin.nio.com/oauth2/profile"),
		ClientID:     os.Getenv("OPENSSO_CLIENT_ID"),
		ClientSecret: os.Getenv("OPENSSO_CLIENT_SECRET"),
		RedirectURI:  getEnv("OPENSSO_REDIRECT_URI", "http://localhost:8384/callback"),
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func randomState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (c Config) AuthorizeURLWithState(state string) string {
	u, _ := url.Parse(c.AuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURI)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

// LoginResult holds the outcome of the OAuth2 flow.
type LoginResult struct {
	UserID       string `json:"userId"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
}

// DoLogin starts a local HTTP server, prints the authorize URL, waits for the
// browser callback, exchanges the code for tokens, and fetches the user profile.
func DoLogin(cfg Config) (*LoginResult, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("OPENSSO_CLIENT_ID is not set")
	}

	state := randomState()
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	ln, err := net.Listen("tcp", cfg.listenAddr())
	if err != nil {
		return nil, fmt.Errorf("listen callback server: %w", err)
	}

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			errCh <- fmt.Errorf("oauth error: %s", e)
			fmt.Fprintf(w, "Login error: %s", e)
			return
		}
		if s := q.Get("state"); s != state {
			errCh <- fmt.Errorf("state mismatch")
			fmt.Fprint(w, "State mismatch")
			return
		}
		code := q.Get("code")
		if code == "" {
			errCh <- fmt.Errorf("missing code")
			fmt.Fprint(w, "Missing authorization code")
			return
		}
		codeCh <- code
		fmt.Fprint(w, "<h1>Login successful!</h1><p>You can close this tab and return to the terminal.</p>")
	})}

	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	defer ln.Close()

	fmt.Println("\nPlease open the following URL in your browser and sign in:")
	fmt.Println(cfg.AuthorizeURLWithState(state))
	fmt.Println()

	select {
	case code := <-codeCh:
		return cfg.exchangeAndProfile(code)
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("login timeout")
	}
}

func (c Config) listenAddr() string {
	u, err := url.Parse(c.RedirectURI)
	if err == nil && u.Host != "" {
		if strings.HasPrefix(u.Host, ":") {
			return "localhost" + u.Host
		}
		return u.Host
	}
	return "localhost:8384"
}

func (c Config) exchangeAndProfile(code string) (*LoginResult, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", c.RedirectURI)
	data.Set("client_id", c.ClientID)
	if c.ClientSecret != "" {
		data.Set("client_secret", c.ClientSecret)
	}

	req, err := http.NewRequest("POST", c.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		// NIO may use camelCase
		AccessTokenC  string `json:"accessToken"`
		RefreshTokenC string `json:"refreshToken"`
		ExpiresInC    int    `json:"expiresIn"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	accessToken := tokenResp.AccessToken
	if accessToken == "" {
		accessToken = tokenResp.AccessTokenC
	}
	refreshToken := tokenResp.RefreshToken
	if refreshToken == "" {
		refreshToken = tokenResp.RefreshTokenC
	}
	expiresIn := tokenResp.ExpiresIn
	if expiresIn == 0 {
		expiresIn = tokenResp.ExpiresInC
	}

	if accessToken == "" {
		return nil, fmt.Errorf("token response did not contain access_token")
	}

	profile, err := c.fetchProfile(accessToken)
	if err != nil {
		return nil, fmt.Errorf("fetch profile: %w", err)
	}

	return &LoginResult{
		UserID:       profile.UserID,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	}, nil
}

type Profile struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Email    string `json:"email"`
	NIOID    string `json:"nioId"`
	// Fallback for any other common field names
	UserIDC  string `json:"user_id"`
	EmailC   string `json:"mail"`
}

func (c Config) fetchProfile(accessToken string) (*Profile, error) {
	data := url.Values{}
	data.Set("accessToken", accessToken)

	req, err := http.NewRequest("POST", c.ProfileURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile request failed (%d): %s", resp.StatusCode, string(body))
	}

	var p Profile
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}

	// Resolve the most appropriate user identifier.
	if p.UserID == "" {
		p.UserID = p.UserIDC
	}
	if p.UserID == "" {
		p.UserID = p.Username
	}
	if p.UserID == "" {
		p.UserID = p.NIOID
	}
	if p.UserID == "" {
		p.UserID = p.Email
	}
	if p.UserID == "" {
		p.UserID = p.EmailC
	}
	if p.UserID == "" {
		return nil, fmt.Errorf("profile response did not contain a recognizable user id: %s", string(body))
	}

	return &p, nil
}

// TokenFilePath returns the path where oauth tokens are persisted.
func TokenFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentgw", "oauth.json")
}

// Save persists the login result to disk.
func (r *LoginResult) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(r)
}

// LoadLoginResult reads the persisted login result.
func LoadLoginResult(path string) (*LoginResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r LoginResult
	if err := json.NewDecoder(f).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}
