package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/mattsolo1/grove-meta/pkg/runner"
	"github.com/spf13/cobra"
)

var runFilter string
var runExclude string

func init() {
	rootCmd.AddCommand(newRunCmd())
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run a command in all workspaces",
		Long: `Execute a command across all discovered workspaces.

The command will be executed in each workspace directory with the
workspace as the current working directory.

Use -- to separate grove run flags from the command and its arguments.`,
		Example: `  # Run grove context stats in all workspaces
  grove run cx stats
  
  # Run git status in all workspaces
  grove run git status
  
  # Filter workspaces by pattern
  grove run --filter "grove-*" -- npm test
  
  # Exclude specific workspaces
  grove run --exclude "grove-core,grove-flow" -- npm test
  
  # Run command with flags (using -- separator)
  grove run -- docgen generate --output docs/
  
  # Run with JSON output aggregation
  grove run --json -- cx stats`,
		Args:                  cobra.MinimumNArgs(1),
		RunE:                  runCommand,
	}

	cmd.Flags().StringVarP(&runFilter, "filter", "f", "", "Filter workspaces by glob pattern")
	cmd.Flags().StringVar(&runExclude, "exclude", "", "Comma-separated list of workspace patterns to exclude (glob patterns)")

	return cmd
}

// discoverTargetProjects determines the appropriate scope of projects based on the current context.
// If run from within an EcosystemWorktree, it returns only the constituents of that worktree.
// Otherwise, it returns all projects in the root ecosystem or standalone project group.
func discoverTargetProjects() ([]*workspace.WorkspaceNode, string, error) {
	// Get current working directory
	cwd, err := filepath.Abs(".")
	if err != nil {
		return nil, "", fmt.Errorf("failed to get current directory: %w", err)
	}

	// Use GetProjectByPath to perform a fast lookup and identify the current workspace node
	currentNode, err := workspace.GetProjectByPath(cwd)
	if err != nil {
		// If we can't determine the current context, fall back to discovering all projects
		logger := logging.NewLogger("run")
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

	// Determine the scope for the run command
	var scopeRoot string
	var filteredProjects []*workspace.WorkspaceNode

	// Check if we're in an EcosystemWorktree or a child of one
	if currentNode.Kind == workspace.KindEcosystemWorktree {
		// We're in an EcosystemWorktree, scope to its constituents
		scopeRoot = currentNode.Path

		// Filter projects to only those within this worktree
		// Exclude the worktree root itself, only include its sub-projects
		for _, p := range allProjects {
			// Include only if:
			// 1. The project path starts with the scopeRoot (is inside the worktree)
			// 2. The project path is not the scopeRoot itself (exclude the worktree root)
			if strings.HasPrefix(strings.ToLower(p.Path), strings.ToLower(scopeRoot+string(filepath.Separator))) {
				filteredProjects = append(filteredProjects, p)
			}
		}
	} else if currentNode.Kind == workspace.KindEcosystemWorktreeSubProject ||
	          currentNode.Kind == workspace.KindEcosystemWorktreeSubProjectWorktree {
		// We're in a sub-project within an EcosystemWorktree
		// Find the parent EcosystemWorktree and use it as scope
		scopeRoot = currentNode.ParentEcosystemPath

		// Filter projects to only those within the parent worktree
		for _, p := range allProjects {
			if strings.HasPrefix(strings.ToLower(p.Path), strings.ToLower(scopeRoot+string(filepath.Separator))) {
				filteredProjects = append(filteredProjects, p)
			}
		}
	} else {
		// Not in an EcosystemWorktree context, use standard discovery
		// This preserves the existing behavior for root ecosystems and standalone projects
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

	return filteredProjects, scopeRoot, nil
}

func runCommand(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("run")
	opts := cli.GetOptions(cmd)

	// Discover projects using the new context-aware helper
	projects, rootDir, err := discoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}

	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	// Apply filter if provided
	if runFilter != "" {
		workspaces = discovery.FilterWorkspaces(workspaces, runFilter)
		if len(workspaces) == 0 {
			return fmt.Errorf("no workspaces matched filter: %s", runFilter)
		}
	}

	// Apply exclude filter if provided
	if runExclude != "" {
		originalCount := len(workspaces)
		workspaces = applyExcludeFilter(workspaces, runExclude)
		if len(workspaces) == 0 {
			return fmt.Errorf("no workspaces remained after applying exclude filter")
		}
		logger.WithField("originalCount", originalCount).WithField("filteredCount", len(workspaces)).Debug("Applied exclude filter")
	}

	logger.WithField("count", len(workspaces)).Info("Discovered workspaces")

	// Prepare runner options
	runnerOpts := runner.Options{
		Workspaces: workspaces,
		Command:    args[0],
		Args:       args[1:],
		JSONMode:   opts.JSONOutput,
		Logger:     logger.Logger,
		RootDir:    rootDir,
	}

	// Execute across workspaces
	return runner.Run(runnerOpts)
}

// runScript is a helper for running shell scripts across workspaces
func runScript(cmd *cobra.Command, script string) error {
	logger := logging.NewLogger("run")

	// Discover projects using the new context-aware helper
	projects, rootDir, err := discoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}

	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	// Apply filter if provided
	if runFilter != "" {
		workspaces = discovery.FilterWorkspaces(workspaces, runFilter)
	}

	// Apply exclude filter if provided
	if runExclude != "" {
		workspaces = applyExcludeFilter(workspaces, runExclude)
	}

	// Execute script
	runnerOpts := runner.Options{
		Workspaces: workspaces,
		Logger:     logger.Logger,
		RootDir:    rootDir,
	}

	return runner.RunScript(runnerOpts, script)
}

// applyExcludeFilter removes workspaces that match any of the exclude patterns
func applyExcludeFilter(workspaces []string, excludeStr string) []string {
	if excludeStr == "" {
		return workspaces
	}

	var excludePatterns []string
	for _, pattern := range strings.Split(excludeStr, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			excludePatterns = append(excludePatterns, pattern)
		}
	}

	if len(excludePatterns) == 0 {
		return workspaces
	}

	var filtered []string
	for _, ws := range workspaces {
		workspaceName := filepath.Base(ws)
		
		// Check if workspace matches any exclude pattern
		excluded := false
		for _, pattern := range excludePatterns {
			if matched, err := filepath.Match(pattern, workspaceName); err == nil && matched {
				excluded = true
				break
			}
		}
		
		// Include workspace if it doesn't match any exclude pattern
		if !excluded {
			filtered = append(filtered, ws)
		}
	}

	return filtered
}
