package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/logging"
	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/discovery"
	"github.com/grovetools/grove/pkg/runner"
)

var (
	runFilter   string
	runExclude  string
	runParallel bool
)

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

Use --parallel (-p) to run with the parallel orchestrator TUI,
the same engine behind grove build/fmt/lint/check.

Use -- to separate grove run flags from the command and its arguments.`,
		Example: `  # Run make test-e2e in parallel across all workspaces
  grove run -p -- make test-e2e

  # Run git status in all workspaces (sequential)
  grove run git status

  # Filter workspaces by pattern
  grove run --filter "grove-*" -- npm test

  # Parallel with affected detection
  grove run -p --affected -- make test-e2e`,
		Args:         cobra.MinimumNArgs(1),
		RunE:         runCommand,
		SilenceUsage: true,
	}

	cmd.Flags().BoolVarP(&runParallel, "parallel", "p", false, "Run in parallel with TUI (uses the orchestrator engine)")
	cmd.Flags().StringVarP(&runFilter, "filter", "f", "", "Filter workspaces by glob pattern")
	cmd.Flags().StringVar(&runExclude, "exclude", "", "Comma-separated list of workspace patterns to exclude (glob patterns)")
	cmd.Flags().Bool("affected", false, "Only run on workspaces with uncommitted changes (requires --parallel)")
	cmd.Flags().Bool("no-cache", false, "Ignore cached task results (requires --parallel)")
	cmd.Flags().IntP("jobs", "j", 10, "Number of parallel workers")
	cmd.Flags().Bool("fail-fast", false, "Stop immediately when one task fails")
	cmd.Flags().Bool("dry-run", false, "Show what would run without executing")
	cmd.Flags().BoolP("interactive", "i", false, "Keep TUI open after completion")

	return cmd
}

func runCommand(cmd *cobra.Command, args []string) error {
	if runParallel {
		// Parallel mode: use the orchestrator with the raw command
		verb := strings.Join(args, " ")
		return executeTaskWithCommand(cmd, verb, args)
	}

	logger := logging.NewLogger("run")
	opts := cli.GetOptions(cmd)

	// Discover projects using the shared context-aware helper
	projects, rootDir, err := DiscoverTargetProjects()
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
