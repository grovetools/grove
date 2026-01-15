package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newWorkspaceCmd())
}

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Workspace operations across the monorepo",
		Long:    "Execute operations and view aggregated information across all discovered workspaces",
	}

	// Subcommands
	cmd.AddCommand(NewWorkspaceWorktreesCmd())
	cmd.AddCommand(newWorkspaceGitHooksCmd())

	// Register deprecated subcommand shims for backwards compatibility
	registerDeprecatedWorkspaceSubcommands(cmd)

	return cmd
}
