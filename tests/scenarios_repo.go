package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// RepoAddDryRunScenario tests the dry-run functionality of grove repo add
func RepoAddDryRunScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "repo-add-dry-run",
		Description: "Verifies 'grove repo add --dry-run' shows intended actions without executing",
		Tags:        []string{"repo", "dry-run"},
		Steps: []harness.Step{
			{
				Name:        "Setup and run dry-run",
				Description: "Setup mock ecosystem and run 'grove repo add' with --dry-run",
				Func: func(ctx *harness.Context) error {
					// Create a minimal mock ecosystem
					ecosystemDir := ctx.NewDir("ecosystem")

					// Create minimal files
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"grove-*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")

					// Test dry-run with default alias (should use repo name)
					cmd := ctx.Bin("repo", "add", "grove-test-dryrun", "--dry-run")
					cmd.Dir(ecosystemDir)

					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("dry-run failed with exit code %d: %s\nStdout: %s", result.ExitCode, result.Stderr, result.Stdout)
					}

					// Verify output
					combinedOutput := result.Stdout + result.Stderr
					if !strings.Contains(combinedOutput, "DRY RUN MODE") {
						return fmt.Errorf("expected DRY RUN MODE in output, got:\nStdout: %s\nStderr: %s", result.Stdout, result.Stderr)
					}

					// Verify alias defaults to repo name in dry-run output
					if !strings.Contains(combinedOutput, "Binary alias: grove-test-dryrun") {
						return fmt.Errorf("expected alias to default to repo name 'grove-test-dryrun', got:\nStdout: %s\nStderr: %s", result.Stdout, result.Stderr)
					}

					// Verify hint about github-init is shown
					if !strings.Contains(combinedOutput, "grove repo github-init") {
						return fmt.Errorf("expected hint about 'grove repo github-init' in output, got:\nStdout: %s\nStderr: %s", result.Stdout, result.Stderr)
					}

					// Verify no files were created
					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-dryrun")); err == nil {
						return fmt.Errorf("directory created in dry-run mode")
					}

					return nil
				},
			},
		},
	}
}

// RepoAddLocalOnlyScenario tests local-only repository creation with grove repo add
func RepoAddLocalOnlyScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "repo-add-local-only",
		Description: "Tests 'grove repo add' creating repository locally without GitHub",
		Tags:        []string{"repo", "local"},
		Steps: []harness.Step{
			{
				Name:        "Create local repository",
				Description: "Tests local repository creation with 'grove repo add'",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")

					// Minimal setup
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"grove-*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")

					// Initialize as a git repository
					cmd := ctx.Command("git", "init")
					cmd.Dir(ecosystemDir)
					if result := cmd.Run(); result.Error != nil {
						return fmt.Errorf("failed to init git: %w", result.Error)
					}

					// Configure git
					cmd = ctx.Command("git", "config", "user.email", "test@example.com")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					cmd = ctx.Command("git", "config", "user.name", "Test User")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					// Add and commit
					cmd = ctx.Command("git", "add", ".")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					cmd = ctx.Command("git", "commit", "-m", "Initial commit")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					// Add mock make to PATH
					mockDir := ctx.NewDir("mocks")
					mockMake := filepath.Join(mockDir, "make")
					fs.WriteString(mockMake, "#!/bin/bash\necho 'make $1: SUCCESS'\nexit 0\n")
					os.Chmod(mockMake, 0755)

					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

					// Run grove repo add with --ecosystem
					groveCmd := ctx.Bin("repo", "add", "grove-test-local", "--ecosystem")
					groveCmd.Dir(ecosystemDir)

					result := groveCmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("command failed: %s\nStdout: %s", result.Stderr, result.Stdout)
					}

					// Verify local files were created
					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-local")); os.IsNotExist(err) {
						return fmt.Errorf("directory not created")
					}

					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-local", "go.mod")); os.IsNotExist(err) {
						return fmt.Errorf("go.mod not created")
					}

					// Verify grove.yml has correct name
					groveYmlPath := filepath.Join(ecosystemDir, "grove-test-local", "grove.yml")
					groveYmlContent, err := os.ReadFile(groveYmlPath)
					if err != nil {
						return fmt.Errorf("failed to read grove.yml: %w", err)
					}
					if !strings.Contains(string(groveYmlContent), "name: grove-test-local") {
						return fmt.Errorf("grove.yml does not contain expected name. Content:\n%s", string(groveYmlContent))
					}

					// Verify go.work was updated
					goWorkPath := filepath.Join(ecosystemDir, "go.work")
					goWorkContent, err := os.ReadFile(goWorkPath)
					if err != nil {
						return fmt.Errorf("failed to read go.work: %w", err)
					}
					if !strings.Contains(string(goWorkContent), "./grove-test-local") {
						return fmt.Errorf("go.work was not updated correctly. Content:\n%s", string(goWorkContent))
					}

					// Verify output mentions github-init for next steps
					combinedOutput := result.Stdout + result.Stderr
					if !strings.Contains(combinedOutput, "grove repo github-init") {
						return fmt.Errorf("expected hint about 'grove repo github-init' in output")
					}

					return nil
				},
			},
		},
	}
}

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

// RepoIncrementalWorkflowScenario tests the full incremental workflow:
// 1. Create local repo with 'grove repo add'
// 2. Initialize GitHub with 'grove repo github-init'
func RepoIncrementalWorkflowScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "repo-incremental-workflow",
		Description: "Tests the incremental workflow: local creation then GitHub init",
		Tags:        []string{"repo", "workflow", "github"},
		Steps: []harness.Step{
			{
				Name:        "Create local repository first",
				Description: "Use 'grove repo add' to create local repo",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")

					// Minimal setup
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"grove-*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")

					// Initialize git
					cmd := ctx.Command("git", "init")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					cmd = ctx.Command("git", "config", "user.email", "test@example.com")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					cmd = ctx.Command("git", "config", "user.name", "Test User")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					cmd = ctx.Command("git", "add", ".")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					cmd = ctx.Command("git", "commit", "-m", "Initial commit")
					cmd.Dir(ecosystemDir)
					cmd.Run()

					// Store ecosystem dir for next step
					ctx.Set("ecosystemDir", ecosystemDir)

					// Set up mocks
					mockDir := ctx.NewDir("mocks")
					mockMake := filepath.Join(mockDir, "make")
					fs.WriteString(mockMake, "#!/bin/bash\necho 'make $1: SUCCESS'\nexit 0\n")
					os.Chmod(mockMake, 0755)

					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

					// Step 1: Create local repo
					groveCmd := ctx.Bin("repo", "add", "grove-test-incremental",
						"--alias=gti",
						"--description=Incremental workflow test",
						"--ecosystem")
					groveCmd.Dir(ecosystemDir)

					result := groveCmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("repo add failed: %s", result.Stderr)
					}

					// Verify local repo exists
					repoDir := filepath.Join(ecosystemDir, "grove-test-incremental")
					if _, err := os.Stat(repoDir); os.IsNotExist(err) {
						return fmt.Errorf("repository directory not created")
					}

					// Verify grove.yml exists
					if _, err := os.Stat(filepath.Join(repoDir, "grove.yml")); os.IsNotExist(err) {
						return fmt.Errorf("grove.yml not created")
					}

					ctx.Set("repoDir", repoDir)
					return nil
				},
			},
			{
				Name:        "Initialize GitHub integration",
				Description: "Use 'grove repo github-init' to add GitHub",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repoDir").(string)
					ecosystemDir := ctx.Get("ecosystemDir").(string)

					// Set up GitHub mocks
					mockDir := ctx.NewDir("gh-mocks")

					// Mock gh
					ghMockPath := filepath.Join(mockDir, "gh")
					fs.WriteString(ghMockPath, ghMockScript)
					os.Chmod(ghMockPath, 0755)

					// Mock git to capture remote operations
					gitMockPath := filepath.Join(mockDir, "git")
					fs.WriteString(gitMockPath, gitMockScript)
					os.Chmod(gitMockPath, 0755)

					ghLogFile := filepath.Join(ecosystemDir, "gh-calls.log")
					os.Setenv("GH_MOCK_LOG", ghLogFile)
					os.Setenv("GROVE_PAT", "test-pat-12345")
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

					// Step 2: Initialize GitHub (dry-run to avoid actual API calls)
					groveCmd := ctx.Bin("repo", "github-init", "--dry-run")
					groveCmd.Dir(repoDir)

					result := groveCmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("github-init failed: %s\nStdout: %s", result.Stderr, result.Stdout)
					}

					// Verify dry-run output shows correct operations
					combinedOutput := result.Stdout + result.Stderr
					if !strings.Contains(combinedOutput, "DRY RUN MODE") {
						return fmt.Errorf("expected DRY RUN MODE in output")
					}
					if !strings.Contains(combinedOutput, "grove-test-incremental") {
						return fmt.Errorf("expected repo name in output")
					}

					ctx.ShowCommandOutput("GitHub init dry-run output", combinedOutput, "")
					return nil
				},
			},
		},
	}
}
