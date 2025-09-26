package cmd

import (
	"fmt"
	"os"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newDevWorkspaceCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("workspace", "Display information about the current workspace context")
	cmd.Long = `Provides information about the currently active Grove workspace.
A workspace is detected by the presence of a '.grove-workspace' file in a parent directory.
When inside a workspace, Grove automatically uses binaries from that workspace.`
	cmd.Example = `  # Show current workspace info
  grove dev workspace

  # Check if in a workspace (for scripts)
  grove dev workspace --check

  # Print the workspace root path
  grove dev workspace --path`

	cmd.Args = cobra.NoArgs

	var checkFlag bool
	var pathFlag bool
	cmd.Flags().BoolVar(&checkFlag, "check", false, "Exit with status 0 if in a workspace, 1 otherwise")
	cmd.Flags().BoolVar(&pathFlag, "path", false, "Print the workspace root path if found")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		workspaceRoot := findWorkspaceRoot()

		if checkFlag {
			if workspaceRoot != "" {
				os.Exit(0)
			}
			os.Exit(1)
		}

		if pathFlag {
			if workspaceRoot != "" {
				fmt.Println(workspaceRoot)
			}
			return nil
		}

		if workspaceRoot != "" {
			fmt.Printf("📍 You are in a Grove workspace: %s\n", workspaceRoot)
			
			// Try to discover binaries, but don't fail if we can't
			binaries, err := workspace.DiscoverLocalBinaries(workspaceRoot)
			if err != nil {
				// Just warn, don't fail - workspace detection still succeeded
				fmt.Printf("\nNote: Could not discover binaries: %v\n", err)
			} else if len(binaries) > 0 {
				fmt.Println("\nBinaries provided by this workspace:")
				for _, binary := range binaries {
					// Check if binary actually exists
					if _, err := os.Stat(binary.Path); err == nil {
						fmt.Printf("  - %s (%s)\n", binary.Name, binary.Path)
					}
				}
			} else {
				fmt.Println("\nNo binaries found in this workspace.")
			}
		} else {
			fmt.Println("Not in a Grove workspace. Using global binaries.")
		}

		return nil
	}
	return cmd
}