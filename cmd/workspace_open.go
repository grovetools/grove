package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/pkg/tmux"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func NewWorkspaceOpenCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("open", "Open a tmux session for a workspace")
	cmd.Use = "open <name>"
	cmd.Args = cobra.ExactArgs(1)
	cmd.Long = "Creates and/or attaches to a tmux session for the specified workspace."

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		logger := cli.GetLogger(cmd)

		gitRoot, err := workspace.FindRoot("")
		if err != nil {
			return err
		}

		// Convention: worktree is located at .grove-worktrees/<name>
		worktreePath := filepath.Join(gitRoot, ".grove-worktrees", name)

		tmuxClient, err := tmux.NewClient()
		if err != nil {
			return err
		}

		sessionName := tmux.SanitizeForTmuxSession(name)
		exists, _ := tmuxClient.SessionExists(context.Background(), sessionName)

		if !exists {
			logger.Infof("Creating new tmux session '%s'...", sessionName)
			opts := tmux.LaunchOptions{
				SessionName:      sessionName,
				WorkingDirectory: worktreePath,
				WindowName:       "workspace",
			}
			if err := tmuxClient.Launch(context.Background(), opts); err != nil {
				return fmt.Errorf("failed to launch tmux session: %w", err)
			}
		}
		
		logger.Infof("Switching to session '%s'...", sessionName)
		return tmuxClient.SwitchClient(context.Background(), sessionName)
	}
	return cmd
}