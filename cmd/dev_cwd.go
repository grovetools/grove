package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newDevCwdCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("cwd", "Link and use binaries from current directory")

	cmd.Long = `Automatically register and activate binaries from the current working directory.
This is a convenience command that combines 'grove dev link' and 'grove dev use' in one step,
using the current directory's binaries.

This command is particularly useful when developing features in a worktree.`

	cmd.Example = `  # In a feature worktree, link and use all binaries
  cd ~/grove-ecosystem/worktrees/my-feature
  grove dev cwd`

	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}

		// Convert to absolute path
		absPath, err := filepath.Abs(cwd)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		// Discover binaries in the current directory
		discoveredBinaries, err := workspace.DiscoverLocalBinaries(absPath)
		if err != nil {
			return fmt.Errorf("failed to discover binaries: %w", err)
		}

		if len(discoveredBinaries) == 0 {
			return fmt.Errorf("no binaries found in %s", absPath)
		}

		// Use directory name as alias
		alias := filepath.Base(absPath)

		// Load existing configuration
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Register and activate each discovered binary
		for _, binary := range discoveredBinaries {
			binaryName := binary.Name
			binaryPath := binary.Path

			// Ensure binary exists
			if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
				fmt.Printf("Warning: binary '%s' not found at %s, skipping\n", binaryName, binaryPath)
				continue
			}

			// Initialize binary info if needed
			if config.Binaries[binaryName] == nil {
				config.Binaries[binaryName] = &devlinks.BinaryLinks{
					Links: make(map[string]devlinks.LinkInfo),
				}
			}

			// Create link info
			linkInfo := devlinks.LinkInfo{
				Path:         binaryPath,
				WorktreePath: absPath,
				RegisteredAt: time.Now().Format(time.RFC3339),
			}

			config.Binaries[binaryName].Links[alias] = linkInfo
			fmt.Printf("Registered binary '%s' version '%s'\n", binaryName, alias)
		}

		// Save configuration
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		// Activate all discovered binaries
		for _, binary := range discoveredBinaries {
			if err := activateDevLink(binary.Name, alias); err != nil {
				fmt.Printf("Warning: failed to activate '%s': %v\n", binary.Name, err)
				continue
			}
			fmt.Printf("Activated '%s' version '%s'\n", binary.Name, alias)
		}

		fmt.Printf("\nSwitched to binaries from: %s\n", absPath)
		fmt.Printf("Use 'grove dev reset' to switch back to main versions\n")

		return nil
	}

	return cmd
}