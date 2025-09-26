package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newActivateCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("activate", "Generate shell commands to activate workspace binaries")
	
	cmd.Long = `Generate shell commands to add workspace binaries to PATH.
This command outputs shell commands that, when evaluated, will modify your
current shell's PATH to prioritize workspace binaries.

Usage:
  eval "$(grove activate)"        # Activate workspace binaries in current shell
  eval "$(grove activate --reset)" # Reset to original PATH

The command detects your shell and outputs appropriate syntax.
If you're not in a workspace, it will inform you without modifying PATH.`
	
	cmd.Example = `  # Activate workspace binaries
  eval "$(grove activate)"
  
  # Check what would be activated (dry run)
  grove activate
  
  # Reset PATH to original
  eval "$(grove activate --reset)"
  
  # Use in shell configuration (e.g., .zshrc)
  alias gwa='eval "$(grove activate)"'
  alias gwd='eval "$(grove activate --reset)"'`
	
	var resetFlag bool
	var shellType string
	
	cmd.Flags().BoolVar(&resetFlag, "reset", false, "Generate commands to reset PATH to original state")
	cmd.Flags().StringVar(&shellType, "shell", "", "Shell type (bash, zsh, fish). Auto-detected if not specified")
	
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Detect shell if not specified
		if shellType == "" {
			shellType = detectShell()
		}
		
		if resetFlag {
			// Generate reset commands
			return generateResetCommands(shellType)
		}
		
		// Check if we're in a workspace
		workspaceRoot := findWorkspaceRoot()
		if workspaceRoot == "" {
			// Not in workspace - output informational message as comment
			switch shellType {
			case "fish":
				fmt.Println("# Not in a Grove workspace. No PATH changes needed.")
				fmt.Println("echo 'Not in a Grove workspace. No PATH changes needed.' >&2")
			default:
				fmt.Println("# Not in a Grove workspace. No PATH changes needed.")
				fmt.Println("echo 'Not in a Grove workspace. No PATH changes needed.' >&2")
			}
			return nil
		}
		
		// Discover workspace binaries
		binaries, err := workspace.DiscoverLocalBinaries(workspaceRoot)
		if err != nil {
			return fmt.Errorf("failed to discover binaries: %w", err)
		}
		
		if len(binaries) == 0 {
			fmt.Printf("# No binaries found in workspace: %s\n", workspaceRoot)
			fmt.Println("echo 'No binaries found in workspace' >&2")
			return nil
		}
		
		// Collect unique bin directories
		binDirs := make(map[string]bool)
		for _, binary := range binaries {
			binDir := filepath.Dir(binary.Path)
			binDirs[binDir] = true
		}
		
		// Build PATH addition
		var pathDirs []string
		for dir := range binDirs {
			// Only add directories that actually exist
			if _, err := os.Stat(dir); err == nil {
				pathDirs = append(pathDirs, dir)
			}
		}
		
		if len(pathDirs) == 0 {
			fmt.Println("# No binary directories found")
			fmt.Println("echo 'No binary directories found' >&2")
			return nil
		}
		
		// Generate shell-specific commands
		return generateActivateCommands(shellType, pathDirs, workspaceRoot)
	}
	
	return cmd
}

func detectShell() string {
	// Check SHELL environment variable
	shell := os.Getenv("SHELL")
	
	// Extract base name
	if shell != "" {
		shell = filepath.Base(shell)
		
		// Normalize common shells
		switch {
		case strings.Contains(shell, "zsh"):
			return "zsh"
		case strings.Contains(shell, "bash"):
			return "bash"
		case strings.Contains(shell, "fish"):
			return "fish"
		}
	}
	
	// Default based on OS
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	
	return "bash" // Safe default
}

func generateActivateCommands(shellType string, binDirs []string, workspaceRoot string) error {
	pathAddition := strings.Join(binDirs, string(os.PathListSeparator))
	
	switch shellType {
	case "fish":
		fmt.Println("# Activate Grove workspace binaries")
		fmt.Printf("# Workspace: %s\n", workspaceRoot)
		
		// Save original PATH if not already saved
		fmt.Println("if not set -q GROVE_ORIGINAL_PATH")
		fmt.Println("    set -gx GROVE_ORIGINAL_PATH $PATH")
		fmt.Println("end")
		
		// Set new PATH
		fmt.Printf("set -gx PATH %s $PATH\n", pathAddition)
		fmt.Printf("set -gx GROVE_WORKSPACE_ROOT '%s'\n", workspaceRoot)
		
		// Confirmation message
		fmt.Printf("echo '🌲 Activated Grove workspace: %s' >&2\n", filepath.Base(workspaceRoot))
		
	case "powershell":
		fmt.Println("# Activate Grove workspace binaries")
		fmt.Printf("# Workspace: %s\n", workspaceRoot)
		
		// Save original PATH if not already saved
		fmt.Println("if (-not $env:GROVE_ORIGINAL_PATH) {")
		fmt.Println("    $env:GROVE_ORIGINAL_PATH = $env:PATH")
		fmt.Println("}")
		
		// Set new PATH
		fmt.Printf("$env:PATH = '%s' + [System.IO.Path]::PathSeparator + $env:PATH\n", pathAddition)
		fmt.Printf("$env:GROVE_WORKSPACE_ROOT = '%s'\n", workspaceRoot)
		
		// Confirmation message
		fmt.Printf("Write-Host '🌲 Activated Grove workspace: %s' -ForegroundColor Green\n", filepath.Base(workspaceRoot))
		
	default: // bash, zsh, sh
		fmt.Println("# Activate Grove workspace binaries")
		fmt.Printf("# Workspace: %s\n", workspaceRoot)
		
		// Save original PATH if not already saved
		fmt.Println("if [ -z \"$GROVE_ORIGINAL_PATH\" ]; then")
		fmt.Println("    export GROVE_ORIGINAL_PATH=\"$PATH\"")
		fmt.Println("fi")
		
		// Set new PATH
		fmt.Printf("export PATH=\"%s:$PATH\"\n", pathAddition)
		fmt.Printf("export GROVE_WORKSPACE_ROOT='%s'\n", workspaceRoot)
		
		// Confirmation message
		fmt.Printf("echo '🌲 Activated Grove workspace: %s' >&2\n", filepath.Base(workspaceRoot))
	}
	
	return nil
}

func generateResetCommands(shellType string) error {
	switch shellType {
	case "fish":
		fmt.Println("# Reset PATH to original state")
		fmt.Println("if set -q GROVE_ORIGINAL_PATH")
		fmt.Println("    set -gx PATH $GROVE_ORIGINAL_PATH")
		fmt.Println("    set -e GROVE_ORIGINAL_PATH")
		fmt.Println("    set -e GROVE_WORKSPACE_ROOT")
		fmt.Println("    echo '✓ Reset PATH to original state' >&2")
		fmt.Println("else")
		fmt.Println("    echo 'No saved PATH to restore' >&2")
		fmt.Println("end")
		
	case "powershell":
		fmt.Println("# Reset PATH to original state")
		fmt.Println("if ($env:GROVE_ORIGINAL_PATH) {")
		fmt.Println("    $env:PATH = $env:GROVE_ORIGINAL_PATH")
		fmt.Println("    Remove-Item Env:GROVE_ORIGINAL_PATH")
		fmt.Println("    Remove-Item Env:GROVE_WORKSPACE_ROOT -ErrorAction SilentlyContinue")
		fmt.Println("    Write-Host '✓ Reset PATH to original state' -ForegroundColor Green")
		fmt.Println("} else {")
		fmt.Println("    Write-Host 'No saved PATH to restore' -ForegroundColor Yellow")
		fmt.Println("}")
		
	default: // bash, zsh, sh
		fmt.Println("# Reset PATH to original state")
		fmt.Println("if [ -n \"$GROVE_ORIGINAL_PATH\" ]; then")
		fmt.Println("    export PATH=\"$GROVE_ORIGINAL_PATH\"")
		fmt.Println("    unset GROVE_ORIGINAL_PATH")
		fmt.Println("    unset GROVE_WORKSPACE_ROOT")
		fmt.Println("    echo '✓ Reset PATH to original state' >&2")
		fmt.Println("else")
		fmt.Println("    echo 'No saved PATH to restore' >&2")
		fmt.Println("fi")
	}
	
	return nil
}