package sdk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// Directory structure constants
	GroveDir          = ".grove"
	VersionsDir       = "versions"
	BinDir            = "bin"
	ActiveVersionFile = "active_version"
	
	// GitHub API constants
	GitHubOwner = "mattsolo1"
	GitHubAPI   = "https://api.github.com"
)

// toolToRepo maps tool names to their repository names
var toolToRepo = map[string]string{
	"grove":  "grove-meta",
	"cx":     "grove-context",
	"flow":   "grove-flow",
	"nb":     "grove-notebook",
	"gvm":    "grove-version",
	"px":     "grove-proxy",
	"sb":     "grove-sandbox",
	"tend":   "grove-tend",
	"canopy": "grove-canopy",
}

// Manager handles SDK installation and version management
type Manager struct {
	homeDir string
	baseDir string
	useGH   bool
}

// NewManager creates a new SDK manager instance
func NewManager() (*Manager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	
	return &Manager{
		homeDir: homeDir,
		baseDir: filepath.Join(homeDir, GroveDir),
		useGH:   false,
	}, nil
}

// SetUseGH sets whether to use gh CLI for downloads
func (m *Manager) SetUseGH(useGH bool) {
	m.useGH = useGH
}

// EnsureDirs creates the necessary directory structure
func (m *Manager) EnsureDirs() error {
	dirs := []string{
		m.baseDir,
		filepath.Join(m.baseDir, BinDir),
		filepath.Join(m.baseDir, VersionsDir),
	}
	
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	
	return nil
}

// GetActiveVersion returns the currently active version
func (m *Manager) GetActiveVersion() (string, error) {
	versionFile := filepath.Join(m.baseDir, ActiveVersionFile)
	data, err := os.ReadFile(versionFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // No active version
		}
		return "", fmt.Errorf("failed to read active version: %w", err)
	}
	
	return strings.TrimSpace(string(data)), nil
}

// SetActiveVersion sets the active version
func (m *Manager) SetActiveVersion(version string) error {
	versionFile := filepath.Join(m.baseDir, ActiveVersionFile)
	return os.WriteFile(versionFile, []byte(version), 0644)
}

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// GetLatestVersionTag fetches the latest release tag from GitHub for a specific tool
func (m *Manager) GetLatestVersionTag(toolName string) (string, error) {
	repoName, ok := toolToRepo[toolName]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
	
	if m.useGH {
		return m.getLatestVersionTagWithGH(repoName)
	}
	
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPI, GitHubOwner, repoName)
	
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	
	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to decode release data: %w", err)
	}
	
	return release.TagName, nil
}

// getLatestVersionTagWithGH fetches the latest release tag using gh CLI
func (m *Manager) getLatestVersionTagWithGH(repoName string) (string, error) {
	cmd := exec.Command("gh", "release", "view", "--repo", fmt.Sprintf("%s/%s", GitHubOwner, repoName), "--json", "tagName")
	
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh CLI failed to get latest release: %w", err)
	}
	
	var result struct {
		TagName string `json:"tagName"`
	}
	
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("failed to parse gh output: %w", err)
	}
	
	return result.TagName, nil
}

// GetRelease fetches release information for a specific tool and version
func (m *Manager) GetRelease(toolName, version string) (*GitHubRelease, error) {
	repoName, ok := toolToRepo[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	
	if m.useGH {
		return m.getReleaseWithGH(repoName, version)
	}
	
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", GitHubAPI, GitHubOwner, repoName, version)
	
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release %s: %w", version, err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d for version %s", resp.StatusCode, version)
	}
	
	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release data: %w", err)
	}
	
	return &release, nil
}

// getReleaseWithGH fetches release information using gh CLI
func (m *Manager) getReleaseWithGH(repoName, version string) (*GitHubRelease, error) {
	cmd := exec.Command("gh", "release", "view", version, "--repo", fmt.Sprintf("%s/%s", GitHubOwner, repoName), "--json", "tagName,assets")
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh CLI failed to get release %s: %w", version, err)
	}
	
	var ghRelease struct {
		TagName string `json:"tagName"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	
	if err := json.Unmarshal(output, &ghRelease); err != nil {
		return nil, fmt.Errorf("failed to parse gh output: %w", err)
	}
	
	// Convert to GitHubRelease format
	release := &GitHubRelease{
		TagName: ghRelease.TagName,
	}
	
	for _, asset := range ghRelease.Assets {
		// Convert gh CLI URL format to browser download URL format
		release.Assets = append(release.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			Name:               asset.Name,
			BrowserDownloadURL: fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", GitHubOwner, repoName, version, asset.Name),
		})
	}
	
	return release, nil
}

// ListInstalledVersions returns all installed versions
func (m *Manager) ListInstalledVersions() ([]string, error) {
	versionsDir := filepath.Join(m.baseDir, VersionsDir)
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read versions directory: %w", err)
	}
	
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
			versions = append(versions, entry.Name())
		}
	}
	
	return versions, nil
}

// InstallTool installs a specific tool at a specific version
func (m *Manager) InstallTool(toolName, versionTag string) error {
	// Ensure directories exist
	if err := m.EnsureDirs(); err != nil {
		return err
	}
	
	// Get release information
	release, err := m.GetRelease(toolName, versionTag)
	if err != nil {
		return err
	}
	
	// Construct the binary name
	osName := runtime.GOOS
	archName := runtime.GOARCH
	binaryName := fmt.Sprintf("%s-%s-%s", toolName, osName, archName)
	
	// Find the asset URL
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == binaryName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	
	if downloadURL == "" {
		return fmt.Errorf("no binary found for %s on %s/%s", toolName, osName, archName)
	}
	
	// Create version directory
	versionBinDir := filepath.Join(m.baseDir, VersionsDir, versionTag, BinDir)
	if err := os.MkdirAll(versionBinDir, 0755); err != nil {
		return fmt.Errorf("failed to create version directory: %w", err)
	}
	
	// Download the binary
	targetPath := filepath.Join(versionBinDir, toolName)
	if err := m.downloadFile(downloadURL, targetPath); err != nil {
		return fmt.Errorf("failed to download %s: %w", toolName, err)
	}
	
	// Make executable
	if err := os.Chmod(targetPath, 0755); err != nil {
		return fmt.Errorf("failed to make %s executable: %w", toolName, err)
	}
	
	return nil
}

// UseVersion switches to a specific version
func (m *Manager) UseVersion(versionTag string) error {
	// Check if version is installed
	versionDir := filepath.Join(m.baseDir, VersionsDir, versionTag)
	if _, err := os.Stat(versionDir); os.IsNotExist(err) {
		return fmt.Errorf("version %s is not installed", versionTag)
	}
	
	// Clear existing symlinks in bin directory
	binDir := filepath.Join(m.baseDir, BinDir)
	entries, err := os.ReadDir(binDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read bin directory: %w", err)
	}
	
	for _, entry := range entries {
		if !entry.IsDir() {
			path := filepath.Join(binDir, entry.Name())
			// Check if it's a symlink
			if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
				os.Remove(path)
			}
		}
	}
	
	// Create new symlinks
	versionBinDir := filepath.Join(versionDir, BinDir)
	binaries, err := os.ReadDir(versionBinDir)
	if err != nil {
		return fmt.Errorf("failed to read version bin directory: %w", err)
	}
	
	for _, binary := range binaries {
		if !binary.IsDir() {
			source := filepath.Join(versionBinDir, binary.Name())
			target := filepath.Join(binDir, binary.Name())
			
			if err := os.Symlink(source, target); err != nil {
				return fmt.Errorf("failed to create symlink for %s: %w", binary.Name(), err)
			}
		}
	}
	
	// Update active version file
	return m.SetActiveVersion(versionTag)
}

// UninstallVersion removes a specific version
func (m *Manager) UninstallVersion(versionTag string) error {
	// Check if it's the active version
	activeVersion, err := m.GetActiveVersion()
	if err != nil {
		return err
	}
	
	if activeVersion == versionTag {
		// Clear active version and symlinks
		if err := m.SetActiveVersion(""); err != nil {
			return err
		}
		
		// Clear symlinks
		binDir := filepath.Join(m.baseDir, BinDir)
		entries, _ := os.ReadDir(binDir)
		for _, entry := range entries {
			if !entry.IsDir() {
				path := filepath.Join(binDir, entry.Name())
				if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
					os.Remove(path)
				}
			}
		}
	}
	
	// Remove version directory
	versionDir := filepath.Join(m.baseDir, VersionsDir, versionTag)
	return os.RemoveAll(versionDir)
}

// downloadFile downloads a file from a URL
func (m *Manager) downloadFile(url, targetPath string) error {
	if m.useGH {
		return m.downloadFileWithGH(url, targetPath)
	}
	
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	
	out, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	return err
}

// downloadFileWithGH downloads a file using gh CLI (supports private repos)
func (m *Manager) downloadFileWithGH(url, targetPath string) error {
	// Extract owner, repo, tag, and asset name from the URL
	// URL format: https://github.com/{owner}/{repo}/releases/download/{tag}/{asset}
	parts := strings.Split(url, "/")
	if len(parts) < 8 || parts[2] != "github.com" || parts[5] != "releases" || parts[6] != "download" {
		return fmt.Errorf("invalid GitHub release URL format: %s", url)
	}
	
	owner := parts[3]
	repo := parts[4]
	tag := parts[7]
	asset := parts[8]
	
	// Use gh CLI to download the release asset
	cmd := exec.Command("gh", "release", "download", tag, "--repo", fmt.Sprintf("%s/%s", owner, repo), "--pattern", asset, "--dir", filepath.Dir(targetPath))
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh CLI download failed: %w\nOutput: %s", err, string(output))
	}
	
	// gh downloads with the original filename, so we may need to rename
	downloadedPath := filepath.Join(filepath.Dir(targetPath), asset)
	if downloadedPath != targetPath {
		if err := os.Rename(downloadedPath, targetPath); err != nil {
			return fmt.Errorf("failed to rename downloaded file: %w", err)
		}
	}
	
	return nil
}

// GetAllTools returns the list of all available tools
func GetAllTools() []string {
	return []string{
		"grove",
		"cx",
		"flow",
		"nb",
		"gvm",
		"px",
		"sb",
		"tend",
		"canopy",
	}
}