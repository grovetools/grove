package cmd

import (
	"fmt"
	"sort"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/spf13/cobra"
)

func newDevListCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("list", "List registered local development versions")
	
	cmd.Long = `Shows all registered local development versions for binaries.
If a binary name is provided, shows only versions for that binary.`
	
	cmd.Example = `  # List all binaries and their versions
  grove dev list
  
  # List versions for a specific binary
  grove dev list flow`
	
	cmd.Args = cobra.MaximumNArgs(1)
	
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		
		if len(args) > 0 {
			// List versions for a specific binary
			binaryName := args[0]
			binInfo, ok := config.Binaries[binaryName]
			if !ok {
				return fmt.Errorf("binary '%s' is not registered", binaryName)
			}
			
			fmt.Printf("Binary: %s\n", binaryName)
			
			// Sort aliases for consistent output
			var aliases []string
			for alias := range binInfo.Links {
				aliases = append(aliases, alias)
			}
			sort.Strings(aliases)
			
			for _, alias := range aliases {
				linkInfo := binInfo.Links[alias]
				prefix := "  "
				if alias == binInfo.Current {
					prefix = "* "
				}
				fmt.Printf("%s%s (%s)\n", prefix, alias, linkInfo.WorktreePath)
			}
		} else {
			// List all binaries
			if len(config.Binaries) == 0 {
				fmt.Println("No local development binaries registered yet.")
				fmt.Println("Use 'grove dev link <path>' to register binaries from a worktree.")
				return nil
			}
			
			// Sort binary names for consistent output
			var binaryNames []string
			for name := range config.Binaries {
				binaryNames = append(binaryNames, name)
			}
			sort.Strings(binaryNames)
			
			for _, name := range binaryNames {
				binInfo := config.Binaries[name]
				fmt.Printf("Binary: %s\n", name)
				
				// Sort aliases for consistent output
				var aliases []string
				for alias := range binInfo.Links {
					aliases = append(aliases, alias)
				}
				sort.Strings(aliases)
				
				for _, alias := range aliases {
					linkInfo := binInfo.Links[alias]
					prefix := "  "
					if alias == binInfo.Current {
						prefix = "* "
					}
					fmt.Printf("%s%s (%s)\n", prefix, alias, linkInfo.WorktreePath)
				}
				fmt.Println()
			}
		}
		
		return nil
	}
	
	return cmd
}