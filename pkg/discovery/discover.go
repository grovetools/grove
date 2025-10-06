package discovery

import (
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/sirupsen/logrus"
)

// DiscoverProjects is a centralized helper that finds all main workspace projects
// (excluding worktrees) within the current ecosystem using the discovery service from grove-core.
func DiscoverProjects() ([]*workspace.ProjectInfo, error) {
	return DiscoverProjectsInEcosystem("", false)
}

// DiscoverAllProjects discovers all projects including worktrees within the current ecosystem.
func DiscoverAllProjects() ([]*workspace.ProjectInfo, error) {
	return DiscoverProjectsInEcosystem("", true)
}

// DiscoverProjectsInEcosystem discovers projects scoped to a specific ecosystem root.
// If ecosystemRoot is empty, it finds the current ecosystem root or parent ecosystem.
// If includeWorktrees is false, worktrees are excluded from the results.
func DiscoverProjectsInEcosystem(ecosystemRoot string, includeWorktrees bool) ([]*workspace.ProjectInfo, error) {
	// Initialize the discovery service from grove-core
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Suppress noisy output
	discoveryService := workspace.NewDiscoveryService(logger)

	// Discover all entities (ecosystems, projects, etc.)
	discoveryResult, err := discoveryService.DiscoverAll()
	if err != nil {
		return nil, err
	}

	// Transform the raw results into the flat ProjectInfo list used by UIs and commands
	allProjects := workspace.TransformToProjectInfo(discoveryResult)

	// If no ecosystem root specified, determine it from the current context
	if ecosystemRoot == "" {
		// First, find what FindEcosystemRoot returns (might be a worktree)
		currentRoot, err := workspace.FindEcosystemRoot("")
		if err != nil {
			// If we can't find an ecosystem root, just return all projects
			return allProjects, nil
		}

		// Normalize path for comparison (lowercase for case-insensitive filesystems)
		currentRootLower := strings.ToLower(currentRoot)

		// Check if any project in allProjects has this as their parent ecosystem
		// This handles the case where we're in a worktree
		for _, p := range allProjects {
			if strings.ToLower(p.Path) == currentRootLower && p.ParentEcosystemPath != "" {
				// We're in a sub-project that has a parent ecosystem
				ecosystemRoot = p.ParentEcosystemPath
				break
			}
		}

		// If we didn't find a parent, use the current root
		if ecosystemRoot == "" {
			ecosystemRoot = currentRoot
		}
	}

	// Normalize ecosystem root for case-insensitive comparison (macOS has case-insensitive filesystem)
	ecosystemRootLower := strings.ToLower(ecosystemRoot)

	// Filter to only projects within the specified ecosystem
	var scopedProjects []*workspace.ProjectInfo
	for _, p := range allProjects {
		// Normalize paths for comparison
		parentPathLower := strings.ToLower(p.ParentEcosystemPath)
		projectPathLower := strings.ToLower(p.Path)

		// Include if:
		// 1. The project's ParentEcosystemPath matches our ecosystem root, OR
		// 2. The project path itself starts with the ecosystem root, OR
		// 3. The project IS the ecosystem root
		if parentPathLower == ecosystemRootLower ||
		   strings.HasPrefix(projectPathLower, ecosystemRootLower) ||
		   projectPathLower == ecosystemRootLower {

			// If not including worktrees, skip:
			// - Projects marked as worktrees
			// - Projects whose path contains /.grove-worktrees/ (inside a worktree)
			if !includeWorktrees {
				if p.IsWorktree || strings.Contains(p.Path, "/.grove-worktrees/") {
					continue
				}
			}

			scopedProjects = append(scopedProjects, p)
		}
	}

	return scopedProjects, nil
}

// FilterWorkspaces applies a glob pattern filter to a list of workspace paths.
// This function is moved here from the old discover.go.
func FilterWorkspaces(workspaces []string, filter string) []string {
	if filter == "" {
		return workspaces
	}

	var filtered []string
	for _, ws := range workspaces {
		base := filepath.Base(ws)
		matched, err := filepath.Match(filter, base)
		if err == nil && matched {
			filtered = append(filtered, ws)
		}
	}

	return filtered
}

// GetWorkspaceName returns a display name for a workspace path.
// This function is moved here from the old discover.go.
func GetWorkspaceName(workspacePath, rootDir string) string {
	if rootDir != "" {
		if rel, err := filepath.Rel(rootDir, workspacePath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(workspacePath)
}
