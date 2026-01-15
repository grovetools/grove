package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// RepoGitHubInitDryRunScenario tests the dry-run functionality of grove repo github-init
func RepoGitHubInitDryRunScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "repo-github-init-dry-run",
		Description: "Verifies 'grove repo github-init --dry-run' shows intended actions",
		Tags:        []string{"repo", "github", "dry-run"},
		Steps: []harness.Step{
			{
				Name:        "Setup and run dry-run",
				Description: "Create a local repo and run github-init with --dry-run",
				Func: func(ctx *harness.Context) error {
					// Create a mock local repository
					repoDir := ctx.NewDir("grove-test-repo")

					// Create minimal grove.yml to make it a Grove repo
					fs.WriteString(filepath.Join(repoDir, "grove.yml"), "name: grove-test-repo\nbinary:\n  alias: gtr\n")
					fs.WriteString(filepath.Join(repoDir, "go.mod"), "module github.com/mattsolo1/grove-test-repo\n\ngo 1.24.4\n")

					// Initialize git
					cmd := ctx.Command("git", "init")
					cmd.Dir(repoDir)
					cmd.Run()

					cmd = ctx.Command("git", "config", "user.email", "test@example.com")
					cmd.Dir(repoDir)
					cmd.Run()

					cmd = ctx.Command("git", "config", "user.name", "Test User")
					cmd.Dir(repoDir)
					cmd.Run()

					cmd = ctx.Command("git", "add", ".")
					cmd.Dir(repoDir)
					cmd.Run()

					cmd = ctx.Command("git", "commit", "-m", "Initial commit")
					cmd.Dir(repoDir)
					cmd.Run()

					// Set up mocks
					mockDir := ctx.NewDir("mocks")

					// Mock gh
					ghMockPath := filepath.Join(mockDir, "gh")
					fs.WriteString(ghMockPath, ghMockScript)
					os.Chmod(ghMockPath, 0755)

					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					os.Setenv("GROVE_PAT", "test-pat-12345")

					// Run github-init with --dry-run
					groveCmd := ctx.Bin("repo", "github-init", "--dry-run")
					groveCmd.Dir(repoDir)

					result := groveCmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("dry-run failed: %s\nStdout: %s", result.Stderr, result.Stdout)
					}

					// Verify output
					combinedOutput := result.Stdout + result.Stderr
					if !strings.Contains(combinedOutput, "DRY RUN MODE") {
						return fmt.Errorf("expected DRY RUN MODE in output")
					}

					// Verify the repo name is mentioned
					if !strings.Contains(combinedOutput, "grove-test-repo") {
						return fmt.Errorf("expected repo name in output")
					}

					// Verify gh commands are mentioned
					if !strings.Contains(combinedOutput, "gh repo create") {
						return fmt.Errorf("expected 'gh repo create' in output")
					}

					return nil
				},
			},
		},
	}
}
