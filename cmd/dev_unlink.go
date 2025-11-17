package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/spf13/cobra"
)

func newDevUnlinkCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("unlink", "Remove a registered local development version")

	cmd.Long = `Removes a specific registered version of a binary from the development links.
If the removed version was the current active version, Grove will automatically fall back:
1. To the 'main' development version, if available.
2. To the active release version, if no 'main' version is linked.`

	cmd.Example = `  # Remove a specific version of flow
  grove dev unlink flow feature-branch
  
  # Remove the main version of cx
  grove dev unlink cx main`

	cmd.Args = cobra.ExactArgs(2)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		binaryName := args[0]
		alias := args[1]

		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		binInfo, ok := config.Binaries[binaryName]
		if !ok {
			return fmt.Errorf("binary '%s' is not registered", binaryName)
		}

		linkInfo, ok := binInfo.Links[alias]
		if !ok {
			return fmt.Errorf("version '%s' not found for binary '%s'", alias, binaryName)
		}

		wasCurrent := binInfo.Current == alias

		// Remove the link
		delete(binInfo.Links, alias)

		if wasCurrent {
			// If the active link was removed, find the best available fallback.
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
				fmt.Printf("Active link removed. Falling back to '%s' version for '%s'.\n", newCurrent, binaryName)
			} else {
				binInfo.Current = "" // Clear current to fall back to release version
				fmt.Printf("Active link removed. No other dev links found. Falling back to release version for '%s'.\n", binaryName)
			}
		}

		// If there are no more links for this binary, remove it entirely
		if len(binInfo.Links) == 0 {
			delete(config.Binaries, binaryName)
		}

		// Save the updated config
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Removed version '%s' of '%s' (%s)\n", alias, binaryName, linkInfo.WorktreePath)

		// If the active link was changed, reconcile the symlink
		if wasCurrent {
			fmt.Printf("Reconciling symlink for '%s'...\n", binaryName)
			// Load tool versions for reconciler
			tv, err := sdk.LoadToolVersions(os.Getenv("HOME") + "/.grove")
			if err != nil {
				// Don't fail, just proceed with an empty config
				tv = &sdk.ToolVersions{Versions: make(map[string]string)}
			}
			r, err := reconciler.NewWithToolVersions(tv)
			if err != nil {
				return fmt.Errorf("failed to create reconciler: %w", err)
			}

			if err := r.Reconcile(binaryName); err != nil {
				return fmt.Errorf("failed to update symlink: %w", err)
			}
			fmt.Println("✓ Symlink updated.")
		}

		return nil
	}

	return cmd
}
