package cmd

import (
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

// newConfigCmd creates the `config` command and its subcommands.
func newConfigCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("config", "Analyze and manage Grove configuration")
	cmd.Long = `Provides tools to inspect, analyze, and manage Grove configurations.`

	// Add subcommands
	cmd.AddCommand(newConfigAnalyzeCmd())

	return cmd
}
