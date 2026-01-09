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

	tablecomponent "github.com/mattsolo1/grove-core/tui/components/table"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	meta_workspace "github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var listUlog = grovelogging.NewUnifiedLogger("grove-meta.list")


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
	logger := logging.NewLogger("list")

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
	if _, err := workspace.FindEcosystemRoot(""); err == nil {
		if projects, err := discovery.DiscoverProjects(); err == nil {
			for _, proj := range projects {
				wsPath := proj.Path
				if binaries, err := meta_workspace.DiscoverLocalBinaries(wsPath); err == nil {
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
			// Check if this is a nightly build
			if strings.HasPrefix(version, "nightly-") {
				info.Status = "nightly"
			} else {
				info.Status = "release"
			}
			info.ActiveVersion = version
			info.ActivePath = path
		}

		// Check for other installed versions - use effective alias from FindTool
		_, _, effectiveAlias, _ := sdk.FindTool(repoName)
		for _, installedVersion := range installedVersions {
			versionBinPath := filepath.Join(groveHome, "versions", installedVersion, "bin", effectiveAlias)
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
		ctx := context.Background()
		listUlog.Info("Tool list").
			Field("tool_count", len(toolInfos)).
			Field("format", "json").
			Pretty(string(jsonData)).
			Log(ctx)
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
			statusSymbol = theme.DefaultTheme.Highlight.Render(theme.IconWorktree + " dev")
		case "release":
			statusSymbol = theme.DefaultTheme.Success.Render(theme.IconRepo + " release")
		case "nightly":
			statusSymbol = theme.DefaultTheme.Warning.Render(theme.IconWorktree + " nightly")
		default:
			statusSymbol = theme.DefaultTheme.Muted.Render(theme.IconUnselect + " not installed")
		}

		if info.Status == "dev" && info.ActiveVersion != "" {
			// For dev versions, show the version
			displayVersion = info.ActiveVersion
			currentVersion = displayVersion
		} else if (info.Status == "release" || info.Status == "nightly") && info.ActiveVersion != "" {
			// For release/nightly versions, show the version
			displayVersion = info.ActiveVersion
			currentVersion = displayVersion
		} else if len(info.OtherVersions) > 0 {
			// Show the most recent installed version
			displayVersion = info.OtherVersions[len(info.OtherVersions)-1]
			currentVersion = fmt.Sprintf("(%s)", displayVersion)
		}

		// Build row
		row := []string{
			theme.DefaultTheme.Bold.Render(info.Name),
			theme.DefaultTheme.Info.Render(info.RepoName),
			statusSymbol,
			theme.DefaultTheme.Info.Render(currentVersion),
		}

		if checkUpdates {
			// Add release status indicator
			releaseStatus := info.LatestRelease
			styledReleaseStatus := ""
			if releaseStatus == "" {
				styledReleaseStatus = theme.DefaultTheme.Muted.Render("-")
			} else if displayVersion != "" && displayVersion == info.LatestRelease {
				// Already at latest, show checkmark
				styledReleaseStatus = theme.DefaultTheme.Success.Render(fmt.Sprintf("%s %s", info.LatestRelease, theme.IconSuccess))
			} else if info.LatestRelease != "" {
				// Update available
				styledReleaseStatus = theme.DefaultTheme.Warning.Render(fmt.Sprintf("%s %s", info.LatestRelease, theme.IconArrowUp))
			}
			row = append(row, styledReleaseStatus)
		}

		rows = append(rows, row)
	}

	// Create styled table
	t := tablecomponent.NewStyledTable().
		Headers(headers...).
		Rows(rows...)

	ctx := context.Background()
	listUlog.Info("Tool list").
		Field("tool_count", len(toolInfos)).
		Field("format", "table").
		Pretty(t.String()).
		Log(ctx)

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
