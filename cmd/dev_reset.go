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

func newDevResetCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("reset", "Reset all binaries to main/released versions")

	cmd.Long = `Reset all Grove binaries back to their main development versions or released versions.
This command removes all active development links and switches binaries to:
- The 'main' dev link if available
- The released version otherwise

This is useful after testing a feature branch to quickly return to stable versions.`

	cmd.Example = `  # Reset all binaries to main versions
  grove dev reset`

	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Load dev config
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load dev config: %w", err)
		}

		// Load SDK manager for released versions
		sdkManager, err := sdk.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create SDK manager: %w", err)
		}

		// Load tool versions for reconciler
		groveHome := os.Getenv("HOME") + "/.grove"
		tv, err := sdk.LoadToolVersions(groveHome)
		if err != nil {
			return fmt.Errorf("failed to load tool versions: %w", err)
		}

		// Create reconciler
		r, err := reconciler.NewWithToolVersions(tv)
		if err != nil {
			return fmt.Errorf("failed to create reconciler: %w", err)
		}

		resetCount := 0

		// Process each binary
		for binaryName, binInfo := range config.Binaries {
			// Check if there's a 'main' link for this binary
			if _, hasMain := binInfo.Links["main"]; hasMain {
				// Switch to main version
				if err := activateDevLink(binaryName, "main"); err != nil {
					fmt.Printf("Warning: failed to switch '%s' to main: %v\n", binaryName, err)
					continue
				}
				fmt.Printf("Switched '%s' to main version\n", binaryName)
				resetCount++
			} else {
				// No main link, switch to released version
				activeVersion, err := sdkManager.GetToolVersion(binaryName)
				if err != nil {
					// No released version either, just clear the current link
					binInfo.Current = ""
					fmt.Printf("Cleared development link for '%s' (no main or released version available)\n", binaryName)
					continue
				}

				// Clear the current dev link
				binInfo.Current = ""

				// Use reconciler to update the symlink to released version
				if err := r.Reconcile(binaryName); err != nil {
					fmt.Printf("Warning: failed to reconcile '%s': %v\n", binaryName, err)
					continue
				}

				// Verify the binary exists in the released version
				releasedBinPath := filepath.Join(groveHome, "versions", activeVersion, "bin", binaryName)
				if _, err := os.Stat(releasedBinPath); err != nil {
					fmt.Printf("Warning: binary '%s' not found in released version '%s'\n", binaryName, activeVersion)
					continue
				}

				fmt.Printf("Switched '%s' to released version '%s'\n", binaryName, activeVersion)
				resetCount++
			}
		}

		// Save the updated configuration
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		if resetCount == 0 {
			fmt.Println("No binaries to reset")
		} else {
			fmt.Printf("\nReset %d binaries to stable versions\n", resetCount)
		}

		return nil
	}

	return cmd
}