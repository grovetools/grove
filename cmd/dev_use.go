package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/spf13/cobra"
)

func newDevUseCmd() *cobra.Command {
	var releaseFlag bool
	
	cmd := cli.NewStandardCommand("use", "Switch to a specific version of a binary")
	
	cmd.Long = `Activates a specific locally-linked version of a Grove binary.
This will update the symlink in ~/.grove/bin to point to the selected version.

With the --release flag, switches the binary back to the currently active released version.`
	
	cmd.Example = `  # Use a specific version of flow
  grove dev use flow feature-branch
  
  # Use the main version of cx
  grove dev use cx main
  
  # Switch flow back to the released version
  grove dev use flow --release`
	
	cmd.Flags().BoolVar(&releaseFlag, "release", false, "Switch back to the released version")
	
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if releaseFlag {
			if len(args) != 1 {
				return fmt.Errorf("exactly one argument (binary name) required when using --release")
			}
		} else {
			if len(args) != 2 {
				return fmt.Errorf("exactly two arguments (binary name and alias) required")
			}
		}
		return nil
	}
	
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		binaryName := args[0]
		
		if releaseFlag {
			// Switch back to released version
			return switchToRelease(binaryName)
		}
		
		// Normal dev link activation
		alias := args[1]
		
		// Activate the specified link
		if err := activateDevLink(binaryName, alias); err != nil {
			return err
		}
		
		// Load config to get the worktree path for display
		config, err := devlinks.LoadConfig()
		if err != nil {
			return err
		}
		
		if binInfo, ok := config.Binaries[binaryName]; ok {
			if linkInfo, ok := binInfo.Links[alias]; ok {
				fmt.Printf("Switched '%s' to version '%s' (%s)\n", 
					binaryName, alias, linkInfo.WorktreePath)
			}
		}
		
		return nil
	}
	
	return cmd
}

// switchToRelease switches a binary back to the currently active released version
func switchToRelease(binaryName string) error {
	// Get the active released version for this tool
	sdkManager, err := sdk.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create SDK manager: %w", err)
	}
	
	activeVersion, err := sdkManager.GetToolVersion(binaryName)
	if err != nil {
		return fmt.Errorf("no active released version found for %s: %w", binaryName, err)
	}
	
	// Load dev config
	config, err := devlinks.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load dev config: %w", err)
	}
	
	// Clear the current dev link for this binary
	if binInfo, exists := config.Binaries[binaryName]; exists {
		binInfo.Current = ""
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}
	
	// Load tool versions for reconciler
	tv, err := sdk.LoadToolVersions(os.Getenv("HOME") + "/.grove")
	if err != nil {
		return fmt.Errorf("failed to load tool versions: %w", err)
	}
	
	// Use reconciler to update the symlink
	r, err := reconciler.NewWithToolVersions(tv)
	if err != nil {
		return fmt.Errorf("failed to create reconciler: %w", err)
	}
	
	if err := r.Reconcile(binaryName); err != nil {
		return fmt.Errorf("failed to reconcile symlink: %w", err)
	}
	
	// Check if the binary exists in the released version
	releasedBinPath := filepath.Join(os.Getenv("HOME"), ".grove", "versions", activeVersion, "bin", binaryName)
	if _, err := os.Stat(releasedBinPath); err != nil {
		return fmt.Errorf("binary '%s' not found in released version '%s'", binaryName, activeVersion)
	}
	
	fmt.Printf("Switched '%s' back to released version '%s'\n", binaryName, activeVersion)
	return nil
}