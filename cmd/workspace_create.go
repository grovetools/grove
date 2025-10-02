package cmd

import (
	"context"
	"fmt"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	meta_workspace "github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func NewWorkspaceCreateCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("create", "Create a new development workspace worktree")
	cmd.Use = "create <name>"
	cmd.Args = cobra.ExactArgs(1)
	cmd.Long = `Creates a new, managed Git worktree for development.
For monorepos (ecosystems), you can specify which submodules to include.
This command is the new standard for setting up an isolated development environment.`
	
	var repos []string
	cmd.Flags().StringSliceVar(&repos, "repos", nil, "For ecosystem worktrees, specify which repos to include (e.g., grove-core,grove-flow)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		pretty := logging.NewPrettyLogger()

		pretty.Progress(fmt.Sprintf("Creating workspace '%s'...", name))

		// Find the git root of the current ecosystem.
		gitRoot, err := meta_workspace.FindRoot("")
		if err != nil {
			return fmt.Errorf("could not find ecosystem root: %w", err)
		}

		// Prepare the options for the new centralized function.
		opts := workspace.PrepareOptions{
			GitRoot:      gitRoot,
			WorktreeName: name,
			BranchName:   name, // By default, branch name matches worktree name.
			Repos:        repos,
		}

		// Call the centralized function in grove-core.
		worktreePath, err := workspace.Prepare(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to create workspace: %w", err)
		}
		
		pretty.Success(fmt.Sprintf("Workspace '%s' created", name))
		pretty.Path("Path", worktreePath)
		pretty.InfoPretty(fmt.Sprintf("To open a session, run: grove ws open %s", name))
		return nil
	}
	return cmd
}