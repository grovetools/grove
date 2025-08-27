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
	"sort"
	"strings"

	"github.com/mattsolo1/grove-meta/pkg/devlinks"
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

// ToolInfo contains information about a tool
type ToolInfo struct {
	RepoName   string
	BinaryName string
}

// toolRegistry maps tool names to their repository and binary names
var toolRegistry = map[string]ToolInfo{
	"canopy":                {RepoName: "grove-canopy", BinaryName: "canopy"},
	"clogs":                 {RepoName: "grove-claude-logs", BinaryName: "clogs"},
	"core":                  {RepoName: "grove-core", BinaryName: "core"},
	"cx":                    {RepoName: "grove-context", BinaryName: "cx"},
	"flow":                  {RepoName: "grove-flow", BinaryName: "flow"},
	"gemapi":                {RepoName: "grove-gemini", BinaryName: "gemapi"},
	"gmux":                  {RepoName: "grove-tmux", BinaryName: "gmux"},
	"grove":                 {RepoName: "grove-meta", BinaryName: "grove"},
	"grove-hooks":           {RepoName: "grove-hooks", BinaryName: "hooks"},
	"hooks":                 {RepoName: "grove-hooks", BinaryName: "hooks"},
	"nb":                    {RepoName: "grove-notebook", BinaryName: "nb"},
	"neogrove":              {RepoName: "grove-nvim", BinaryName: "neogrove"},
	"notify":                {RepoName: "grove-notifications", BinaryName: "notify"},
	"nvim":                  {RepoName: "grove-nvim", BinaryName: "neogrove"}, // Alias
	"project-tmpl-go":       {RepoName: "grove-project-tmpl-go", BinaryName: "project-tmpl-go"},
	"project-tmpl-maturin":  {RepoName: "grove-project-tmpl-maturin", BinaryName: "project-tmpl-maturin"},
	"project-tmpl-react-ts": {RepoName: "grove-project-tmpl-react-ts", BinaryName: "project-tmpl-react-ts"},
	"px":                    {RepoName: "grove-proxy", BinaryName: "px"},
	"sb":                    {RepoName: "grove-sandbox", BinaryName: "sb"},
	"tend":                  {RepoName: "grove-tend", BinaryName: "tend"},
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

	baseDir := filepath.Join(homeDir, GroveDir)

	// Run migration on initialization
	if err := MigrateFromSingleVersion(baseDir); err != nil {
		// Ignore migration errors - it's a one-time operation
	}

	return &Manager{
		homeDir: homeDir,
		baseDir: baseDir,
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

// GetActiveVersion returns the currently active version (DEPRECATED)
// This method is kept for backward compatibility but should not be used
func (m *Manager) GetActiveVersion() (string, error) {
	// Try to migrate from old format if needed
	if err := MigrateFromSingleVersion(m.baseDir); err != nil {
		// Ignore migration errors
	}

	// Return empty string as there's no single active version anymore
	return "", fmt.Errorf("no single active version - use GetToolVersion instead")
}

// GetToolVersion returns the active version for a specific tool
func (m *Manager) GetToolVersion(tool string) (string, error) {
	tv, err := LoadToolVersions(m.baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to load tool versions: %w", err)
	}

	version := tv.GetToolVersion(tool)
	if version == "" {
		return "", fmt.Errorf("no active version for %s", tool)
	}

	return version, nil
}

// SetActiveVersion sets the active version (DEPRECATED)
// This method is kept for backward compatibility but should not be used
func (m *Manager) SetActiveVersion(version string) error {
	return fmt.Errorf("SetActiveVersion is deprecated - use SetToolVersion instead")
}

// SetToolVersion sets the active version for a specific tool
func (m *Manager) SetToolVersion(tool, version string) error {
	tv, err := LoadToolVersions(m.baseDir)
	if err != nil {
		return fmt.Errorf("failed to load tool versions: %w", err)
	}

	tv.SetToolVersion(tool, version)

	if err := tv.Save(m.baseDir); err != nil {
		return fmt.Errorf("failed to save tool versions: %w", err)
	}

	return nil
}

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// resolveRepoName resolves a tool name to its repository name
// It handles both old tool names (keys in toolRegistry) and actual binary names
func resolveRepoName(toolName string) (string, error) {
	// First check if it's a direct key in toolRegistry
	if info, ok := toolRegistry[toolName]; ok {
		return info.RepoName, nil
	}

	// Check if the toolName is already a valid repository name
	if strings.HasPrefix(toolName, "grove-") {
		// Verify it's a known repository by checking if it exists as a value
		for _, info := range toolRegistry {
			if info.RepoName == toolName {
				return toolName, nil
			}
		}
		// Even if not in our map, allow it - it might be a new repository
		return toolName, nil
	}

	// Check if adding "grove-" prefix gives us a valid repo
	expectedRepo := "grove-" + toolName
	for _, info := range toolRegistry {
		if info.RepoName == expectedRepo {
			return info.RepoName, nil
		}
	}

	return "", fmt.Errorf("unknown tool: %s", toolName)
}

// GetLatestVersionTag fetches the latest release tag from GitHub for a specific tool
func (m *Manager) GetLatestVersionTag(toolName string) (string, error) {
	repoName, err := resolveRepoName(toolName)
	if err != nil {
		return "", err
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
	repoName, err := resolveRepoName(toolName)
	if err != nil {
		return nil, err
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

	// Get tool info
	toolInfo, ok := toolRegistry[toolName]
	if !ok {
		return fmt.Errorf("unknown tool: %s", toolName)
	}

	// Get release information
	release, err := m.GetRelease(toolName, versionTag)
	if err != nil {
		return err
	}

	// Construct the binary name using the tool's binary name (not repo name)
	osName := runtime.GOOS
	archName := runtime.GOARCH
	binaryAssetName := fmt.Sprintf("%s-%s-%s", toolInfo.BinaryName, osName, archName)

	// Find the asset URL
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == binaryAssetName {
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

// UseVersion switches to a specific version (DEPRECATED)
func (m *Manager) UseVersion(versionTag string) error {
	return fmt.Errorf("UseVersion is deprecated - use UseToolVersion instead")
}

// UseToolVersion switches a specific tool to a specific version
func (m *Manager) UseToolVersion(tool, versionTag string) error {
	// Check if the tool at this version is installed
	toolPath := filepath.Join(m.baseDir, VersionsDir, versionTag, BinDir, tool)
	if _, err := os.Stat(toolPath); os.IsNotExist(err) {
		return fmt.Errorf("tool %s version %s is not installed", tool, versionTag)
	}

	// Update the tool version
	if err := m.SetToolVersion(tool, versionTag); err != nil {
		return err
	}

	// The caller should handle symlinking via reconciler
	// This avoids circular dependencies

	return nil
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

// resetDevLinks clears all active development links
func (m *Manager) resetDevLinks() error {
	return devlinks.ClearAllCurrentLinks()
}

// GetAllTools returns the list of all available tools
func GetAllTools() []string {
	// Extract all tool names from the toolRegistry map to ensure consistency
	tools := make([]string, 0, len(toolRegistry))
	for tool := range toolRegistry {
		tools = append(tools, tool)
	}

	// Sort for consistent output
	sort.Strings(tools)
	return tools
}

// GetToolToRepoMap returns the tool to repository name mapping
func GetToolToRepoMap() map[string]string {
	// Return a copy to prevent external modification
	result := make(map[string]string, len(toolRegistry))
	for k, v := range toolRegistry {
		result[k] = v.RepoName
	}
	return result
}
