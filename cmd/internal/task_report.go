package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/grovetools/core/git"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
)

func newTaskReportCmd() *cobra.Command {
	var durationMs int64

	cmd := &cobra.Command{
		Use:   "task-report <workspace-dir> <verb> <exit-code>",
		Short: "Report a task execution result to the daemon",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			wsDir := args[0]
			verb := args[1]

			exitCode, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("invalid exit code: %w", err)
			}

			absDir, err := filepath.Abs(wsDir)
			if err != nil {
				return fmt.Errorf("failed to resolve path: %w", err)
			}
			workspace := filepath.Base(absDir)

			commitHash, err := git.GetHeadCommit(absDir)
			if err != nil {
				commitHash = "unknown"
			}

			client := daemon.New(absDir)
			defer client.Close()

			if !client.IsRunning() {
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			return client.ReportTask(ctx, workspace, verb, exitCode, commitHash, durationMs)
		},
	}

	cmd.Flags().Int64Var(&durationMs, "duration", 0, "Duration of the task in milliseconds")
	return cmd
}
