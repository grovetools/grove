package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/git"
	"github.com/grovetools/core/pkg/daemon"
	grovecontext "github.com/grovetools/cx/pkg/context"
	"github.com/spf13/cobra"
)

func newTestSmartCmd() *cobra.Command {
	var skipTags []string

	cmd := &cobra.Command{
		Use:   "test-smart",
		Short: "Run tests scoped to changed files via cx rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			wsDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %w", err)
			}
			absDir, err := filepath.Abs(wsDir)
			if err != nil {
				return fmt.Errorf("failed to resolve path: %w", err)
			}

			cfg, err := config.LoadFrom(absDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			dirtyFiles, err := getDirtyFiles(absDir)
			if err != nil {
				return fmt.Errorf("failed to get dirty files: %w", err)
			}

			var triggeredScenarios []string
			if len(dirtyFiles) > 0 && len(cfg.TestScopes) > 0 {
				cxMgr := grovecontext.NewManager(absDir)
				cxMgr.SetContext(cmd.Context())

				for _, scope := range cfg.TestScopes {
					hit, intersectErr := cxMgr.Intersects(scope.Rules, dirtyFiles)
					if intersectErr != nil {
						fmt.Fprintf(os.Stderr, "warning: scope %q: %v\n", scope.Name, intersectErr)
						continue
					}
					if hit {
						triggeredScenarios = append(triggeredScenarios, scope.Scenarios...)
					}
				}
				triggeredScenarios = dedup(triggeredScenarios)
			}

			tendArgs := []string{"run", "-p"}
			if len(triggeredScenarios) > 0 {
				for _, s := range triggeredScenarios {
					tendArgs = append(tendArgs, "-s", s)
				}
			}
			for _, tag := range skipTags {
				tendArgs = append(tendArgs, "--skip-tags", tag)
			}
			if len(triggeredScenarios) == 0 && len(skipTags) == 0 {
				tendArgs = append(tendArgs, "--skip-tags", "slow")
			}

			fmt.Fprintf(os.Stderr, "test-smart: tend %s\n", strings.Join(tendArgs, " "))

			tendCmd := exec.CommandContext(cmd.Context(), "tend", tendArgs...)
			tendCmd.Dir = absDir
			tendCmd.Stdout = os.Stdout
			tendCmd.Stderr = os.Stderr
			exitCode := 0
			if runErr := tendCmd.Run(); runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					return fmt.Errorf("failed to run tend: %w", runErr)
				}
			}

			commitHash, _ := git.GetHeadCommit(absDir)
			if commitHash == "" {
				commitHash = "unknown"
			}
			workspace := filepath.Base(absDir)
			client := daemon.New(absDir)
			defer client.Close()
			if client.IsRunning() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = client.ReportTask(ctx, workspace, "test-smart", exitCode, commitHash, 0, "")
			}

			os.Exit(exitCode)
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&skipTags, "skip-tags", nil, "Tags to skip when running tests")
	return cmd
}

func getDirtyFiles(repoDir string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "-uall")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status failed: %w", err)
	}

	var files []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// porcelain format: XY <path> or XY <orig> -> <path>
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		files = append(files, path)
	}
	return files, nil
}

func dedup(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
