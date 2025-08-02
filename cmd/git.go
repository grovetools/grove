package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newGitCmd())
}

func newGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Git operations across workspaces",
		Long:  "Execute git commands across all discovered workspaces, with special aggregated status view",
		// This will run if no subcommand is specified
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no subcommand, delegate to 'grove run git [args]'
			if len(args) == 0 {
				return cmd.Help()
			}
			
			// Prepend 'git' to the args and use the run command logic
			runArgs := append([]string{"git"}, args...)
			return runCommand(cmd, runArgs)
		},
		// Allow arbitrary args for git commands
		Args: cobra.ArbitraryArgs,
	}
	
	// Add the custom status subcommand
	cmd.AddCommand(newGitStatusCmd())
	
	return cmd
}