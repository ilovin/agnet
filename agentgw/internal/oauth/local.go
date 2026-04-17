package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// DoLocalLogin prompts for a userID and generates a random token.
func DoLocalLogin() (*LocalAuth, error) {
	userID := os.Getenv("USER")
	if userID == "" {
		userID = "default"
	}
	fmt.Printf("Enter userId (default: %s): ", userID)
	var input string
	fmt.Scanln(&input)
	if input != "" {
		userID = input
	}

	token := RandomToken()
	auth := &LocalAuth{UserID: userID, Token: token}
	path := LocalAuthFilePath()
	if err := auth.Save(path); err != nil {
		return nil, fmt.Errorf("save local auth: %w", err)
	}
	return auth, nil
}
