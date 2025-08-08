package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/command"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/depsgraph"
	"github.com/mattsolo1/grove-meta/pkg/release"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

var (
	releaseDryRun      bool
	releaseForce       bool
	releaseForceIncrement bool
	releasePush        bool
	releaseRepos       []string
	releaseMajor       []string
	releaseMinor       []string
	releasePatch       []string
	releaseYes         bool
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
  grove release --major grove-core --minor grove-meta  # Mixed bumps`,
		Args: cobra.NoArgs,
		RunE: runRelease,
	}

	cmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "Print commands without executing them")
	cmd.Flags().BoolVar(&releaseForce, "force", false, "Skip clean workspace checks")
	cmd.Flags().BoolVar(&releaseForceIncrement, "force-increment", false, "Force version increment even if current commit has a tag")
	cmd.Flags().BoolVar(&releasePush, "push", false, "Push all repositories to remote before tagging")
	cmd.Flags().StringSliceVar(&releaseRepos, "repos", []string{}, "Only release specified repositories (e.g., grove-meta,grove-core)")
	cmd.Flags().StringSliceVar(&releaseMajor, "major", []string{}, "Repositories to receive major version bump")
	cmd.Flags().StringSliceVar(&releaseMinor, "minor", []string{}, "Repositories to receive minor version bump")
	cmd.Flags().StringSliceVar(&releasePatch, "patch", []string{}, "Repositories to receive patch version bump (default for all)")
	cmd.Flags().BoolVar(&releaseYes, "yes", false, "Skip interactive confirmation (for CI/CD)")

	// Add subcommands
	cmd.AddCommand(newReleaseChangelogCmd())

	return cmd
}

func runRelease(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	logger := cli.GetLogger(cmd)

	logger.Info("Preparing release...")

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
	logger.WithField("workspaceCount", len(workspaces)).Info("Discovered workspaces")

	// Build dependency graph with ALL workspaces to get complete dependency info
	logger.Info("Building dependency graph...")
	graph, err := depsgraph.BuildGraph(rootDir, workspaces)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}

	// Get topologically sorted release order
	releaseLevels, err := graph.TopologicalSort()
	if err != nil {
		return fmt.Errorf("failed to sort dependencies: %w", err)
	}

	// Calculate versions for all submodules
	versions, currentVersions, hasChanges, err := calculateNextVersions(ctx, rootDir, releaseMajor, releaseMinor, releasePatch, logger)
	if err != nil {
		return fmt.Errorf("failed to calculate versions: %w", err)
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
	parentVersion := determineParentVersion(rootDir, versions, hasChanges)

	// Display proposed versions and get confirmation
	if !displayAndConfirmVersionsWithOrder(rootDir, versions, currentVersions, hasChanges, releaseLevels, graph, parentVersion, logger) {
		logger.Info("Release cancelled by user")
		return nil
	}

	// Auto-commit grove-ecosystem changes if needed (only for repos being released)
	if err := autoCommitEcosystemChanges(ctx, rootDir, hasChanges, logger); err != nil {
		return fmt.Errorf("failed to auto-commit ecosystem changes: %w", err)
	}

	// Run pre-flight checks
	if err := runPreflightChecks(ctx, rootDir, parentVersion, logger); err != nil {
		return err
	}

	// Push repositories to remote if requested
	if releasePush {
		if err := pushRepositories(ctx, rootDir, hasChanges, logger); err != nil {
			return fmt.Errorf("failed to push repositories: %w", err)
		}
	}

	// Execute dependency-aware release orchestration
	if err := orchestrateRelease(ctx, rootDir, releaseLevels, versions, hasChanges, graph, logger); err != nil {
		return fmt.Errorf("failed to orchestrate release: %w", err)
	}

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
		logger.Info("No changes to commit after staging submodules")
	}

	// Recalculate parent version to handle same-day releases
	finalParentVersion := determineParentVersion(rootDir, versions, hasChanges)
	if finalParentVersion != parentVersion {
		logger.WithFields(logrus.Fields{
			"original": parentVersion,
			"adjusted": finalParentVersion,
		}).Info("Adjusted parent version for same-day release")
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

	logger.Info("✅ Release successfully created")
	logger.Info("")
	logger.Info("GitHub Actions will now:")
	logger.Info("  - Build and release each tool independently in its own repository")
	logger.Info("")
	logger.Info("The new tag on grove-ecosystem (%s) now marks this coordinated release.", finalParentVersion)
	logger.Info("")
	logger.Info("Monitor the individual tool releases in their respective repositories.")

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
		logger.Info("[DRY RUN] Would auto-commit grove-ecosystem changes")
		return nil
	}

	logger.Info("Auto-committing grove-ecosystem changes...")

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
		logger.Info("No submodule updates needed for repos being released")
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
	
	logger.WithField("submodules", relevantSubmodules).Info("Successfully auto-committed submodule updates")

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
		Path       string
		Branch     string
		Dirty      bool
		AheadCount int
		Error      error
	}
	
	var submoduleStatuses []submoduleStatus
	
	// Create a map for filtering repositories if specified
	repoFilter := make(map[string]bool)
	if len(releaseRepos) > 0 {
		for _, repo := range releaseRepos {
			repoFilter[repo] = true
		}
	}
	
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
		
		repoName := filepath.Base(smPath)
		
		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
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
			smStatus.AheadCount = status.AheadCount
		}
		
		submoduleStatuses = append(submoduleStatuses, smStatus)
	}
	
	// Display status table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\nREPOSITORY\tBRANCH\tSTATUS\tISSUES")
	fmt.Fprintln(w, "----------\t------\t------\t------")
	
	// Main repository
	mainIssues := []string{}
	// Don't consider dirty status an issue since we auto-commit
	// but still show it in the display
	if mainStatus.Branch != "main" {
		mainIssues = append(mainIssues, "not on main branch")
	}
	if mainStatus.HasUpstream && mainStatus.AheadCount > 0 {
		// Only add as issue if we're not going to push
		if !releasePush {
			mainIssues = append(mainIssues, fmt.Sprintf("ahead of remote by %d commits", mainStatus.AheadCount))
		}
	}
	
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
	fmt.Fprintf(w, "grove-ecosystem\t%s\t%s\t%s\n", 
		mainStatus.Branch, 
		mainStatusStr,
		strings.Join(displayIssues, ", "))
	
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
			if sm.AheadCount > 0 {
				// Only add as issue if we're not going to push
				if !releasePush {
					issues = append(issues, fmt.Sprintf("ahead of remote by %d commits", sm.AheadCount))
				}
			}
		}
		
		if len(issues) > 0 {
			hasIssues = true
		}
		
		// Add ahead info even if not an issue
		displayIssues := issues
		if releasePush && sm.AheadCount > 0 && len(issues) == 0 {
			displayIssues = []string{fmt.Sprintf("ahead of remote by %d commits (will push)", sm.AheadCount)}
		}
		
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", 
			sm.Path,
			branch,
			statusStr,
			strings.Join(displayIssues, ", "))
	}
	
	w.Flush()
	fmt.Println()
	
	// Check if the parent version tag already exists
	checkCmd := exec.Command("git", "tag", "-l", version)
	checkOutput, _ := checkCmd.Output()
	if len(checkOutput) > 0 {
		logger.WithField("version", version).Error("Parent repository tag already exists")
		hasIssues = true
	}
	
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

func pushRepositories(ctx context.Context, rootDir string, hasChanges map[string]bool, logger *logrus.Logger) error {
	logger.Info("Pushing repositories to remote...")
	
	// Create a map for filtering repositories if specified
	repoFilter := make(map[string]bool)
	if len(releaseRepos) > 0 {
		for _, repo := range releaseRepos {
			repoFilter[repo] = true
		}
	}
	
	// First push the main repository
	logger.Info("Pushing main repository")
	if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", "main"}, "Push main repository", logger); err != nil {
		return fmt.Errorf("failed to push main repository: %w", err)
	}
	
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
	
	// Process each submodule
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		
		// Parse submodule path from status output
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		smPath := parts[1]
		repoName := filepath.Base(smPath)
		
		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
			continue
		}
		
		// Push submodule
		smFullPath := filepath.Join(rootDir, smPath)
		logger.WithField("path", smPath).Info("Pushing submodule")
		
		if err := executeGitCommand(ctx, smFullPath, []string{"push", "origin", "main"}, 
			fmt.Sprintf("Push %s", smPath), logger); err != nil {
			// Only warn on push failures, don't fail the entire process
			logger.WithFields(logrus.Fields{"path": smPath, "error": err}).Warn("Failed to push submodule")
		}
	}
	
	logger.Info("All repositories pushed to remote")
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
			"dir": dir,
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

func calculateNextVersions(ctx context.Context, rootDir string, major, minor, patch []string, logger *logrus.Logger) (map[string]string, map[string]string, map[string]bool, error) {
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
	
	// Get list of submodules
	cmd := command.NewSafeBuilder()
	listCmd, err := cmd.Build(ctx, "git", "submodule", "status")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
	}
	
	execCmd := listCmd.Exec()
	execCmd.Dir = rootDir
	output, err := execCmd.Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list submodules: %w", err)
	}
	
	// Process each submodule
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		
		// Parse submodule path
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		smPath := parts[1]
		repoName := filepath.Base(smPath)
		
		// Skip if not in the filter list (when filter is specified)
		if len(repoFilter) > 0 && !repoFilter[repoName] {
			continue
		}
		
		smFullPath := filepath.Join(rootDir, smPath)
		
		// Get latest tag
		tagCmd, err := cmd.Build(ctx, "git", "describe", "--tags", "--abbrev=0")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
		}
		
		execCmd := tagCmd.Exec()
		execCmd.Dir = smFullPath
		tagOutput, err := execCmd.Output()
		
		var currentVersion *semver.Version
		var currentTag string
		hasTag := err == nil
		
		if !hasTag {
			// No tags found, start with v0.1.0
			currentVersion = semver.MustParse("0.0.0")
			currentVersions[repoName] = "v0.0.0"
			logger.WithFields(logrus.Fields{"repo": repoName, "default": "v0.0.0"}).Info("No tags found, using default")
			hasChanges[repoName] = true  // New repo always needs initial release
		} else {
			currentTag = strings.TrimSpace(string(tagOutput))
			currentVersions[repoName] = currentTag
			currentVersion, err = semver.NewVersion(currentTag)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to parse version %s for %s: %w", currentTag, repoName, err)
			}
			
			// Check if there are commits since the last tag
			commitCountCmd, err := cmd.Build(ctx, "git", "rev-list", "--count", currentTag+"..HEAD")
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
			}
			
			execCmd := commitCountCmd.Exec()
			execCmd.Dir = smFullPath
			countOutput, err := execCmd.Output()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to count commits for %s: %w", repoName, err)
			}
			
			commitCount := strings.TrimSpace(string(countOutput))
			if commitCount == "0" && !releaseForceIncrement {
				// Check if current commit already has the tag
				tagCheckCmd, err := cmd.Build(ctx, "git", "describe", "--exact-match", "--tags", "HEAD")
				if err != nil {
					return nil, nil, nil, fmt.Errorf("failed to build git command: %w", err)
				}
				
				execCmd := tagCheckCmd.Exec()
				execCmd.Dir = smFullPath
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

func orchestrateRelease(ctx context.Context, rootDir string, releaseLevels [][]string, versions map[string]string, hasChanges map[string]bool, graph *depsgraph.Graph, logger *logrus.Logger) error {
	logger.Info("Starting dependency-aware release orchestration...")
	
	// Process each level of dependencies
	for levelIndex, level := range releaseLevels {
		logger.WithField("level", levelIndex).Info("Processing release level")
		
		// Process all modules in this level
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
			
			smPath := repoName
			smFullPath := filepath.Join(rootDir, smPath)
			
			logger.WithFields(logrus.Fields{
				"repo": repoName,
				"version": version,
			}).Info("Releasing module")
			
			// Update dependencies if this is not the first level (skip in dry-run mode)
			if levelIndex > 0 && !releaseDryRun {
				if err := updateGoDependencies(ctx, smFullPath, versions, graph, logger); err != nil {
					return fmt.Errorf("failed to update dependencies for %s: %w", repoName, err)
				}
			}
			
			// Generate and commit changelog
			if !releaseDryRun {
				changelogCmd := exec.CommandContext(ctx, "grove", "release", "changelog", smFullPath, "--version", version)
				if err := changelogCmd.Run(); err != nil {
					// Log a warning but don't fail the release if changelog fails
					logger.WithError(err).Warnf("Failed to generate changelog for %s", repoName)
				} else {
					// Commit the changelog if it was modified
					status, _ := git.GetStatus(smFullPath)
					if status.IsDirty {
						logger.Infof("Committing CHANGELOG.md for %s", repoName)
						if err := executeGitCommand(ctx, smFullPath, []string{"add", "CHANGELOG.md"}, "Stage changelog", logger); err != nil {
							logger.WithError(err).Warnf("Failed to stage changelog for %s", repoName)
						} else {
							commitMsg := fmt.Sprintf("docs(changelog): update CHANGELOG.md for %s", version)
							if err := executeGitCommand(ctx, smFullPath, []string{"commit", "-m", commitMsg}, "Commit changelog", logger); err != nil {
								logger.WithError(err).Warnf("Failed to commit changelog for %s", repoName)
							}
						}
					}
				}
			}
			
			// Tag the module
			if err := executeGitCommand(ctx, smFullPath, []string{"tag", "-a", version, "-m", fmt.Sprintf("Release %s", version)}, 
				fmt.Sprintf("Tag %s", repoName), logger); err != nil {
				return err
			}
			
			// Push the tag
			if err := executeGitCommand(ctx, smFullPath, []string{"push", "origin", version}, 
				fmt.Sprintf("Push tag for %s", repoName), logger); err != nil {
				return err
			}
			
			// Get module path for waiting
			node, exists := graph.GetNode(repoName)
			if !exists {
				return fmt.Errorf("node not found in graph: %s", repoName)
			}
			
			// Wait for module to be available (skip in dry-run mode)
			if !releaseDryRun {
				logger.WithFields(logrus.Fields{
					"module": node.Path,
					"version": version,
				}).Info("Waiting for module availability...")
				
				if err := release.WaitForModuleAvailability(ctx, node.Path, version); err != nil {
					return fmt.Errorf("failed waiting for %s@%s: %w", node.Path, version, err)
				}
				
				logger.WithField("repo", repoName).Info("Module successfully released and available")
			}
		}
	}
	
	logger.Info("All modules released successfully")
	return nil
}

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

func displayAndConfirmVersionsWithOrder(rootDir string, versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool, releaseLevels [][]string, graph *depsgraph.Graph, parentVersion string, logger *logrus.Logger) bool {
	fmt.Println("\nProposed versions:")
	fmt.Println("==================")
	
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
	
	// Define styles
	highlightStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00ff00"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	
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
		styledParentVersion := highlightStyle.Render(displayParentVersion)
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
		// Pre-style the proposed version
		styledProposed := highlightStyle.Render(proposed)
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
			dimStyle.Render(repo), 
			dimStyle.Render(current), 
			dimStyle.Render(proposed), 
			dimStyle.Render("-"),
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
	
	// Display release order by dependency level
	fmt.Println("\nRelease Order (by dependency level):")
	fmt.Println("====================================")
	
	// Show dependency levels
	if len(releaseLevels) > 0 {
		levelCount := 0
		for levelIdx, level := range releaseLevels {
			// Check if this level has any repos with changes
			hasReposInLevel := false
			var reposInLevel []string
			for _, repo := range level {
				// Check if this repo is in our release set (has a version)
				if _, hasVersion := versions[repo]; hasVersion && hasChanges[repo] {
					hasReposInLevel = true
					reposInLevel = append(reposInLevel, repo)
				}
			}
			
			if hasReposInLevel {
				levelCount++
				fmt.Printf("\nLevel %d (can release in parallel):\n", levelCount)
				for _, repo := range reposInLevel {
					current := currentVersions[repo]
					if current == "" {
						current = "-"
					}
					proposed := versions[repo]
					increment := getVersionIncrement(current, proposed)
					
					// Show dependencies if not first level
					deps := graph.GetDependencies(repo)
					depStr := ""
					if len(deps) > 0 && levelIdx > 0 {
						var depNames []string
						for _, dep := range deps {
							// Find the repo name for this dependency
							for name, node := range graph.GetAllNodes() {
								if node.Path == dep {
									depNames = append(depNames, name)
									break
								}
							}
						}
						if len(depNames) > 0 {
							depStr = fmt.Sprintf(" (depends on: %s)", strings.Join(depNames, ", "))
						}
					}
					
					fmt.Printf("  - %s: %s → %s (%s)%s\n", repo, current, proposed, increment, depStr)
				}
			}
		}
		
		// If only one level, all repos are independent
		if levelCount == 1 {
			fmt.Println("\nNote: All repositories are independent and will be released in parallel.")
		} else if levelCount == 0 {
			fmt.Println("\nNo repositories with changes found in release plan.")
		}
	}
	
	fmt.Printf("\n%d repositories will be released.\n", changeCount)
	
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
	fmt.Println("\nProposed versions:")
	fmt.Println("==================")
	
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
	
	// Define styles
	highlightStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00ff00"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	
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
		styledProposed := highlightStyle.Render(proposed)
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
			dimStyle.Render(repo), 
			dimStyle.Render(current), 
			dimStyle.Render(proposed), 
			dimStyle.Render("-"),
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