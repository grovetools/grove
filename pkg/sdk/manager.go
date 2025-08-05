package sdk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	GitHubOwner = "grove-ecosystem"
	GitHubRepo  = "grove-ecosystem"
	GitHubAPI   = "https://api.github.com"
)

// Manager handles SDK installation and version management
type Manager struct {
	homeDir string
	baseDir string
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
	}, nil
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

// GetLatestVersionTag fetches the latest release tag from GitHub
func (m *Manager) GetLatestVersionTag() (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPI, GitHubOwner, GitHubRepo)
	
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

// GetRelease fetches release information for a specific version
func (m *Manager) GetRelease(version string) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", GitHubAPI, GitHubOwner, GitHubRepo, version)
	
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
	release, err := m.GetRelease(versionTag)
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
func (m *Manager) downloadFile(url, filepath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	return err
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