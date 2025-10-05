package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/tmux"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/spf13/cobra"
)

func NewWorkspaceOpenCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("open", "Open a tmux session for a workspace")
	cmd.Use = "open <name>"
	cmd.Args = cobra.ExactArgs(1)
	cmd.Long = "Creates and/or attaches to a tmux session for the specified workspace."

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		logger := logging.NewLogger("ws-open")
		pretty := logging.NewPrettyLogger()

		gitRoot, err := workspace.FindEcosystemRoot("")
		if err != nil {
			return err
		}

		// Find the worktree path - it could be in different locations
		worktreePath, err := findWorktreePath(gitRoot, name)
		if err != nil {
			return err
		}

		tmuxClient, err := tmux.NewClient()
		if err != nil {
			return err
		}

		// Sanitize session name by replacing invalid characters
		sessionName := strings.ReplaceAll(name, ".", "-")
		sessionName = strings.ReplaceAll(sessionName, ":", "-")
		exists, _ := tmuxClient.SessionExists(context.Background(), sessionName)

		if !exists {
			logger.Infof("Creating new tmux session '%s'...", sessionName)
			pretty.InfoPretty(fmt.Sprintf("Creating new tmux session '%s'...", sessionName))
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
		pretty.InfoPretty(fmt.Sprintf("Switching to session '%s'...", sessionName))
		return tmuxClient.SwitchClient(context.Background(), sessionName)
	}
	return cmd
}

// findWorktreePath finds the actual path to a worktree by checking multiple possible locations
func findWorktreePath(gitRoot, name string) (string, error) {
	// First, try to discover workspaces to find the repo that might contain the worktree
	projects, err := discovery.DiscoverProjects()
	if err == nil {
		// Check each workspace for the worktree
		for _, p := range projects {
			worktreePath := filepath.Join(p.Path, ".grove-worktrees", name)
			if _, err := os.Stat(worktreePath); err == nil {
				return worktreePath, nil
			}
		}
	}

	// Fall back to ecosystem root convention
	worktreePath := filepath.Join(gitRoot, ".grove-worktrees", name)
	if _, err := os.Stat(worktreePath); err == nil {
		return worktreePath, nil
	}

	return "", fmt.Errorf("worktree '%s' not found in any workspace or ecosystem root", name)
}