package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func NewWorkspaceRemoveCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("remove", "Remove a development workspace worktree")
	cmd.Use = "remove <name>"
	cmd.Args = cobra.ExactArgs(1)
	cmd.Long = "Removes the specified Git worktree and its associated directory."

	var force bool
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force removal without confirmation")
	
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		logger := cli.GetLogger(cmd)

		if !force {
			fmt.Printf("Are you sure you want to remove the workspace '%s'? [y/N]: ", name)
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(input)) != "y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		gitRoot, err := workspace.FindRoot("")
		if err != nil {
			return err
		}

		worktreePath := filepath.Join(gitRoot, ".grove-worktrees", name)
		manager := git.NewWorktreeManager()

		logger.Infof("Removing worktree '%s'...", name)
		if err := manager.RemoveWorktree(context.Background(), gitRoot, worktreePath); err != nil {
			// Try with force if there are uncommitted changes
			if strings.Contains(err.Error(), "uncommitted changes") {
				logger.Warnf("Worktree has uncommitted changes. Forcing removal...")
				removeCmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
				removeCmd.Dir = gitRoot
				if err := removeCmd.Run(); err != nil {
					return fmt.Errorf("forced removal failed: %w", err)
				}
			} else {
				return fmt.Errorf("failed to remove worktree: %w", err)
			}
		}

		logger.Infof("✓ Workspace '%s' removed successfully.", name)
		return nil
	}
	return cmd
}