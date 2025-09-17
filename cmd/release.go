package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/command"
	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/depsgraph"
	"github.com/mattsolo1/grove-meta/pkg/gh"
	"github.com/mattsolo1/grove-meta/pkg/project"
	"github.com/mattsolo1/grove-meta/pkg/release"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
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
	releaseSyncDeps       bool
	releaseLLMChangelog   bool
	releaseInteractive    bool // New flag for interactive TUI mode
)

func init() {
	rootCmd.AddCommand(newReleaseCmd())
}

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Create a new release for the Grove ecosystem",
		Long: `Create a new release by automatically calculating version bumps for all submodules.

This command will:
1. Calculate the next version for each submodule based on its latest tag
2. Check that all repositories are clean (unless --force is used)
3. Display the proposed versions and ask for confirmation
4. Tag each submodule with its new version (triggers individual releases)
5. Update submodule references in the parent repository
6. Create a parent repository tag that documents the release
7. Push all tags to origin

By default, all submodules receive a patch version bump. Use flags to specify
major or minor bumps for specific repositories.

Examples:
  grove release                                    # Patch bump for all
  grove release --minor grove-core                 # Minor bump for grove-core, patch for others
  grove release --major grove-core --minor grove-meta  # Mixed bumps
  grove release --interactive                      # Launch interactive TUI for release planning`,
		Args: cobra.NoArgs,
		RunE: runRelease,
	}

	cmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "Print commands without executing them")
	cmd.Flags().BoolVar(&releaseForce, "force", false, "Skip clean workspace checks")
	cmd.Flags().BoolVar(&releaseForceIncrement, "force-increment", false, "Force version increment even if current commit has a tag")
	cmd.Flags().BoolVar(&releasePush, "push", false, "Push all repositories to remote before tagging")
	cmd.Flags().StringSliceVar(&releaseRepos, "repos", []string{}, "Only release specified repositories (e.g., grove-meta,grove-core)")
	cmd.Flags().BoolVar(&releaseWithDeps, "with-deps", false, "Include all dependencies of specified repositories in the release")
	cmd.Flags().BoolVar(&releaseSyncDeps, "sync-deps", false, "Sync Grove dependencies to latest versions between levels")
	cmd.Flags().StringSliceVar(&releaseMajor, "major", []string{}, "Repositories to receive major version bump")
	cmd.Flags().StringSliceVar(&releaseMinor, "minor", []string{}, "Repositories to receive minor version bump")
	cmd.Flags().StringSliceVar(&releasePatch, "patch", []string{}, "Repositories to receive patch version bump (default for all)")
	cmd.Flags().BoolVar(&releaseYes, "yes", false, "Skip interactive confirmation (for CI/CD)")
	cmd.Flags().BoolVar(&releaseSkipParent, "skip-parent", false, "Skip parent repository updates (submodules and tagging)")
	cmd.Flags().BoolVar(&releaseLLMChangelog, "llm-changelog", false, "Generate changelog using an LLM instead of conventional commits")
	cmd.Flags().BoolVar(&releaseInteractive, "interactive", false, "Launch interactive TUI for release planning and approval")

	// Add subcommands
	cmd.AddCommand(newReleaseChangelogCmd())
	cmd.AddCommand(newReleaseTuiCmd()) // Add TUI as a subcommand

	return cmd
}

func runRelease(cmd *cobra.Command, args []string) error {
	// Check if interactive mode is requested
	if releaseInteractive {
		ctx := context.Background()
		return runReleaseTUI(ctx)
	}
	
	// Otherwise, run the original release command
	ctx := context.Background()
	logger := cli.GetLogger(cmd)

	displayPhase("Preparing Release")

	// Find the root directory
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover all workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}
	displayInfo(fmt.Sprintf("Discovered workspaces: %d", len(workspaces)))

	// Build dependency graph with ALL workspaces to get complete dependency info
	displayProgress("Building dependency graph...")
	graph, err := depsgraph.BuildGraph(rootDir, workspaces)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}

	// Track which repos were auto-included due to dependencies
	var autoDependencies map[string]bool

	// If specific repos are requested with dependencies, expand the list
	if len(releaseRepos) > 0 && releaseWithDeps {
		expandedRepos, autoDeps := expandReposWithDependencies(releaseRepos, graph)
		autoDependencies = autoDeps

		// Log the expansion
		originalCount := len(releaseRepos)
		displayInfo(fmt.Sprintf("Expanded from %d to %d repositories", originalCount, len(expandedRepos)))
		if len(autoDeps) > 0 {
			displayInfo(fmt.Sprintf("Auto-including %d dependencies", len(autoDeps)))
			for dep := range autoDeps {
				logger.WithField("repo", dep).Info("Auto-including dependency")
			}
		}

		// Update releaseRepos with the expanded list
		releaseRepos = expandedRepos
	}

	// Calculate versions for all workspaces first to know which repos have changes
	versions, currentVersions, hasChanges, err := calculateNextVersions(ctx, rootDir, workspaces, releaseMajor, releaseMinor, releasePatch, logger)
	if err != nil {
		return fmt.Errorf("failed to calculate versions: %w", err)
	}

	// If using --with-deps, force include auto-dependencies even if they don't have changes
	if releaseWithDeps && autoDependencies != nil {
		for dep := range autoDependencies {
			if !hasChanges[dep] {
				hasChanges[dep] = true
				logger.WithField("repo", dep).Info("Force including dependency without changes")
			}
		}
	}

	// Create a map of nodes to release (only those with changes)
	nodesToRelease := make(map[string]bool)
	for repo, changes := range hasChanges {
		if changes {
			nodesToRelease[repo] = true
		}
	}

	// Get topologically sorted release order for only the repos being released
	releaseLevels, err := graph.TopologicalSortWithFilter(nodesToRelease)
	if err != nil {
		return fmt.Errorf("failed to sort dependencies: %w", err)
	}

	// Check if any repos have changes
	hasAnyChanges := false
	for _, changes := range hasChanges {
		if changes {
			hasAnyChanges = true
			break
		}
	}

	if !hasAnyChanges {
		logger.Info("No repositories have changes since their last release. Nothing to release.")
		return nil
	}

	// Determine parent repo version early so we can display it
	parentVersion := ""
	if !releaseSkipParent {
		parentVersion = determineParentVersion(rootDir, versions, hasChanges)
	}

	// Display proposed versions and get confirmation
	if !displayAndConfirmVersionsWithOrder(rootDir, versions, currentVersions, hasChanges, releaseLevels, graph, parentVersion, autoDependencies, logger) {
		logger.Info("Release cancelled by user")
		return nil
	}

	// Auto-commit grove-ecosystem changes if needed (only for repos being released)
	if err := autoCommitEcosystemChanges(ctx, rootDir, hasChanges, logger); err != nil {
		return fmt.Errorf("failed to auto-commit ecosystem changes: %w", err)
	}

	// Run pre-flight checks
	if err := runPreflightChecks(ctx, rootDir, parentVersion, workspaces, logger); err != nil {
		return err
	}

	// Check for outdated Grove dependencies and warn user
	if err := checkForOutdatedDependencies(ctx, rootDir, workspaces, logger); err != nil {
		displayWarning("Failed to check for outdated dependencies: " + err.Error())
	}

	// Push repositories to remote if requested
	if releasePush {
		if err := pushRepositories(ctx, rootDir, workspaces, hasChanges, logger); err != nil {
			return fmt.Errorf("failed to push repositories: %w", err)
		}
	}

	// Execute dependency-aware release orchestration
	if err := orchestrateRelease(ctx, rootDir, releaseLevels, versions, currentVersions, hasChanges, graph, logger, releaseLLMChangelog); err != nil {
		return fmt.Errorf("failed to orchestrate release: %w", err)
	}

	// Final phase: commit and tag ecosystem
	displaySection("🏁 Finalizing Ecosystem Release")

	if !releaseSkipParent {
		// Stage only the submodules that were released
		for repo := range hasChanges {
			if hasChanges[repo] {
				if err := executeGitCommand(ctx, rootDir, []string{"add", repo}, fmt.Sprintf("Stage %s", repo), logger); err != nil {
					return err
				}
			}
		}

		// Check if there are actually changes to commit after staging
		status, err := git.GetStatus(rootDir)
		if err != nil {
			return fmt.Errorf("failed to check git status: %w", err)
		}

		// Only commit if there are staged changes
		if status.IsDirty {
			// Commit the submodule updates
			commitMsg := createReleaseCommitMessage(versions, hasChanges)
			if err := executeGitCommand(ctx, rootDir, []string{"commit", "-m", commitMsg}, "Commit release", logger); err != nil {
				return err
			}
		} else {
			displayInfo("No changes to commit after staging submodules")
		}
	}

	// Count actually released modules
	releasedCount := 0
	for _, hasChanges := range hasChanges {
		if hasChanges {
			releasedCount++
		}
	}

	// Handle parent repository updates unless skipped
	var finalParentVersion string
	if !releaseSkipParent {
		// Recalculate parent version to handle same-day releases
		finalParentVersion = determineParentVersion(rootDir, versions, hasChanges)
		if finalParentVersion != parentVersion {
			displayInfo(fmt.Sprintf("Adjusted parent version: %s → %s (same-day release)", parentVersion, finalParentVersion))
		}

		// Tag the main repository
		if err := executeGitCommand(ctx, rootDir, []string{"tag", "-a", finalParentVersion, "-m", fmt.Sprintf("Release %s", finalParentVersion)}, "Tag main repository", logger); err != nil {
			return err
		}

		// Push the main repository changes
		if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", "main"}, "Push main branch", logger); err != nil {
			return err
		}

		// Push the main repository tag
		if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", finalParentVersion}, "Push release tag", logger); err != nil {
			return err
		}

		displayFinalSuccess(finalParentVersion, releasedCount)
	} else {
		displayInfo("Skipping parent repository updates (--skip-parent flag)")
		displaySuccess(fmt.Sprintf("Released %d module(s) successfully", releasedCount))
	}

	return nil
}

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
	displaySection("🔍 Pre-flight Checks")

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
		// Get relative path from root
		relPath, err := filepath.Rel(rootDir, wsPath)
		if err != nil {
			relPath = wsPath
		}

		repoName := filepath.Base(wsPath)

		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
			continue
		}

		wsStatus := workspaceStatus{Path: relPath}

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
	mainStatusStr := "✓ Clean"
	if mainStatus.IsDirty {
		mainStatusStr = "✗ Dirty"
	}
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
	tableRows = append(tableRows, []string{
		"grove-ecosystem",
		mainStatus.Branch,
		mainStatusStr,
		strings.Join(displayIssues, ", "),
	})

	// Workspaces
	hasIssues := len(mainIssues) > 0
	for _, ws := range workspaceStatuses {
		issues := []string{}
		statusStr := "✓ Clean"
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
					statusStr = "✗ Dirty"
				} else if allChanges == "CHANGELOG.md" {
					// Show it's dirty but with a note that it's just the changelog
					statusStr = "✗ Dirty"
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
		if statusStr == "✗ Dirty" && len(issues) == 0 {
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

func pushRepositories(ctx context.Context, rootDir string, workspaces []string, hasChanges map[string]bool, logger *logrus.Logger) error {
	displaySection("📤 Pushing Repositories")

	// Create a map for filtering repositories if specified
	repoFilter := make(map[string]bool)
	if len(releaseRepos) > 0 {
		for _, repo := range releaseRepos {
			repoFilter[repo] = true
		}
	}

	// First push the main repository
	displayProgress("Pushing main repository...")
	if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", "main"}, "Push main repository", logger); err != nil {
		return fmt.Errorf("failed to push main repository: %w", err)
	}

	// Process each workspace
	for _, wsPath := range workspaces {
		// Get relative path from root
		relPath, err := filepath.Rel(rootDir, wsPath)
		if err != nil {
			relPath = wsPath
		}

		repoName := filepath.Base(wsPath)

		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
			continue
		}

		// Push workspace
		displayProgress(fmt.Sprintf("Pushing %s...", relPath))

		if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "main"},
			fmt.Sprintf("Push %s", relPath), logger); err != nil {
			// Only warn on push failures, don't fail the entire process
			logger.WithFields(logrus.Fields{"path": relPath, "error": err}).Warn("Failed to push workspace")
		}
	}

	displaySuccess("All repositories pushed to remote")
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
	if releaseDryRun {
		logger.WithFields(logrus.Fields{
			"command": fmt.Sprintf("git %s", strings.Join(args, " ")),
			"dir":     dir,
		}).Info("[DRY RUN] Would execute")
		return nil
	}

	logger.WithField("command", fmt.Sprintf("git %s", strings.Join(args, " "))).Info(description)

	cmdBuilder := command.NewSafeBuilder()
	cmd, err := cmdBuilder.Build(ctx, "git", args...)
	if err != nil {
		return fmt.Errorf("failed to build git command: %w", err)
	}

	execCmd := cmd.Exec()
	execCmd.Dir = dir

	// Capture combined output to display git's actual error messages
	output, err := execCmd.CombinedOutput()

	// Always print the output if there's any (preserves existing behavior for successful commands)
	if len(output) > 0 {
		fmt.Print(string(output))
	}

	if err != nil {
		// The error message from git has already been printed above
		// Return a wrapped error for proper error handling
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

func calculateNextVersions(ctx context.Context, rootDir string, workspaces []string, major, minor, patch []string, logger *logrus.Logger) (map[string]string, map[string]string, map[string]bool, error) {
	versions := make(map[string]string)
	currentVersions := make(map[string]string)
	hasChanges := make(map[string]bool)

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
			hasChanges[repoName] = true // New repo always needs initial release
		} else {
			currentTag = strings.TrimSpace(string(tagOutput))
			currentVersions[repoName] = currentTag
			currentVersion, err = semver.NewVersion(currentTag)
			if err != nil {
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

			commitCount := strings.TrimSpace(string(countOutput))
			if commitCount == "0" && !releaseForceIncrement {
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
					hasChanges[repoName] = false
					continue
				}
			}

			// Either there are new commits, or force increment is enabled
			hasChanges[repoName] = true
		}

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

	return versions, currentVersions, hasChanges, nil
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

func orchestrateRelease(ctx context.Context, rootDir string, releaseLevels [][]string, versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool, graph *depsgraph.Graph, logger *logrus.Logger, useLLMChangelog bool) error {
	displaySection("🎯 Release Orchestration")

	// Process each level of dependencies
	for levelIndex, level := range releaseLevels {
		logger.WithField("level", levelIndex).Info("Processing release level")

		// Collect repositories that need releasing at this level
		var reposToRelease []string
		for _, repoName := range level {
			// Skip if no changes
			if changes, ok := hasChanges[repoName]; !ok || !changes {
				logger.WithField("repo", repoName).Info("Skipping (no changes)")
				continue
			}

			// Skip if no version
			version, ok := versions[repoName]
			if !ok {
				logger.WithField("repo", repoName).Warn("No version found, skipping")
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

			reposToRelease = append(reposToRelease, repoName)
		}

		if len(reposToRelease) == 0 {
			logger.WithField("level", levelIndex).Info("No repositories to release at this level")
			continue
		}

		// Process all repositories at this level in parallel
		if len(reposToRelease) > 1 {
			logger.WithFields(logrus.Fields{
				"level": levelIndex,
				"count": len(reposToRelease),
				"repos": strings.Join(reposToRelease, ", "),
			}).Info("Releasing repositories in parallel")
			displayInfo(fmt.Sprintf("🚀 Releasing %d repositories in parallel: %s", len(reposToRelease), strings.Join(reposToRelease, ", ")))
		} else {
			logger.WithFields(logrus.Fields{
				"level": levelIndex,
				"repo":  reposToRelease[0],
			}).Info("Releasing repository")
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

				logger.WithFields(logrus.Fields{
					"repo":    repo,
					"version": version,
				}).Info("Releasing module")

				// Update dependencies if this is not the first level (skip in dry-run mode)
				if levelIndex > 0 && !releaseDryRun {
					if err := updateDependencies(ctx, wsPath, versions, graph, logger); err != nil {
						errChan <- fmt.Errorf("failed to update dependencies for %s: %w", repo, err)
						return
					}
				}

				// Handle changelog - either use existing modifications or generate new
				if !releaseDryRun {
					// Check if CHANGELOG.md is already modified (from TUI workflow)
					// Use git diff to check if the file has changes
					changelogModified := false
					
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
								}
							}
						}
					} else {
						// No existing changelog modifications, generate new one
						displayInfo(fmt.Sprintf("Generating changelog for %s", repo))
						changelogCmdArgs := []string{"release", "changelog", wsPath, "--version", version}
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
										}
									}
								}
							}
						}
					}
				}

				// Tag the module
				if err := executeGitCommand(ctx, wsPath, []string{"tag", "-a", version, "-m", fmt.Sprintf("Release %s", version)},
					fmt.Sprintf("Tag %s", repo), logger); err != nil {
					errChan <- err
					return
				}

				// Push the tag
				if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", version},
					fmt.Sprintf("Push tag for %s", repo), logger); err != nil {
					errChan <- err
					return
				}

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

						logger.Infof("✅ CI release for %s@%s successful.", repo, version)
						displayInfo(fmt.Sprintf("✅ CI release for %s@%s successful.", repo, version))
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

		// After releasing this level, sync dependencies for the next levels if requested
		if releaseSyncDeps && levelIndex < len(releaseLevels)-1 && !releaseDryRun {
			logger.Info("Syncing Grove dependencies to latest versions before next level...")
			displayInfo("🔄 Syncing Grove dependencies to latest versions...")

			// Collect repositories in the next levels that need syncing
			var reposToSync []string
			for nextLevel := levelIndex + 1; nextLevel < len(releaseLevels); nextLevel++ {
				for _, repo := range releaseLevels[nextLevel] {
					// Only sync repos that will be released
					if changes, ok := hasChanges[repo]; ok && changes {
						reposToSync = append(reposToSync, repo)
					}
				}
			}

			if len(reposToSync) > 0 {
				// Run dependency sync for these repositories
				if err := syncDependenciesForRepos(ctx, rootDir, reposToSync, graph, logger); err != nil {
					logger.WithError(err).Warn("Failed to sync dependencies, continuing anyway")
					displayWarning("Failed to sync some dependencies: " + err.Error())
				} else {
					displaySuccess("Dependencies synced successfully")
				}
			}
		}
	}

	displaySuccess("All modules released successfully")
	return nil
}

func updateDependencies(ctx context.Context, modulePath string, releasedVersions map[string]string, graph *depsgraph.Graph, logger *logrus.Logger) error {
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

		// Check if this dependency has a new version
		newVersion, hasNewVersion := releasedVersions[depWorkspaceName]
		if !hasNewVersion {
			continue
		}

		logger.WithFields(logrus.Fields{
			"dep": dep.Name,
			"old": dep.Version,
			"new": newVersion,
		}).Info("Updating dependency")

		// Update to new version
		dep.Version = newVersion
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

				commitMsg := "chore(deps): bump dependencies"
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

		// Check if this dependency has a new version
		newVersion, hasNewVersion := releasedVersions[depName]
		if !hasNewVersion {
			continue
		}

		logger.WithFields(logrus.Fields{
			"dep": req.Mod.Path,
			"old": req.Mod.Version,
			"new": newVersion,
		}).Info("Updating dependency")

		// Update to new version
		cmd := exec.CommandContext(ctx, "go", "get", fmt.Sprintf("%s@%s", req.Mod.Path, newVersion))
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

		// Check for changes
		status, err := git.GetStatus(modulePath)
		if err != nil {
			return fmt.Errorf("failed to get git status: %w", err)
		}

		if status.IsDirty {
			// Commit the dependency updates
			if err := executeGitCommand(ctx, modulePath, []string{"add", "go.mod", "go.sum"},
				"Stage dependency updates", logger); err != nil {
				return err
			}

			commitMsg := "chore(deps): bump dependencies"
			if err := executeGitCommand(ctx, modulePath, []string{"commit", "-m", commitMsg},
				"Commit dependency updates", logger); err != nil {
				return err
			}

			// Push if requested
			if releasePush {
				if err := executeGitCommand(ctx, modulePath, []string{"push", "origin", "HEAD"},
					"Push dependency updates", logger); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func displayAndConfirmVersionsWithOrder(rootDir string, versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool, releaseLevels [][]string, graph *depsgraph.Graph, parentVersion string, autoDependencies map[string]bool, logger *logrus.Logger) bool {
	displaySection("📊 Proposed Versions")

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
	re := lipgloss.NewRenderer(os.Stdout)

	baseStyle := re.NewStyle().Padding(0, 1)
	headerStyle := baseStyle.Copy().Bold(true).Foreground(lipgloss.Color("255"))

	// Create the table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
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
	displaySection("📊 Proposed Versions")

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
	re := lipgloss.NewRenderer(os.Stdout)

	baseStyle := re.NewStyle().Padding(0, 1)
	headerStyle := baseStyle.Copy().Bold(true).Foreground(lipgloss.Color("255"))

	// Create the table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
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

	// Display warnings for outdated dependencies
	if len(outdatedDeps) > 0 {
		displayWarning("⚠️  Outdated Grove dependencies detected:")
		for wsName, deps := range outdatedDeps {
			fmt.Printf("  📦 %s:\n", wsName)
			for dep, versions := range deps {
				depName := filepath.Base(dep) // Extract just the repo name
				fmt.Printf("    • %s: %s\n", depName, versions)
			}
		}
		fmt.Printf("\n💡 Consider running `grove deps sync --commit --push` before releasing\n\n")
	}

	return nil
}

// extractCurrentVersions parses go.mod content and returns a map of module -> version
func extractCurrentVersions(goModContent string) map[string]string {
	versions := make(map[string]string)
	lines := strings.Split(goModContent, "\n")
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
			parts := strings.Fields(line)
			if len(parts) >= 2 && strings.HasPrefix(parts[0], "github.com/mattsolo1/") {
				versions[parts[0]] = parts[1]
			}
		}
	}

	return versions
}

func syncDependenciesForRepos(ctx context.Context, rootDir string, repos []string, graph *depsgraph.Graph, logger *logrus.Logger) error {
	// Map repo names to workspace paths
	repoPaths := make(map[string]string)
	for _, repo := range repos {
		if node, exists := graph.GetNode(repo); exists {
			repoPaths[repo] = node.Dir
		}
	}

	// Track all unique Grove dependencies that need updating
	uniqueDeps := make(map[string]bool)

	// Scan each repository for Grove dependencies
	for repo, wsPath := range repoPaths {
		goModPath := filepath.Join(wsPath, "go.mod")
		goModContent, err := os.ReadFile(goModPath)
		if err != nil {
			logger.WithError(err).Warnf("Failed to read go.mod for %s", repo)
			continue
		}

		// Parse dependencies
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
					if len(parts) >= 1 {
						dep := parts[0]
						if strings.HasPrefix(dep, "github.com/mattsolo1/") {
							uniqueDeps[dep] = true
						}
					}
				}
			}
		}
	}

	// Get latest versions for all dependencies
	depVersions := make(map[string]string)
	for dep := range uniqueDeps {
		version, err := getLatestModuleVersion(dep)
		if err != nil {
			logger.WithError(err).Warnf("Failed to get latest version for %s", dep)
			continue
		}
		depVersions[dep] = version
	}

	// Update each repository
	var updateErrors []error
	for repo, wsPath := range repoPaths {
		logger.WithField("repo", repo).Info("Syncing dependencies")

		// Track what actually changed for better commit messages
		versionChanges := make(map[string]string)

		// Get current versions before updating
		goModPath := filepath.Join(wsPath, "go.mod")
		beforeContent, _ := os.ReadFile(goModPath)
		currentVersions := extractCurrentVersions(string(beforeContent))

		// Update each dependency
		for dep, version := range depVersions {
			// Check if this update would actually change anything
			depName := filepath.Base(dep)
			if currentVersion, exists := currentVersions[dep]; exists && currentVersion != version {
				versionChanges[depName] = fmt.Sprintf("%s → %s", currentVersion, version)
			}

			cmd := exec.CommandContext(ctx, "go", "get", fmt.Sprintf("%s@%s", dep, version))
			cmd.Dir = wsPath
			cmd.Env = append(os.Environ(),
				"GOPRIVATE=github.com/mattsolo1/*",
				"GOPROXY=direct",
			)

			if output, err := cmd.CombinedOutput(); err != nil {
				logger.WithError(err).WithField("output", string(output)).
					Warnf("Failed to update %s in %s", dep, repo)
				updateErrors = append(updateErrors, fmt.Errorf("%s: %w", repo, err))
			}
		}

		// Run go mod tidy
		cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
		cmd.Dir = wsPath
		cmd.Env = append(os.Environ(),
			"GOPRIVATE=github.com/mattsolo1/*",
			"GOPROXY=direct",
		)

		if output, err := cmd.CombinedOutput(); err != nil {
			logger.WithError(err).WithField("output", string(output)).
				Warnf("go mod tidy failed for %s", repo)
			updateErrors = append(updateErrors, fmt.Errorf("%s: go mod tidy: %w", repo, err))
		}

		// Check for changes and commit
		status, err := git.GetStatus(wsPath)
		if err != nil {
			logger.WithError(err).Warnf("Failed to get git status for %s", repo)
			continue
		}

		if status.IsDirty {
			// Stage go.mod and go.sum
			if err := executeGitCommand(ctx, wsPath, []string{"add", "go.mod", "go.sum"},
				"Stage dependency sync", logger); err != nil {
				logger.WithError(err).Warnf("Failed to stage changes for %s", repo)
				continue
			}

			// Build detailed commit message
			commitMsg := "chore(deps): sync Grove dependencies to latest versions\n\n"
			if len(versionChanges) > 0 {
				for depName, change := range versionChanges {
					commitMsg += fmt.Sprintf("- %s: %s\n", depName, change)
				}
			}

			if err := executeGitCommand(ctx, wsPath, []string{"commit", "-m", commitMsg},
				"Commit dependency sync", logger); err != nil {
				logger.WithError(err).Warnf("Failed to commit changes for %s", repo)
				continue
			}

			// Push
			if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "HEAD:main"},
				"Push dependency sync", logger); err != nil {
				logger.WithError(err).Warnf("Failed to push changes for %s", repo)
				continue
			}

			logger.WithField("repo", repo).Info("Dependencies synced and pushed")
		}
	}

	if len(updateErrors) > 0 {
		return fmt.Errorf("some repositories failed to sync: %d errors", len(updateErrors))
	}

	return nil
}

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
