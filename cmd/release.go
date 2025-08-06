package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/command"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
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
3. Tag each submodule with the specified version (triggers individual releases)
4. Update submodule references in the parent repository
5. Tag the parent repository (triggers meta-release)
6. Push all tags to origin

Note: Each submodule now has its own release workflow that will build and 
publish binaries independently. The parent repository creates a meta-release
that documents the versions of all included submodules.

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

	// Run pre-flight checks
	if err := runPreflightChecks(ctx, rootDir, version, logger); err != nil {
		return err
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
	logger.Info("")
	logger.Info("GitHub Actions will now:")
	logger.Info("  - Build and release each tool independently in its own repository")
	logger.Info("  - Create a meta-release in grove-ecosystem with submodule versions")
	logger.Info("")
	logger.Info("Monitor the release progress at:")
	logger.Info("  https://github.com/mattsolo1/grove-ecosystem/actions")

	return nil
}

func runPreflightChecks(ctx context.Context, rootDir, version string, logger *logrus.Logger) error {
	logger.Info("Running pre-flight checks...")
	
	// Check main repository status
	mainStatus, err := git.GetStatus(rootDir)
	if err != nil {
		return fmt.Errorf("failed to get main repository status: %w", err)
	}
	
	// Collect submodule statuses
	type submoduleStatus struct {
		Path   string
		Branch string
		Dirty  bool
		Error  error
	}
	
	var submoduleStatuses []submoduleStatus
	
	// Get submodule statuses
	cmdBuilder := command.NewSafeBuilder()
	cmd, err := cmdBuilder.Build(ctx, "git", "submodule", "foreach", "--quiet", "echo $sm_path")
	if err != nil {
		return fmt.Errorf("failed to build submodule list command: %w", err)
	}
	
	execCmd := cmd.Exec()
	execCmd.Dir = rootDir
	output, err := execCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list submodules: %w", err)
	}
	
	// Parse submodule paths
	submodulePaths := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(submodulePaths) == 1 && submodulePaths[0] == "" {
		submodulePaths = []string{}
	}
	
	// Check each submodule
	for _, smPath := range submodulePaths {
		if smPath == "" {
			continue
		}
		
		smFullPath := filepath.Join(rootDir, smPath)
		smStatus := submoduleStatus{Path: smPath}
		
		// Get submodule git status
		status, err := git.GetStatus(smFullPath)
		if err != nil {
			smStatus.Error = err
		} else {
			smStatus.Branch = status.Branch
			smStatus.Dirty = status.IsDirty
		}
		
		submoduleStatuses = append(submoduleStatuses, smStatus)
	}
	
	// Display status table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\nREPOSITORY\tBRANCH\tSTATUS\tISSUES")
	fmt.Fprintln(w, "----------\t------\t------\t------")
	
	// Main repository
	mainIssues := []string{}
	if mainStatus.IsDirty {
		mainIssues = append(mainIssues, "uncommitted changes")
	}
	if mainStatus.Branch != "main" {
		mainIssues = append(mainIssues, "not on main branch")
	}
	mainStatusStr := "✓ Clean"
	if mainStatus.IsDirty {
		mainStatusStr = "✗ Dirty"
	}
	fmt.Fprintf(w, "grove-ecosystem\t%s\t%s\t%s\n", 
		mainStatus.Branch, 
		mainStatusStr,
		strings.Join(mainIssues, ", "))
	
	// Submodules
	hasIssues := len(mainIssues) > 0
	for _, sm := range submoduleStatuses {
		issues := []string{}
		statusStr := "✓ Clean"
		branch := sm.Branch
		
		if sm.Error != nil {
			issues = append(issues, "error checking status")
			statusStr = "? Error"
			branch = "unknown"
		} else {
			if sm.Dirty {
				issues = append(issues, "uncommitted changes")
				statusStr = "✗ Dirty"
			}
			if sm.Branch != "main" {
				issues = append(issues, "not on main branch")
			}
		}
		
		if len(issues) > 0 {
			hasIssues = true
		}
		
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", 
			sm.Path,
			branch,
			statusStr,
			strings.Join(issues, ", "))
	}
	
	w.Flush()
	fmt.Println()
	
	// Check if we should proceed
	if hasIssues && !releaseForce && !releaseDryRun {
		logger.Error("Pre-flight checks failed. Fix the issues above or use --force to proceed anyway.")
		return fmt.Errorf("pre-flight checks failed")
	}
	
	if hasIssues && releaseForce {
		logger.Warn("Issues detected but proceeding with --force flag")
	} else if !hasIssues {
		logger.Info("✅ All pre-flight checks passed")
	}
	
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