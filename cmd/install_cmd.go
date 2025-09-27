package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
Use 'all' to install all available tools, or 'all@nightly' to install nightly builds of all tools.

Examples:
  grove install cx           # Install latest version of cx
  grove install cx@v0.1.0    # Install specific version of cx
  grove install cx@nightly   # Build and install cx from main branch
  grove install cx nb flow   # Install multiple tools
  grove install all          # Install all available tools
  grove install all@nightly  # Install nightly builds of all tools
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

	// Expand "all" to list of all tools, handling all@nightly specially
	var toolsToProcess []string
	var versionForAll string
	
	if len(args) == 1 && args[0] == "all" {
		toolsToProcess = sdk.GetAllTools()
		versionForAll = "latest"
		fmt.Println(toolNameStyle.Render("Installing all Grove tools..."))
	} else if len(args) == 1 && args[0] == "all@nightly" {
		toolsToProcess = sdk.GetAllTools()
		versionForAll = "nightly"
		fmt.Println(toolNameStyle.Render("Installing nightly builds for all Grove tools..."))
	} else {
		// Resolve dependencies for the requested tools
		resolvedTools, err := manager.ResolveDependencies(args)
		if err != nil {
			return fmt.Errorf("failed to resolve dependencies: %w", err)
		}

		// Inform user about added dependencies
		if len(resolvedTools) > len(args) {
			added := getAddedDependencies(args, resolvedTools)
			logger.Infof("The following dependencies will also be installed: %s", strings.Join(added, ", "))
		}

		toolsToProcess = resolvedTools
	}

	// Migration: ensure we're using the new per-tool version system
	if err := sdk.MigrateFromSingleVersion(os.Getenv("HOME") + "/.grove"); err != nil {
		logger.Debugf("Migration check failed: %v", err)
	}

	// Install each tool
	for _, toolSpec := range toolsToProcess {
		var toolName, version string
		
		// If we're processing all tools with a specific version, use that
		if versionForAll != "" {
			toolName = toolSpec
			version = versionForAll
		} else {
			// Parse tool[@version]
			parts := strings.Split(toolSpec, "@")
			toolName = parts[0]
			version = "latest"
			if len(parts) > 1 {
				version = parts[1]
			}
		}

		// Get currently installed version
		currentVersion, _ := manager.GetToolVersion(toolName)

		// Check if the binary actually exists for the current version
		var binaryExists bool
		if currentVersion != "" {
			// Load tool versions and create reconciler to find the binary path
			tv, _ := sdk.LoadToolVersions(os.Getenv("HOME") + "/.grove")
			if tv == nil {
				tv = &sdk.ToolVersions{Versions: make(map[string]string)}
			}
			r, _ := reconciler.NewWithToolVersions(tv)
			if r != nil {
				_, _, currentPath := r.GetEffectiveSource(toolName)
				if currentPath != "" {
					if _, err := os.Stat(currentPath); err == nil {
						binaryExists = true
					}
				}
			}
		}

		// Handle nightly builds
		if version == "nightly" {
			fmt.Printf("%s %s from main branch...\n",
				faintStyle.Render("Building"),
				toolNameStyle.Render(toolName))

			builtVersion, err := manager.InstallToolFromSource(toolName)
			if err != nil {
				fmt.Printf("%s %s: %s\n",
					errorStyle.Render("✗"),
					toolNameStyle.Render(toolName),
					errorStyle.Render(err.Error()))
				continue
			}
			version = builtVersion
		} else {
			// Get actual version tag if "latest" specified
			if version == "latest" {
				latestVersion, err := manager.GetLatestVersionTag(toolName)
				if err != nil {
					fmt.Printf("%s %s: %s\n",
						errorStyle.Render("✗"),
						toolNameStyle.Render(toolName),
						errorStyle.Render(fmt.Sprintf("Failed to get latest version: %v", err)))
					continue
				}
				version = latestVersion

				// Check if already up-to-date AND binary exists
				if currentVersion == version && binaryExists {
					fmt.Printf("%s %s... %s (%s)\n",
						faintStyle.Render("Checking"),
						toolNameStyle.Render(toolName),
						successStyle.Render("already up to date"),
						versionStyle.Render(version))
					continue
				}
			}

			// Determine if this is an install, update, or reinstall
			var action string
			if currentVersion == "" {
				action = "Installing"
			} else if currentVersion == version {
				action = "Reinstalling"
			} else {
				action = "Updating"
			}

			fmt.Printf("%s %s %s...\n",
				faintStyle.Render(action),
				toolNameStyle.Render(toolName),
				versionStyle.Render(version))

			if err := manager.InstallTool(toolName, version); err != nil {
				// Check for "no binary found" error
				if strings.Contains(err.Error(), "no binary found") {
					fmt.Printf("%s %s %s: %s\n",
						errorStyle.Render("✗"),
						toolNameStyle.Render(toolName),
						versionStyle.Render(version),
						errorStyle.Render(fmt.Sprintf("No binary available for your system (%s/%s)", runtime.GOOS, runtime.GOARCH)))
				} else {
					fmt.Printf("%s %s %s: %s\n",
						errorStyle.Render("✗"),
						toolNameStyle.Render(toolName),
						versionStyle.Render(version),
						errorStyle.Render(err.Error()))
				}
				continue
			}
		}

		// Auto-activate the installed version
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
					// Print success message
					if strings.HasPrefix(version, "nightly-") {
						fmt.Printf("%s %s %s built from source and is now active\n",
							successStyle.Render("✅"),
							toolNameStyle.Render(toolName),
							versionStyle.Render(version))
					} else if currentVersion == "" {
						fmt.Printf("%s %s %s installed and active\n",
							successStyle.Render("✅"),
							toolNameStyle.Render(toolName),
							versionStyle.Render(version))
					} else if currentVersion == version {
						fmt.Printf("%s %s %s reinstalled and active\n",
							successStyle.Render("✅"),
							toolNameStyle.Render(toolName),
							versionStyle.Render(version))
					} else {
						fmt.Printf("%s %s updated to %s (was %s) and is now active\n",
							updateStyle.Render("✅"),
							toolNameStyle.Render(toolName),
							versionStyle.Render(version),
							faintStyle.Render(currentVersion))
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

// getAddedDependencies finds which tools were added by the resolver
func getAddedDependencies(original, resolved []string) []string {
	originalSet := make(map[string]bool)
	for _, spec := range original {
		repoName, _, _, _ := sdk.FindTool(strings.Split(spec, "@")[0])
		if repoName != "" {
			originalSet[repoName] = true
		}
	}

	var added []string
	for _, spec := range resolved {
		repoName := strings.Split(spec, "@")[0]
		if !originalSet[repoName] {
			_, _, alias, _ := sdk.FindTool(repoName)
			added = append(added, alias)
		}
	}
	return added
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
