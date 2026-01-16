package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/overrides"
	meta_workspace "github.com/grovetools/grove/pkg/workspace"
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

	cmd.Example = `  # Point to all binaries from a workspace (auto-discovers from grove.yml)
  cd ~/myproject
  grove dev point /path/to/grove-flow/.grove-worktrees/feature

  # Point a specific binary to a workspace
  grove dev point flow /path/to/grove-flow/.grove-worktrees/feature

  # Point to a specific binary file
  grove dev point flow /path/to/grove-flow/.grove-worktrees/feature/bin/flow

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

		// Remove mode
		if removeFlag {
			binaryName := args[0]
			if err := overrides.RemoveBinaryOverride(workspaceRoot, binaryName); err != nil {
				return fmt.Errorf("failed to remove override: %w", err)
			}
			successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Removed override for '%s'", theme.IconSuccess, binaryName))
			fmt.Println(successMsg)
			return nil
		}

		// Determine if we have one arg (path) or two args (binary name + path)
		var targetPath string
		var specificBinary string

		if len(args) == 1 {
			// Single argument: could be a path to a workspace
			targetPath = args[0]
		} else {
			// Two arguments: binary name + path
			specificBinary = args[0]
			targetPath = args[1]
		}

		// Resolve to absolute path
		absPath, err := filepath.Abs(targetPath)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		// Check if path exists
		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("path does not exist: %s", absPath)
		}

		// If it's a file, set override for specific binary
		if !info.IsDir() {
			if specificBinary == "" {
				return fmt.Errorf("when providing a file path, you must specify the binary name: grove dev point <binary-name> <file-path>")
			}
			if err := overrides.SetBinaryOverride(workspaceRoot, specificBinary, absPath); err != nil {
				return fmt.Errorf("failed to set override: %w", err)
			}
			successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Configured '%s' to use:", theme.IconSuccess, specificBinary))
			pathMsg := theme.DefaultTheme.Muted.Render(fmt.Sprintf("  %s", absPath))
			fmt.Println(successMsg)
			fmt.Println(pathMsg)
			return nil
		}

		// It's a directory - try to discover binaries from grove.yml
		binaries, err := discoverBinariesFromWorkspace(absPath, specificBinary)
		if err != nil {
			return fmt.Errorf("failed to discover binaries: %w", err)
		}

		if len(binaries) == 0 {
			return fmt.Errorf("no binaries found in %s", absPath)
		}

		// Set overrides for all discovered binaries
		successCount := 0
		for binaryName, binaryPath := range binaries {
			if err := overrides.SetBinaryOverride(workspaceRoot, binaryName, binaryPath); err != nil {
				fmt.Printf("Warning: failed to set override for %s: %v\n", binaryName, err)
				continue
			}
			successCount++
		}

		if successCount == 0 {
			return fmt.Errorf("failed to set any overrides")
		}

		// Display success message
		if len(binaries) == 1 {
			for binaryName, binaryPath := range binaries {
				successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Configured '%s' to use:", theme.IconSuccess, binaryName))
				pathMsg := theme.DefaultTheme.Muted.Render(fmt.Sprintf("  %s", binaryPath))
				fmt.Println(successMsg)
				fmt.Println(pathMsg)
			}
		} else {
			successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Configured %d binaries from:", theme.IconSuccess, successCount))
			pathMsg := theme.DefaultTheme.Muted.Render(fmt.Sprintf("  %s", absPath))
			fmt.Println(successMsg)
			fmt.Println(pathMsg)
			for binaryName := range binaries {
				fmt.Printf("  %s %s\n", theme.IconArrow, theme.DefaultTheme.Bold.Render(binaryName))
			}
		}

		return nil
	}

	return cmd
}

// discoverBinariesFromWorkspace discovers which binaries the target workspace provides.
// If specificBinary is set, only return that binary.
func discoverBinariesFromWorkspace(workspacePath, specificBinary string) (map[string]string, error) {
	// Use grove-meta's binary discovery
	discovered, err := meta_workspace.DiscoverLocalBinaries(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("failed to discover binaries: %w", err)
	}

	binaries := make(map[string]string)

	for _, binary := range discovered {
		// If a specific binary was requested, only include that one
		if specificBinary != "" && binary.Name != specificBinary {
			continue
		}

		// Verify the binary exists
		if _, err := os.Stat(binary.Path); err != nil {
			continue // Skip if binary doesn't exist
		}

		binaries[binary.Name] = binary.Path
	}

	return binaries, nil
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
