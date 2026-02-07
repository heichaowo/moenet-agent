// Package updater provides self-update functionality for moenet-agent.
// It checks GitHub releases for new versions and performs atomic binary replacement.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// Config holds auto-update configuration
type Config struct {
	Enabled       bool   `json:"enabled"`
	CheckInterval int    `json:"checkInterval"` // minutes
	Channel       string `json:"channel"`       // stable / beta
}

// GitHubRelease represents a GitHub release response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt string        `json:"published_at"`
	Body        string        `json:"body"`
	Assets      []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a release asset
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Updater handles automatic updates
type Updater struct {
	currentVersion string
	binaryPath     string
	githubRepo     string
	config         Config
	httpClient     *http.Client
}

// New creates a new Updater instance
func New(currentVersion, binaryPath string, config Config, githubRepo string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		githubRepo:     githubRepo,
		config:         config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Run starts the update check loop
func (u *Updater) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	interval := time.Duration(u.config.CheckInterval) * time.Minute
	if interval < time.Minute {
		interval = time.Hour // Default to 1 hour
	}

	log.Printf("[Updater] Starting with check interval: %v", interval)
	log.Printf("[Updater] Current version: %s", u.currentVersion)

	// Initial check after short delay
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
		u.checkAndUpdate(ctx)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Updater] Shutting down")
			return
		case <-ticker.C:
			u.checkAndUpdate(ctx)
		}
	}
}

// checkAndUpdate checks for updates and applies them if available
func (u *Updater) checkAndUpdate(ctx context.Context) {
	release, err := u.CheckForUpdate(ctx)
	if err != nil {
		log.Printf("[Updater] Failed to check for updates: %v", err)
		return
	}

	if release == nil {
		log.Println("[Updater] Already running latest version")
		return
	}

	log.Printf("[Updater] New version available: %s -> %s", u.currentVersion, release.TagName)

	if err := u.DownloadAndApply(ctx, release); err != nil {
		log.Printf("[Updater] Failed to apply update: %v", err)
		return
	}
}

// CheckForUpdate checks if a new version is available
func (u *Updater) CheckForUpdate(ctx context.Context) (*GitHubRelease, error) {
	var url string

	// For dev/beta channels, we need to list all releases to find prereleases
	// The /latest endpoint only returns non-prerelease versions
	if u.config.Channel == "dev" || u.config.Channel == "beta" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=10", u.githubRepo)
	} else {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", u.githubRepo)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "moenet-agent-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // No releases
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var release *GitHubRelease

	if u.config.Channel == "dev" || u.config.Channel == "beta" {
		// Parse as array of releases, find latest prerelease
		var releases []GitHubRelease
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return nil, fmt.Errorf("decode releases: %w", err)
		}

		for i := range releases {
			if releases[i].Prerelease {
				release = &releases[i]
				break // First prerelease is the latest
			}
		}

		if release == nil {
			log.Printf("[Updater] No prerelease found for %s channel", u.config.Channel)
			return nil, nil
		}
	} else {
		// Parse as single release (stable channel)
		var r GitHubRelease
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}

		// Skip prereleases for stable channel
		if r.Prerelease {
			return nil, nil
		}
		release = &r
	}

	// Compare versions using semver
	currentV := normalizeVersion(u.currentVersion)
	releaseV := normalizeVersion(release.TagName)

	if !semver.IsValid(currentV) || !semver.IsValid(releaseV) {
		// Fall back to string comparison for dev builds
		if u.currentVersion == "dev" || u.currentVersion == release.TagName {
			return nil, nil
		}
		return release, nil
	}

	if semver.Compare(releaseV, currentV) <= 0 {
		return nil, nil // Current version is up-to-date or newer
	}

	return release, nil
}

// DownloadAndApply downloads the new binary and applies the update
func (u *Updater) DownloadAndApply(ctx context.Context, release *GitHubRelease) error {
	// Find the correct asset for this platform
	assetName := fmt.Sprintf("moenet-agent-%s-%s", runtime.GOOS, runtime.GOARCH)
	var asset *GitHubAsset
	for i := range release.Assets {
		if strings.Contains(release.Assets[i].Name, assetName) {
			asset = &release.Assets[i]
			break
		}
	}

	if asset == nil {
		return fmt.Errorf("no asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	log.Printf("[Updater] Downloading %s (%d bytes)", asset.Name, asset.Size)

	// Download to temp file
	tempPath := u.binaryPath + ".new"
	if err := u.downloadFile(ctx, asset.BrowserDownloadURL, tempPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("download: %w", err)
	}

	// Make executable
	if err := os.Chmod(tempPath, 0755); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("chmod: %w", err)
	}

	// Verify the new binary runs
	cmd := exec.CommandContext(ctx, tempPath, "-v")
	if err := cmd.Run(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("verify new binary: %w", err)
	}

	// Atomic replacement
	backupPath := u.binaryPath + ".backup"

	// Remove old backup if exists
	os.Remove(backupPath)

	// Backup current binary
	if err := os.Rename(u.binaryPath, backupPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("backup current: %w", err)
	}

	// Move new binary to current path
	if err := os.Rename(tempPath, u.binaryPath); err != nil {
		// Rollback
		os.Rename(backupPath, u.binaryPath)
		return fmt.Errorf("install new: %w", err)
	}

	log.Printf("[Updater] Update applied successfully: %s", release.TagName)
	log.Println("[Updater] Restarting agent...")

	// Trigger restart by exiting - systemd will restart
	os.Exit(0)
	return nil
}

// downloadFile downloads a file from URL to the given path
func (u *Updater) downloadFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "moenet-agent-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, h), resp.Body)
	if err != nil {
		return err
	}

	checksum := hex.EncodeToString(h.Sum(nil))
	log.Printf("[Updater] Downloaded %d bytes, SHA256: %s", written, checksum[:16]+"...")

	return nil
}

// GetCurrentVersion returns the current version
func (u *Updater) GetCurrentVersion() string {
	return u.currentVersion
}

// normalizeVersion ensures version starts with 'v'
func normalizeVersion(v string) string {
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
