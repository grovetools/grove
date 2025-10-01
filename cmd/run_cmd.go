package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/runner"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var runFilter string
var runExclude string

func init() {
	rootCmd.AddCommand(newRunCmd())
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <command> [args...]",
		Short: "Run a command in all workspaces",
		Long: `Execute a command across all discovered workspaces.

The command will be executed in each workspace directory with the
workspace as the current working directory.`,
		Example: `  # Run grove context stats in all workspaces
  grove run cx stats
  
  # Run git status in all workspaces
  grove run git status
  
  # Filter workspaces by pattern
  grove run --filter "grove-*" npm test
  
  # Exclude specific workspaces
  grove run --exclude "grove-core,grove-flow" npm test
  
  # Run with JSON output aggregation
  grove run --json cx stats`,
		Args: cobra.MinimumNArgs(1),
		RunE: runCommand,
	}

	cmd.Flags().StringVarP(&runFilter, "filter", "f", "", "Filter workspaces by glob pattern")
	cmd.Flags().StringVar(&runExclude, "exclude", "", "Comma-separated list of workspace patterns to exclude (glob patterns)")

	return cmd
}

func runCommand(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	opts := cli.GetOptions(cmd)

	// Find root directory with workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	logger.WithField("root", rootDir).Debug("Found workspace root")

	// Discover workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	// Apply filter if provided
	if runFilter != "" {
		workspaces = workspace.FilterWorkspaces(workspaces, runFilter)
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
		Logger:     logger,
		RootDir:    rootDir,
	}

	// Execute across workspaces
	return runner.Run(runnerOpts)
}

// runScript is a helper for running shell scripts across workspaces
func runScript(cmd *cobra.Command, script string) error {
	logger := cli.GetLogger(cmd)

	// Find root directory with workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	// Apply filter if provided
	if runFilter != "" {
		workspaces = workspace.FilterWorkspaces(workspaces, runFilter)
	}

	// Apply exclude filter if provided
	if runExclude != "" {
		workspaces = applyExcludeFilter(workspaces, runExclude)
	}

	// Execute script
	runnerOpts := runner.Options{
		Workspaces: workspaces,
		Logger:     logger,
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
