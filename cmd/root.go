package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

var rootCmd = cli.NewStandardCommand("grove", "Grove workspace orchestrator and tool manager")

func init() {
	
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
	registry, err := LoadRegistry()
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	
	// Find the tool by name or alias
	var tool *Tool
	for _, t := range registry.Tools {
		if t.Name == toolName || t.Alias == toolName {
			tool = &t
			break
		}
	}
	
	if tool == nil {
		return fmt.Errorf("unknown command: %s", toolName)
	}
	
	// Construct the binary name
	binaryName := tool.Binary
	if binaryName == "" {
		binaryName = fmt.Sprintf("grove-%s", tool.Name)
	}
	
	// Check if binary exists
	groveDir := os.Getenv("GROVE_DIR")
	if groveDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		groveDir = filepath.Join(homeDir, ".grove")
	}
	
	binaryPath := filepath.Join(groveDir, "bin", binaryName)
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("tool '%s' is not installed. Run 'grove install %s' to install it", toolName, tool.Name)
	}
	
	// Execute the binary
	cmd := exec.Command(binaryPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	return cmd.Run()
}