package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/pkg/workspace"

	"github.com/grovetools/grove/pkg/discovery"
)

// DiscoverTargetProjects determines the appropriate scope of projects based on the current context.
// If run from within an EcosystemWorktree, it returns only the constituents of that worktree.
// If run from within an Ecosystem root, it returns only the direct sub-projects.
// If run from within a sub-project or standalone project, it returns only that project.
// When the current directory cannot be classified it fails closed with an error —
// it never falls back to a machine-wide discovery.
func DiscoverTargetProjects() ([]*workspace.WorkspaceNode, string, error) {
	// Get current working directory
	cwd, err := filepath.Abs(".")
	if err != nil {
		return nil, "", fmt.Errorf("failed to get current directory: %w", err)
	}

	// Use GetProjectByPath to perform a fast lookup and identify the current workspace node
	currentNode, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		// Fail closed: without a classified context we must never fall back to
		// a machine-wide discovery.
		return nil, "", fmt.Errorf("cannot determine grove workspace context for %s: %w (run grove from inside an ecosystem or project)", cwd, err)
	}

	// Determine the scope for the command
	var scopeRoot string
	var filteredProjects []*workspace.WorkspaceNode

	switch currentNode.Kind {
	case workspace.KindEcosystemWorktree:
		// We're in an EcosystemWorktree, scope to its constituents
		scopeRoot = currentNode.Path
		scopeRootLower := strings.ToLower(scopeRoot)

		// Get all projects to filter from
		allProjects, err := discovery.DiscoverAllProjects()
		if err != nil {
			return nil, "", fmt.Errorf("failed to discover projects in %s: %w", scopeRoot, err)
		}

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
		// We're in a sub-project within an EcosystemWorktree.
		// Scope the command to only this project.
		// To operate on all projects, run the command from the worktree root.
		filteredProjects = []*workspace.WorkspaceNode{currentNode}
		scopeRoot = currentNode.Path

	case workspace.KindEcosystemRoot:
		// We're in an ecosystem root, only include its direct children (not the root itself)
		scopeRoot = currentNode.Path
		scopeRootLower := strings.ToLower(scopeRoot)

		// Get all projects to filter from
		allProjects, err := discovery.DiscoverAllProjects()
		if err != nil {
			return nil, "", fmt.Errorf("failed to discover projects in %s: %w", scopeRoot, err)
		}

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
		// Unrecognized context (e.g. a non-grove repo). Scope to the enclosing
		// ecosystem when there is one; otherwise fail closed — never fall back
		// to a machine-wide discovery.
		rootDir, rootErr := workspace.FindEcosystemRoot(cwd)
		if rootErr != nil {
			return nil, "", fmt.Errorf("cannot determine grove workspace context for %s (classified as %s): %v (run grove from inside an ecosystem or project)", cwd, currentNode.Kind, rootErr)
		}

		projects, err := discovery.DiscoverProjectsInEcosystem(rootDir, false)
		if err != nil {
			return nil, "", err
		}

		// Only include direct ecosystem children (main repos, not worktrees)
		rootDirLower := strings.ToLower(rootDir)
		for _, p := range projects {
			parentPathLower := strings.ToLower(p.ParentEcosystemPath)
			if parentPathLower == rootDirLower && p.Kind == workspace.KindEcosystemSubProject {
				filteredProjects = append(filteredProjects, p)
			}
		}

		return filteredProjects, rootDir, nil
	}

	return filteredProjects, scopeRoot, nil
}
