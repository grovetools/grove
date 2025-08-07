package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/spf13/cobra"
)

func newDevUnlinkCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("unlink", "Remove a registered local development version")
	
	cmd.Long = `Removes a specific registered version of a binary from the development links.
If the removed version was the current active version, the symlink will also be removed.`
	
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
		
		// Remove the link
		delete(binInfo.Links, alias)
		
		// If this was the current version, clear it and remove the symlink
		if binInfo.Current == alias {
			binInfo.Current = ""
			
			// Remove the symlink
			groveHome, err := devlinks.GetGroveHome()
			if err == nil {
				symlink := filepath.Join(groveHome, "bin", binaryName)
				_ = os.Remove(symlink)
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
		
		return nil
	}
	
	return cmd
}