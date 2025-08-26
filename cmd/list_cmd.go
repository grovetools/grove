package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

// Define lipgloss styles
var (
	// Status styles
	devStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF79C6")).Bold(true)
	releaseStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B")).Bold(true)
	notInstalledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))

	// Version styles
	versionStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2"))
	updateAvailableStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C"))
	upToDateStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))

	// Header style
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#BD93F9"))

	// Tool name style
	toolStyle = lipgloss.NewStyle().Bold(true)

	// Repository style
	repoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8BE9FD"))
)

func init() {
	rootCmd.AddCommand(newListCmd())
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available Grove tools",
		Long:  "Display all available Grove tools and their installation status",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}

	cmd.Flags().Bool("check-updates", true, "Check for latest releases from GitHub")

	return cmd
}

type ToolInfo struct {
	Name          string   `json:"name"`
	RepoName      string   `json:"repo_name"`
	Status        string   `json:"status"`
	ActiveVersion string   `json:"active_version,omitempty"`
	ActivePath    string   `json:"active_path,omitempty"`
	OtherVersions []string `json:"other_versions,omitempty"`
	LatestRelease string   `json:"latest_release,omitempty"`
}

func runList(cmd *cobra.Command, args []string) error {
	opts := cli.GetOptions(cmd)
	logger := cli.GetLogger(cmd)

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Auto-detect gh CLI for fetching releases
	if checkGHAuth() {
		manager.SetUseGH(true)
		logger.Debug("Using gh CLI for fetching release information")
	}

	// Load tool versions
	groveHome := filepath.Join(os.Getenv("HOME"), ".grove")
	toolVersions, err := sdk.LoadToolVersions(groveHome)
	if err != nil {
		logger.WithError(err).Warn("Failed to load tool versions")
		toolVersions = &sdk.ToolVersions{
			Versions: make(map[string]string),
		}
	}

	// Create reconciler
	r, err := reconciler.NewWithToolVersions(toolVersions)
	if err != nil {
		return fmt.Errorf("failed to create reconciler: %w", err)
	}

	// Get all installed versions
	installedVersions, err := manager.ListInstalledVersions()
	if err != nil {
		logger.WithError(err).Warn("Failed to list installed versions")
		installedVersions = []string{}
	}

	// Get available tools from workspace discovery and SDK
	toolMap := make(map[string]string) // toolName -> repoName

	// Try to discover from workspaces first
	if rootDir, err := workspace.FindRoot(""); err == nil {
		if workspaces, err := workspace.Discover(rootDir); err == nil {
			for _, wsPath := range workspaces {
				if binaries, err := workspace.DiscoverLocalBinaries(wsPath); err == nil {
					for _, binary := range binaries {
						repoName := filepath.Base(wsPath)
						toolMap[binary.Name] = repoName
					}
				}
			}
		}
	}

	// Add any tools from SDK that weren't found in workspaces
	sdkToolToRepo := sdk.GetToolToRepoMap()
	for toolName, repoName := range sdkToolToRepo {
		if _, exists := toolMap[toolName]; !exists {
			toolMap[toolName] = repoName
		}
	}

	// Extract sorted list of tool names
	var allTools []string
	for toolName := range toolMap {
		allTools = append(allTools, toolName)
	}
	sort.Strings(allTools)

	// Build tool info
	var toolInfos []ToolInfo
	for _, toolName := range allTools {
		repoName := toolMap[toolName]

		info := ToolInfo{
			Name:          toolName,
			RepoName:      repoName,
			Status:        "not installed",
			OtherVersions: []string{},
		}

		// Get effective source from reconciler
		source, version, path := r.GetEffectiveSource(toolName)

		if source == "dev" {
			info.Status = "dev"
			info.ActiveVersion = version
			info.ActivePath = path

			// Try to get version from the dev binary
			if devVersion := getDevBinaryVersion(path); devVersion != "" {
				info.ActiveVersion = devVersion
			}
		} else if source == "release" {
			info.Status = "release"
			info.ActiveVersion = version
			info.ActivePath = path
		}

		// Check for other installed versions
		for _, installedVersion := range installedVersions {
			versionBinPath := filepath.Join(groveHome, "versions", installedVersion, "bin", toolName)
			if _, err := os.Stat(versionBinPath); err == nil {
				// Only add to other versions if it's not the active version
				if source != "release" || version != installedVersion {
					info.OtherVersions = append(info.OtherVersions, installedVersion)
				}
			}
		}

		// Sort other versions to ensure most recent is last
		if len(info.OtherVersions) > 0 {
			sort.Strings(info.OtherVersions)
		}

		// Update status if we found installed versions but no active one
		if source == "none" && len(info.OtherVersions) > 0 {
			info.Status = fmt.Sprintf("inactive (installed: %s)", strings.Join(info.OtherVersions, ", "))
		}

		toolInfos = append(toolInfos, info)
	}

	// Fetch latest releases if requested
	checkUpdates, _ := cmd.Flags().GetBool("check-updates")
	if checkUpdates {
		// Fetch latest releases for all tools in parallel
		var wg sync.WaitGroup
		for i := range toolInfos {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				// Try to get latest version using the repository name first
				latestVersion, err := manager.GetLatestVersionTag(toolInfos[idx].RepoName)
				if err != nil {
					// If that fails, try with the tool name (for backward compatibility)
					latestVersion, err = manager.GetLatestVersionTag(toolInfos[idx].Name)
					if err != nil {
						logger.WithError(err).Debugf("Failed to get latest version for %s/%s", toolInfos[idx].Name, toolInfos[idx].RepoName)
						// Don't show "unknown" - just leave it empty
					} else {
						toolInfos[idx].LatestRelease = latestVersion
					}
				} else {
					toolInfos[idx].LatestRelease = latestVersion
				}
			}(i)
		}
		wg.Wait()
	}

	if opts.JSONOutput {
		// Output as JSON
		jsonData, err := json.MarshalIndent(toolInfos, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
	}

	// Build table headers
	var headers []string
	headers = append(headers, "TOOL", "REPOSITORY", "STATUS", "CURRENT VERSION")
	if checkUpdates {
		headers = append(headers, "LATEST")
	}

	// Build rows
	var rows [][]string
	for _, info := range toolInfos {
		currentVersion := "-"
		displayVersion := ""
		statusSymbol := ""

		// Determine status symbol with styling
		switch info.Status {
		case "dev":
			statusSymbol = devStyle.Render("◆ dev")
		case "release":
			statusSymbol = releaseStyle.Render("● release")
		default:
			statusSymbol = notInstalledStyle.Render("○ not installed")
		}

		if info.Status == "dev" && info.ActiveVersion != "" {
			// For dev versions, show the version
			displayVersion = info.ActiveVersion
			currentVersion = displayVersion
		} else if info.Status == "release" && info.ActiveVersion != "" {
			// For release versions, show the version
			displayVersion = info.ActiveVersion
			currentVersion = displayVersion
		} else if len(info.OtherVersions) > 0 {
			// Show the most recent installed version
			displayVersion = info.OtherVersions[len(info.OtherVersions)-1]
			currentVersion = fmt.Sprintf("(%s)", displayVersion)
		}

		// Build row
		row := []string{
			toolStyle.Render(info.Name),
			repoStyle.Render(info.RepoName),
			statusSymbol,
			versionStyle.Render(currentVersion),
		}

		if checkUpdates {
			// Add release status indicator
			releaseStatus := info.LatestRelease
			styledReleaseStatus := ""
			if releaseStatus == "" {
				styledReleaseStatus = notInstalledStyle.Render("-")
			} else if displayVersion != "" && displayVersion == info.LatestRelease {
				// Already at latest, show checkmark
				styledReleaseStatus = upToDateStyle.Render(fmt.Sprintf("%s ✓", info.LatestRelease))
			} else if info.LatestRelease != "" {
				// Update available
				styledReleaseStatus = updateAvailableStyle.Render(fmt.Sprintf("%s ↑", info.LatestRelease))
			}
			row = append(row, styledReleaseStatus)
		}

		rows = append(rows, row)
	}

	// Create lipgloss table
	re := lipgloss.NewRenderer(os.Stdout)
	baseStyle := re.NewStyle().Padding(0, 1)
	tableHeaderStyle := baseStyle.Copy().Bold(true).Foreground(lipgloss.Color("255"))

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(rows...)

	// Apply header styling
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return tableHeaderStyle
		}
		// Return minimal style to preserve pre-styled content
		return lipgloss.NewStyle().Padding(0, 1)
	})

	fmt.Println(t)

	return nil
}

// getDevBinaryVersion attempts to get version from a dev binary
func getDevBinaryVersion(binaryPath string) string {
	if binaryPath == "" || !strings.Contains(binaryPath, "/") {
		return ""
	}

	// Set a timeout for the version command
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "version")
	cmd.Env = append(os.Environ(), "NO_COLOR=1") // Disable color output

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	// Run with timeout
	if err := cmd.Run(); err != nil {
		return ""
	}

	output := out.String()

	// Try to extract version from output
	// First try to find a line starting with "Version:"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Version:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}

	// Fallback to pattern matching
	// Look for patterns like "Version: main-986ed5d-dirty" or "v0.2.8"
	versionPatterns := []string{
		`Version:\s+(\S+)`,
		`version\s+(\S+)`,
		`(v\d+\.\d+\.\d+\S*)`,
		`(main-[a-f0-9]+-?(?:dirty)?)`,
		`([a-zA-Z]+-[a-f0-9]+-?(?:dirty)?)`, // branch-hash-dirty pattern
	}

	for _, pattern := range versionPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(output)
		if len(matches) > 1 {
			version := matches[1]
			// Clean up the version string
			version = strings.TrimSpace(version)
			return version
		}
	}

	return ""
}
