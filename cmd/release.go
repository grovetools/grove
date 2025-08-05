package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/command"
	"github.com/mattsolo1/grove-core/git"
	"github.com/grovepm/grove/pkg/workspace"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	releaseDryRun bool
	releaseForce  bool
)

func init() {
	rootCmd.AddCommand(newReleaseCmd())
}

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release <version>",
		Short: "Create a new release for the Grove ecosystem",
		Long: `Create a new release by tagging all submodules and the parent repository.

This command will:
1. Validate the version format (vMAJOR.MINOR.PATCH)
2. Check that all repositories are clean (unless --force is used)
3. Tag each submodule with the specified version
4. Update submodule references in the parent repository
5. Tag the parent repository
6. Push all tags to origin

Example:
  grove release v0.1.0`,
		Args: cobra.ExactArgs(1),
		RunE: runRelease,
	}

	cmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "Print commands without executing them")
	cmd.Flags().BoolVar(&releaseForce, "force", false, "Skip clean workspace checks")

	return cmd
}

func runRelease(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	logger := cli.GetLogger(cmd)
	version := args[0]

	// Validate version format
	versionRegex := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	if !versionRegex.MatchString(version) {
		return fmt.Errorf("invalid version format: %s (must be vMAJOR.MINOR.PATCH)", version)
	}

	logger.Info("Preparing release", "version", version)

	// Find the root directory
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Check if the main repository is clean
	if !releaseForce && !releaseDryRun {
		status, err := git.GetStatus(rootDir)
		if err != nil {
			return fmt.Errorf("failed to get git status: %w", err)
		}
		if status.IsDirty {
			return fmt.Errorf("main repository has uncommitted changes. Use --force to skip this check")
		}
		if status.Branch != "main" {
			return fmt.Errorf("main repository is not on main branch (current: %s). Use --force to skip this check", status.Branch)
		}
	}

	// Execute git submodule foreach to check cleanliness and tag
	if err := tagSubmodules(ctx, rootDir, version, logger); err != nil {
		return fmt.Errorf("failed to tag submodules: %w", err)
	}

	// Stage the updated submodule references
	if err := executeGitCommand(ctx, rootDir, []string{"add", "."}, "Stage submodule updates", logger); err != nil {
		return err
	}

	// Commit the submodule updates
	commitMsg := fmt.Sprintf("chore: Release %s", version)
	if err := executeGitCommand(ctx, rootDir, []string{"commit", "-m", commitMsg}, "Commit release", logger); err != nil {
		return err
	}

	// Tag the main repository
	if err := executeGitCommand(ctx, rootDir, []string{"tag", "-a", version, "-m", fmt.Sprintf("Release %s", version)}, "Tag main repository", logger); err != nil {
		return err
	}

	// Push the main repository changes
	if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", "main"}, "Push main branch", logger); err != nil {
		return err
	}

	// Push the main repository tag
	if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", version}, "Push release tag", logger); err != nil {
		return err
	}

	logger.Info("✅ Release successfully created", "version", version)
	logger.Info("A GitHub Actions workflow will now build the binaries and create the release")

	return nil
}

func tagSubmodules(ctx context.Context, rootDir, version string, logger *logrus.Logger) error {
	// Build the git submodule foreach command
	submoduleScript := fmt.Sprintf(`
if [ -z "$sm_path" ]; then
	echo "Error: sm_path not set"
	exit 1
fi
echo "Processing submodule: $sm_path"

# Check if the submodule is clean
if [ "$(%s)" != "true" ] && [ -n "$(git status --porcelain)" ]; then
	echo "Error: Submodule $sm_path has uncommitted changes"
	exit 1
fi

# Check if on main branch
current_branch=$(git branch --show-current)
if [ "$(%s)" != "true" ] && [ "$current_branch" != "main" ]; then
	echo "Error: Submodule $sm_path is not on main branch (current: $current_branch)"
	exit 1
fi

# Tag the submodule
git tag -a "%s" -m "Release %s"
git push origin "%s"
`, 
		boolToString(releaseForce),
		boolToString(releaseForce),
		version, version, version)

	args := []string{"submodule", "foreach", "--recursive", submoduleScript}
	
	return executeGitCommand(ctx, rootDir, args, "Tag submodules", logger)
}

func executeGitCommand(ctx context.Context, dir string, args []string, description string, logger *logrus.Logger) error {
	if releaseDryRun {
		logger.Info("[DRY RUN] Would execute", "command", fmt.Sprintf("git %s", strings.Join(args, " ")), "dir", dir)
		return nil
	}

	logger.Info(description, "command", fmt.Sprintf("git %s", strings.Join(args, " ")))

	cmdBuilder := command.NewSafeBuilder()
	cmd, err := cmdBuilder.Build(ctx, "git", args...)
	if err != nil {
		return fmt.Errorf("failed to build git command: %w", err)
	}

	execCmd := cmd.Exec()
	execCmd.Dir = dir
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	if err := execCmd.Run(); err != nil {
		return fmt.Errorf("git command failed: %w", err)
	}

	return nil
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}