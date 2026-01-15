package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/cmd/internal"
	"github.com/mattsolo1/grove-meta/pkg/delegation"
	"github.com/mattsolo1/grove-meta/pkg/overrides"
	meta_workspace "github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var rootCmd = cli.NewStandardCommand("grove", "Grove workspace orchestrator and tool manager")

func init() {
	// Set long description
	rootCmd.Long = `Grove workspace orchestrator and tool manager.

Grove acts as a command delegator. When you run 'grove <tool>', it finds and
executes the specified tool.

Delegation Behavior:
  By default, grove uses a 'global-first' strategy, always using the globally
  configured binaries (as shown in 'grove list' or 'grove dev current').

  To switch to 'workspace-aware' delegation, run:
    grove dev delegate workspace

  This will make grove prioritize binaries from your current workspace, which is
  useful for local development. To switch back to global-first, run:
    grove dev delegate global`

	// Add subcommands
	rootCmd.AddCommand(newBootstrapCmd())
	rootCmd.AddCommand(newBuildCmd())
	rootCmd.AddCommand(newChangelogCmd())
	rootCmd.AddCommand(newDepsCmd())
	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newSchemaCmd())
	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(newStarshipCmd())
	rootCmd.AddCommand(internal.NewInternalCmd())

	// Set up the root command's RunE to handle tool delegation
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}

		// If it's not a known command, try to delegate to an installed tool
		toolName := args[0]
		toolArgs := args[1:]

		return delegateToTool(toolName, toolArgs)
	}

	// Allow arbitrary args for tool delegation
	rootCmd.FParseErrWhitelist.UnknownFlags = true
	rootCmd.Args = cobra.ArbitraryArgs  // Allow any arguments to be passed
}

// Execute runs the root command
func Execute() error {
	// Check if the first argument might be a tool to delegate to
	if len(os.Args) > 1 {
		potentialTool := os.Args[1]
		
		// Check if this is NOT a known subcommand
		if _, _, err := rootCmd.Find(os.Args[1:]); err != nil {
			// This might be a tool delegation request
			// Try to delegate to the tool
			if err := delegateToTool(potentialTool, os.Args[2:]); err != nil {
				// If delegation fails, show the original cobra error
				return rootCmd.Execute()
			}
			return nil
		}
	}
	
	return rootCmd.Execute()
}

// findWorkspaceRoot uses grove-core's workspace detection to find the workspace root.
// This properly handles all workspace types: standalone projects, ecosystem roots,
// worktrees, and sub-projects.
func findWorkspaceRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	node, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		return ""
	}

	return node.Path
}

// delegateToTool attempts to run an installed Grove tool.
// By default, it uses globally managed binaries (global-first).
// Set GROVE_DELEGATION_MODE=workspace to opt-in to workspace-aware delegation.
func delegateToTool(toolName string, args []string) error {
	logger := logging.NewLogger("grove-meta")
	logger.WithField("tool", toolName).Debug("Delegating to tool")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	var toolPath string
	var cmdEnv []string // Environment for the command
	delegationMode := delegation.GetMode()

	// Check if we're in a workspace for potential overrides
	workspaceRoot := findWorkspaceRoot()

	// PRIORITY 1: Check for workspace-specific binary overrides
	if workspaceRoot != "" {
		if overridePath := overrides.GetBinaryOverride(workspaceRoot, toolName); overridePath != "" {
			// Verify the override binary still exists
			if _, err := os.Stat(overridePath); err == nil {
				toolPath = overridePath
				logger.WithField("path", toolPath).Debug("Using workspace override binary")
			} else {
				logger.WithField("path", overridePath).Warn("Workspace override binary not found, ignoring")
			}
		}
	}

	// PRIORITY 2: Check for workspace-local binaries (if delegation mode is workspace)
	if toolPath == "" && delegationMode == delegation.ModeWorkspace {
		logger.Debug("Delegation mode is workspace; attempting workspace-aware binary discovery.")
		if workspaceRoot != "" {
			logger.WithField("workspace", workspaceRoot).Debug("Found workspace root")
			// Try to find the binary in this workspace
			workspaceBinaries, err := meta_workspace.DiscoverLocalBinaries(workspaceRoot)
			if err == nil {
				var foundBinary *meta_workspace.BinaryMeta
				for i, binary := range workspaceBinaries {
					if binary.Name == toolName {
						// Check if the binary actually exists
						if _, err := os.Stat(binary.Path); err == nil {
							foundBinary = &workspaceBinaries[i]
							break
						}
					}
				}

				if foundBinary != nil {
					toolPath = foundBinary.Path
					logger.WithField("path", toolPath).Debug("Using workspace binary")

					// Build PATH with all workspace bin directories first for correct inter-tool calls
					var binDirs []string
					seenDirs := make(map[string]bool)
					for _, b := range workspaceBinaries {
						binDir := filepath.Dir(b.Path)
						if !seenDirs[binDir] {
							binDirs = append(binDirs, binDir)
							seenDirs[binDir] = true
						}
					}
					if len(binDirs) > 0 {
						currentPath := os.Getenv("PATH")
						newPath := strings.Join(binDirs, string(os.PathListSeparator)) + string(os.PathListSeparator) + currentPath

						cmdEnv = os.Environ()
						// Update PATH in the environment
						pathSet := false
						for i, env := range cmdEnv {
							if strings.HasPrefix(env, "PATH=") {
								cmdEnv[i] = "PATH=" + newPath
								pathSet = true
								break
							}
						}
						if !pathSet {
							cmdEnv = append(cmdEnv, "PATH="+newPath)
						}
						cmdEnv = append(cmdEnv, "GROVE_WORKSPACE_ROOT="+workspaceRoot)
					}
				}
			}
		}
	}

	// PRIORITY 3 (DEFAULT): Fall back to the globally managed binary.
	// This block is executed if the opt-in is not active, or if a local binary
	// was not found in the workspace.
	if toolPath == "" {
		toolPath = filepath.Join(homeDir, ".grove", "bin", toolName)
		logger.WithField("path", toolPath).Debug("Using global binary")

		// Check if the tool exists
		if _, err := os.Stat(toolPath); os.IsNotExist(err) {
			return fmt.Errorf("unknown tool: %s. Run 'grove install %s' or check spelling.", toolName, toolName)
		}
	}

	// Execute the binary
	cmd := exec.Command(toolPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if len(cmdEnv) > 0 {
		cmd.Env = cmdEnv
	}

	return cmd.Run()
}
