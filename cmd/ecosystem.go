package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newEcosystemCmd())
}

func newEcosystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ecosystem",
		Short: "Manage Grove ecosystems",
		Long: `Manage Grove ecosystems (monorepos).

Commands:
  init   Create a new Grove ecosystem
  add    Add an existing repository to the ecosystem
  list   List repositories in the ecosystem

Examples:
  # Create a new ecosystem
  grove ecosystem init

  # Add an existing repo as submodule
  grove ecosystem add ../my-existing-tool
  grove ecosystem add github.com/user/repo

  # List repos in the ecosystem
  grove ecosystem list`,
	}

	cmd.AddCommand(newEcosystemInitCmd())
	cmd.AddCommand(newEcosystemAddCmd())
	cmd.AddCommand(newEcosystemListCmd())

	return cmd
}
