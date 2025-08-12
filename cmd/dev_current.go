package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/spf13/cobra"
)

func newDevCurrentCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("current", "Show currently active local development versions")

	cmd.Long = `Displays the effective configuration for all Grove tools, showing whether each tool
is using a development link or the released version.

This command shows the layered status where development links override released versions.`

	cmd.Example = `  # Show current versions for all binaries
  grove dev current
  
  # Show current version for a specific binary
  grove dev current flow`

	cmd.Args = cobra.MaximumNArgs(1)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Load tool versions for reconciler
		tv, err := sdk.LoadToolVersions(os.Getenv("HOME") + "/.grove")
		if err != nil {
			tv = &sdk.ToolVersions{
				Versions: make(map[string]string),
			}
		}

		// Create reconciler to get effective sources
		r, err := reconciler.NewWithToolVersions(tv)
		if err != nil {
			return fmt.Errorf("failed to create reconciler: %w", err)
		}

		fmt.Println("Tool Status:")

		// Get all tools and sort them
		tools := sdk.GetAllTools()
		sort.Strings(tools)

		if len(args) > 0 {
			// Show status for a specific tool
			toolName := args[0]
			source, version, path := r.GetEffectiveSource(toolName)
			displayToolStatus(toolName, source, version, path)
		} else {
			// Show status for all tools
			for _, toolName := range tools {
				source, version, path := r.GetEffectiveSource(toolName)
				displayToolStatus(toolName, source, version, path)
			}

			fmt.Println("\nUse 'grove dev use <binary> <alias>' to activate a dev version")
			fmt.Println("Use 'grove dev use <binary> --release' to switch back to release")
		}

		return nil
	}

	return cmd
}

// displayToolStatus shows the status of a single tool
func displayToolStatus(toolName, source, version, path string) {
	switch source {
	case "dev":
		fmt.Printf("  %s: %s (%s) [dev]\n", toolName, version, path)
	case "release":
		fmt.Printf("  %s: %s (%s) [release]\n", toolName, version, path)
	case "none":
		fmt.Printf("  %s: (not installed)\n", toolName)
	}
}
