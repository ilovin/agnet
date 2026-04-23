package main

import (
	"encoding/json"
	"os"
)

// readTunnelAuthFromConfig reads tunnel token and userId from config.json
// as a fallback when local_auth.json does not exist.
func readTunnelAuthFromConfig(cfgPath string) (token, userID string) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", ""
	}
	var raw struct {
		Tunnel struct {
			Token  string `json:"token"`
			UserID string `json:"user_id"`
		} `json:"tunnel"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", ""
	}
	return raw.Tunnel.Token, raw.Tunnel.UserID
}
