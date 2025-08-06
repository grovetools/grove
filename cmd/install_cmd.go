package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/grovepm/grove/pkg/sdk"
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
	
	// Track if we need to set an active version
	currentActive, _ := manager.GetActiveVersion()
	needsActive := currentActive == ""
	var versionToActivate string
	
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
			latestVersion, err := manager.GetLatestVersionTag()
			if err != nil {
				logger.WithError(err).Errorf("Failed to get latest version for %s", toolName)
				continue
			}
			version = latestVersion
		}
		
		// Track version for activation
		if needsActive && versionToActivate == "" {
			versionToActivate = version
		}
		
		logger.Infof("Installing %s %s...", toolName, version)
		
		if err := manager.InstallTool(toolName, version); err != nil {
			logger.WithError(err).Errorf("Failed to install %s", toolName)
			// Continue with other tools
		} else {
			logger.Infof("✅ Successfully installed %s %s", toolName, version)
		}
	}
	
	// If no version is active, activate the first version we installed
	if needsActive && versionToActivate != "" {
		logger.Infof("Setting %s as the active version...", versionToActivate)
		if err := manager.UseVersion(versionToActivate); err != nil {
			logger.WithError(err).Error("Failed to set active version")
		} else {
			logger.Infof("✅ Version %s is now active", versionToActivate)
		}
	}
	
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