package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newInstallCmd())
}

func newInstallCmd() *cobra.Command {
	var useGH bool

	cmd := &cobra.Command{
		Use:   "install [tool[@version]...]",
		Short: "Install Grove tools from GitHub releases",
		Long: `Install one or more Grove tools from GitHub releases.

You can specify a specific version using the @ syntax, or install the latest version.
Use 'all' to install all available tools.

Examples:
  grove install cx           # Install latest version of cx
  grove install cx@v0.1.0    # Install specific version of cx
  grove install cx nb flow   # Install multiple tools
  grove install all          # Install all available tools
  grove install --use-gh cx  # Use gh CLI for private repo access`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args, useGH)
		},
	}

	cmd.Flags().BoolVar(&useGH, "use-gh", false, "Use gh CLI for downloading (supports private repos)")

	return cmd
}

func runInstall(cmd *cobra.Command, args []string, useGH bool) error {
	logger := cli.GetLogger(cmd)

	// Auto-detect gh CLI if not explicitly set
	if !useGH && checkGHAuth() {
		useGH = true
		logger.Debug("Authenticated 'gh' CLI detected, using it for downloads")
	}

	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Set the download method
	manager.SetUseGH(useGH)

	// Ensure directory structure exists
	if err := manager.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}

	// Expand "all" to list of all tools
	tools := args
	if len(args) == 1 && args[0] == "all" {
		tools = sdk.GetAllTools()
		logger.Info("Installing all Grove tools...")
	}

	// Migration: ensure we're using the new per-tool version system
	if err := sdk.MigrateFromSingleVersion(os.Getenv("HOME") + "/.grove"); err != nil {
		logger.Debugf("Migration check failed: %v", err)
	}

	// Install each tool
	for _, toolSpec := range tools {
		// Parse tool[@version]
		parts := strings.Split(toolSpec, "@")
		toolName := parts[0]
		version := "latest"

		if len(parts) > 1 {
			version = parts[1]
		}

		// Get actual version tag if "latest" specified
		if version == "latest" {
			latestVersion, err := manager.GetLatestVersionTag(toolName)
			if err != nil {
				logger.WithError(err).Errorf("Failed to get latest version for %s", toolName)
				continue
			}
			version = latestVersion
		}

		logger.Infof("Installing %s %s...", toolName, version)

		if err := manager.InstallTool(toolName, version); err != nil {
			logger.WithError(err).Errorf("Failed to install %s", toolName)
			// Continue with other tools
		} else {
			logger.Infof("✅ Successfully installed %s %s", toolName, version)

			// Auto-activate the installed version
			logger.Infof("Activating %s %s...", toolName, version)
			if err := manager.UseToolVersion(toolName, version); err != nil {
				logger.WithError(err).Warnf("Failed to activate %s %s", toolName, version)
			} else {
				// Clear any dev link for this tool
				if err := clearDevLinkForTool(toolName); err != nil {
					logger.WithError(err).Debugf("Failed to clear dev link for %s", toolName)
				}

				// Reconcile the symlink
				tv, err := sdk.LoadToolVersions(os.Getenv("HOME") + "/.grove")
				if err != nil {
					// Log the error but proceed with an empty config so we don't block the user
					logger.WithError(err).Warn("Could not load tool versions for reconciliation")
					tv = &sdk.ToolVersions{Versions: make(map[string]string)}
				}
				
				r, err := reconciler.NewWithToolVersions(tv)
				if err != nil {
					logger.WithError(err).Warnf("Could not create reconciler, skipping symlink update for %s", toolName)
				} else {
					if err := r.Reconcile(toolName); err != nil {
						logger.WithError(err).Warnf("Failed to reconcile symlink for %s", toolName)
					} else {
						logger.Infof("✅ %s %s is now active", toolName, version)
					}
				}
			}
		}
	}

	// No longer need to set a single active version - each tool is activated individually

	// Check if ~/.grove/bin is in PATH
	homeDir, _ := os.UserHomeDir()
	groveBin := filepath.Join(homeDir, ".grove", "bin")
	path := os.Getenv("PATH")

	if !strings.Contains(path, groveBin) {
		logger.Warn("")
		logger.Warn("⚠️  IMPORTANT: Add Grove to your PATH")
		logger.Warn("")
		logger.Warnf("Add the following line to your shell profile (~/.zshrc, ~/.bashrc, etc.):")
		logger.Warnf("  export PATH=\"%s:$PATH\"", groveBin)
		logger.Warn("")
		logger.Warn("Then restart your terminal or run:")
		logger.Warn("  source ~/.zshrc  # or ~/.bashrc")
	}

	return nil
}


// clearDevLinkForTool clears the active dev link for a specific tool
func clearDevLinkForTool(toolName string) error {
	config, err := devlinks.LoadConfig()
	if err != nil {
		return err
	}

	// Check if the tool has any dev links
	if binLinks, exists := config.Binaries[toolName]; exists {
		// Clear the current active link
		binLinks.Current = ""
		return devlinks.SaveConfig(config)
	}

	return nil
}
