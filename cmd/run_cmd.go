package cmd

import (
	"fmt"
	
	"github.com/mattsolo1/grove-meta/pkg/runner"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

var runFilter string

func init() {
	runCmd := newRunCmd()
	runCmd.Flags().StringVarP(&runFilter, "filter", "f", "", "Filter workspaces by glob pattern")
	rootCmd.AddCommand(runCmd)
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
  
  # Run with JSON output aggregation
  grove run --json cx stats`,
		Args: cobra.MinimumNArgs(1),
		RunE: runCommand,
	}
	
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
	
	// Execute script
	runnerOpts := runner.Options{
		Workspaces: workspaces,
		Logger:     logger,
		RootDir:    rootDir,
	}
	
	return runner.RunScript(runnerOpts, script)
}