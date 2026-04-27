package cmd

import (
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

func newFmtCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("fmt", "Format ecosystem workspaces in parallel")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return executeTask(cmd, "fmt", orch.StrategyFlat)
	}
	cmd.SilenceUsage = true
	addTaskFlags(cmd)
	return cmd
}
