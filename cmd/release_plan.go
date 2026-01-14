package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/git"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/depsgraph"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/mattsolo1/grove-meta/pkg/release"
)

// runReleasePlan generates a release plan without executing it
func runReleasePlan(ctx context.Context, isRC bool) (*release.ReleasePlan, error) {
	// Create logger directly with proper name
	logger := grovelogging.NewLogger("grove-meta").Logger

	displayPhase("Preparing Release Plan")

	// Find the root directory
	rootDir, err := workspace.FindEcosystemRoot("")
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover all workspaces
	projects, err := discovery.DiscoverProjects()
	if err != nil {
		return nil, fmt.Errorf("failed to discover workspaces: %w", err)
	}
	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}
	displayInfo(fmt.Sprintf("Discovered workspaces: %d", len(workspaces)))

	// For RC releases, checkout all workspaces to rc-nightly
	if isRC {
		displayProgress("Checking out workspaces to rc-nightly...")
		if err := checkoutRCNightly(ctx, projects); err != nil {
			return nil, fmt.Errorf("failed to checkout rc-nightly: %w", err)
		}
	}

	// Build dependency graph with ALL workspaces
	displayProgress("Building dependency graph...")
	graph, err := depsgraph.BuildGraph(rootDir, workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to build dependency graph: %w", err)
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

	// Calculate versions for all workspaces
	versions, currentVersions, commitsSinceTag, err := calculateNextVersions(ctx, rootDir, workspaces, releaseMajor, releaseMinor, releasePatch, isRC, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate versions: %w", err)
	}

	// Derive hasChanges map from commitsSinceTag
	hasChanges := make(map[string]bool)
	for repo, count := range commitsSinceTag {
		hasChanges[repo] = count > 0
	}

	// If using --with-deps, force include auto-dependencies even if they don't have changes
	if releaseWithDeps && autoDependencies != nil {
		for dep := range autoDependencies {
			if !hasChanges[dep] {
				hasChanges[dep] = true
				commitsSinceTag[dep] = 0  // Mark as included but no commits
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

	// Get topologically sorted release order
	releaseLevels, err := graph.TopologicalSortWithFilter(nodesToRelease)
	if err != nil {
		return nil, fmt.Errorf("failed to sort dependencies: %w", err)
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
		return nil, fmt.Errorf("no repositories have changes")
	}

	// Determine parent repo version
	parentVersion := ""
	parentCurrentVersion := ""
	if !releaseSkipParent {
		parentVersion = determineParentVersion(rootDir, versions, hasChanges)
		// Get current parent version
		parentCurrentVersion = getCurrentParentVersion(rootDir)
	}

	// Create release plan
	plan := &release.ReleasePlan{
		CreatedAt:            time.Now(),
		Repos:                make(map[string]*release.RepoReleasePlan),
		ReleaseLevels:        releaseLevels,
		RootDir:              rootDir,
		ParentVersion:        parentVersion,
		ParentCurrentVersion: parentCurrentVersion,
	}

	// Set plan type
	if isRC {
		plan.Type = "rc"
	} else {
		plan.Type = "full"
	}

	// Create staging directory
	stagingDir, err := getStagingDirPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get staging directory: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Process ALL repositories (including those without changes for display)
	for repo, currentVersion := range currentVersions {
		nextVersion := versions[repo]
		hasRepoChanges := hasChanges[repo]

		// Get repository path
		node, ok := graph.GetNode(repo)
		if !ok {
			logger.WithField("repo", repo).Warn("Node not found in graph, skipping")
			continue
		}
		wsPath := node.Dir

		// Generate LLM changelog and get version suggestion
		var suggestedBump, suggestionReasoning string
		changelogContent := ""

		// Only generate changelog for repos with changes, and not for RC releases
		if hasRepoChanges && releaseLLMChangelog && !isRC {
			displayInfo(fmt.Sprintf("Generating LLM changelog for %s...", repo))

			// Get commit range
			lastTag, err := getLastTag(wsPath)
			if err != nil {
				logger.WithField("repo", repo).Warn("Could not get last tag, analyzing all commits")
				lastTag = ""
			}

			commitRange := "HEAD"
			if lastTag != "" {
				commitRange = fmt.Sprintf("%s..HEAD", lastTag)
			}

			// Gather git context with commit hashes
			// Format includes both full and short hash for easy reference
			logCmd := exec.Command("git", "log", commitRange, "--pretty=format:commit %H (%h)%nAuthor: %an <%ae>%nDate: %ad%nCommit: %cn <%ce>%nCommitDate: %cd%n%n    %s%n%n%b%n")
			logCmd.Dir = wsPath
			logOutput, err := logCmd.CombinedOutput()
			if err != nil {
				logger.WithField("repo", repo).WithError(err).Warn("Failed to get git log")
			}

			diffCmd := exec.Command("git", "diff", "--stat", commitRange)
			diffCmd.Dir = wsPath
			diffOutput, err := diffCmd.CombinedOutput()
			if err != nil {
				logger.WithField("repo", repo).WithError(err).Warn("Failed to get git diff")
			}

			gitContext := fmt.Sprintf("GIT LOG:\n%s\n\nGIT DIFF STAT:\n%s", string(logOutput), string(diffOutput))

			// Generate changelog with LLM
			result, err := generateChangelogWithLLM(gitContext, nextVersion, wsPath)
			if err != nil {
				logger.WithField("repo", repo).WithError(err).Warn("Failed to generate LLM changelog")
				// Fall back to conventional commits
				suggestedBump = "patch"
				suggestionReasoning = "Failed to get LLM suggestion, defaulting to patch"
			} else {
				suggestedBump = result.Suggestion
				suggestionReasoning = result.Justification
				changelogContent = result.Changelog
			}
		} else if hasRepoChanges {
			// Use conventional commits analysis for repos with changes
			lastTag, _ := getLastTag(wsPath)
			suggestedBump = determineVersionBumpFromCommits(wsPath, lastTag)
			suggestionReasoning = "Based on conventional commit analysis"
		} else {
			// No changes - keep current version
			suggestedBump = "-"
			suggestionReasoning = "No changes since last release"
		}

		// If no bump was determined, default to patch
		if suggestedBump == "" {
			suggestedBump = "patch"
		}

		// Check if user has specified a bump for this repo (only for repos with changes)
		selectedBump := suggestedBump
		if hasRepoChanges {
			if contains(releaseMajor, repo) {
				selectedBump = "major"
			} else if contains(releaseMinor, repo) {
				selectedBump = "minor"
			} else if contains(releasePatch, repo) {
				selectedBump = "patch"
			}
		}

		// Save changelog to staging
		changelogPath := ""
		if changelogContent != "" {
			changelogPath = filepath.Join(stagingDir, repo, "CHANGELOG.md")
			if err := os.MkdirAll(filepath.Dir(changelogPath), 0755); err != nil {
				logger.WithField("repo", repo).WithError(err).Warn("Failed to create changelog directory")
			} else {
				if err := os.WriteFile(changelogPath, []byte(changelogContent), 0644); err != nil {
					logger.WithField("repo", repo).WithError(err).Warn("Failed to write staged changelog")
				}
			}
		}

		// Set default status
		status := "Pending Review"
		if !hasRepoChanges {
			status = "-" // No changes, no review needed
		}
		
		// For repos without changes, nextVersion should be same as current
		if !hasRepoChanges {
			nextVersion = currentVersion
		}

		// Get Git status for the repository
		gitStatus, err := git.GetStatus(wsPath)
		if err != nil {
			logger.WithField("repo", repo).WithError(err).Warn("Failed to get git status")
			// Create empty status if error
			gitStatus = &git.StatusInfo{}
		}

		// Add repo to plan with Git status info
		plan.Repos[repo] = &release.RepoReleasePlan{
			CurrentVersion:      currentVersion,
			SuggestedBump:       suggestedBump,
			SuggestionReasoning: suggestionReasoning,
			SelectedBump:        selectedBump,
			NextVersion:         nextVersion,
			ChangelogPath:       changelogPath,
			Status:              status,
			Selected:            hasRepoChanges, // Auto-select repos with changes
			// Git status fields
			Branch:              gitStatus.Branch,
			IsDirty:             gitStatus.IsDirty,
			HasUpstream:         gitStatus.HasUpstream,
			AheadCount:          gitStatus.AheadCount,
			BehindCount:         gitStatus.BehindCount,
			ModifiedCount:       gitStatus.ModifiedCount,
			StagedCount:         gitStatus.StagedCount,
			UntrackedCount:      gitStatus.UntrackedCount,
			CommitsSinceLastTag: commitsSinceTag[repo],
		}
	}

	// Save the plan
	if err := release.SavePlan(plan); err != nil {
		return nil, fmt.Errorf("failed to save release plan: %w", err)
	}

	displaySuccess("Release plan generated successfully")
	return plan, nil
}

// Helper function to get staging directory path
func getStagingDirPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grove", "release_staging"), nil
}

// Helper function to get current parent version
func getCurrentParentVersion(rootDir string) string {
	// Try to get the latest tag for the parent repository
	cmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
	cmd.Dir = rootDir
	output, err := cmd.Output()
	if err != nil {
		// No tags found
		return ""
	}
	return strings.TrimSpace(string(output))
}

// Helper function to determine version bump from conventional commits
func determineVersionBumpFromCommits(repoPath, lastTag string) string {
	// Simple implementation - can be enhanced
	commitRange := "HEAD"
	if lastTag != "" {
		commitRange = fmt.Sprintf("%s..HEAD", lastTag)
	}

	logCmd := exec.Command("git", "log", commitRange, "--pretty=%s")
	logCmd.Dir = repoPath
	output, err := logCmd.Output()
	if err != nil {
		return "patch"
	}

	commits := strings.Split(string(output), "\n")
	hasFeat := false
	for _, commit := range commits {
		if strings.Contains(commit, "BREAKING") || strings.Contains(commit, "!:") {
			return "major"
		}
		if strings.HasPrefix(commit, "feat:") || strings.HasPrefix(commit, "feat(") {
			hasFeat = true
		}
	}

	if hasFeat {
		return "minor"
	}
	return "patch"
}

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// runReleaseApply executes a previously generated release plan
func runReleaseApply(ctx context.Context) error {
	// Create logger directly with proper name
	logger := grovelogging.NewLogger("grove-meta").Logger

	displayPhase("Applying Release Plan")

	// Load the release plan
	plan, err := release.LoadPlan()
	if err != nil {
		return fmt.Errorf("failed to load release plan: %w", err)
	}

	// Reconstruct the dependency graph
	projects, err := discovery.DiscoverProjects()
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}
	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}

	graph, err := depsgraph.BuildGraph(plan.RootDir, workspaces)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}

	// Prepare version and hasChanges maps from plan
	versions := make(map[string]string)
	currentVersions := make(map[string]string)
	hasChanges := make(map[string]bool)

	// Only include selected repos in the release
	var selectedRepos []string
	for repo, repoPlan := range plan.Repos {
		if repoPlan.Selected {
			versions[repo] = repoPlan.NextVersion
			currentVersions[repo] = repoPlan.CurrentVersion
			hasChanges[repo] = true
			selectedRepos = append(selectedRepos, repo)
		} else {
			// Keep current version for unselected repos
			currentVersions[repo] = repoPlan.CurrentVersion
		}
	}
	
	// Display selected repos summary
	if len(selectedRepos) > 0 {
		displayInfo(fmt.Sprintf("%s Releasing %d selected repositories: %s", theme.IconArchive, len(selectedRepos), strings.Join(selectedRepos, ", ")))
	} else {
		return fmt.Errorf("no repositories selected for release")
	}

	// Auto-commit grove-ecosystem changes if needed - DISABLED to avoid submodule conflicts
	// if err := autoCommitEcosystemChanges(ctx, plan.RootDir, hasChanges, logger); err != nil {
	//	return fmt.Errorf("failed to auto-commit ecosystem changes: %w", err)
	// }

	// Sync dependencies if requested
	if releaseSyncDeps {
		displaySection(theme.IconSync + " Syncing Dependencies")
		displayInfo("Updating grove dependencies to latest versions...")
		if err := runDepsSync(true, true); err != nil {
			return fmt.Errorf("failed to sync dependencies: %w", err)
		}
		displaySuccess("Dependencies synced successfully")
	}

	// Run pre-flight checks (only for selected repos)
	parentVersion := determineParentVersion(plan.RootDir, versions, hasChanges)
	selectedWorkspaces := make([]string, 0, len(selectedRepos))
	for _, repo := range selectedRepos {
		selectedWorkspaces = append(selectedWorkspaces, filepath.Join(plan.RootDir, repo))
	}
	if err := runPreflightChecks(ctx, plan.RootDir, parentVersion, selectedWorkspaces, logger); err != nil {
		return err
	}

	// Check for outdated Grove dependencies
	if err := checkForOutdatedDependencies(ctx, plan.RootDir, workspaces, logger); err != nil {
		displayWarning("Failed to check for outdated dependencies: " + err.Error())
	}


	// Note: Changelogs are already written to repositories by the 'w' command in the TUI
	// The orchestrateRelease function will detect and commit these existing changelogs

	// Execute dependency-aware release orchestration
	if err := orchestrateRelease(ctx, plan.RootDir, plan.ReleaseLevels, versions, currentVersions, hasChanges, graph, logger, false, plan); err != nil {
		return fmt.Errorf("failed to orchestrate release: %w", err)
	}

	// Final phase: commit and tag ecosystem
	displaySection(theme.IconStatusCompleted + " Finalizing Ecosystem Release")

	if !releaseSkipParent {
		// Stage only the submodules that were released
		for repo := range hasChanges {
			if hasChanges[repo] {
				if err := executeGitCommand(ctx, plan.RootDir, []string{"add", repo}, fmt.Sprintf("Stage %s", repo), logger); err != nil {
					return err
				}
			}
		}

		// Check if there are actually changes to commit
		status, err := git.GetStatus(plan.RootDir)
		if err != nil {
			return fmt.Errorf("failed to check git status: %w", err)
		}

		// Only commit if there are staged changes
		if status.IsDirty {
			commitMsg := createReleaseCommitMessage(versions, hasChanges)
			if err := executeGitCommand(ctx, plan.RootDir, []string{"commit", "-m", commitMsg}, "Commit release", logger); err != nil {
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

	// Parent repository (grove-ecosystem) updates are currently disabled
	// Submodule releases are independent - the parent repo is updated manually
	displayInfo("Parent repo (grove-ecosystem) not auto-updated - commit submodule changes manually if needed")
	displaySuccess(fmt.Sprintf("Released %d module(s) successfully", releasedCount))

	// Clear the plan after successful completion (but not in dry-run mode)
	if !releaseDryRun {
		if err := release.ClearPlan(); err != nil {
			logger.WithError(err).Warn("Failed to clear release plan")
		}
	}

	return nil
}

// checkoutRCNightly checks out all workspaces to rc-nightly branch
func checkoutRCNightly(ctx context.Context, projects []*workspace.WorkspaceNode) error {
	errorCount := 0

	for _, project := range projects {
		wsPath := project.Path
		repoName := filepath.Base(wsPath)

		// First, ensure we're on main to check its status
		checkoutMainCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "checkout", "main")
		if output, err := checkoutMainCmd.CombinedOutput(); err != nil {
			displayError(fmt.Sprintf("Failed to checkout main for %s: %s", repoName, string(output)))
			errorCount++
			continue
		}

		// Check if main has unpushed commits
		statusCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "status", "--porcelain", "--branch")
		statusOutput, err := statusCmd.Output()
		if err != nil {
			displayError(fmt.Sprintf("Failed to get git status for %s: %v", repoName, err))
			errorCount++
			continue
		}

		statusStr := string(statusOutput)
		if strings.Contains(statusStr, "[ahead") {
			displayError(fmt.Sprintf("%s: main has unpushed commits - please push before running RC release", repoName))
			errorCount++
			continue
		}

		// Fetch latest from origin to ensure main is up-to-date
		fetchCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "fetch", "origin", "main")
		if output, err := fetchCmd.CombinedOutput(); err != nil {
			displayError(fmt.Sprintf("Failed to fetch origin/main for %s: %s", repoName, string(output)))
			errorCount++
			continue
		}

		// Check if main is behind origin/main
		behindCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "rev-list", "--count", "main..origin/main")
		behindOutput, err := behindCmd.Output()
		if err == nil {
			behindCount := strings.TrimSpace(string(behindOutput))
			if behindCount != "0" {
				displayError(fmt.Sprintf("%s: main is behind origin/main by %s commits - please pull first", repoName, behindCount))
				errorCount++
				continue
			}
		}

		// Check if rc-nightly branch exists
		branchCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "rev-parse", "--verify", "rc-nightly")
		branchExists := branchCmd.Run() == nil

		if !branchExists {
			// Create rc-nightly from main
			createCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "checkout", "-b", "rc-nightly", "main")
			if output, err := createCmd.CombinedOutput(); err != nil {
				displayError(fmt.Sprintf("Failed to create rc-nightly for %s: %s", repoName, string(output)))
				errorCount++
				continue
			}
		} else {
			// Checkout existing rc-nightly
			checkoutCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "checkout", "rc-nightly")
			if output, err := checkoutCmd.CombinedOutput(); err != nil {
				displayError(fmt.Sprintf("Failed to checkout rc-nightly for %s: %s", repoName, string(output)))
				errorCount++
				continue
			}

			// Rebase on main to get latest changes
			rebaseCmd := exec.CommandContext(ctx, "git", "-C", wsPath, "rebase", "main")
			if output, err := rebaseCmd.CombinedOutput(); err != nil {
				displayWarning(fmt.Sprintf("%s: rebase failed (may need manual resolution): %s", repoName, string(output)))
				// Don't increment errorCount - this is not fatal, user can resolve manually
			}
		}
	}

	if errorCount > 0 {
		return fmt.Errorf("failed to checkout %d workspaces", errorCount)
	}

	displaySuccess("All workspaces checked out to rc-nightly")
	return nil
}