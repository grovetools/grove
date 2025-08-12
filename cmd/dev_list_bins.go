package cmd

import (
	"fmt"
	"sort"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/spf13/cobra"
)

func newDevListBinsCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("list-bins", "List all binaries managed by local development links")

	cmd.Long = `Shows a simple list of all binary names that have registered local
development versions. This is useful for scripts and automation.`

	cmd.Example = `  # List all managed binaries
  grove dev list-bins`

	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if len(config.Binaries) == 0 {
			// No output for scripts
			return nil
		}

		// Get and sort binary names
		var binaryNames []string
		for name := range config.Binaries {
			binaryNames = append(binaryNames, name)
		}
		sort.Strings(binaryNames)

		// Check if JSON output is requested
		jsonFlag, _ := cmd.Flags().GetBool("json")
		if jsonFlag {
			// For JSON output, we'd need to implement JSON formatting
			// For now, just print the list
			fmt.Println("Managed binaries:")
			for _, name := range binaryNames {
				fmt.Printf("  %s\n", name)
			}
		} else {
			fmt.Println("Managed binaries:")
			for _, name := range binaryNames {
				fmt.Printf("  %s\n", name)
			}
		}

		return nil
	}

	return cmd
}
