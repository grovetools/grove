package cmd

import (
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

// NewDevCmd creates the dev command for managing local development binaries
func NewDevCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("dev", "Manage local development binaries")

	cmd.Long = `The 'grove dev' commands manage local development binaries built from source.
This allows you to switch between different versions of Grove tools built from
different git worktrees during development.

These commands are distinct from 'grove version' which manages official releases.`

	// Add subcommands
	cmd.AddCommand(newDevLinkCmd())
	cmd.AddCommand(newDevUseCmd())
	cmd.AddCommand(newDevListCmd())
	cmd.AddCommand(newDevCurrentCmd())
	cmd.AddCommand(newDevUnlinkCmd())
	cmd.AddCommand(newDevPruneCmd())
	cmd.AddCommand(newDevListBinsCmd())

	return cmd
}

func init() {
	rootCmd.AddCommand(NewDevCmd())
}
