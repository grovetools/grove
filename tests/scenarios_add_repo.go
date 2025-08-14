package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// AddRepoDryRunScenario tests the dry-run functionality
func AddRepoDryRunScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "add-repo-dry-run",
		Description: "Verifies dry-run shows intended actions without executing",
		Tags:        []string{"add-repo", "dry-run"},
		Steps: []harness.Step{
			{
				Name:        "Setup and run dry-run",
				Description: "Setup mock ecosystem and run add-repo with --dry-run",
				Func: func(ctx *harness.Context) error {
					// Create a minimal mock ecosystem
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Create minimal files
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"grove-*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")
					fs.WriteString(filepath.Join(ecosystemDir, "Makefile"), 
						"PACKAGES = grove-core\n# GROVE-META:ADD-REPO:PACKAGES\n\nBINARIES = grove\n# GROVE-META:ADD-REPO:BINARIES\n")
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					// Set required env
					os.Setenv("GROVE_PAT", "test-pat")
					
					// Set up mocks directory
					mockDir := ctx.NewDir("mocks")
					
					// Create gh mock
					ghMockPath := filepath.Join(mockDir, "gh")
					fs.WriteString(ghMockPath, ghMockScript)
					os.Chmod(ghMockPath, 0755)
					
					// Set PATH to use our mocks
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					cmd := command.New(groveBinary, "add-repo", "grove-test-dryrun",
						"--alias=gtdr",
						"--dry-run")
					
					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("dry-run failed with exit code %d: %s\nStdout: %s", result.ExitCode, result.Stderr, result.Stdout)
					}
					
					// Verify output - check both stdout and stderr as grove uses logging
					combinedOutput := result.Stdout + result.Stderr
					if !strings.Contains(combinedOutput, "DRY RUN MODE") {
						return fmt.Errorf("expected DRY RUN MODE in output, got:\nStdout: %s\nStderr: %s", result.Stdout, result.Stderr)
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

// AddRepoWithGitHubScenario tests full repository creation with GitHub integration using mocks
func AddRepoWithGitHubScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "add-repo-with-github",
		Description: "Tests creating repository with full GitHub integration using mocks",
		Tags:        []string{"add-repo", "github"},
		Steps: []harness.Step{
			{
				Name:        "Create repository with GitHub integration",
				Description: "Tests full repository creation flow with mocked GitHub",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Minimal setup
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"grove-*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")
					fs.WriteString(filepath.Join(ecosystemDir, "Makefile"), 
						"PACKAGES = grove-core\n# GROVE-META:ADD-REPO:PACKAGES\n\nBINARIES = grove\n# GROVE-META:ADD-REPO:BINARIES\n")
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					os.Setenv("GROVE_PAT", "test-pat-12345")
					
					// Set up mocks directory with both gh and make
					mockDir := ctx.NewDir("mocks")
					
					// Create gh mock
					ghMockPath := filepath.Join(mockDir, "gh")
					fs.WriteString(ghMockPath, ghMockScript)
					os.Chmod(ghMockPath, 0755)
					
					// Create make mock
					makeMockPath := filepath.Join(mockDir, "make")
					fs.WriteString(makeMockPath, makeMockScript)
					os.Chmod(makeMockPath, 0755)
					
					// Create git mock to avoid actual git operations
					gitMockPath := filepath.Join(mockDir, "git")
					fs.WriteString(gitMockPath, gitMockScript)
					os.Chmod(gitMockPath, 0755)
					
					// Create go mock to handle go mod operations
					goMockPath := filepath.Join(mockDir, "go")
					fs.WriteString(goMockPath, goMockScript)
					os.Chmod(goMockPath, 0755)
					
					// Create gofmt mock
					gofmtMockPath := filepath.Join(mockDir, "gofmt")
					fs.WriteString(gofmtMockPath, gofmtMockScript)
					os.Chmod(gofmtMockPath, 0755)
					
					// Set PATH to use our mocks
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					
					// Set mock log files for verification
					ghLogFile := filepath.Join(ecosystemDir, "gh-calls.log")
					os.Setenv("GH_MOCK_LOG", ghLogFile)
					
					groveBinary := ctx.GroveBinary
					cmd := command.New(groveBinary, "add-repo", "grove-test-github",
						"--alias=gtg",
						"--description=Test repository with GitHub")
					
					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("command failed: %s\nStdout: %s", result.Stderr, result.Stdout)
					}
					
					// Verify the expected gh commands were called
					ghLog, err := fs.ReadString(ghLogFile)
					if err != nil {
						return fmt.Errorf("failed to read gh log: %w", err)
					}
					
					expectedCalls := []string{
						"gh auth status",
						"gh repo view mattsolo1/grove-test-github",
						"gh repo create mattsolo1/grove-test-github",
						"gh secret set GROVE_PAT",
						"gh api repos/mattsolo1/grove-core/releases/latest",
						"gh api repos/mattsolo1/grove-tend/releases/latest",
					}
					
					for _, expected := range expectedCalls {
						if !strings.Contains(ghLog, expected) {
							return fmt.Errorf("expected gh call '%s' not found in log:\n%s", expected, ghLog)
						}
					}
					
					// Verify local files were created
					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-github")); os.IsNotExist(err) {
						return fmt.Errorf("directory not created")
					}
					
					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-github", "go.mod")); os.IsNotExist(err) {
						return fmt.Errorf("go.mod not created")
					}
					
					ctx.ShowCommandOutput("GitHub CLI calls", ghLog, "")
					
					return nil
				},
			},
		},
	}
}

// AddRepoSkipGitHubScenario tests local-only repository creation  
func AddRepoSkipGitHubScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "add-repo-skip-github",
		Description: "Tests creating repository locally without GitHub integration",
		Tags:        []string{"add-repo", "local"},
		Steps: []harness.Step{
			{
				Name:        "Create repository with --skip-github",
				Description: "Tests local repository creation only",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Minimal setup
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"grove-*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")
					fs.WriteString(filepath.Join(ecosystemDir, "Makefile"), 
						"PACKAGES = grove-core\n# GROVE-META:ADD-REPO:PACKAGES\n\nBINARIES = grove\n# GROVE-META:ADD-REPO:BINARIES\n")
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					os.Setenv("GROVE_PAT", "test-pat")
					
					// Add mock make to PATH for local verification
					mockDir := ctx.NewDir("mocks")
					
					// Create a simple mock make that succeeds
					mockMake := filepath.Join(mockDir, "make")
					fs.WriteString(mockMake, "#!/bin/bash\necho 'make $1: SUCCESS'\nexit 0\n")
					os.Chmod(mockMake, 0755)
					
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					
					groveBinary := ctx.GroveBinary
					cmd := command.New(groveBinary, "add-repo", "grove-test-local",
						"--alias=gtl", 
						"--skip-github")
					
					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("command failed: %s", result.Stderr)
					}
					
					// Verify local files were created
					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-local")); os.IsNotExist(err) {
						return fmt.Errorf("directory not created")
					}
					
					if _, err := os.Stat(filepath.Join(ecosystemDir, "grove-test-local", "go.mod")); os.IsNotExist(err) {
						return fmt.Errorf("go.mod not created")
					}
					
					return nil
				},
			},
		},
	}
}