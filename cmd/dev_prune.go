package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/spf13/cobra"
)

func newDevPruneCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("prune", "Remove registered versions whose binaries no longer exist")

	cmd.Long = `Scans all registered local development links and removes those whose
binary paths no longer exist on the filesystem. This helps clean up after
deleted worktrees or moved directories.`

	cmd.Example = `  # Remove all broken links
  grove dev prune`

	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		removedCount := 0

		// Track binaries to clean up
		binariesToClean := make(map[string]bool)

		// Check each binary and its links
		for binaryName, binInfo := range config.Binaries {
			linksToRemove := []string{}

			for alias, linkInfo := range binInfo.Links {
				// Check if the binary path exists
				if _, err := os.Stat(linkInfo.Path); os.IsNotExist(err) {
					linksToRemove = append(linksToRemove, alias)
					fmt.Printf("Removing %s:%s (path no longer exists: %s)\n",
						binaryName, alias, linkInfo.Path)
					removedCount++
				}
			}

			// Remove broken links
			for _, alias := range linksToRemove {
				delete(binInfo.Links, alias)

				// If this was the current version, clear it
				if binInfo.Current == alias {
					binInfo.Current = ""
					binariesToClean[binaryName] = true
				}
			}

			// If no links remain, mark binary for removal
			if len(binInfo.Links) == 0 {
				binariesToClean[binaryName] = true
			}
		}

		// Clean up binaries with no links and remove symlinks
		groveHome, _ := devlinks.GetGroveHome()
		binDir := filepath.Join(groveHome, "bin")

		for binaryName := range binariesToClean {
			if binInfo, ok := config.Binaries[binaryName]; ok && len(binInfo.Links) == 0 {
				delete(config.Binaries, binaryName)
			}

			// Remove symlink if no current version
			if binInfo, ok := config.Binaries[binaryName]; !ok || binInfo.Current == "" {
				symlink := filepath.Join(binDir, binaryName)
				_ = os.Remove(symlink)
			}
		}

		// Save the updated config
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		if removedCount == 0 {
			fmt.Println("No broken links found.")
		} else {
			fmt.Printf("Removed %d broken link(s).\n", removedCount)
		}

		return nil
	}

	return cmd
}
