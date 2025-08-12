package cmd

import (
	"fmt"
	"os"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newSelfUpdateCmd())
}

func newSelfUpdateCmd() *cobra.Command {
	var useGH bool

	cmd := cli.NewStandardCommand("self-update", "Update the grove CLI to the latest version")

	cmd.Long = `Update the grove CLI itself to the latest version.
This command will download and replace the current grove binary with the latest release.`

	cmd.Example = `  grove self-update              # Update using curl (public repos)
  grove self-update --use-gh       # Update using gh CLI (private repos)`

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		logger := cli.GetLogger(cmd)

		// Check if running as root (not recommended)
		if os.Geteuid() == 0 {
			logger.Warn("Running as root is not recommended")
		}

		logger.Info("Updating grove CLI to the latest version...")

		// Use the install function to update grove itself
		if err := runInstall(cmd, []string{"grove"}, useGH); err != nil {
			return fmt.Errorf("failed to update grove: %w", err)
		}

		logger.Info("✅ Grove CLI has been updated successfully!")
		logger.Info("Run 'grove version' to see the new version.")

		return nil
	}

	cmd.Flags().BoolVar(&useGH, "use-gh", false, "Use gh CLI for downloading (supports private repos)")

	return cmd
}
