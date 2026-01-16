package cmd

import (
	"fmt"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/grove/pkg/repository"
	"github.com/spf13/cobra"
)

var (
	repoGitHubInitVisibility string
	repoGitHubInitDryRun     bool
)

func newRepoGitHubInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github-init",
		Short: "Add GitHub integration to an existing local repository",
		Long: `Add GitHub integration to an existing local Grove repository.

This command must be run from within a Grove repository (directory with grove.yml).
It will:
1. Create a GitHub repository (public or private)
2. Add the remote origin
3. Push existing commits and tags
4. Set up GitHub Actions workflows
5. Configure repository secrets (for private repos, requires GROVE_PAT)

Prerequisites:
- Must be run from within a Grove repository
- GitHub CLI (gh) must be installed and authenticated
- No existing 'origin' remote (use 'git remote remove origin' first if needed)
- GROVE_PAT environment variable for private repositories

Examples:
  # Initialize GitHub for current repo (private by default)
  cd my-tool
  grove repo github-init

  # Initialize as public
  grove repo github-init --visibility=public

  # Preview what would happen
  grove repo github-init --dry-run`,
		RunE: runRepoGitHubInit,
	}

	cmd.Flags().StringVar(&repoGitHubInitVisibility, "visibility", "private", "Repository visibility: public or private")
	cmd.Flags().BoolVar(&repoGitHubInitDryRun, "dry-run", false, "Preview operations without executing")

	return cmd
}

func runRepoGitHubInit(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("repo-github-init")

	// Validate visibility
	if repoGitHubInitVisibility != "public" && repoGitHubInitVisibility != "private" {
		return fmt.Errorf("--visibility must be 'public' or 'private', got '%s'", repoGitHubInitVisibility)
	}

	creator := repository.NewCreator(logger.Logger)

	opts := repository.GitHubInitOptions{
		Visibility: repoGitHubInitVisibility,
		DryRun:     repoGitHubInitDryRun,
	}

	logger.Info("Initializing GitHub integration...")

	return creator.InitializeGitHub(opts)
}
