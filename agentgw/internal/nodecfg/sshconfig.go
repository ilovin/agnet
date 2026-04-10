package nodecfg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHHost represents a parsed SSH config host entry.
type SSHHost struct {
	Name       string   // Host alias (e.g., "ws", "a100")
	HostName   string   // Actual hostname/IP
	User       string   // SSH user
	Port       int      // SSH port (default 22)
	IdentityFile string // SSH key path
	ProxyCommand string // Proxy command if any
}

// ParseSSHConfig parses ~/.ssh/config and returns all Host entries.
func ParseSSHConfig() ([]SSHHost, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	configPath := filepath.Join(home, ".ssh", "config")
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []SSHHost{}, nil
		}
		return nil, fmt.Errorf("open ssh config: %w", err)
	}
	defer file.Close()

	var hosts []SSHHost
	var current *SSHHost

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split into fields
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		key := strings.ToLower(fields[0])
		value := strings.Join(fields[1:], " ")

		switch key {
		case "host":
			// Save previous host if exists
			if current != nil && current.Name != "" && current.HostName != "" {
				hosts = append(hosts, *current)
			}
			// Start new host - only take the first name, skip wildcards
			hostNames := strings.Fields(value)
			if len(hostNames) > 0 && !strings.Contains(hostNames[0], "*") && !strings.Contains(hostNames[0], "?") {
				current = &SSHHost{
					Name: hostNames[0],
					Port: 22,
				}
			} else {
				current = nil // Skip wildcard entries
			}
		case "hostname":
			if current != nil {
				current.HostName = value
			}
		case "user":
			if current != nil {
				current.User = value
			}
		case "port":
			if current != nil {
				if port, err := strconv.Atoi(value); err == nil {
					current.Port = port
				}
			}
		case "identityfile":
			if current != nil {
				// Expand ~ to home directory
				if strings.HasPrefix(value, "~") {
					value = filepath.Join(home, value[1:])
				}
				current.IdentityFile = value
			}
		case "proxycommand":
			if current != nil {
				current.ProxyCommand = value
			}
		}
	}

	// Don't forget the last host
	if current != nil && current.Name != "" && current.HostName != "" {
		hosts = append(hosts, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan ssh config: %w", err)
	}

	return hosts, nil
}

// FilterHostsWithoutAgentd returns hosts that likely don't have agentd running.
// It filters out hosts that are already configured as nodes.
func FilterHostsWithoutAgentd(hosts []SSHHost, existingNodes []NodeEntry) []SSHHost {
	existing := make(map[string]bool)
	for _, node := range existingNodes {
		existing[node.Host] = true
		if node.SSHAlias != "" {
			existing[node.SSHAlias] = true
		}
	}

	var filtered []SSHHost
	for _, h := range hosts {
		if !existing[h.HostName] && !existing[h.Name] {
			filtered = append(filtered, h)
		}
	}
	return filtered
}
