package cmd

import (
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

func newCheckCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("check", "Run full validation check across ecosystem in parallel")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return executeTask(cmd, "check", orch.StrategyWaveSorted)
	}
	cmd.SilenceUsage = true
	addTaskFlags(cmd)
	return cmd
}
