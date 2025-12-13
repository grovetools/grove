package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
)

// DiscoverTargetProjects determines the appropriate scope of projects based on the current context.
// If run from within an EcosystemWorktree, it returns only the constituents of that worktree.
// If run from within an Ecosystem root, it returns only the direct sub-projects.
// Otherwise, it returns all projects in the root ecosystem or standalone project group.
func DiscoverTargetProjects() ([]*workspace.WorkspaceNode, string, error) {
	// Get current working directory
	cwd, err := filepath.Abs(".")
	if err != nil {
		return nil, "", fmt.Errorf("failed to get current directory: %w", err)
	}

	// Use GetProjectByPath to perform a fast lookup and identify the current workspace node
	currentNode, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		// If we can't determine the current context, fall back to discovering all projects
		logger := logging.NewLogger("discovery")
		logger.WithField("error", err).Debug("Could not determine current workspace context, falling back to full discovery")

		projects, err := discovery.DiscoverProjects()
		if err != nil {
			return nil, "", err
		}

		rootDir, _ := workspace.FindEcosystemRoot("")
		if rootDir == "" {
			rootDir = cwd
		}
		return projects, rootDir, nil
	}

	// Get all projects to filter from
	allProjects, err := discovery.DiscoverAllProjects()
	if err != nil {
		return nil, "", fmt.Errorf("failed to discover all projects: %w", err)
	}

	// Determine the scope for the command
	var scopeRoot string
	var filteredProjects []*workspace.WorkspaceNode

	switch currentNode.Kind {
	case workspace.KindEcosystemWorktree:
		// We're in an EcosystemWorktree, scope to its constituents
		scopeRoot = currentNode.Path
		scopeRootLower := strings.ToLower(scopeRoot)

		// Don't include the ecosystem worktree itself - it's a meta-project

		// Filter to include both regular sub-projects and linked worktrees (the preferred state)
		for _, p := range allProjects {
			parentPathLower := strings.ToLower(p.ParentEcosystemPath)
			if parentPathLower == scopeRootLower &&
				(p.Kind == workspace.KindEcosystemWorktreeSubProject ||
					p.Kind == workspace.KindEcosystemWorktreeSubProjectWorktree) {
				filteredProjects = append(filteredProjects, p)
			}
		}

	case workspace.KindEcosystemWorktreeSubProject, workspace.KindEcosystemWorktreeSubProjectWorktree:
		// We're in a sub-project within an EcosystemWorktree
		// Find the parent EcosystemWorktree and use it as scope
		scopeRoot = currentNode.ParentEcosystemPath
		scopeRootLower := strings.ToLower(scopeRoot)

		// Don't include the ecosystem worktree itself - it's a meta-project

		// Filter to include both regular sub-projects and linked worktrees
		for _, p := range allProjects {
			parentPathLower := strings.ToLower(p.ParentEcosystemPath)
			if parentPathLower == scopeRootLower &&
				(p.Kind == workspace.KindEcosystemWorktreeSubProject ||
					p.Kind == workspace.KindEcosystemWorktreeSubProjectWorktree) {
				filteredProjects = append(filteredProjects, p)
			}
		}

	case workspace.KindEcosystemRoot:
		// We're in an ecosystem root, only include its direct children (not the root itself)
		scopeRoot = currentNode.Path
		scopeRootLower := strings.ToLower(scopeRoot)

		// Don't include the ecosystem root itself - it's a meta-project

		// Only include direct ecosystem children (main repos, not worktrees)
		for _, p := range allProjects {
			parentPathLower := strings.ToLower(p.ParentEcosystemPath)
			if parentPathLower == scopeRootLower && p.Kind == workspace.KindEcosystemSubProject {
				filteredProjects = append(filteredProjects, p)
			}
		}

	case workspace.KindEcosystemSubProject, workspace.KindEcosystemSubProjectWorktree:
		// We're in a sub-project within an ecosystem root.
		// Scope the command to only this project.
		// To operate on all projects, run the command from the ecosystem root.
		filteredProjects = []*workspace.WorkspaceNode{currentNode}
		scopeRoot = currentNode.Path

	case workspace.KindStandaloneProject:
		// We're in a standalone project, only include it
		filteredProjects = []*workspace.WorkspaceNode{currentNode}
		scopeRoot = currentNode.Path

	case workspace.KindStandaloneProjectWorktree:
		// We're in a worktree of a standalone project, only include it
		filteredProjects = []*workspace.WorkspaceNode{currentNode}
		scopeRoot = currentNode.Path

	default:
		// For any other case, fall back to standard discovery
		projects, err := discovery.DiscoverProjects()
		if err != nil {
			return nil, "", err
		}

		rootDir, _ := workspace.FindEcosystemRoot("")
		if rootDir == "" {
			rootDir = cwd
		}

		// Filter to only direct children of the current ecosystem if we're in one
		rootDirLower := strings.ToLower(rootDir)
		for _, p := range projects {
			parentPathLower := strings.ToLower(p.ParentEcosystemPath)

			// Only include direct ecosystem children (main repos, not worktrees)
			if parentPathLower == rootDirLower && p.Kind == workspace.KindEcosystemSubProject {
				filteredProjects = append(filteredProjects, p)
			} else if p.ParentEcosystemPath == "" {
				// Include standalone projects when not in an ecosystem
				filteredProjects = append(filteredProjects, p)
			}
		}

		return filteredProjects, rootDir, nil
	}

	return filteredProjects, scopeRoot, nil
}