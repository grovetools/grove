package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-meta/cmd/internal"
	meta_workspace "github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var rootCmd = cli.NewStandardCommand("grove", "Grove workspace orchestrator and tool manager")

func init() {
	// Add subcommands
	rootCmd.AddCommand(newActivateCmd())
	rootCmd.AddCommand(newChangelogCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newDepsCmd())
	rootCmd.AddCommand(newDocsCmd())
	rootCmd.AddCommand(newLogsCmd())
	rootCmd.AddCommand(newRunCmd())
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

// findWorkspaceRoot searches upward from the current directory for a
// .grove/workspace marker file, indicating we're in a managed workspace.
// This supports both Grove ecosystem worktrees and user monorepos.
func findWorkspaceRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	dir := cwd
	for {
		// Check for the new generic marker
		markerPath := filepath.Join(dir, ".grove", "workspace")
		if _, err := os.Stat(markerPath); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

// delegateToTool attempts to run an installed Grove tool, prioritizing
// binaries from the current workspace if one is detected.
func delegateToTool(toolName string, args []string) error {
	logger := logging.NewLogger("grove-meta")
	logger.WithField("tool", toolName).Debug("Delegating to tool")
	
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	var toolPath string
	var cmdEnv []string // Environment for the command
	
	// First check if we're in a workspace
	if workspaceRoot := findWorkspaceRoot(); workspaceRoot != "" {
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
	
	// If not found in workspace (or not in a workspace), fall back to the standard location
	if toolPath == "" {
		toolPath = filepath.Join(homeDir, ".grove", "bin", toolName)
		logger.WithField("path", toolPath).Debug("Using global binary")
		
		// Check if the tool exists
		if _, err := os.Stat(toolPath); os.IsNotExist(err) {
			return fmt.Errorf("unknown tool: %s. Run 'grove install %s' or check if you are in the correct workspace.", toolName, toolName)
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
