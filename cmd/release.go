package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/lipgloss"
	tablecomponent "github.com/grovetools/core/tui/components/table"
	"github.com/grovetools/core/command"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/git"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/depsgraph"
	"github.com/grovetools/grove/pkg/gh"
	"github.com/grovetools/grove/pkg/project"
	"github.com/grovetools/grove/pkg/release"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

var (
	releaseDryRun         bool
	releaseForce          bool
	releaseForceIncrement bool
	releasePush           bool
	releaseRepos          []string
	releaseMajor          []string
	releaseMinor          []string
	releasePatch          []string
	releaseYes            bool
	releaseSkipParent     bool
	releaseWithDeps       bool
	releaseLLMChangelog   bool
	releaseInteractive    bool // New flag for interactive TUI mode
	releaseSkipCI         bool // Skip CI waits after changelog updates
	releaseResume         bool // Only process repos that haven't completed successfully
)

func init() {
	rootCmd.AddCommand(newReleaseCmd())
}

// Helper function to check if a git tag exists locally or remotely
func tagExists(ctx context.Context, repoPath, tag string) (bool, error) {
	// Check local tags first
	cmd := exec.CommandContext(ctx, "git", "tag", "-l", tag)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err == nil && len(strings.TrimSpace(string(output))) > 0 {
		return true, nil
	}
	
	// Check remote tags
	cmd = exec.CommandContext(ctx, "git", "ls-remote", "--tags", "origin", tag)
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err != nil {
		return false, err
	}
	
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// Helper function to check if a repo is fully released
func isRepoFullyReleased(ctx context.Context, repoPath string, repoPlan *release.RepoReleasePlan) bool {
	if repoPlan == nil {
		return false
	}
	
	// Check if all release stages are complete
	allStagesComplete := repoPlan.ChangelogPushed && repoPlan.CIPassed && repoPlan.TagPushed
	
	// If plan says it's complete, verify tag actually exists
	if allStagesComplete {
		tagExists, err := tagExists(ctx, repoPath, repoPlan.NextVersion)
		if err != nil {
			// If we can't check, assume not complete to be safe
			return false
		}
		return tagExists
	}
	
	return false
}

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Manage releases for the Grove ecosystem",
		Long: `Manage releases for the Grove ecosystem using a stateful, multi-step workflow.

The release process is divided into distinct commands:
  plan       - Generate a release plan analyzing all repositories for changes
  tui        - Review and approve the release plan interactively (or use 'review')
  apply      - Execute the approved release plan
  clear-plan - Clear the current release plan and start over
  undo-tag   - Remove tags locally and optionally from remote
  rollback   - Rollback commits in repositories from the release plan

Typical workflow:
  1. grove release plan --rc         # Generate RC release plan (auto-checks out rc-nightly)
  2. grove release tui               # Review and approve
  3. grove release apply             # Execute the release

Recovery commands:
  grove release undo-tag --from-plan --remote  # Remove all tags from failed release
  grove release rollback --hard               # Reset repositories to previous state
  grove release clear-plan                    # Start over with a new plan

Examples:
  grove release plan --rc            # Plan a Release Candidate (no docs)
  grove release plan --repos grove-core --with-deps  # Specific repos with dependencies
  grove release tui                  # Review and modify the plan
  grove release apply --dry-run      # Preview what would be done`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// For backwards compatibility, if --interactive flag is used, run TUI
			if releaseInteractive {
				ctx := context.Background()
				return runReleaseTUI(ctx)
			}
			
			// Otherwise show help for the parent command
			return cmd.Help()
		},
	}

	// Legacy flags for backwards compatibility
	cmd.Flags().BoolVar(&releaseInteractive, "interactive", false, "Launch interactive TUI (deprecated: use 'grove release tui')")
	cmd.Flags().MarkHidden("interactive")

	// Add subcommands
	cmd.AddCommand(newReleasePlanCmd())
	cmd.AddCommand(newReleaseTuiCmd())
	cmd.AddCommand(newReleaseReviewCmd()) // Alias for TUI
	cmd.AddCommand(newReleaseApplyCmd())
	cmd.AddCommand(newReleaseClearPlanCmd())
	cmd.AddCommand(newReleaseUndoTagCmd())
	cmd.AddCommand(newReleaseRollbackCmd())
	cmd.AddCommand(newChangelogCmd())

	return cmd
}

// Legacy runRelease function removed - replaced by stateful workflow:
// - runReleasePlan (in release_plan.go)
// - runReleaseTUI (in release_tui.go)  
// - runReleaseApply (in release_plan.go)

func autoCommitEcosystemChanges(ctx context.Context, rootDir string, hasChanges map[string]bool, logger *logrus.Logger) error {
	// Check if grove-ecosystem has uncommitted changes
	status, err := git.GetStatus(rootDir)
	if err != nil {
		return fmt.Errorf("failed to get git status: %w", err)
	}

	if !status.IsDirty {
		// No changes to commit
		return nil
	}

	if releaseDryRun {
		displayInfo("[DRY RUN] Would auto-commit grove-ecosystem changes")
		return nil
	}

	displayInfo("Auto-committing grove-ecosystem changes...")

	// Check what files are modified
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCmd.Dir = rootDir
	statusOutput, err := statusCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get git status output: %w", err)
	}

	// Parse the status output to understand what changed
	lines := strings.Split(string(statusOutput), "\n")
	var submodulesToCommit []string
	var relevantSubmodules []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Status format is "XY filename"
		if len(line) < 3 {
			continue
		}

		filename := strings.TrimSpace(line[2:])

		// Check if it's a submodule (git status shows them as modified)
		if strings.HasPrefix(filename, "grove-") {
			// Check if this is actually a submodule
			checkCmd := exec.CommandContext(ctx, "git", "submodule", "status", filename)
			checkCmd.Dir = rootDir
			if _, err := checkCmd.Output(); err == nil {
				// It's a submodule - check if it's being released
				if changes, ok := hasChanges[filename]; ok && changes {
					submodulesToCommit = append(submodulesToCommit, filename)
					relevantSubmodules = append(relevantSubmodules, filename)
				}
			}
		}
	}

	// Only proceed if we have submodules to commit that are being released
	if len(submodulesToCommit) == 0 {
		displayInfo("No submodule updates needed for repos being released")
		return nil
	}

	// Stage only the specific submodules being released
	for _, submodule := range submodulesToCommit {
		if err := executeGitCommand(ctx, rootDir, []string{"add", submodule}, fmt.Sprintf("Stage submodule %s", submodule), logger); err != nil {
			return err
		}
	}

	// Create a descriptive commit message
	commitMsg := fmt.Sprintf("chore: update submodule references for release (%s)", strings.Join(relevantSubmodules, ", "))

	if err := executeGitCommand(ctx, rootDir, []string{"commit", "-m", commitMsg}, "Auto-commit submodule updates", logger); err != nil {
		// Check if there's nothing to commit
		if strings.Contains(err.Error(), "nothing to commit") {
			return nil
		}
		return err
	}

	displaySuccess(fmt.Sprintf("Auto-committed submodule updates for: %s", strings.Join(relevantSubmodules, ", ")))

	return nil
}

func runPreflightChecks(ctx context.Context, rootDir, version string, workspaces []string, logger *logrus.Logger) error {
	displaySection(theme.IconFilter + " Pre-flight Checks")

	// Check main repository status
	mainStatus, err := git.GetStatus(rootDir)
	if err != nil {
		return fmt.Errorf("failed to get main repository status: %w", err)
	}

	// Collect workspace statuses
	type workspaceStatus struct {
		Path       string
		Branch     string
		Dirty      bool
		AheadCount int
		Error      error
	}

	var workspaceStatuses []workspaceStatus

	// Create a map for filtering repositories if specified
	repoFilter := make(map[string]bool)
	if len(releaseRepos) > 0 {
		for _, repo := range releaseRepos {
			repoFilter[repo] = true
		}
	}

	// Check each workspace
	for _, wsPath := range workspaces {
		repoName := filepath.Base(wsPath)

		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
			continue
		}

		// Use just the repo name for display
		wsStatus := workspaceStatus{Path: repoName}

		// Get workspace git status
		status, err := git.GetStatus(wsPath)
		if err != nil {
			wsStatus.Error = err
		} else {
			wsStatus.Branch = status.Branch
			wsStatus.Dirty = status.IsDirty
			wsStatus.AheadCount = status.AheadCount
		}

		workspaceStatuses = append(workspaceStatuses, wsStatus)
	}

	// Prepare table data
	var tableRows [][]string

	// Main repository
	mainIssues := []string{}
	// Don't consider dirty status an issue since we auto-commit
	// but still show it in the display
	if mainStatus.Branch != "main" {
		mainIssues = append(mainIssues, "not on main branch")
	}
	// Don't treat "ahead of remote" as a blocking issue for main repo either

	// For display purposes, still show uncommitted changes
	displayMainIssues := make([]string, len(mainIssues))
	copy(displayMainIssues, mainIssues)
	if mainStatus.IsDirty {
		displayMainIssues = append(displayMainIssues, "uncommitted changes (will auto-commit relevant submodules)")
	}
	// Skip grove-ecosystem status display since ecosystem operations are disabled
	// Add ahead info even if not an issue
	displayIssues := displayMainIssues
	if releasePush && mainStatus.HasUpstream && mainStatus.AheadCount > 0 && len(displayMainIssues) == 0 {
		displayIssues = []string{fmt.Sprintf("ahead of remote by %d commits (will push)", mainStatus.AheadCount)}
	} else if releasePush && mainStatus.HasUpstream && mainStatus.AheadCount > 0 {
		// Update the ahead message to indicate it will be pushed
		for i, issue := range displayIssues {
			if strings.Contains(issue, "ahead of remote") {
				displayIssues[i] = fmt.Sprintf("ahead of remote by %d commits (will push)", mainStatus.AheadCount)
			}
		}
	}
	// Skip adding grove-ecosystem to the table - ecosystem operations are disabled

	// Workspaces
	hasIssues := false // Don't consider grove-ecosystem issues since ecosystem operations are disabled
	for _, ws := range workspaceStatuses {
		issues := []string{}
		statusStr := "* Clean"
		branch := ws.Branch

		if ws.Error != nil {
			issues = append(issues, "error checking status")
			statusStr = "? Error"
			branch = "unknown"
		} else {
			if ws.Dirty {
				// Check if the only uncommitted change is CHANGELOG.md
				// which might have been pre-generated from the TUI workflow
				fullPath := filepath.Join(rootDir, ws.Path)
				diffCmd := exec.Command("git", "diff", "--name-only")
				diffCmd.Dir = fullPath
				diffOutput, _ := diffCmd.Output()
				
				stagedCmd := exec.Command("git", "diff", "--cached", "--name-only")
				stagedCmd.Dir = fullPath
				stagedOutput, _ := stagedCmd.Output()
				
				allChanges := strings.TrimSpace(string(diffOutput)) + "\n" + strings.TrimSpace(string(stagedOutput))
				allChanges = strings.TrimSpace(allChanges)
				
				// If the only change is CHANGELOG.md, don't treat it as a blocker
				if allChanges != "" && allChanges != "CHANGELOG.md" {
					issues = append(issues, "uncommitted changes")
					statusStr = "x Dirty"
				} else if allChanges == "CHANGELOG.md" {
					// Show it's changelog only - use circle and orange color
					statusStr = "◯ Changelog"
					// Don't add to issues - it's not a blocker
				}
			}
			if ws.Branch != "main" {
				issues = append(issues, "not on main branch")
			}
			// Don't treat "ahead of remote" as a blocking issue
			// It's just informational - we can still release locally
		}

		if len(issues) > 0 {
			hasIssues = true
		}

		// Add ahead info as display-only (not a blocking issue)
		displayIssues := make([]string, len(issues))
		copy(displayIssues, issues)
		
		if ws.AheadCount > 0 {
			if releasePush {
				displayIssues = append(displayIssues, fmt.Sprintf("ahead of remote by %d commits (will push)", ws.AheadCount))
			} else {
				displayIssues = append(displayIssues, fmt.Sprintf("ahead of remote by %d commits", ws.AheadCount))
			}
		}
		
		// If dirty but only CHANGELOG.md, add a note
		if statusStr == "◯ Changelog" && len(issues) == 0 {
			// This means it's only the CHANGELOG.md that's dirty
			displayIssues = append(displayIssues, "uncommitted CHANGELOG.md (will be committed)")
		}

		tableRows = append(tableRows, []string{
			ws.Path,
			branch,
			statusStr,
			strings.Join(displayIssues, ", "),
		})
	}

	// Display styled table
	fmt.Println()
	displayPreflightTable(
		[]string{"REPOSITORY", "BRANCH", "STATUS", "ISSUES"},
		tableRows,
	)

	// Check if the parent version tag already exists
	checkCmd := exec.Command("git", "tag", "-l", version)
	checkOutput, _ := checkCmd.Output()
	if len(checkOutput) > 0 {
		displayError(fmt.Sprintf("Parent repository tag %s already exists", version))
		hasIssues = true
	}

	// Check if we should proceed
	if hasIssues && !releaseForce && !releaseDryRun {
		displayError("Pre-flight checks failed. Fix the issues above or use --force to proceed anyway.")
		return fmt.Errorf("pre-flight checks failed")
	}

	if hasIssues && releaseForce {
		displayWarning("Issues detected but proceeding with --force flag")
	} else if !hasIssues {
		displaySuccess("All pre-flight checks passed")
	}

	return nil
}


func tagSubmodules(ctx context.Context, rootDir string, versions map[string]string, hasChanges map[string]bool, logger *logrus.Logger) error {
	// Get list of submodules
	cmd := command.NewSafeBuilder()
	listCmd, err := cmd.Build(ctx, "git", "submodule", "status")
	if err != nil {
		return fmt.Errorf("failed to build git command: %w", err)
	}

	execCmd := listCmd.Exec()
	execCmd.Dir = rootDir
	output, err := execCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list submodules: %w", err)
	}

	// Parse and process each submodule
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse submodule path from status output
		// Format: " <hash> <path> (<description>)"
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		smPath := parts[1]

		// Get the repo name from path
		repoName := filepath.Base(smPath)
		version, ok := versions[repoName]
		if !ok {
			// Don't warn if we're filtering repos - this is expected
			if len(releaseRepos) == 0 {
				logger.WithField("path", smPath).Warn("No version found for submodule")
			}
			continue
		}

		// Skip if no changes
		if changes, ok := hasChanges[repoName]; ok && !changes {
			logger.WithField("path", smPath).Info("Skipping submodule (no changes)")
			continue
		}

		smFullPath := filepath.Join(rootDir, smPath)
		logger.WithFields(logrus.Fields{"path": smPath, "version": version}).Info("Tagging submodule")

		// Check if clean (unless force)
		if !releaseForce {
			status, err := git.GetStatus(smFullPath)
			if err != nil {
				return fmt.Errorf("failed to get status for %s: %w", smPath, err)
			}
			if status.IsDirty {
				return fmt.Errorf("submodule %s has uncommitted changes", smPath)
			}
			if status.Branch != "main" {
				return fmt.Errorf("submodule %s is not on main branch (current: %s)", smPath, status.Branch)
			}
			// Check if local branch is ahead of remote (only if we didn't just push)
			if !releasePush && status.HasUpstream && status.AheadCount > 0 {
				return fmt.Errorf("submodule %s is ahead of remote by %d commits - please push changes first", smPath, status.AheadCount)
			}
		}

		// Tag the submodule
		if err := executeGitCommand(ctx, smFullPath, []string{"tag", "-a", version, "-m", fmt.Sprintf("Release %s", version)},
			fmt.Sprintf("Tag %s", smPath), logger); err != nil {
			return err
		}

		// Push the tag
		if err := executeGitCommand(ctx, smFullPath, []string{"push", "origin", version},
			fmt.Sprintf("Push tag for %s", smPath), logger); err != nil {
			return err
		}
	}

	return nil
}

func executeGitCommand(ctx context.Context, dir string, args []string, description string, logger *logrus.Logger) error {
	cmd := fmt.Sprintf("git %s", strings.Join(args, " "))
	if releaseDryRun {
		ulog.Info("[DRY RUN] Would execute").
			Field("command", cmd).
			Field("dir", dir).
			Pretty(fmt.Sprintf("%s [DRY RUN] %s", theme.IconInfo, cmd)).
			Log(ctx)
		return nil
	}

	ulog.Info(description).
		Field("command", cmd).
		Pretty(fmt.Sprintf("%s %s", theme.IconRunning, cmd)).
		Log(ctx)

	// Retry logic for git lock errors
	maxRetries := 3
	retryDelay := 500 * time.Millisecond

	var lastErr error
	var output []byte

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			logger.WithFields(logrus.Fields{
				"attempt": attempt + 1,
				"max":     maxRetries,
			}).Warn("Retrying git command due to lock error")
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}

		cmdBuilder := command.NewSafeBuilder()
		cmd, err := cmdBuilder.Build(ctx, "git", args...)
		if err != nil {
			return fmt.Errorf("failed to build git command: %w", err)
		}

		execCmd := cmd.Exec()
		execCmd.Dir = dir

		// Capture combined output to display git's actual error messages
		output, lastErr = execCmd.CombinedOutput()

		// Check if it's a lock error
		if lastErr != nil {
			outputStr := string(output)
			if strings.Contains(outputStr, "index.lock") || strings.Contains(outputStr, "Unable to create") {
				// This is a lock error, retry
				if len(output) > 0 && attempt == 0 {
					// Print error on first attempt
					fmt.Print(outputStr)
				}
				continue
			}
			// Not a lock error, fail immediately
			break
		}

		// Success
		if len(output) > 0 {
			fmt.Print(string(output))
		}
		return nil
	}

	// All retries exhausted or non-lock error
	if len(output) > 0 {
		fmt.Print(string(output))
	}

	if lastErr != nil {
		return fmt.Errorf("git command failed: %w", lastErr)
	}

	return nil
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// expandReposWithDependencies expands the list of repositories to include all their dependencies
func expandReposWithDependencies(repos []string, graph *depsgraph.Graph) ([]string, map[string]bool) {
	expanded := make(map[string]bool)
	autoDeps := make(map[string]bool) // Track which were auto-added

	// Helper function to recursively add dependencies
	var addDeps func(repo string)
	addDeps = func(repo string) {
		// Skip if already processed
		if expanded[repo] {
			return
		}
		expanded[repo] = true

		// Get the node to access its dependencies
		node, exists := graph.GetNode(repo)
		if !exists {
			return
		}

		// Find Grove dependencies
		for _, dep := range node.Deps {
			// Check if this is a Grove dependency by looking for matching nodes
			for name, n := range graph.GetAllNodes() {
				if n.Path == dep {
					// This is a Grove dependency
					if !expanded[name] {
						autoDeps[name] = true
					}
					addDeps(name)
					break
				}
			}
		}
	}

	// Process each explicitly requested repo
	for _, repo := range repos {
		expanded[repo] = false // false means explicitly requested
		addDeps(repo)
	}

	// Convert map to slice
	var result []string
	for repo := range expanded {
		result = append(result, repo)
	}

	return result, autoDeps
}

func calculateNextVersions(ctx context.Context, rootDir string, workspaces []string, major, minor, patch []string, isRC bool, logger *logrus.Logger) (map[string]string, map[string]string, map[string]int, error) {
	versions := make(map[string]string)
	currentVersions := make(map[string]string)
	commitsSinceTag := make(map[string]int)

	// Create a map for filtering repositories if specified
	repoFilter := make(map[string]bool)
	if len(releaseRepos) > 0 {
		for _, repo := range releaseRepos {
			repoFilter[repo] = true
		}
	}

	// Create bump type map for easier lookup
	bumpTypes := make(map[string]string)
	for _, repo := range major {
		bumpTypes[repo] = "major"
	}
	for _, repo := range minor {
		bumpTypes[repo] = "minor"
	}
	for _, repo := range patch {
		bumpTypes[repo] = "patch"
	}

	// Process each workspace
	for _, wsPath := range workspaces {
		repoName := filepath.Base(wsPath)

		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
			continue
		}

		// Get latest tag
		cmdBuilder := command.NewSafeBuilder()
		tagCmd, err := cmdBuilder.Build(ctx, "git", "describe", "--tags", "--abbrev=0")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
		}

		execCmd := tagCmd.Exec()
		execCmd.Dir = wsPath
		tagOutput, err := execCmd.Output()

		var currentVersion *semver.Version
		var currentTag string
		hasTag := err == nil

		if !hasTag {
			// No tags found, start with v0.1.0
			currentVersion = semver.MustParse("0.0.0")
			currentVersions[repoName] = "v0.0.0"
			logger.WithFields(logrus.Fields{"repo": repoName, "default": "v0.0.0"}).Info("No tags found, using default")
			commitsSinceTag[repoName] = 1 // New repo always needs initial release
		} else {
			currentTag = strings.TrimSpace(string(tagOutput))
			currentVersions[repoName] = currentTag
			currentVersion, err = semver.NewVersion(currentTag)
			if err != nil {
				// Skip repos with non-semver tags (like grove-ecosystem with calendar versioning)
				// The parent ecosystem uses date-based versioning (v2025.01.14 or v2025.01.14.1)
				// which is handled separately by determineParentVersion
				if repoName == "grove-ecosystem" || isRC {
					logger.WithFields(logrus.Fields{"repo": repoName, "tag": currentTag}).Info("Skipping non-semver repo (calendar versioning)")
					versions[repoName] = currentTag
					commitsSinceTag[repoName] = 0
					continue
				}
				return nil, nil, nil, fmt.Errorf("failed to parse version %s for %s: %w", currentTag, repoName, err)
			}

			// Check if there are commits since the last tag
			commitCountCmd, err := cmdBuilder.Build(ctx, "git", "rev-list", "--count", currentTag+"..HEAD")
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
			}

			execCmd := commitCountCmd.Exec()
			execCmd.Dir = wsPath
			countOutput, err := execCmd.Output()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to count commits for %s: %w", repoName, err)
			}

			commitCountStr := strings.TrimSpace(string(countOutput))
			commitCount, _ := strconv.Atoi(commitCountStr)
			if commitCount == 0 && !releaseForceIncrement {
				// Check if current commit already has the tag
				tagCheckCmd, err := cmdBuilder.Build(ctx, "git", "describe", "--exact-match", "--tags", "HEAD")
				if err != nil {
					return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
				}

				execCmd := tagCheckCmd.Exec()
				execCmd.Dir = wsPath
				tagCheckOutput, err := execCmd.Output()

				if err == nil && strings.TrimSpace(string(tagCheckOutput)) == currentTag {
					// Current commit already has this tag, keep the same version
					versions[repoName] = currentTag
					commitsSinceTag[repoName] = 0
					continue
				}
			}

			// Either there are new commits, or force increment is enabled
			commitsSinceTag[repoName] = commitCount
		}

		if isRC {
			// For RC releases, determine the base version
			var newVersion semver.Version

			// If current version is a pre-release (RC tag), reuse its base version
			if currentVersion.Prerelease() != "" {
				// Current tag is an RC tag (e.g., v0.4.1-nightly.abc), reuse v0.4.1
				baseVer, _ := semver.NewVersion(fmt.Sprintf("%d.%d.%d",
					currentVersion.Major(),
					currentVersion.Minor(),
					currentVersion.Patch()))
				newVersion = *baseVer
			} else {
				// Current version is stable (e.g., v0.4.0), increment patch for RC
				newVersion = currentVersion.IncPatch()
			}

			// Get short commit SHA
			shaCmd, err := cmdBuilder.Build(ctx, "git", "rev-parse", "--short", "HEAD")
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to build git command for SHA: %w", err)
			}
			shaExec := shaCmd.Exec()
			shaExec.Dir = wsPath
			shaOutput, err := shaExec.Output()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to get short SHA for %s: %w", repoName, err)
			}
			shortSHA := strings.TrimSpace(string(shaOutput))

			// Construct pre-release version, e.g., v0.1.2-nightly.a1b2c3d
			preReleaseID := fmt.Sprintf("nightly.%s", shortSHA)
			finalVersion, err := newVersion.SetPrerelease(preReleaseID)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to set prerelease version for %s: %w", repoName, err)
			}
			versions[repoName] = "v" + finalVersion.String()
		} else {
			// Determine bump type (default to patch)
			bumpType, ok := bumpTypes[repoName]
			if !ok {
				bumpType = "patch"
			}

			// Calculate new version
			var newVersion semver.Version
			switch bumpType {
			case "major":
				newVersion = currentVersion.IncMajor()
			case "minor":
				newVersion = currentVersion.IncMinor()
			case "patch":
				newVersion = currentVersion.IncPatch()
			}

			versions[repoName] = "v" + newVersion.String()
		}
	}

	return versions, currentVersions, commitsSinceTag, nil
}

func getVersionIncrement(current, proposed string) string {
	if current == "-" || current == "" {
		return "initial"
	}

	// Check if this looks like a date-based version (v2025.01.08 format)
	if strings.Count(proposed, ".") >= 2 && len(proposed) > 6 {
		// Try to parse as date version
		var year1, month1, day1, suffix1 int
		var year2, month2, day2, suffix2 int

		// Parse current version
		n1, _ := fmt.Sscanf(current, "v%d.%d.%d.%d", &year1, &month1, &day1, &suffix1)
		if n1 < 3 {
			n1, _ = fmt.Sscanf(current, "v%d.%d.%d", &year1, &month1, &day1)
		}

		// Parse proposed version
		n2, _ := fmt.Sscanf(proposed, "v%d.%d.%d.%d", &year2, &month2, &day2, &suffix2)
		if n2 < 3 {
			n2, _ = fmt.Sscanf(proposed, "v%d.%d.%d", &year2, &month2, &day2)
		}

		// If both parsed as date versions
		if n1 >= 3 && n2 >= 3 && year1 > 2000 && year2 > 2000 {
			if year1 != year2 || month1 != month2 || day1 != day2 {
				return "new date"
			} else if suffix1 != suffix2 {
				return "same day"
			}
			return "-"
		}
	}

	// Fall back to semver parsing
	currentVer, err1 := semver.NewVersion(current)
	proposedVer, err2 := semver.NewVersion(proposed)

	if err1 != nil || err2 != nil {
		return "update"
	}

	if currentVer.Major() != proposedVer.Major() {
		return "major"
	} else if currentVer.Minor() != proposedVer.Minor() {
		return "minor"
	} else if currentVer.Patch() != proposedVer.Patch() {
		return "patch"
	}

	return "-"
}

func orchestrateRelease(ctx context.Context, rootDir string, releaseLevels [][]string, versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool, graph *depsgraph.Graph, logger *logrus.Logger, useLLMChangelog bool, plan *release.ReleasePlan) error {
	displaySection(theme.IconBullet + " Release Orchestration")

	// Process each level of dependencies
	for levelIndex, level := range releaseLevels {
		ulog.Info("Processing release level").
			Field("level", levelIndex).
			Pretty(fmt.Sprintf("%s Processing release level %d", theme.IconArrow, levelIndex)).
			Log(ctx)

		// Collect repositories that need releasing at this level
		var reposToRelease []string
		for _, repoName := range level {
			// Skip if no changes
			if changes, ok := hasChanges[repoName]; !ok || !changes {
				ulog.Debug("Skipping repo").
					Field("repo", repoName).
					Field("reason", "no changes").
					Pretty(fmt.Sprintf("  %s Skipping %s (no changes)", theme.IconPending, repoName)).
					Log(ctx)
				continue
			}

			// Skip if no version
			version, ok := versions[repoName]
			if !ok {
				ulog.Warn("No version found").
					Field("repo", repoName).
					Pretty(fmt.Sprintf("  %s No version found for %s, skipping", theme.IconWarning, repoName)).
					Log(ctx)
				continue
			}

			// Check if the version is actually changing
			currentVersion := currentVersions[repoName]
			if currentVersion == version {
				logger.WithFields(logrus.Fields{
					"repo":    repoName,
					"version": version,
				}).Info("Skipping release - version unchanged")
				continue
			}

			// Enhanced state detection: check if repo is already fully released
			wsPath := filepath.Join(rootDir, repoName)
			var repoPlan *release.RepoReleasePlan
			if plan != nil && plan.Repos != nil {
				repoPlan = plan.Repos[repoName]
			}
			
			if isRepoFullyReleased(ctx, wsPath, repoPlan) {
				displayInfo(fmt.Sprintf("%s %s already fully released (%s), skipping", theme.IconSuccess, repoName, version))
				continue
			}
			
			// If --resume flag is used, provide detailed status
			if releaseResume && repoPlan != nil {
				if repoPlan.LastFailedOperation != "" {
					displayInfo(fmt.Sprintf(" %s needs retry (failed at: %s)", repoName, repoPlan.LastFailedOperation))
				} else if repoPlan.ChangelogPushed || repoPlan.CIPassed {
					displayInfo(fmt.Sprintf(" %s partially complete, continuing", repoName))
				}
			}

			reposToRelease = append(reposToRelease, repoName)
		}

		if len(reposToRelease) == 0 {
			ulog.Debug("No repositories to release at this level").
				Field("level", levelIndex).
				Log(ctx)
			continue
		}

		// Process all repositories at this level in parallel
		if len(reposToRelease) > 1 {
			ulog.Info("Releasing repositories in parallel").
				Field("level", levelIndex).
				Field("count", len(reposToRelease)).
				Field("repos", strings.Join(reposToRelease, ", ")).
				Pretty(fmt.Sprintf("%s Releasing %d repositories in parallel: %s", theme.IconRunning, len(reposToRelease), strings.Join(reposToRelease, ", "))).
				Log(ctx)
		} else {
			ulog.Info("Releasing repository").
				Field("level", levelIndex).
				Field("repo", reposToRelease[0]).
				Pretty(fmt.Sprintf("%s Releasing %s", theme.IconArrow, reposToRelease[0])).
				Log(ctx)
		}

		// Use goroutines to release repositories in parallel
		var wg sync.WaitGroup
		errChan := make(chan error, len(reposToRelease))

		for _, repoName := range reposToRelease {
			wg.Add(1)
			go func(repo string) {
				defer wg.Done()

				version := versions[repo]
				node, ok := graph.GetNode(repo)
				if !ok {
					errChan <- fmt.Errorf("node not found in graph: %s", repo)
					return
				}

				wsPath := node.Dir

				ulog.Info("Releasing module").
					Field("repo", repo).
					Field("version", version).
					Pretty(fmt.Sprintf("  %s %s %s", theme.IconRepo, repo, theme.DefaultTheme.Success.Render(version))).
					Log(ctx)

				// For RC releases, checkout/create rc-nightly branch (always reset to main)
				if !releaseDryRun && plan.Type == "rc" {
					// Fetch latest main to ensure we're up to date
					displayInfo(fmt.Sprintf("Fetching latest main for %s...", repo))
					if err := executeGitCommand(ctx, wsPath, []string{"fetch", "origin", "main"}, "Fetch main", logger); err != nil {
						errChan <- fmt.Errorf("failed to fetch main for %s: %w", repo, err)
						return
					}

					// Check if rc-nightly branch exists locally
					checkLocalCmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "rc-nightly")
					checkLocalCmd.Dir = wsPath
					localExists := checkLocalCmd.Run() == nil

					if localExists {
						// Branch exists locally, checkout and reset to main
						displayInfo(fmt.Sprintf("Resetting rc-nightly to main for %s...", repo))
						if err := executeGitCommand(ctx, wsPath, []string{"checkout", "rc-nightly"}, "Checkout rc-nightly", logger); err != nil {
							errChan <- fmt.Errorf("failed to checkout rc-nightly for %s: %w", repo, err)
							return
						}
						// Hard reset to origin/main to get latest code
						if err := executeGitCommand(ctx, wsPath, []string{"reset", "--hard", "origin/main"}, "Reset rc-nightly to main", logger); err != nil {
							errChan <- fmt.Errorf("failed to reset rc-nightly to main for %s: %w", repo, err)
							return
						}
					} else {
						// Branch doesn't exist locally, create from main
						displayInfo(fmt.Sprintf("Creating rc-nightly branch from main for %s...", repo))
						if err := executeGitCommand(ctx, wsPath, []string{"checkout", "-b", "rc-nightly", "origin/main"}, "Create rc-nightly from main", logger); err != nil {
							errChan <- fmt.Errorf("failed to create rc-nightly for %s: %w", repo, err)
							return
						}
					}
				}

				// Update dependencies to latest versions (skip in dry-run mode)
				if !releaseDryRun {
					logger.WithFields(logrus.Fields{
						"repo": repo,
						"wsPath": wsPath,
					}).Info("[orchestrateRelease] Calling updateDependencies")
					if err := updateDependencies(ctx, wsPath, versions, graph, plan.Type, logger); err != nil {
						logger.WithFields(logrus.Fields{
							"repo": repo,
							"error": err,
						}).Error("[orchestrateRelease] updateDependencies failed")
						errChan <- fmt.Errorf("failed to update dependencies for %s: %w", repo, err)
						return
					}
					logger.WithField("repo", repo).Info("[orchestrateRelease] updateDependencies completed successfully")
					
					// After updating dependencies, check for changes and push them
					status, _ := git.GetStatus(wsPath)
					if status.IsDirty {
						// Determine target branch based on release type
						targetBranch := "main"
						if plan.Type == "rc" {
							targetBranch = "rc-nightly"
						}

						displayInfo(fmt.Sprintf("Pushing dependency updates for %s to %s...", repo, targetBranch))
						if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "HEAD:" + targetBranch}, "Push dependency updates", logger); err != nil {
							errChan <- fmt.Errorf("failed to push dependency updates for %s: %w", repo, err)
							return
						}
						// Wait for CI to complete (only for non-RC or if not skipping)
						if !releaseSkipCI {
							displayInfo(fmt.Sprintf("Waiting for CI to pass for %s after dependency updates...", repo))
							if err := gh.WaitForCIWorkflow(ctx, wsPath); err != nil {
								errChan <- fmt.Errorf("CI workflow for %s failed after dependency update: %w", repo, err)
								return
							}
						} else {
							displayInfo(fmt.Sprintf("Skipping CI wait for %s after dependency updates (--skip-ci enabled)", repo))
						}
					}
				}

				// Handle changelog - either use existing modifications or generate new
				if !releaseDryRun && plan.Type == "full" {
					// Check partial release state from previous attempts
					if plan != nil && plan.Repos != nil {
						if repoPlan, ok := plan.Repos[repo]; ok {
							if repoPlan.ChangelogPushed && repoPlan.CIPassed {
								displayInfo(fmt.Sprintf("Changelog pushed and CI passed for %s, skipping to tag creation", repo))
								goto createTag // Skip everything - go straight to tagging
							} else if repoPlan.ChangelogPushed {
								displayInfo(fmt.Sprintf("Changelog already pushed for %s, skipping to CI wait", repo))
								goto waitForCI // Skip changelog operations, but still wait for CI
							}
						}
					}
					
					// Check if CHANGELOG.md is already modified (from TUI workflow)
					// First check the plan's ChangelogState if available
					changelogModified := false
					skipChangelog := false
					
					// Check plan for changelog state
					if plan != nil && plan.Repos != nil {
						if repoPlan, ok := plan.Repos[repo]; ok && repoPlan.ChangelogState == "dirty" {
							// The changelog was written and modified by user
							changelogModified = true
							skipChangelog = true // Don't regenerate, use the dirty version
							displayInfo(fmt.Sprintf("Using manually edited changelog for %s", repo))
						}
					}
					
					// If not marked in plan, check git status
					if !changelogModified {
						// Check using git diff --name-only to see if CHANGELOG.md has changes
						diffCmd := exec.Command("git", "diff", "--name-only", "CHANGELOG.md")
						diffCmd.Dir = wsPath
						if diffOutput, _ := diffCmd.Output(); len(diffOutput) > 0 {
							changelogModified = true
						}
						
						// Also check if it's staged
						if !changelogModified {
							diffCmd = exec.Command("git", "diff", "--cached", "--name-only", "CHANGELOG.md")
							diffCmd.Dir = wsPath
							if diffOutput, _ := diffCmd.Output(); len(diffOutput) > 0 {
								changelogModified = true
							}
						}
					}
					
					if changelogModified {
						// CHANGELOG.md was already modified (likely from TUI workflow)
						// Just commit it as-is
						displayInfo(fmt.Sprintf("Using pre-generated changelog for %s", repo))
						if err := executeGitCommand(ctx, wsPath, []string{"add", "CHANGELOG.md"}, "Stage changelog", logger); err != nil {
							logger.WithError(err).Warnf("Failed to stage changelog for %s", repo)
						} else {
							commitMsg := fmt.Sprintf("docs(changelog): update CHANGELOG.md for %s", version)
							if err := executeGitCommand(ctx, wsPath, []string{"commit", "-m", commitMsg}, "Commit changelog", logger); err != nil {
								logger.WithError(err).Warnf("Failed to commit changelog for %s", repo)
							} else {
								// Push the changelog commit to remote
								if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "HEAD:main"},
									fmt.Sprintf("Push changelog for %s", repo), logger); err != nil {
									logger.WithError(err).Warnf("Failed to push changelog commit for %s", repo)
								} else {
									// Wait for CI on main to complete after pushing changelog
									// Update plan state to mark changelog as pushed first
									if plan != nil && plan.Repos != nil {
										if repoPlan, ok := plan.Repos[repo]; ok {
											repoPlan.ChangelogPushed = true
											repoPlan.LastFailedOperation = "ci_wait" // Next operation that could fail
											release.SavePlan(plan) // Save updated state
										}
									}
									
									if !releaseSkipCI {
										displayInfo(fmt.Sprintf("Waiting for CI to pass for %s after changelog update...", repo))
										if err := gh.WaitForCIWorkflow(ctx, wsPath); err != nil {
											errChan <- fmt.Errorf("CI workflow for %s failed after changelog update: %w", repo, err)
											return
										}
									} else {
										displayInfo(fmt.Sprintf("Skipping CI wait for %s after changelog update (--skip-ci enabled)", repo))
									}
									
									// Mark CI as passed only after successful wait or skip
									if plan != nil && plan.Repos != nil {
										if repoPlan, ok := plan.Repos[repo]; ok {
											repoPlan.CIPassed = true
											repoPlan.LastFailedOperation = "" // Clear failure since CI passed
											release.SavePlan(plan) // Save updated state
										}
									}
								}
							}
						}
					} else if !skipChangelog {
						// No existing changelog modifications, generate new one
						displayInfo(fmt.Sprintf("Generating changelog for %s", repo))
						changelogCmdArgs := []string{"changelog", wsPath, "--version", version}
						if useLLMChangelog {
							changelogCmdArgs = append(changelogCmdArgs, "--llm")
						}
						changelogCmd := exec.CommandContext(ctx, "grove", changelogCmdArgs...)
						if err := changelogCmd.Run(); err != nil {
							// Log a warning but don't fail the release if changelog fails
							logger.WithError(err).Warnf("Failed to generate changelog for %s", repo)
						} else {
							// Commit the changelog if it was modified
							status, _ := git.GetStatus(wsPath)
							if status.IsDirty {
								displayInfo(fmt.Sprintf("Committing CHANGELOG.md for %s", repo))
								if err := executeGitCommand(ctx, wsPath, []string{"add", "CHANGELOG.md"}, "Stage changelog", logger); err != nil {
									logger.WithError(err).Warnf("Failed to stage changelog for %s", repo)
								} else {
									commitMsg := fmt.Sprintf("docs(changelog): update CHANGELOG.md for %s", version)
									if err := executeGitCommand(ctx, wsPath, []string{"commit", "-m", commitMsg}, "Commit changelog", logger); err != nil {
										logger.WithError(err).Warnf("Failed to commit changelog for %s", repo)
									} else {
										// Push the changelog commit to remote
										if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "HEAD:main"},
											fmt.Sprintf("Push changelog for %s", repo), logger); err != nil {
											logger.WithError(err).Warnf("Failed to push changelog commit for %s", repo)
										} else {
											// Wait for CI on main to complete after pushing changelog
											// Update plan state to mark changelog as pushed first
											if plan != nil && plan.Repos != nil {
												if repoPlan, ok := plan.Repos[repo]; ok {
													repoPlan.ChangelogPushed = true
													repoPlan.LastFailedOperation = "ci_wait" // Next operation that could fail
													release.SavePlan(plan) // Save updated state
												}
											}
											
											if !releaseSkipCI {
												displayInfo(fmt.Sprintf("Waiting for CI to pass for %s after changelog update...", repo))
												if err := gh.WaitForCIWorkflow(ctx, wsPath); err != nil {
													errChan <- fmt.Errorf("CI workflow for %s failed after changelog update: %w", repo, err)
													return
												}
											} else {
												displayInfo(fmt.Sprintf("Skipping CI wait for %s after changelog update (--skip-ci enabled)", repo))
											}
											
											// Mark CI as passed only after successful wait or skip
											if plan != nil && plan.Repos != nil {
												if repoPlan, ok := plan.Repos[repo]; ok {
													repoPlan.CIPassed = true
													repoPlan.LastFailedOperation = "" // Clear failure since CI passed
													release.SavePlan(plan) // Save updated state
												}
											}
										}
									}
								}
							}
						}
					}
				}

			waitForCI:
				// If we jumped here, changelog was already pushed, but we need to wait for CI
				// This handles the case where changelog succeeded but CI failed in a previous attempt
				if plan != nil && plan.Repos != nil {
					if repoPlan, ok := plan.Repos[repo]; ok && repoPlan.ChangelogPushed && !repoPlan.CIPassed {
						if !releaseSkipCI {
							displayInfo(fmt.Sprintf("Waiting for CI to pass for %s (changelog already pushed)...", repo))
							if err := gh.WaitForCIWorkflow(ctx, wsPath); err != nil {
								errChan <- fmt.Errorf("CI workflow for %s failed: %w", repo, err)
								return
							}
							// Mark CI as passed and save plan
							repoPlan.CIPassed = true
							repoPlan.LastFailedOperation = "" // Clear failure since CI passed
							release.SavePlan(plan)
						} else {
							displayInfo(fmt.Sprintf("Skipping CI wait for %s (--skip-ci enabled)", repo))
							// Even with --skip-ci, mark it as passed to avoid future waits
							repoPlan.CIPassed = true
							repoPlan.LastFailedOperation = "" // Clear failure since we're skipping CI
							release.SavePlan(plan)
						}
					}
				}

			createTag:
				// Check for tag conflicts before creating
				tagExistsLocal, _ := tagExists(ctx, wsPath, version)
				if tagExistsLocal {
					// Tag exists, check if it's in our plan state
					if plan != nil && plan.Repos != nil {
						if repoPlan, ok := plan.Repos[repo]; ok && repoPlan.TagPushed {
							displayInfo(fmt.Sprintf("Tag %s already exists and marked as pushed for %s, skipping tag creation", version, repo))
							goto releaseWorkflow
						} else {
							// Tag exists but not marked in plan - potential conflict
							displayWarning(fmt.Sprintf("Tag %s exists for %s but not marked as pushed in plan - proceeding anyway", version, repo))
						}
					}
				}
				
				// Track operation for error reporting
				if plan != nil && plan.Repos != nil {
					if repoPlan, ok := plan.Repos[repo]; ok {
						repoPlan.LastFailedOperation = "tag_creation"
						release.SavePlan(plan)
					}
				}
				
				// Tag the module
				if err := executeGitCommand(ctx, wsPath, []string{"tag", "-a", version, "-m", fmt.Sprintf("Release %s", version)},
					fmt.Sprintf("Tag %s", repo), logger); err != nil {
					errChan <- err
					return
				}

				// For RC releases, push the rc-nightly branch first so the tag has a home
				if plan.Type == "rc" {
					if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "rc-nightly"},
						fmt.Sprintf("Push rc-nightly branch for %s", repo), logger); err != nil {
						errChan <- err
						return
					}
				}

				// Push the tag
				if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", version},
					fmt.Sprintf("Push tag for %s", repo), logger); err != nil {
					errChan <- err
					return
				}

				// Mark tag as successfully pushed
				if plan != nil && plan.Repos != nil {
					if repoPlan, ok := plan.Repos[repo]; ok {
						repoPlan.TagPushed = true
						repoPlan.LastFailedOperation = "" // Clear any previous failures
						release.SavePlan(plan)
					}
				}

			releaseWorkflow:

				// Wait for CI workflow to complete (skip in dry-run mode)
				if !releaseDryRun {
					// Check if the project has a .github directory with workflows
					githubDir := filepath.Join(wsPath, ".github")
					if _, err := os.Stat(githubDir); err == nil {
						// .github directory exists, wait for workflows
						logger.Infof("Waiting for CI release of %s@%s to complete (timeout: 60 minutes)...", repo, version)
						displayInfo(fmt.Sprintf("Waiting for CI release of %s@%s to complete (timeout: 60 minutes)...", repo, version))

						if err := gh.WaitForReleaseWorkflow(ctx, wsPath, version); err != nil {
							// This is a critical failure
							errChan <- fmt.Errorf("release workflow for %s@%s failed: %w", repo, version, err)
							return
						}

						ulog.Success("CI release successful").
							Field("repo", repo).
							Field("version", version).
							Pretty(fmt.Sprintf("  %s CI release for %s@%s successful", theme.IconSuccess, repo, version)).
							Log(ctx)
					} else {
						// No .github directory, skip CI workflow monitoring
						logger.Infof("No .github directory found for %s, skipping CI workflow monitoring", repo)
						displayInfo(fmt.Sprintf("No .github directory found for %s, skipping CI workflow monitoring", repo))
					}

					// Check if we need to wait for module availability (skip for template projects)
					needsModuleCheck, err := shouldWaitForModuleAvailability(wsPath)
					if err != nil {
						logger.WithError(err).Warnf("Failed to determine if %s needs module availability check, defaulting to check", repo)
						needsModuleCheck = true
					}

					if needsModuleCheck {
						// Now wait for module to be available on the proxy
						displayInfo(fmt.Sprintf("Waiting for %s@%s to be available...", node.Path, version))

						if err := release.WaitForModuleAvailability(ctx, node.Path, version); err != nil {
							errChan <- fmt.Errorf("failed waiting for %s@%s: %w", node.Path, version, err)
							return
						}
					} else {
						displayInfo(fmt.Sprintf("Skipping module availability check for %s (not a Go module)", repo))
					}

					displayComplete(fmt.Sprintf("%s successfully released", repo))
				}
			}(repoName)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(errChan)

		// Check for errors
		for err := range errChan {
			if err != nil {
				return err
			}
		}

	}

	displaySuccess("All modules released successfully")
	return nil
}

func updateDependencies(ctx context.Context, modulePath string, releasedVersions map[string]string, graph *depsgraph.Graph, planType string, logger *logrus.Logger) error {
	logger.WithFields(logrus.Fields{
		"modulePath": modulePath,
		"releasedVersions": releasedVersions,
		"planType": planType,
	}).Info("[updateDependencies] Starting dependency update")
	
	// Load grove.yml to get project type
	groveYmlPath := filepath.Join(modulePath, "grove.yml")
	cfg, err := config.Load(groveYmlPath)
	if err != nil {
		// If no grove.yml, assume Go project for backward compatibility
		return updateGoDependencies(ctx, modulePath, releasedVersions, graph, logger)
	}

	// Get project type
	var projectTypeStr string
	if err := cfg.UnmarshalExtension("type", &projectTypeStr); err != nil || projectTypeStr == "" {
		// Default to Go for backward compatibility
		projectTypeStr = string(project.TypeGo)
	}

	projectType := project.Type(projectTypeStr)

	// Get appropriate handler
	registry := project.NewRegistry()
	handler, err := registry.Get(projectType)
	if err != nil {
		logger.WithError(err).Warnf("No handler for project type %s, skipping dependency update", projectType)
		return nil
	}

	// Parse current dependencies
	deps, err := handler.ParseDependencies(modulePath)
	if err != nil {
		logger.WithError(err).Warnf("Failed to parse dependencies for %s", filepath.Base(modulePath))
		return nil
	}

	// Track if we made any updates
	hasUpdates := false

	// Check each dependency
	for _, dep := range deps {
		if !dep.Workspace {
			continue
		}

		// Find the workspace name for this dependency
		var depWorkspaceName string
		if projectType == project.TypeGo {
			// For Go projects, map module path to workspace name
			for name, node := range graph.GetAllNodes() {
				if node.Path == dep.Name {
					depWorkspaceName = name
					break
				}
			}
		} else {
			// For other project types, dependency name should match workspace name
			depWorkspaceName = dep.Name
		}

		if depWorkspaceName == "" {
			continue
		}

		// Determine the target version for this dependency
		var targetVersion string
		
		// First check if this dependency is being released in the current batch
		if newVersion, hasNewVersion := releasedVersions[depWorkspaceName]; hasNewVersion {
			targetVersion = newVersion
			logger.WithFields(logrus.Fields{
				"dep": dep.Name,
				"workspace": depWorkspaceName,
				"version": newVersion,
			}).Info("Using version from current release batch")
		} else {
			// Dependency is not in current release batch, fetch latest version
			// Only fetch latest for Go projects (others don't have a module proxy)
			if projectType == project.TypeGo {
				var latestVersion string
				var err error

				// For RC releases, fetch latest prerelease version; otherwise fetch latest stable
				if planType == "rc" {
					latestVersion, err = getLatestPrereleaseModuleVersion(dep.Name)
					if err != nil {
						logger.WithError(err).Warnf("Failed to get latest prerelease version for %s, keeping current version", dep.Name)
						continue
					}
					logger.WithFields(logrus.Fields{
						"dep": dep.Name,
						"workspace": depWorkspaceName,
						"version": latestVersion,
					}).Info("Using latest prerelease version from module proxy")
				} else {
					latestVersion, err = getLatestModuleVersion(dep.Name)
					if err != nil {
						logger.WithError(err).Warnf("Failed to get latest version for %s, keeping current version", dep.Name)
						continue
					}
					logger.WithFields(logrus.Fields{
						"dep": dep.Name,
						"workspace": depWorkspaceName,
						"version": latestVersion,
					}).Info("Using latest version from module proxy")
				}
				targetVersion = latestVersion
			} else {
				// For non-Go projects, skip if not in release batch
				continue
			}
		}

		// Check if update is needed
		if dep.Version == targetVersion {
			logger.WithFields(logrus.Fields{
				"dep": dep.Name,
				"version": dep.Version,
			}).Debug("Dependency already at target version")
			continue
		}

		logger.WithFields(logrus.Fields{
			"dep": dep.Name,
			"old": dep.Version,
			"new": targetVersion,
		}).Info("Updating dependency")

		// Update to target version
		dep.Version = targetVersion
		if err := handler.UpdateDependency(modulePath, dep); err != nil {
			return fmt.Errorf("failed to update %s: %w", dep.Name, err)
		}

		hasUpdates = true
	}

	if hasUpdates {
		// Check for changes
		status, err := git.GetStatus(modulePath)
		if err != nil {
			return fmt.Errorf("failed to get git status: %w", err)
		}

		if status.IsDirty {
			// Commit the dependency updates
			commitFiles := []string{}
			if projectType == project.TypeGo {
				commitFiles = []string{"go.mod", "go.sum"}
			} else if projectType == project.TypeMaturin {
				commitFiles = []string{"pyproject.toml"}
			}

			if len(commitFiles) > 0 {
				if err := executeGitCommand(ctx, modulePath, append([]string{"add"}, commitFiles...),
					"Stage dependency updates", logger); err != nil {
					return err
				}

				commitMsg := "chore(deps): update Grove dependencies to latest versions"
				if err := executeGitCommand(ctx, modulePath, []string{"commit", "-m", commitMsg},
					"Commit dependency updates", logger); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Keep the original function for backward compatibility
func updateGoDependencies(ctx context.Context, modulePath string, releasedVersions map[string]string, graph *depsgraph.Graph, logger *logrus.Logger) error {
	logger.WithFields(logrus.Fields{
		"modulePath": modulePath,
		"releasedVersions": releasedVersions,
	}).Info("[updateGoDependencies] Starting dependency update")
	
	goModPath := filepath.Join(modulePath, "go.mod")

	// Read current go.mod
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("failed to read go.mod: %w", err)
	}

	modFile, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Track if we made any updates
	hasUpdates := false
	updatedDeps := []string{}

	// Check each dependency
	for _, req := range modFile.Require {
		// Only update Grove ecosystem dependencies
		if !strings.HasPrefix(req.Mod.Path, "github.com/mattsolo1/") {
			continue
		}

		// Find the module name from path
		var depName string
		for name, node := range graph.GetAllNodes() {
			if node.Path == req.Mod.Path {
				depName = name
				break
			}
		}

		if depName == "" {
			continue
		}

		// Determine the target version for this dependency
		var targetVersion string
		
		// First check if this dependency is being released in the current batch
		if newVersion, hasNewVersion := releasedVersions[depName]; hasNewVersion {
			targetVersion = newVersion
			logger.WithFields(logrus.Fields{
				"dep": req.Mod.Path,
				"depName": depName,
				"version": newVersion,
			}).Info("[updateGoDependencies] Using version from current release batch")
		} else {
			// Dependency is not in current release batch, fetch latest version
			latestVersion, err := getLatestModuleVersion(req.Mod.Path)
			if err != nil {
				logger.WithError(err).Warnf("[updateGoDependencies] Failed to get latest version for %s, keeping current version", req.Mod.Path)
				continue
			}
			targetVersion = latestVersion
			logger.WithFields(logrus.Fields{
				"dep": req.Mod.Path,
				"depName": depName,
				"version": latestVersion,
			}).Info("[updateGoDependencies] Using latest version from module proxy")
		}

		// Check if update is needed
		if req.Mod.Version == targetVersion {
			logger.WithFields(logrus.Fields{
				"dep": req.Mod.Path,
				"version": req.Mod.Version,
			}).Debug("[updateGoDependencies] Dependency already at target version")
			continue
		}

		logger.WithFields(logrus.Fields{
			"dep": req.Mod.Path,
			"old": req.Mod.Version,
			"new": targetVersion,
		}).Info("[updateGoDependencies] Updating dependency")
		updatedDeps = append(updatedDeps, fmt.Sprintf("%s: %s -> %s", req.Mod.Path, req.Mod.Version, targetVersion))

		// Update to target version
		cmd := exec.CommandContext(ctx, "go", "get", fmt.Sprintf("%s@%s", req.Mod.Path, targetVersion))
		cmd.Dir = modulePath
		cmd.Env = append(os.Environ(),
			"GOPRIVATE=github.com/mattsolo1/*",
			"GOPROXY=direct",
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to update %s: %w (output: %s)", req.Mod.Path, err, output)
		}

		hasUpdates = true
	}

	if hasUpdates {
		logger.WithFields(logrus.Fields{
			"modulePath": modulePath,
			"updatedDeps": updatedDeps,
		}).Info("[updateGoDependencies] Running go mod tidy after updates")
		
		// Run go mod tidy
		cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
		cmd.Dir = modulePath
		cmd.Env = append(os.Environ(),
			"GOPRIVATE=github.com/mattsolo1/*",
			"GOPROXY=direct",
		)

		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go mod tidy failed: %w (output: %s)", err, output)
		}

		// Always stage the dependency files first
		logger.WithField("modulePath", modulePath).Info("[updateGoDependencies] Staging go.mod and go.sum")
		if err := executeGitCommand(ctx, modulePath, []string{"add", "go.mod", "go.sum"},
			"Stage dependency updates", logger); err != nil {
			logger.WithError(err).Error("[updateGoDependencies] Failed to stage dependency files")
			return err
		}

		// Check if there are any staged changes before committing
		logger.WithField("modulePath", modulePath).Info("[updateGoDependencies] Checking for staged changes")
		diffCmd := exec.CommandContext(ctx, "git", "diff", "--staged", "--quiet")
		diffCmd.Dir = modulePath
		if err := diffCmd.Run(); err != nil {
			// An error (usually exit code 1) means there are staged changes
			logger.WithField("modulePath", modulePath).Info("[updateGoDependencies] Found staged changes, will commit")
			commitMsg := "chore(deps): update Grove dependencies to latest versions"
			if err := executeGitCommand(ctx, modulePath, []string{"commit", "-m", commitMsg},
				"Commit dependency updates", logger); err != nil {
				return err
			}

		} else {
			logger.WithField("modulePath", modulePath).Info("[updateGoDependencies] No staged changes found, skipping commit")
		}
	} else {
		logger.WithField("modulePath", modulePath).Info("[updateGoDependencies] No updates needed")
	}

	logger.WithField("modulePath", modulePath).Info("[updateGoDependencies] Completed successfully")
	return nil
}

func displayAndConfirmVersionsWithOrder(rootDir string, versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool, releaseLevels [][]string, graph *depsgraph.Graph, parentVersion string, autoDependencies map[string]bool, logger *logrus.Logger) bool {
	displaySection(" Proposed Versions")

	// Create separate lists for repos with and without changes
	var reposWithChanges []string
	var reposWithoutChanges []string
	for repo := range versions {
		if hasChanges[repo] {
			reposWithChanges = append(reposWithChanges, repo)
		} else {
			reposWithoutChanges = append(reposWithoutChanges, repo)
		}
	}

	// Sort both lists
	sortRepos := func(repos []string) {
		for i := 0; i < len(repos); i++ {
			for j := i + 1; j < len(repos); j++ {
				if repos[i] > repos[j] {
					repos[i], repos[j] = repos[j], repos[i]
				}
			}
		}
	}
	sortRepos(reposWithChanges)
	sortRepos(reposWithoutChanges)

	// Count repos with changes
	changeCount := len(reposWithChanges)

	if changeCount == 0 {
		fmt.Println("\nNo repositories have changes to release.")
		return false
	}

	// Prepare rows
	var rows [][]string

	// Add grove-ecosystem first (parent repository)
	if changeCount > 0 {
		// Get current version of grove-ecosystem - find the latest tag
		cmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
		cmd.Dir = rootDir // Ensure we're in the root directory
		output, err := cmd.Output()
		currentEcosystemVersion := "-"
		if err == nil {
			currentEcosystemVersion = strings.TrimSpace(string(output))
		}

		// Check if parent version would need a suffix
		displayParentVersion := parentVersion
		// Check if this version already exists
		checkCmd := exec.Command("git", "tag", "-l", parentVersion)
		checkCmd.Dir = rootDir
		checkOutput, _ := checkCmd.Output()
		if len(strings.TrimSpace(string(checkOutput))) > 0 {
			// Show with asterisk to indicate it will be adjusted
			displayParentVersion = parentVersion + "*"
		}

		// Style the parent version
		styledParentVersion := releaseHighlightStyle.Render(displayParentVersion)
		// For date-based versions, show "date" as increment type
		parentIncrement := "date"
		if currentEcosystemVersion == parentVersion {
			parentIncrement = "-"
		}
		rows = append(rows, []string{"grove-ecosystem", currentEcosystemVersion, styledParentVersion, parentIncrement})

		// Add a separator
		rows = append(rows, []string{"───────────────", "─────────", "──────────", "───────────"})
	}

	// Add repos with changes
	for _, repo := range reposWithChanges {
		current := currentVersions[repo]
		if current == "" {
			current = "-"
		}
		proposed := versions[repo]
		increment := getVersionIncrement(current, proposed)

		// Add indicator if this was auto-included as a dependency
		repoDisplay := repo
		if autoDependencies != nil && autoDependencies[repo] {
			repoDisplay = repo + " (auto)"
		}

		// Pre-style the proposed version
		styledProposed := releaseHighlightStyle.Render(proposed)
		rows = append(rows, []string{repoDisplay, current, styledProposed, increment})
	}

	// Then add repos without changes
	for _, repo := range reposWithoutChanges {
		current := currentVersions[repo]
		if current == "" {
			current = "-"
		}
		proposed := versions[repo]
		// Pre-style all columns for dimmed rows
		rows = append(rows, []string{
			releaseDimStyle.Render(repo),
			releaseDimStyle.Render(current),
			releaseDimStyle.Render(proposed),
			releaseDimStyle.Render("-"),
		})
	}

	// Create table with lipgloss

	headerStyle := theme.DefaultTheme.Header.Copy().Padding(0, 1)

	// Create the table
	t := tablecomponent.NewStyledTable().
		Border(lipgloss.NormalBorder()).
		BorderStyle(theme.DefaultTheme.Muted).
		Headers("REPOSITORY", "CURRENT", "PROPOSED", "INCREMENT").
		Rows(rows...)

	// Apply styling only to header since content is pre-styled
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return headerStyle
		}
		// Return a minimal style that preserves the pre-styled content
		return lipgloss.NewStyle().Padding(0, 1)
	})

	fmt.Println(t)

	// Display release order with better formatting
	displayReleaseSummary(releaseLevels, versions, currentVersions, hasChanges)

	fmt.Printf("\n%d repositories will be released.\n", changeCount)

	// Show auto-included dependencies info
	if autoDependencies != nil && len(autoDependencies) > 0 {
		fmt.Printf("\nNote: %d repositories were auto-included as dependencies (marked with 'auto').\n", len(autoDependencies))
		if releaseWithDeps {
			fmt.Println("Use --repos without --with-deps to release only the specified repositories.")
		}
	}

	// Check if parent version needs adjustment
	checkCmd := exec.Command("git", "tag", "-l", parentVersion)
	checkCmd.Dir = rootDir
	checkOutput, _ := checkCmd.Output()
	if len(strings.TrimSpace(string(checkOutput))) > 0 {
		fmt.Printf("\nNote: grove-ecosystem tag %s already exists - will use %s.1 instead\n",
			parentVersion, parentVersion)
	}

	if releaseDryRun {
		fmt.Println("\n[DRY RUN] Would proceed with these versions")
		return true
	}

	if releaseYes {
		fmt.Println("\n[AUTO-CONFIRM] Proceeding with release (--yes flag)")
		return true
	}

	// Ask for confirmation
	fmt.Print("\nProceed with release? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		logger.Error("Failed to read input", "error", err)
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

func displayAndConfirmVersions(versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool, logger *logrus.Logger) bool {
	displaySection(" Proposed Versions")

	// Create separate lists for repos with and without changes
	var reposWithChanges []string
	var reposWithoutChanges []string
	for repo := range versions {
		if hasChanges[repo] {
			reposWithChanges = append(reposWithChanges, repo)
		} else {
			reposWithoutChanges = append(reposWithoutChanges, repo)
		}
	}

	// Sort both lists
	sortRepos := func(repos []string) {
		for i := 0; i < len(repos); i++ {
			for j := i + 1; j < len(repos); j++ {
				if repos[i] > repos[j] {
					repos[i], repos[j] = repos[j], repos[i]
				}
			}
		}
	}
	sortRepos(reposWithChanges)
	sortRepos(reposWithoutChanges)

	// Count repos with changes
	changeCount := len(reposWithChanges)

	// Prepare rows
	var rows [][]string

	// Add repos with changes first
	for _, repo := range reposWithChanges {
		current := currentVersions[repo]
		if current == "" {
			current = "-"
		}
		proposed := versions[repo]
		increment := getVersionIncrement(current, proposed)
		// Pre-style the proposed version
		styledProposed := releaseHighlightStyle.Render(proposed)
		rows = append(rows, []string{repo, current, styledProposed, increment})
	}

	// Then add repos without changes
	for _, repo := range reposWithoutChanges {
		current := currentVersions[repo]
		if current == "" {
			current = "-"
		}
		proposed := versions[repo]
		// Pre-style all columns for dimmed rows
		rows = append(rows, []string{
			releaseDimStyle.Render(repo),
			releaseDimStyle.Render(current),
			releaseDimStyle.Render(proposed),
			releaseDimStyle.Render("-"),
		})
	}

	// Create table with lipgloss

	headerStyle := theme.DefaultTheme.Header.Copy().Padding(0, 1)

	// Create the table
	t := tablecomponent.NewStyledTable().
		Border(lipgloss.NormalBorder()).
		BorderStyle(theme.DefaultTheme.Muted).
		Headers("REPOSITORY", "CURRENT", "PROPOSED", "INCREMENT").
		Rows(rows...)

	// Apply styling only to header since content is pre-styled
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return headerStyle
		}
		// Return a minimal style that preserves the pre-styled content
		return lipgloss.NewStyle().Padding(0, 1)
	})

	fmt.Println(t)

	if changeCount == 0 {
		fmt.Println("\nNo repositories have changes to release.")
		return false
	}

	fmt.Printf("\n%d repositories will be released.\n", changeCount)

	if releaseDryRun {
		fmt.Println("\n[DRY RUN] Would proceed with these versions")
		return true
	}

	// Ask for confirmation
	fmt.Print("\nProceed with release? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		logger.Error("Failed to read input", "error", err)
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

func determineParentVersion(rootDir string, versions map[string]string, hasChanges map[string]bool) string {
	// Check if any submodules have changes
	hasAnyChanges := false
	for _, hasChange := range hasChanges {
		if hasChange {
			hasAnyChanges = true
			break
		}
	}

	// Get the latest date-based tag
	cmd := exec.Command("git", "tag", "-l", "--sort=-version:refname")
	cmd.Dir = rootDir
	output, err := cmd.Output()

	currentTag := ""
	if err == nil {
		tags := strings.Split(strings.TrimSpace(string(output)), "\n")
		// Find the latest date-based tag
		for _, tag := range tags {
			if tag != "" {
				// Check if it's a date-based tag
				var year, month, day int
				n, _ := fmt.Sscanf(tag, "v%d.%d.%d", &year, &month, &day)
				if n >= 3 && year > 2000 {
					currentTag = tag
					break
				}
			}
		}
	}

	// If no submodules have changes, return current version
	if !hasAnyChanges {
		if currentTag != "" {
			return currentTag
		}
		// Generate today's date as default
		now := time.Now()
		return fmt.Sprintf("v%d.%02d.%02d", now.Year(), int(now.Month()), now.Day())
	}

	// Generate date-based version
	now := time.Now()
	baseVersion := fmt.Sprintf("v%d.%02d.%02d", now.Year(), int(now.Month()), now.Day())

	// Check if we already have a release today
	checkCmd := exec.Command("git", "tag", "-l", baseVersion+"*")
	checkCmd.Dir = rootDir
	checkOutput, _ := checkCmd.Output()

	if len(checkOutput) == 0 {
		// No release today yet
		return baseVersion
	}

	// Find the highest suffix for today
	existingTags := strings.Split(strings.TrimSpace(string(checkOutput)), "\n")
	maxSuffix := 0
	hasBasicVersion := false

	for _, tag := range existingTags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}

		if tag == baseVersion {
			// Found base version without suffix
			hasBasicVersion = true
		} else if strings.HasPrefix(tag, baseVersion+".") {
			// Extract numeric suffix
			suffix := strings.TrimPrefix(tag, baseVersion+".")
			var num int
			if _, err := fmt.Sscanf(suffix, "%d", &num); err == nil && num > maxSuffix {
				maxSuffix = num
			}
		}
	}

	// Return next available version
	if !hasBasicVersion && maxSuffix == 0 {
		return baseVersion
	}

	// If base version exists or we have numeric suffixes, increment
	return fmt.Sprintf("%s.%d", baseVersion, maxSuffix+1)
}

func createReleaseCommitMessage(versions map[string]string, hasChanges map[string]bool) string {
	// Create a sorted list of repos with changes
	var repos []string
	for repo := range versions {
		// Only include repos with changes
		if changes, ok := hasChanges[repo]; ok && !changes {
			continue
		}
		repos = append(repos, repo)
	}
	// Simple sort
	for i := 0; i < len(repos); i++ {
		for j := i + 1; j < len(repos); j++ {
			if repos[i] > repos[j] {
				repos[i], repos[j] = repos[j], repos[i]
			}
		}
	}

	// Build commit message
	var updates []string
	for _, repo := range repos {
		updates = append(updates, fmt.Sprintf("%s@%s", repo, versions[repo]))
	}

	return fmt.Sprintf("chore: release components (%s)", strings.Join(updates, ", "))
}

func checkForOutdatedDependencies(ctx context.Context, rootDir string, workspaces []string, logger *logrus.Logger) error {
	outdatedDeps := make(map[string]map[string]string) // workspace -> dep -> current version

	for _, ws := range workspaces {
		// Skip the root workspace
		if ws == rootDir {
			continue
		}

		wsName := filepath.Base(ws)
		goModPath := filepath.Join(ws, "go.mod")
		goModContent, err := os.ReadFile(goModPath)
		if err != nil {
			continue
		}

		// Parse current Grove dependencies
		lines := strings.Split(string(goModContent), "\n")
		inRequire := false

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "require (" {
				inRequire = true
				continue
			}
			if inRequire && line == ")" {
				break
			}

			if inRequire || strings.HasPrefix(line, "require ") {
				if strings.Contains(line, "github.com/mattsolo1/") {
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						dep := parts[0]
						if strings.HasPrefix(dep, "github.com/mattsolo1/") {
							currentVersion := parts[1]

							// Get latest version
							latestVersion, err := getLatestModuleVersion(dep)
							if err != nil {
								continue // Skip if we can't get latest version
							}

							// Check if outdated
							if currentVersion != latestVersion {
								if outdatedDeps[wsName] == nil {
									outdatedDeps[wsName] = make(map[string]string)
								}
								outdatedDeps[wsName][dep] = fmt.Sprintf("%s → %s", currentVersion, latestVersion)
							}
						}
					}
				}
			}
		}
	}

	// Display info about dependency updates that will occur
	if len(outdatedDeps) > 0 {
		displayInfo(theme.IconArchive + " This release will update dependencies:")
		for wsName, deps := range outdatedDeps {
			fmt.Printf("  %s %s:\n", theme.IconRepo, wsName)
			for dep, versions := range deps {
				depName := filepath.Base(dep) // Extract just the repo name
				fmt.Printf("    %s %s: %s\n", theme.IconBullet, depName, versions)
			}
		}
		fmt.Printf("\n%s Consider running `grove deps sync --commit --push` before releasing\n\n", theme.IconLightbulb)
	}

	return nil
}

// extractCurrentVersions parses go.mod content and returns a map of module -> version

// shouldWaitForModuleAvailability determines if a project needs module availability checking
// Template projects and other non-Go modules should skip this check
func shouldWaitForModuleAvailability(workspacePath string) (bool, error) {
	// Load grove.yml to determine project type
	groveConfigPath := filepath.Join(workspacePath, "grove.yml")
	cfg, err := config.Load(groveConfigPath)
	if err != nil {
		// If no grove.yml, assume it's a Go project for backward compatibility
		return true, nil
	}

	// Get project type from grove.yml
	var projectTypeStr string
	if err := cfg.UnmarshalExtension("type", &projectTypeStr); err != nil || projectTypeStr == "" {
		// Default to Go for backward compatibility
		return true, nil
	}

	projectType := project.Type(projectTypeStr)

	// Only Go modules need module availability checking
	// Template, Maturin, and Node projects are not published to Go module proxy
	switch projectType {
	case project.TypeGo:
		return true, nil
	case project.TypeTemplate, project.TypeMaturin, project.TypeNode:
		return false, nil
	default:
		// Unknown project type, default to checking for safety
		return true, nil
	}
}
