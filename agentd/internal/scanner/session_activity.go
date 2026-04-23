package scanner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SessionCandidate represents a candidate session file with its activity time.
type SessionCandidate struct {
	SessionID    string
	JSONLPath    string
	LastActivity time.Time
}

// getLastActivityTime reads the last few lines of a JSONL file and extracts
// the timestamp of the most recent message. Falls back to file mtime.
func getLastActivityTime(jsonlPath string) time.Time {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()

	// Read the last 8KB of the file to find recent timestamps
	info, err := f.Stat()
	if err != nil {
		return time.Time{}
	}
	tailSize := int64(8192)
	if info.Size() < tailSize {
		tailSize = info.Size()
	}
	buf := make([]byte, tailSize)
	if _, err := f.ReadAt(buf, info.Size()-tailSize); err != nil {
		return time.Time{}
	}

	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var obj struct {
			Timestamp string `json:"timestamp"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if obj.Timestamp == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, obj.Timestamp); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, obj.Timestamp); err == nil {
			return t
		}
	}

	return info.ModTime()
}

// getPaneLastActivity returns the last activity time of a tmux pane, or nil if unavailable.
func getPaneLastActivity(tmuxTarget string) *time.Time {
	if tmuxTarget == "" {
		return nil
	}
	cmd := exec.Command("tmux", "display", "-t", tmuxTarget, "-p", "#{pane_activity}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "0" {
		return nil
	}
	epoch, err := strconv.ParseInt(s, 10, 64)
	if err != nil || epoch <= 0 {
		return nil
	}
	t := time.Unix(epoch, 0)
	return &t
}

// listSessionCandidates lists all .jsonl files in projectDir with their activity times.
func listSessionCandidates(projectDir string) []SessionCandidate {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}
	var candidates []SessionCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		jsonlPath := filepath.Join(projectDir, entry.Name())
		candidates = append(candidates, SessionCandidate{
			SessionID:    strings.TrimSuffix(entry.Name(), ".jsonl"),
			JSONLPath:    jsonlPath,
			LastActivity: getLastActivityTime(jsonlPath),
		})
	}
	return candidates
}

// filterByPaneActivity filters candidates whose lastActivity is close to paneActivity.
// tolerance is the maximum allowed time difference.
// Returns filtered candidates sorted by time proximity (closest first).
// If paneActivity is nil, returns all candidates sorted by lastActivity descending.
func filterByPaneActivity(candidates []SessionCandidate, paneActivity *time.Time, tolerance time.Duration) []SessionCandidate {
	if paneActivity == nil {
		// No pane activity available, sort by lastActivity descending
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].LastActivity.After(candidates[j].LastActivity)
		})
		return candidates
	}

	var filtered []SessionCandidate
	for _, c := range candidates {
		diff := c.LastActivity.Sub(*paneActivity)
		if diff < 0 {
			diff = -diff
		}
		if diff <= tolerance {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		// Tolerance too strict, return all sorted by lastActivity
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].LastActivity.After(candidates[j].LastActivity)
		})
		return candidates
	}

	// Sort by time proximity to paneActivity (closest first)
	pa := *paneActivity
	sort.Slice(filtered, func(i, j int) bool {
		di := filtered[i].LastActivity.Sub(pa)
		if di < 0 {
			di = -di
		}
		dj := filtered[j].LastActivity.Sub(pa)
		if dj < 0 {
			dj = -dj
		}
		return di < dj
	})

	return filtered
}
