package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/grovepm/grove/pkg/sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newVersionCmd())
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Manage Grove tool versions",
		Long:  "List, switch between, and uninstall different versions of Grove tools",
	}

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
		Use:   "use <version>",
		Short: "Switch to a specific version",
		Long: `Switch to a specific installed version of Grove tools.

This command updates the symlinks in ~/.grove/bin to point to the specified version.

Example:
  grove version use v0.1.0`,
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
	logger := cli.GetLogger(cmd)
	opts := cli.GetOptions(cmd)

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Get installed versions
	versions, err := manager.ListInstalledVersions()
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	if len(versions) == 0 {
		logger.Info("No versions installed. Use 'grove install' to install tools.")
		return nil
	}

	// Get active version
	activeVersion, err := manager.GetActiveVersion()
	if err != nil {
		logger.WithError(err).Warn("Failed to get active version")
		activeVersion = ""
	}

	if opts.JSONOutput {
		// Output as JSON
		type VersionInfo struct {
			Version string `json:"version"`
			Active  bool   `json:"active"`
		}
		
		var versionInfos []VersionInfo
		for _, v := range versions {
			versionInfos = append(versionInfos, VersionInfo{
				Version: v,
				Active:  v == activeVersion,
			})
		}
		
		jsonData, err := json.MarshalIndent(versionInfos, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
	}

	// Output as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tSTATUS")
	fmt.Fprintln(w, "-------\t------")

	for _, version := range versions {
		status := ""
		if version == activeVersion {
			status = "Active"
		}
		fmt.Fprintf(w, "%s\t%s\n", version, status)
	}

	w.Flush()
	return nil
}

func runVersionUse(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
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
		logger.Errorf("Version %s is not installed", version)
		logger.Info("Use 'grove install <tool>@%s' to install this version first", version)
		return fmt.Errorf("version not found")
	}

	// Switch to the version
	logger.Infof("Switching to version %s...", version)
	
	if err := manager.UseVersion(version); err != nil {
		return fmt.Errorf("failed to switch version: %w", err)
	}

	logger.Infof("✅ Successfully switched to version %s", version)
	return nil
}

func runVersionUninstall(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
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
	}

	// Uninstall the version
	logger.Infof("Uninstalling version %s...", version)
	
	if err := manager.UninstallVersion(version); err != nil {
		return fmt.Errorf("failed to uninstall version: %w", err)
	}

	logger.Infof("✅ Successfully uninstalled version %s", version)
	
	if activeVersion == version {
		logger.Info("No version is currently active. Use 'grove version use <version>' to activate a version.")
	}
	
	return nil
}