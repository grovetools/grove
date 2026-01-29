package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/version"
	"github.com/grovetools/grove/pkg/devlinks"
	"github.com/grovetools/grove/pkg/reconciler"
	"github.com/grovetools/grove/pkg/sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newVersionCmd())
}

func newVersionCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Manage Grove tool versions",
		Long:  "List, switch between, and uninstall different versions of Grove tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			// When run without subcommands, print grove version info
			info := version.GetInfo()

			if jsonOutput {
				jsonData, err := json.MarshalIndent(info, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal version info to JSON: %w", err)
				}
				fmt.Println(string(jsonData))
			} else {
				fmt.Println(info.String())
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version information in JSON format")

	cmd.AddCommand(newVersionListCmd())
	cmd.AddCommand(newVersionUseCmd())
	cmd.AddCommand(newVersionUninstallCmd())

	return cmd
}

func newVersionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed versions",
		Long:  "Display all installed versions of Grove tools",
		Args:  cobra.NoArgs,
		RunE:  runVersionList,
	}
}

func newVersionUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <tool@version>",
		Short: "Switch a tool to a specific version",
		Long: `Switch a specific tool to an installed version.

This command updates the symlink in ~/.grove/bin for the specified tool.

Examples:
  grove version use cx@v0.1.0
  grove version use flow@v1.2.3
  grove version use grove@v0.5.0`,
		Args: cobra.ExactArgs(1),
		RunE: runVersionUse,
	}
}

func newVersionUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <version>",
		Short: "Uninstall a specific version",
		Long: `Remove a specific version of Grove tools.

If the version being uninstalled is currently active, the active version will be cleared.

Example:
  grove version uninstall v0.1.0`,
		Args: cobra.ExactArgs(1),
		RunE: runVersionUninstall,
	}
}

func runVersionList(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("version")
	pretty := logging.NewPrettyLogger()
	opts := cli.GetOptions(cmd)

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Load tool versions
	tv, err := sdk.LoadToolVersions()
	if err != nil {
		tv = &sdk.ToolVersions{
			Versions: make(map[string]string),
		}
	}

	// Get all installed versions
	versions, err := manager.ListInstalledVersions()
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	if len(versions) == 0 {
		logger.Info("No versions installed. Use 'grove install' to install tools.")
		pretty.InfoPretty("No versions installed. Use 'grove install' to install tools.")
		return nil
	}

	// Build a map of version -> tools
	versionTools := make(map[string][]string)
	for _, version := range versions {
		versionDir := filepath.Join(paths.DataDir(), "versions", version, "bin")
		entries, err := os.ReadDir(versionDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				versionTools[version] = append(versionTools[version], entry.Name())
			}
		}
	}

	if opts.JSONOutput {
		// Output as JSON
		type ToolVersion struct {
			Tool    string `json:"tool"`
			Version string `json:"version"`
			Active  bool   `json:"active"`
		}

		var toolVersions []ToolVersion
		for version, tools := range versionTools {
			for _, tool := range tools {
				activeVersion := tv.GetToolVersion(tool)
				toolVersions = append(toolVersions, ToolVersion{
					Tool:    tool,
					Version: version,
					Active:  version == activeVersion,
				})
			}
		}

		jsonData, err := json.MarshalIndent(toolVersions, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
	}

	// Output as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tVERSION\tSTATUS")
	fmt.Fprintln(w, "----\t-------\t------")

	// Sort tools for consistent output
	type toolVersionInfo struct {
		tool    string
		version string
		active  bool
	}
	var allTools []toolVersionInfo

	for version, tools := range versionTools {
		for _, tool := range tools {
			activeVersion := tv.GetToolVersion(tool)
			allTools = append(allTools, toolVersionInfo{
				tool:    tool,
				version: version,
				active:  version == activeVersion,
			})
		}
	}

	// Sort by tool name, then version
	sort.Slice(allTools, func(i, j int) bool {
		if allTools[i].tool != allTools[j].tool {
			return allTools[i].tool < allTools[j].tool
		}
		return allTools[i].version < allTools[j].version
	})

	for _, info := range allTools {
		status := ""
		if info.active {
			status = "Active"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", info.tool, info.version, status)
	}

	w.Flush()
	return nil
}

func runVersionUse(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("version")
	pretty := logging.NewPrettyLogger()
	toolSpec := args[0]

	// Parse tool@version
	parts := strings.Split(toolSpec, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid format: use 'tool@version' (e.g., cx@v0.1.0)")
	}

	toolName := parts[0]
	version := parts[1]

	// Ensure version starts with 'v'
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Switch to the version
	logger.Infof("Switching %s to version %s...", toolName, version)
	pretty.InfoPretty(fmt.Sprintf("Switching %s to version %s...", toolName, version))

	if err := manager.UseToolVersion(toolName, version); err != nil {
		return fmt.Errorf("failed to switch version: %w", err)
	}

	// Clear any dev override for this tool since user explicitly wants a released version
	devConfig, err := devlinks.LoadConfig()
	if err == nil {
		if binInfo, exists := devConfig.Binaries[toolName]; exists && binInfo.Current != "" {
			logger.Infof("Clearing dev override for %s", toolName)
			pretty.InfoPretty(fmt.Sprintf("Clearing dev override for %s", toolName))
			binInfo.Current = ""
			devlinks.SaveConfig(devConfig)
		}
	}

	// Load tool versions and reconcile
	tv, err := sdk.LoadToolVersions()
	if err != nil {
		return fmt.Errorf("failed to load tool versions: %w", err)
	}

	r, err := reconciler.NewWithToolVersions(tv)
	if err != nil {
		return fmt.Errorf("failed to create reconciler: %w", err)
	}

	if err := r.Reconcile(toolName); err != nil {
		return fmt.Errorf("failed to update symlink: %w", err)
	}

	logger.Infof("Successfully switched %s to version %s", toolName, version)
	pretty.Success(fmt.Sprintf("Successfully switched %s to version %s", toolName, version))
	return nil
}

func runVersionUninstall(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("version")
	pretty := logging.NewPrettyLogger()
	version := args[0]

	// Ensure version starts with 'v'
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Check if version is installed
	installed, err := manager.ListInstalledVersions()
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	found := false
	for _, v := range installed {
		if v == version {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("version %s is not installed", version)
	}

	// Get active version to warn if uninstalling it
	activeVersion, _ := manager.GetActiveVersion()
	if activeVersion == version {
		logger.Warnf("Version %s is currently active. It will be deactivated.", version)
		pretty.InfoPretty(fmt.Sprintf("Version %s is currently active. It will be deactivated.", version))
	}

	// Uninstall the version
	logger.Infof("Uninstalling version %s...", version)
	pretty.InfoPretty(fmt.Sprintf("Uninstalling version %s...", version))

	if err := manager.UninstallVersion(version); err != nil {
		return fmt.Errorf("failed to uninstall version: %w", err)
	}

	logger.Infof("Successfully uninstalled version %s", version)
	pretty.Success(fmt.Sprintf("Successfully uninstalled version %s", version))

	if activeVersion == version {
		logger.Info("No version is currently active. Use 'grove version use <version>' to activate a version.")
		pretty.InfoPretty("No version is currently active. Use 'grove version use <version>' to activate a version.")
	}

	return nil
}
