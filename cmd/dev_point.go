package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/overrides"
	"github.com/spf13/cobra"
)

// newDevPointCmd creates the `dev point` command.
func newDevPointCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("point", "Point current workspace to use specific binaries")
	cmd.Long = `Configure the current workspace to use a specific binary from another location.

This is useful for testing development versions of Grove tools against your projects.
For example, you can point your crud-app workspace to use a feature branch of the
'flow' tool while keeping everything else using global binaries.

Overrides are stored in .grove/overrides.json within the current workspace.`

	cmd.Example = `  # Point 'flow' binary to a specific worktree
  cd ~/myproject
  grove dev point flow /path/to/grove-flow/.grove-worktrees/feature/bin/flow

  # Point 'flow' to the binary in another workspace directory
  grove dev point flow /path/to/grove-flow/.grove-worktrees/feature

  # List all configured overrides
  grove dev point

  # Remove an override
  grove dev point --remove flow`

	cmd.Args = cobra.MaximumNArgs(2)

	var removeFlag bool
	cmd.Flags().BoolVar(&removeFlag, "remove", false, "Remove the override for a binary")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Find workspace root using grove-core's workspace detection
		workspaceRoot := findWorkspaceRoot()
		if workspaceRoot == "" {
			return fmt.Errorf("not in a Grove workspace. Use 'grove dev workspace' to check workspace status")
		}

		// List mode
		if len(args) == 0 {
			return listOverrides(workspaceRoot)
		}

		binaryName := args[0]

		// Remove mode
		if removeFlag {
			if err := overrides.RemoveBinaryOverride(workspaceRoot, binaryName); err != nil {
				return fmt.Errorf("failed to remove override: %w", err)
			}
			successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Removed override for '%s'", theme.IconSuccess, binaryName))
			fmt.Println(successMsg)
			return nil
		}

		// Set mode requires both binary name and path
		if len(args) < 2 {
			return fmt.Errorf("usage: grove dev point <binary-name> <path-to-binary-or-worktree>")
		}

		binaryPath := args[1]

		// Resolve to absolute path
		absPath, err := filepath.Abs(binaryPath)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		// Check if path exists
		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("path does not exist: %s", absPath)
		}

		// If it's a directory, assume it's a worktree and look for bin/<binaryName>
		if info.IsDir() {
			absPath = filepath.Join(absPath, "bin", binaryName)
			if _, err := os.Stat(absPath); err != nil {
				return fmt.Errorf("binary not found at %s", absPath)
			}
		}

		// Verify the file is executable
		if info, err := os.Stat(absPath); err != nil {
			return fmt.Errorf("binary not found: %s", absPath)
		} else if info.IsDir() {
			return fmt.Errorf("path is a directory, not a binary: %s", absPath)
		}

		// Set the override
		if err := overrides.SetBinaryOverride(workspaceRoot, binaryName, absPath); err != nil {
			return fmt.Errorf("failed to set override: %w", err)
		}

		successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Configured '%s' to use:", theme.IconSuccess, binaryName))
		pathMsg := theme.DefaultTheme.Muted.Render(fmt.Sprintf("  %s", absPath))
		fmt.Println(successMsg)
		fmt.Println(pathMsg)

		return nil
	}

	return cmd
}

func listOverrides(workspaceRoot string) error {
	overrideMap, err := overrides.ListOverrides(workspaceRoot)
	if err != nil {
		return fmt.Errorf("failed to list overrides: %w", err)
	}

	if len(overrideMap) == 0 {
		infoMsg := theme.DefaultTheme.Info.Render(fmt.Sprintf("%s No binary overrides configured", theme.IconInfo))
		detailMsg := theme.DefaultTheme.Muted.Render("Use 'grove dev point <binary> <path>' to configure overrides.")
		fmt.Println(infoMsg)
		fmt.Println(detailMsg)
		return nil
	}

	header := theme.DefaultTheme.Header.Render("Binary Overrides")
	fmt.Println(header)

	for binaryName, binaryPath := range overrideMap {
		nameLabel := theme.DefaultTheme.Bold.Render(fmt.Sprintf("%s %s:", theme.IconArrow, binaryName))
		pathValue := theme.DefaultTheme.Muted.Render(binaryPath)
		fmt.Printf("%s %s\n", nameLabel, pathValue)
	}

	return nil
}
