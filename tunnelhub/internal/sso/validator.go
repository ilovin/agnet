package sso

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var defaultProfileURL = "https://signin.nio.com/oauth2/profile"

func profileURL() string {
	if v := os.Getenv("OPENSSO_PROFILE_URL"); v != "" {
		return v
	}
	return defaultProfileURL
}

// Profile holds the parsed OpenSSO profile response.
type Profile struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Email    string `json:"email"`
	NIOID    string `json:"nioId"`
	UserIDC  string `json:"user_id"`
	EmailC   string `json:"mail"`
}

func (p *Profile) ResolveUserID() string {
	if p.UserID != "" {
		return p.UserID
	}
	if p.Username != "" {
		return p.Username
	}
	if p.NIOID != "" {
		return p.NIOID
	}
	if p.Email != "" {
		return p.Email
	}
	if p.UserIDC != "" {
		return p.UserIDC
	}
	if p.EmailC != "" {
		return p.EmailC
	}
	return ""
}

type cachedProfile struct {
	profile   *Profile
	expiresAt time.Time
}

// Validator validates OpenSSO access tokens by calling the profile API.
type Validator struct {
	mu      sync.RWMutex
	cache   map[string]cachedProfile
	ttl     time.Duration
	client  *http.Client
	profile string
}

func NewValidator() *Validator {
	return &Validator{
		cache:   make(map[string]cachedProfile),
		ttl:     5 * time.Minute,
		client:  &http.Client{Timeout: 10 * time.Second},
		profile: profileURL(),
	}
}

func (v *Validator) Validate(token string) (*Profile, error) {
	if token == "" {
		return nil, fmt.Errorf("missing token")
	}

	v.mu.RLock()
	cp, ok := v.cache[token]
	v.mu.RUnlock()
	if ok && time.Now().Before(cp.expiresAt) {
		return cp.profile, nil
	}

	p, err := v.fetch(token)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	v.cache[token] = cachedProfile{profile: p, expiresAt: time.Now().Add(v.ttl)}
	v.mu.Unlock()
	return p, nil
}

func (v *Validator) fetch(token string) (*Profile, error) {
	data := url.Values{}
	data.Set("accessToken", token)
	req, err := http.NewRequest("POST", v.profile, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("profile request: %w", err)
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

	uid := p.ResolveUserID()
	if uid == "" {
		return nil, fmt.Errorf("profile response did not contain a recognizable user id: %s", string(body))
	}
	p.UserID = uid
	return &p, nil
}
