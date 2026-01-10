package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newRepoCmd())
}

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Repository management commands",
		Long: `Manage Grove repositories.

Examples:
  # Create a new local repository
  grove repo add my-tool --description "My new tool"

  # Create and add to an existing ecosystem
  grove repo add my-tool --ecosystem

  # Use a different template
  grove repo add my-tool --template maturin`,
	}

	// Add subcommands
	cmd.AddCommand(newRepoAddCmd())

	// github-init is hidden - for internal Grove ecosystem use only
	githubInitCmd := newRepoGitHubInitCmd()
	githubInitCmd.Hidden = true
	cmd.AddCommand(githubInitCmd)

	return cmd
}
