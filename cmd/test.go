package cmd

import (
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

func newTestCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("test", "Run unit tests across ecosystem in parallel")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return executeTask(cmd, "test", orch.StrategyWaveSorted)
	}
	cmd.SilenceUsage = true
	addTaskFlags(cmd)
	return cmd
}
