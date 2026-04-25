package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func DownloadBytesAndVerify(baseURL string, asset Asset) ([]byte, error) {
	if asset.Path == "" {
		return nil, fmt.Errorf("empty asset path")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	cleanPath := strings.TrimLeft(asset.Path, "/")
	url := baseURL + "/" + cleanPath
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read asset: %w", err)
	}
	if asset.Size > 0 && int64(len(data)) != asset.Size {
		return nil, fmt.Errorf("size mismatch: got %d want %d", len(data), asset.Size)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if asset.SHA256 != "" && got != asset.SHA256 {
		return nil, fmt.Errorf("sha256 mismatch: got %s want %s", got, asset.SHA256)
	}
	return data, nil
}

func DownloadAndVerify(baseURL string, asset Asset, destPath string) error {
	data, err := DownloadBytesAndVerify(baseURL, asset)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir destination: %w", err)
	}
	tmpPath := destPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}
