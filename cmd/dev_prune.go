package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/grove/pkg/devlinks"
	"github.com/grovetools/grove/pkg/reconciler"
	"github.com/grovetools/grove/pkg/sdk"
	"github.com/spf13/cobra"
)

func newDevPruneCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("prune", "Remove registered versions whose binaries no longer exist")

	cmd.Long = `Scans all registered local development links and removes those whose
binary paths no longer exist on the filesystem. This helps clean up after
deleted worktrees or moved directories.

If a pruned link was the active version, Grove will automatically fall back
to the 'main' version or the active release version.`

	cmd.Example = `  # Remove all broken links
  grove dev prune`

	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		removedCount := 0
		binariesToReconcile := make(map[string]bool)

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
				wasCurrent := binInfo.Current == alias
				delete(binInfo.Links, alias)

				// If the pruned link was active, find the best available fallback.
				if wasCurrent {
					var mainRepoAliases []string
					var worktreeAliases []string
					for aliasName, linkInfo := range binInfo.Links {
						node, err := workspace.GetProjectByPath(linkInfo.WorktreePath)
						if err != nil {
							// If we can't classify it, assume it's a worktree to be safe.
							worktreeAliases = append(worktreeAliases, aliasName)
							continue
						}

						if node.IsWorktree() {
							worktreeAliases = append(worktreeAliases, aliasName)
						} else {
							mainRepoAliases = append(mainRepoAliases, aliasName)
						}
					}

					var newCurrent string
					if len(mainRepoAliases) > 0 {
						sort.Strings(mainRepoAliases)
						// Prefer 'main', 'master', or the repo name itself if available.
						repoName, _, _, _ := sdk.FindTool(binaryName)

						preferredAliases := []string{"main", "master", repoName}
						for _, pAlias := range preferredAliases {
							for _, mAlias := range mainRepoAliases {
								if mAlias == pAlias {
									newCurrent = mAlias
									break
								}
							}
							if newCurrent != "" {
								break
							}
						}
						// If no preferred alias found, take the first alphabetical one.
						if newCurrent == "" {
							newCurrent = mainRepoAliases[0]
						}

					} else if len(worktreeAliases) > 0 {
						sort.Strings(worktreeAliases)
						newCurrent = worktreeAliases[0]
					}

					if newCurrent != "" {
						binInfo.Current = newCurrent
						fmt.Printf("Active link pruned. Falling back to '%s' version for '%s'.\n", newCurrent, binaryName)
					} else {
						binInfo.Current = "" // Clear current to fall back to release version
						fmt.Printf("Active link pruned. No other dev links found. Falling back to release version for '%s'.\n", binaryName)
					}
					binariesToReconcile[binaryName] = true
				}
			}

			// If no links remain, mark binary for removal from config
			if len(binInfo.Links) == 0 {
				binariesToReconcile[binaryName] = true
			}
		}

		// Clean up binaries with no links
		for binaryName := range binariesToReconcile {
			if binInfo, ok := config.Binaries[binaryName]; ok && len(binInfo.Links) == 0 {
				delete(config.Binaries, binaryName)
			}
		}

		// Save the updated config before reconciling
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		// Reconcile binaries whose active links were removed or that now have no links
		if len(binariesToReconcile) > 0 {
			fmt.Println("\nReconciling symlinks for affected binaries...")
			// Load tool versions for reconciler
			tv, err := sdk.LoadToolVersions(os.Getenv("HOME") + "/.grove")
			if err != nil {
				tv = &sdk.ToolVersions{Versions: make(map[string]string)}
			}
			r, err := reconciler.NewWithToolVersions(tv)
			if err != nil {
				return fmt.Errorf("failed to create reconciler: %w", err)
			}

			for binaryName := range binariesToReconcile {
				if err := r.Reconcile(binaryName); err != nil {
					fmt.Printf("Warning: failed to reconcile %s: %v\n", binaryName, err)
				}
			}
			fmt.Println("* Symlinks updated.")
		}

		if removedCount == 0 {
			fmt.Println("No broken links found.")
		} else {
			fmt.Printf("\nRemoved %d broken link(s).\n", removedCount)
		}

		return nil
	}

	return cmd
}
