package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newUpdateCmd())
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [tools...]",
		Short: "Update Grove tools",
		Long:  "Update one or more Grove tools by reinstalling them",
		Example: `  grove update context version
  grove update cx nb`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Update is just an alias for install
			return runInstall(cmd, args)
		},
	}
	
	return cmd
}