package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newUpdateCmd())
}

func newUpdateCmd() *cobra.Command {
	var useGH bool
	
	cmd := &cobra.Command{
		Use:   "update [tools...]",
		Short: "Update Grove tools",
		Long: `Update one or more Grove tools by reinstalling them.
If no tools are specified, updates grove itself.`,
		Example: `  grove update                # Update grove itself
  grove update context version   # Update specific tools
  grove update cx nb
  grove update --use-gh cx       # Use gh CLI for private repos`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no args provided, update grove itself
			if len(args) == 0 {
				args = []string{"grove"}
			}
			
			// Update is just an alias for install
			return runInstall(cmd, args, useGH)
		},
	}
	
	cmd.Flags().BoolVar(&useGH, "use-gh", false, "Use gh CLI for downloading (supports private repos)")
	
	return cmd
}