package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/depsgraph"
	"github.com/mattsolo1/grove-meta/pkg/release"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// runReleasePlan generates a release plan without executing it
func runReleasePlan(ctx context.Context) (*release.ReleasePlan, error) {
	// Create a temporary command to get logger
	cmd := &cobra.Command{}
	var logger *logrus.Logger = cli.GetLogger(cmd)

	displayPhase("Preparing Release Plan")

	// Find the root directory
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover all workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to discover workspaces: %w", err)
	}
	displayInfo(fmt.Sprintf("Discovered workspaces: %d", len(workspaces)))

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
	versions, currentVersions, hasChanges, err := calculateNextVersions(ctx, rootDir, workspaces, releaseMajor, releaseMinor, releasePatch, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate versions: %w", err)
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

		// Only generate changelog for repos with changes
		if hasRepoChanges && releaseLLMChangelog {
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

		// Add repo to plan
		plan.Repos[repo] = &release.RepoReleasePlan{
			CurrentVersion:      currentVersion,
			SuggestedBump:       suggestedBump,
			SuggestionReasoning: suggestionReasoning,
			SelectedBump:        selectedBump,
			NextVersion:         nextVersion,
			ChangelogPath:       changelogPath,
			Status:              status,
			Selected:            hasRepoChanges, // Auto-select repos with changes
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
	// Create a temporary command to get logger
	cmd := &cobra.Command{}
	var logger *logrus.Logger = cli.GetLogger(cmd)

	displayPhase("Applying Release Plan")

	// Load the release plan
	plan, err := release.LoadPlan()
	if err != nil {
		return fmt.Errorf("failed to load release plan: %w", err)
	}

	// Reconstruct the dependency graph
	workspaces, err := workspace.Discover(plan.RootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
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
		displayInfo(fmt.Sprintf("📦 Releasing %d selected repositories: %s", len(selectedRepos), strings.Join(selectedRepos, ", ")))
	} else {
		return fmt.Errorf("no repositories selected for release")
	}

	// Auto-commit grove-ecosystem changes if needed
	if err := autoCommitEcosystemChanges(ctx, plan.RootDir, hasChanges, logger); err != nil {
		return fmt.Errorf("failed to auto-commit ecosystem changes: %w", err)
	}

	// Run pre-flight checks
	parentVersion := determineParentVersion(plan.RootDir, versions, hasChanges)
	if err := runPreflightChecks(ctx, plan.RootDir, parentVersion, workspaces, logger); err != nil {
		return err
	}

	// Check for outdated Grove dependencies
	if err := checkForOutdatedDependencies(ctx, plan.RootDir, workspaces, logger); err != nil {
		displayWarning("Failed to check for outdated dependencies: " + err.Error())
	}

	// Push repositories to remote if requested
	if releasePush {
		if err := pushRepositories(ctx, plan.RootDir, workspaces, hasChanges, logger); err != nil {
			return fmt.Errorf("failed to push repositories: %w", err)
		}
	}

	// Copy staged changelogs back to repositories
	for repo, repoPlan := range plan.Repos {
		if repoPlan.Selected && repoPlan.ChangelogPath != "" && repoPlan.Status == "Approved" {
			node, ok := graph.GetNode(repo)
			if !ok {
				continue
			}

			targetPath := filepath.Join(node.Dir, "CHANGELOG.md")
			existingContent, _ := os.ReadFile(targetPath)

			// Read staged changelog
			stagedContent, err := os.ReadFile(repoPlan.ChangelogPath)
			if err != nil {
				logger.WithField("repo", repo).WithError(err).Warn("Failed to read staged changelog")
				continue
			}

			// Prepend to existing content
			newContent := stagedContent
			if len(existingContent) > 0 {
				newContent = append(stagedContent, '\n')
				newContent = append(newContent, existingContent...)
			}

			if err := os.WriteFile(targetPath, newContent, 0644); err != nil {
				logger.WithField("repo", repo).WithError(err).Warn("Failed to write changelog")
			}
		}
	}

	// Execute dependency-aware release orchestration
	if err := orchestrateRelease(ctx, plan.RootDir, plan.ReleaseLevels, versions, currentVersions, hasChanges, graph, logger, false); err != nil {
		return fmt.Errorf("failed to orchestrate release: %w", err)
	}

	// Final phase: commit and tag ecosystem
	displaySection("🏁 Finalizing Ecosystem Release")

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

	// Handle parent repository updates unless skipped
	var finalParentVersion string
	if !releaseSkipParent {
		// Recalculate parent version to handle same-day releases
		finalParentVersion = determineParentVersion(plan.RootDir, versions, hasChanges)

		// Tag the main repository
		if err := executeGitCommand(ctx, plan.RootDir, []string{"tag", "-a", finalParentVersion, "-m", fmt.Sprintf("Release %s", finalParentVersion)}, "Tag main repository", logger); err != nil {
			return err
		}

		// Push the main repository changes
		if err := executeGitCommand(ctx, plan.RootDir, []string{"push", "origin", "main"}, "Push main branch", logger); err != nil {
			return err
		}

		// Push the main repository tag
		if err := executeGitCommand(ctx, plan.RootDir, []string{"push", "origin", finalParentVersion}, "Push release tag", logger); err != nil {
			return err
		}

		displayFinalSuccess(finalParentVersion, releasedCount)
	} else {
		displayInfo("Skipping parent repository updates (--skip-parent flag)")
		displaySuccess(fmt.Sprintf("Released %d module(s) successfully", releasedCount))
	}

	// Clear the plan after successful completion
	if err := release.ClearPlan(); err != nil {
		logger.WithError(err).Warn("Failed to clear release plan")
	}

	return nil
}