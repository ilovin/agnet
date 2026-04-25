package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phone-talk/agentgw/internal/node"
)

type Service struct {
	ManifestURL string
	Manager     *node.Manager
	RestartFn   func() error
	NowVersion  func() string
	Executable  func() (string, error)
}

type CheckResult struct {
	CurrentVersion string `json:"currentVersion"`
	TargetVersion  string `json:"targetVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	ManifestURL    string `json:"manifestUrl"`
	IncludesApp    bool   `json:"includesApp"`
}

type ApplyResult struct {
	PreviousVersion string   `json:"previousVersion"`
	TargetVersion   string   `json:"targetVersion"`
	UpgradedGateway bool     `json:"upgradedGateway"`
	UpgradedNodes   []string `json:"upgradedNodes"`
	SkippedAPK      bool     `json:"skippedApk"`
}

func (s *Service) Check() (*CheckResult, error) {
	client := NewClient(s.ManifestURL)
	manifest, err := client.FetchManifest()
	if err != nil {
		return nil, err
	}
	current := ""
	if s.NowVersion != nil {
		current = s.NowVersion()
	}
	hasUpdate := manifest.Version != "" && manifest.Version != current
	includesApp := manifest.Components.AgentApp.APK != nil || manifest.Components.AgentApp.IPA != nil
	return &CheckResult{
		CurrentVersion: current,
		TargetVersion:  manifest.Version,
		HasUpdate:      hasUpdate,
		ManifestURL:    s.ManifestURL,
		IncludesApp:    includesApp,
	}, nil
}

func (s *Service) Apply() (*ApplyResult, error) {
	return s.apply(true)
}

func (s *Service) ApplyWithoutRestart() (*ApplyResult, error) {
	return s.apply(false)
}

func (s *Service) apply(restart bool) (*ApplyResult, error) {
	if s.Manager == nil {
		return nil, fmt.Errorf("node manager is required")
	}
	if restart && s.RestartFn == nil {
		return nil, fmt.Errorf("gateway restart is not configured")
	}
	client := NewClient(s.ManifestURL)
	manifest, err := client.FetchManifest()
	if err != nil {
		return nil, err
	}

	baseURL := manifestBaseURL(s.ManifestURL)
	osName, arch := LocalRuntime()
	gwAsset := manifest.FindAsset("agentgw", osName, arch)
	if gwAsset == nil {
		return nil, fmt.Errorf("agentgw asset not found for %s/%s", osName, arch)
	}
	agentdAsset := manifest.FindAsset("agentd", "linux", "amd64")
	if agentdAsset == nil {
		return nil, fmt.Errorf("agentd asset not found for linux/amd64")
	}

	exPath, err := s.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	backupPath := exPath + ".bak"
	if err := copyFile(exPath, backupPath); err != nil {
		return nil, fmt.Errorf("backup gateway binary: %w", err)
	}
	if err := DownloadAndVerify(baseURL, *gwAsset, exPath); err != nil {
		return nil, fmt.Errorf("upgrade gateway binary: %w", err)
	}

	agentdBin, err := DownloadBytesAndVerify(baseURL, *agentdAsset)
	if err != nil {
		return nil, fmt.Errorf("download agentd binary: %w", err)
	}
	s.Manager.SetAgentdBinary(agentdBin)

	nodes := s.Manager.List()
	upgradedNodes := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.IsLocal() {
			continue
		}
		if err := s.Manager.Deploy(n.ID, "/opt/agentd"); err != nil {
			continue
		}
		if err := s.Manager.Restart(n.ID, "/opt/agentd"); err != nil {
			continue
		}
		upgradedNodes = append(upgradedNodes, n.ID)
	}

	prev := ""
	if s.NowVersion != nil {
		prev = s.NowVersion()
	}

	if restart {
		if err := s.RestartFn(); err != nil {
			return nil, fmt.Errorf("restart gateway: %w", err)
		}
	}

	return &ApplyResult{
		PreviousVersion: prev,
		TargetVersion:   manifest.Version,
		UpgradedGateway: true,
		UpgradedNodes:   upgradedNodes,
		SkippedAPK:      true,
	}, nil
}

func manifestBaseURL(manifestURL string) string {
	idx := strings.LastIndex(manifestURL, "/")
	if idx <= 0 {
		return manifestURL
	}
	return manifestURL[:idx]
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return err
	}
	return os.Chtimes(dst, time.Now(), time.Now())
}
