package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
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
	releaseMajor  []string
	releaseMinor  []string
	releasePatch  []string
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
	cmd.Flags().StringSliceVar(&releaseMajor, "major", []string{}, "Repositories to receive major version bump")
	cmd.Flags().StringSliceVar(&releaseMinor, "minor", []string{}, "Repositories to receive minor version bump")
	cmd.Flags().StringSliceVar(&releasePatch, "patch", []string{}, "Repositories to receive patch version bump (default for all)")

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

	// Display proposed versions and get confirmation
	if !displayAndConfirmVersions(versions, currentVersions, hasChanges, logger) {
		logger.Info("Release cancelled by user")
		return nil
	}

	// Determine parent repo version (use the highest version among all submodules with changes)
	parentVersion := determineParentVersion(versions, hasChanges)

	// Run pre-flight checks
	if err := runPreflightChecks(ctx, rootDir, parentVersion, logger); err != nil {
		return err
	}

	// Execute git submodule foreach to check cleanliness and tag
	if err := tagSubmodules(ctx, rootDir, versions, hasChanges, logger); err != nil {
		return fmt.Errorf("failed to tag submodules: %w", err)
	}

	// Stage the updated submodule references
	if err := executeGitCommand(ctx, rootDir, []string{"add", "."}, "Stage submodule updates", logger); err != nil {
		return err
	}

	// Commit the submodule updates
	commitMsg := createReleaseCommitMessage(versions, hasChanges)
	if err := executeGitCommand(ctx, rootDir, []string{"commit", "-m", commitMsg}, "Commit release", logger); err != nil {
		return err
	}

	// Tag the main repository
	if err := executeGitCommand(ctx, rootDir, []string{"tag", "-a", parentVersion, "-m", fmt.Sprintf("Release %s", parentVersion)}, "Tag main repository", logger); err != nil {
		return err
	}

	// Push the main repository changes
	if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", "main"}, "Push main branch", logger); err != nil {
		return err
	}

	// Push the main repository tag
	if err := executeGitCommand(ctx, rootDir, []string{"push", "origin", parentVersion}, "Push release tag", logger); err != nil {
		return err
	}

	logger.Info("✅ Release successfully created")
	logger.Info("")
	logger.Info("GitHub Actions will now:")
	logger.Info("  - Build and release each tool independently in its own repository")
	logger.Info("")
	logger.Info("The new tag on grove-ecosystem (%s) now marks this coordinated release.", parentVersion)
	logger.Info("")
	logger.Info("Monitor the individual tool releases in their respective repositories.")

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
			logger.Warn("No version found for submodule", "path", smPath)
			continue
		}
		
		// Skip if no changes
		if changes, ok := hasChanges[repoName]; ok && !changes {
			logger.Info("Skipping submodule (no changes)", "path", smPath)
			continue
		}
		
		smFullPath := filepath.Join(rootDir, smPath)
		logger.Info("Tagging submodule", "path", smPath, "version", version)
		
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

func calculateNextVersions(ctx context.Context, rootDir string, major, minor, patch []string, logger *logrus.Logger) (map[string]string, map[string]string, map[string]bool, error) {
	versions := make(map[string]string)
	currentVersions := make(map[string]string)
	hasChanges := make(map[string]bool)
	
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
			logger.Info("No tags found for", "repo", repoName, "defaulting to", "v0.0.0")
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
			if commitCount == "0" {
				// Keep the same version - no bump needed
				versions[repoName] = currentTag
				hasChanges[repoName] = false
				continue
			}
			
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
	
	currentVer, err1 := semver.NewVersion(current)
	proposedVer, err2 := semver.NewVersion(proposed)
	
	if err1 != nil || err2 != nil {
		return "unknown"
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

func determineParentVersion(versions map[string]string, hasChanges map[string]bool) string {
	// Use the highest version among all submodules with changes
	var highest *semver.Version
	
	for repo, versionStr := range versions {
		// Skip repos without changes
		if changes, ok := hasChanges[repo]; ok && !changes {
			continue
		}
		v, err := semver.NewVersion(versionStr)
		if err != nil {
			continue
		}
		
		if highest == nil || v.GreaterThan(highest) {
			highest = v
		}
	}
	
	if highest == nil {
		return "v0.1.0"
	}
	
	return "v" + highest.String()
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