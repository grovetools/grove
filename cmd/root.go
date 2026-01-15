package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/cmd/internal"
	"github.com/mattsolo1/grove-meta/pkg/delegation"
	"github.com/mattsolo1/grove-meta/pkg/overrides"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	meta_workspace "github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var rootCmd = cli.NewStandardCommand("grove", "Grove workspace orchestrator and tool manager")

func init() {
	// Set long description
	rootCmd.Long = `Grove workspace orchestrator and tool manager.

Run 'grove <tool>' to delegate to installed tools, or use subcommands below.`

	// Add subcommands
	rootCmd.AddCommand(newBootstrapCmd())
	rootCmd.AddCommand(newBuildCmd())
	rootCmd.AddCommand(newDepsCmd())
	rootCmd.AddCommand(newSchemaCmd())
	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(internal.NewInternalCmd())

	// Register deprecated command shims for backwards compatibility
	registerDeprecatedCommands(rootCmd)

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
	rootCmd.Args = cobra.ArbitraryArgs // Allow any arguments to be passed

	// Custom help function to add available tools section
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		defaultHelp(cmd, args)
		// Only show tools section for root command
		if cmd == rootCmd {
			printAvailableTools()
		}
	})
}

// printAvailableTools prints the available ecosystem tools
func printAvailableTools() {
	// Get all tools and their aliases
	var tools []struct{ alias, repo string }
	for repo, info := range sdk.GetToolRegistry() {
		tools = append(tools, struct{ alias, repo string }{info.Alias, repo})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].alias < tools[j].alias
	})

	fmt.Println("\nAvailable Tools (run 'grove <tool>'):")
	for _, t := range tools {
		fmt.Printf("  %-16s %s\n", t.alias, t.repo)
	}
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
