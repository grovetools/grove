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

// WorkspaceBootstrappingScenario tests the entire bootstrapping process from scratch.
func WorkspaceBootstrappingScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "workspace-bootstrapping",
		Description: "Tests the full bootstrapping workflow: init ecosystem, then add the first repo.",
		Tags:        []string{"bootstrap", "init", "add-repo"},
		Steps: []harness.Step{
			{
				Name:        "Initialize new ecosystem",
				Description: "Runs 'grove ws init' and verifies the creation of the ecosystem skeleton.",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("bootstrap-ecosystem")
					ctx.Set("ecosystem_dir", ecosystemDir)
					
					// Create the directory first
					if err := os.MkdirAll(ecosystemDir, 0755); err != nil {
						return fmt.Errorf("failed to create ecosystem directory: %w", err)
					}

					cmd := command.New(ctx.GroveBinary, "ws", "init").Dir(ecosystemDir)
					result := cmd.Run()
					if result.Error != nil {
						return fmt.Errorf("'grove ws init' failed: %w\n%s", result.Error, result.Stderr)
					}

					// Verify all expected files and directories are created
					expectedPaths := []string{
						"grove.yml",
						"go.work",
						"Makefile",
						".gitignore",
						".git",
					}
					for _, p := range expectedPaths {
						if !fs.Exists(filepath.Join(ecosystemDir, p)) {
							return fmt.Errorf("expected path '%s' was not created by 'ws init'", p)
						}
					}
					return nil
				},
			},
			{
				Name:        "Add first repository to ecosystem",
				Description: "Runs 'grove add-repo --ecosystem' to add the first project.",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.GetString("ecosystem_dir")
					repoName := "my-first-tool"

					// We use --skip-github to keep the test local and fast.
					cmd := command.New(ctx.GroveBinary, "add-repo", repoName,
						"--alias", "mft",
						"--ecosystem",
						"--skip-github").Dir(ecosystemDir)

					result := cmd.Run()
					if result.Error != nil {
						return fmt.Errorf("'grove add-repo' failed: %w\n%s", result.Error, result.Stderr)
					}

					// Verify the repository directory was created
					repoPath := filepath.Join(ecosystemDir, repoName)
					if !fs.Exists(repoPath) {
						return fmt.Errorf("repository directory '%s' was not created", repoPath)
					}
					if !fs.Exists(filepath.Join(repoPath, "grove.yml")) {
						return fmt.Errorf("repository '%s' is missing grove.yml", repoName)
					}
					return nil
				},
			},
			{
				Name:        "Verify ecosystem integration files",
				Description: "Asserts the content of root files like go.work and .gitmodules.",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.GetString("ecosystem_dir")
					repoName := "my-first-tool"

					// 1. Verify go.work content
					goWorkPath := filepath.Join(ecosystemDir, "go.work")
					goWorkContent, err := os.ReadFile(goWorkPath)
					if err != nil {
						return fmt.Errorf("failed to read go.work: %w", err)
					}
					expectedGoWorkUse := fmt.Sprintf("./%s", repoName)
					if !strings.Contains(string(goWorkContent), expectedGoWorkUse) {
						return fmt.Errorf("go.work was not updated correctly. Expected to find '%s' in:\n%s", expectedGoWorkUse, string(goWorkContent))
					}

					// 2. Verify .gitmodules content for submodule integration
					// Note: --skip-github uses a local path for the submodule URL.
					gitModulesPath := filepath.Join(ecosystemDir, ".gitmodules")
					gitModulesContent, err := os.ReadFile(gitModulesPath)
					if err != nil {
						return fmt.Errorf("failed to read .gitmodules: %w", err)
					}
					expectedSubmoduleEntry := fmt.Sprintf("[submodule \"%s\"]", repoName)
					expectedSubmoduleURL := fmt.Sprintf("url = ./%s", repoName)
					if !strings.Contains(string(gitModulesContent), expectedSubmoduleEntry) || !strings.Contains(string(gitModulesContent), expectedSubmoduleURL) {
						return fmt.Errorf(".gitmodules was not updated correctly. Expected to find submodule entry for '%s' in:\n%s", repoName, string(gitModulesContent))
					}
					
					// 3. Verify Makefile was NOT changed (as per current implementation)
					makefileContent, err := os.ReadFile(filepath.Join(ecosystemDir, "Makefile"))
					if err != nil {
						return fmt.Errorf("failed to read Makefile: %w", err)
					}
					if strings.Contains(string(makefileContent), repoName) {
						return fmt.Errorf("Makefile should not have been modified, but it contains the new repo name")
					}

					return nil
				},
			},
		},
	}
}