package cmd

import (
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

func newVetCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("vet", "Vet ecosystem workspaces in parallel")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return executeTask(cmd, "vet", orch.StrategyFlat)
	}
	cmd.SilenceUsage = true
	addTaskFlags(cmd)
	return cmd
}
