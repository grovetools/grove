package cmd

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func NewWorkspaceManageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manage",
		Short: "Interactively manage notes in the current workspace",
		Long: `A convenient alias for 'nb manage'. Provides an interactive TUI to view
and archive notes in the current workspace context.

This command allows you to:
- View all notes in the current workspace in a table format
- Select multiple notes for bulk operations
- Archive selected notes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Execute nb manage command
			nbCmd := exec.Command("nb", "manage")
			nbCmd.Stdin = os.Stdin
			nbCmd.Stdout = os.Stdout
			nbCmd.Stderr = os.Stderr

			return nbCmd.Run()
		},
	}

	return cmd
}
