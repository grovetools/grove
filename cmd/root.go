package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/cmd/internal"
	"github.com/spf13/cobra"
)

var rootCmd = cli.NewStandardCommand("grove", "Grove workspace orchestrator and tool manager")

func init() {
	// Add subcommands
	rootCmd.AddCommand(newDepsCmd())
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
	rootCmd.DisableFlagParsing = false
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

// delegateToTool attempts to run an installed Grove tool
func delegateToTool(toolName string, args []string) error {
	// Check if the tool exists in the active version's bin directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	toolPath := filepath.Join(homeDir, ".grove", "bin", toolName)

	// Check if the tool exists
	if _, err := os.Stat(toolPath); os.IsNotExist(err) {
		return fmt.Errorf("unknown tool: %s", toolName)
	}

	// Execute the binary
	cmd := exec.Command(toolPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
