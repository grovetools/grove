package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/tend/pkg/harness"
)

// setupMockEcosystemStep is a reusable step to create a consistent test ecosystem.
func setupMockEcosystemStep() harness.Step {
	return harness.NewStep("Setup mock ecosystem", func(ctx *harness.Context) error {
		ecosystemDir := ctx.NewDir("mock-ecosystem")
		ctx.Set("ecosystem_dir", ecosystemDir)

		// Setup global grove config for workspace discovery
		if err := setupGlobalGroveConfig(ctx, ctx.RootDir); err != nil {
			return err
		}

		// Ensure the directory exists
		if err := os.MkdirAll(ecosystemDir, 0755); err != nil {
			return fmt.Errorf("failed to create ecosystem dir: %w", err)
		}

		// Initialize ecosystem root as git repo
		ecoGit := func(args ...string) error {
			cmd := ctx.Command("git", args...).Dir(ecosystemDir)
			res := cmd.Run()
			return res.Error
		}

		if err := ecoGit("init"); err != nil {
			return err
		}

		// Create ecosystem grove.yml
		groveYmlContent := "name: grove-ecosystem\nworkspaces:\n  - \"*\"\n"
		if err := os.WriteFile(filepath.Join(ecosystemDir, "grove.yml"), []byte(groveYmlContent), 0644); err != nil {
			return err
		}

		if err := ecoGit("add", "grove.yml"); err != nil {
			return err
		}
		if err := ecoGit("commit", "-m", "Initial ecosystem setup"); err != nil {
			return err
		}

		// Create mock repositories
		repos := map[string]string{
			"lib-a": "",
			"app-b": "require github.com/test/lib-a v0.1.0",
		}

		for name, goModRequire := range repos {
			repoDir := filepath.Join(ecosystemDir, name)
			ctx.Set(name+"_dir", repoDir)
			os.MkdirAll(repoDir, 0755)

			// Create files
			groveContent := fmt.Sprintf("name: %s\n", name)
			if err := os.WriteFile(filepath.Join(repoDir, "grove.yml"), []byte(groveContent), 0644); err != nil {
				return err
			}

			goModContent := fmt.Sprintf("module github.com/test/%s\n\ngo 1.21\n\n%s", name, goModRequire)
			if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte(goModContent), 0644); err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Join(repoDir, ".github/workflows"), 0755); err != nil {
				return err
			}

			releaseYaml := "name: Release\non:\n  push:\n    tags: ['v*']"
			if err := os.WriteFile(filepath.Join(repoDir, ".github/workflows/release.yml"), []byte(releaseYaml), 0644); err != nil {
				return err
			}

			// Use mock git to initialize and create commits
			git := func(args ...string) error {
				cmd := ctx.Command("git", args...).Dir(repoDir)
				res := cmd.Run()
				return res.Error
			}

			if err := git("init"); err != nil {
				return err
			}
			if err := git("add", "."); err != nil {
				return err
			}
			if err := git("commit", "-m", "Initial commit"); err != nil {
				return err
			}
			if err := git("tag", "v0.1.0"); err != nil {
				return err
			}

			// Add a new commit to make the repo "dirty" for release
			mainGoContent := "package main\n\nfunc main() {}"
			if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte(mainGoContent), 0644); err != nil {
				return err
			}
			if err := git("add", "."); err != nil {
				return err
			}
			if err := git("commit", "-m", "feat: new feature"); err != nil {
				return err
			}

			// Add as submodule to ecosystem
			if err := ecoGit("submodule", "add", "./"+name); err != nil {
				return err
			}
		}

		// Commit submodules to ecosystem
		if err := ecoGit("add", ".gitmodules"); err != nil {
			return err
		}
		if err := ecoGit("commit", "-m", "Add project submodules"); err != nil {
			return err
		}

		return nil
	})
}

// ReleasePlan represents the structure of a release plan
type ReleasePlan struct {
	Type  string     `json:"type"`
	Repos []RepoInfo `json:"repos"`
}

// RepoInfo represents information about a repository in the release plan
type RepoInfo struct {
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
	Status   string `json:"status"`
	Version  string `json:"version"`
}

// StreamlinedFullReleaseScenario tests the complete workflow for a full release.
func StreamlinedFullReleaseScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "streamlined-full-release",
		Description: "Tests the full, multi-stage release workflow with changelogs.",
		Tags:        []string{"release", "refactor", "full"},
		Steps: []harness.Step{
			harness.SetupMocks(
				harness.Mock{CommandName: "git"},
				harness.Mock{CommandName: "gh"},
				harness.Mock{CommandName: "grove-gemini"},
				harness.Mock{CommandName: "go"},
			),
			setupMockEcosystemStep(),
			harness.NewStep("Run 'grove release plan'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				
				// Change to ecosystem directory for the command
				cmd := ctx.Command("grove", "release", "plan", "--llm-changelog").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir)
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release plan failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				// Assert that release_plan.json exists
				planPath := filepath.Join(ecosystemDir, ".grove", "release_plan.json")
				if _, err := os.Stat(planPath); os.IsNotExist(err) {
					return fmt.Errorf("release_plan.json not created at %s", planPath)
				}

				// Load and check the plan type
				planData, err := os.ReadFile(planPath)
				if err != nil {
					return fmt.Errorf("failed to read release plan: %v", err)
				}

				var plan ReleasePlan
				if err := json.Unmarshal(planData, &plan); err != nil {
					return fmt.Errorf("failed to parse release plan: %v", err)
				}

				if plan.Type != "full" {
					return fmt.Errorf("expected plan type 'full', got '%s'", plan.Type)
				}

				// Check for staged changelogs
				stagingDir := filepath.Join(ctx.RootDir, ".grove", "release_staging")
				for _, repo := range []string{"lib-a", "app-b"} {
					changelogPath := filepath.Join(stagingDir, repo, "CHANGELOG.md")
					if _, err := os.Stat(changelogPath); os.IsNotExist(err) {
						return fmt.Errorf("staged CHANGELOG.md not found at %s", changelogPath)
					}
				}

				ctx.Set("release_plan_path", planPath)
				return nil
			}),
			harness.NewStep("Simulate TUI Review & Approval", func(ctx *harness.Context) error {
				planPath := ctx.Get("release_plan_path").(string)
				ecosystemDir := ctx.Get("ecosystem_dir").(string)

				// Load the plan
				planData, err := os.ReadFile(planPath)
				if err != nil {
					return fmt.Errorf("failed to read release plan: %v", err)
				}

				var plan ReleasePlan
				if err := json.Unmarshal(planData, &plan); err != nil {
					return fmt.Errorf("failed to parse release plan: %v", err)
				}

				// Approve all repos
				for i := range plan.Repos {
					plan.Repos[i].Selected = true
					plan.Repos[i].Status = "Approved"
				}

				// Copy staged changelogs to repos and commit
				stagingDir := filepath.Join(ctx.RootDir, ".grove", "release_staging")
				for _, repo := range []string{"lib-a", "app-b"} {
					stagedChangelog := filepath.Join(stagingDir, repo, "CHANGELOG.md")
					repoChangelog := filepath.Join(ecosystemDir, repo, "CHANGELOG.md")
					
					content, err := os.ReadFile(stagedChangelog)
					if err != nil {
						return fmt.Errorf("failed to read staged changelog: %v", err)
					}
					
					if err := os.WriteFile(repoChangelog, content, 0644); err != nil {
						return fmt.Errorf("failed to write changelog: %v", err)
					}

					// Commit the changelog
					repoDir := filepath.Join(ecosystemDir, repo)
					git := func(args ...string) error {
						cmd := ctx.Command("git", args...).Dir(repoDir)
						res := cmd.Run()
						return res.Error
					}

					if err := git("add", "CHANGELOG.md"); err != nil {
						return err
					}
					if err := git("commit", "-m", "docs: update changelog"); err != nil {
						return err
					}
				}

				// Save the modified plan
				modifiedPlanData, err := json.Marshal(plan)
				if err != nil {
					return fmt.Errorf("failed to marshal modified plan: %v", err)
				}

				if err := os.WriteFile(planPath, modifiedPlanData, 0644); err != nil {
					return fmt.Errorf("failed to save modified plan: %v", err)
				}

				return nil
			}),
			harness.NewStep("Run 'grove release apply'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				planPath := ctx.Get("release_plan_path").(string)

				// Set environment for successful CI
				cmd := ctx.Command("grove", "release", "apply").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir).
					Env("GH_MOCK_CI_STATUS", "success")
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release apply failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				// Assert that release_plan.json is deleted
				if _, err := os.Stat(planPath); !os.IsNotExist(err) {
					return fmt.Errorf("release_plan.json should be deleted after successful apply")
				}

				// Check that go.mod was updated in app-b
				appBGoMod := filepath.Join(ecosystemDir, "app-b", "go.mod")
				content, err := os.ReadFile(appBGoMod)
				if err != nil {
					return fmt.Errorf("failed to read app-b go.mod: %v", err)
				}

				// The version should have been updated from v0.1.0
				if !contains(string(content), "github.com/test/lib-a v0.1.1") {
					return fmt.Errorf("app-b go.mod not updated correctly")
				}

				return nil
			}),
		},
	}
}

// StreamlinedRCReleaseScenario tests the streamlined release candidate workflow.
func StreamlinedRCReleaseScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "streamlined-rc-release",
		Description: "Tests the Release Candidate (RC) workflow, skipping changelogs.",
		Tags:        []string{"release", "refactor", "rc"},
		Steps: []harness.Step{
			harness.SetupMocks(
				harness.Mock{CommandName: "git"},
				harness.Mock{CommandName: "gh"},
				harness.Mock{CommandName: "grove-gemini"},
				harness.Mock{CommandName: "go"},
			),
			setupMockEcosystemStep(),
			harness.NewStep("Run 'grove release plan --rc'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				
				cmd := ctx.Command("grove", "release", "plan", "--rc").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir)
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release plan --rc failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				// Assert that release_plan.json exists
				planPath := filepath.Join(ecosystemDir, ".grove", "release_plan.json")
				if _, err := os.Stat(planPath); os.IsNotExist(err) {
					return fmt.Errorf("release_plan.json not created at %s", planPath)
				}

				// Load and check the plan type
				planData, err := os.ReadFile(planPath)
				if err != nil {
					return fmt.Errorf("failed to read release plan: %v", err)
				}

				var plan ReleasePlan
				if err := json.Unmarshal(planData, &plan); err != nil {
					return fmt.Errorf("failed to parse release plan: %v", err)
				}

				if plan.Type != "rc" {
					return fmt.Errorf("expected plan type 'rc', got '%s'", plan.Type)
				}

				// Assert that staging directory does NOT exist
				stagingDir := filepath.Join(ctx.RootDir, ".grove", "release_staging")
				if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
					return fmt.Errorf("staging directory should not exist for RC releases")
				}

				ctx.Set("release_plan_path", planPath)
				return nil
			}),
			harness.NewStep("Simulate Approval", func(ctx *harness.Context) error {
				planPath := ctx.Get("release_plan_path").(string)

				// Load the plan
				planData, err := os.ReadFile(planPath)
				if err != nil {
					return fmt.Errorf("failed to read release plan: %v", err)
				}

				var plan ReleasePlan
				if err := json.Unmarshal(planData, &plan); err != nil {
					return fmt.Errorf("failed to parse release plan: %v", err)
				}

				// Approve all repos
				for i := range plan.Repos {
					plan.Repos[i].Selected = true
					plan.Repos[i].Status = "Approved"
				}

				// Save the modified plan
				modifiedPlanData, err := json.Marshal(plan)
				if err != nil {
					return fmt.Errorf("failed to marshal modified plan: %v", err)
				}

				if err := os.WriteFile(planPath, modifiedPlanData, 0644); err != nil {
					return fmt.Errorf("failed to save modified plan: %v", err)
				}

				return nil
			}),
			harness.NewStep("Run 'grove release apply'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				planPath := ctx.Get("release_plan_path").(string)

				cmd := ctx.Command("grove", "release", "apply").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir).
					Env("GH_MOCK_CI_STATUS", "success")
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release apply failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				// Assert that release_plan.json is deleted
				if _, err := os.Stat(planPath); !os.IsNotExist(err) {
					return fmt.Errorf("release_plan.json should be deleted after successful apply")
				}

				// Check that NO changelog commits were made
				// In a real test, we'd check git log, but for now we can verify
				// that no CHANGELOG.md files exist in the repos
				for _, repo := range []string{"lib-a", "app-b"} {
					changelogPath := filepath.Join(ecosystemDir, repo, "CHANGELOG.md")
					if _, err := os.Stat(changelogPath); !os.IsNotExist(err) {
						return fmt.Errorf("CHANGELOG.md should not exist in %s for RC release", repo)
					}
				}

				return nil
			}),
		},
	}
}

// StreamlinedFailureScenario tests failure handling and rollback
func StreamlinedFailureScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "streamlined-failure-rollback",
		Description: "Tests failure handling with undo-tag and clear-plan commands.",
		Tags:        []string{"release", "refactor", "failure"},
		Steps: []harness.Step{
			harness.SetupMocks(
				harness.Mock{CommandName: "git"},
				harness.Mock{CommandName: "gh"},
				harness.Mock{CommandName: "grove-gemini"},
				harness.Mock{CommandName: "go"},
			),
			setupMockEcosystemStep(),
			harness.NewStep("Run 'grove release plan'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				
				cmd := ctx.Command("grove", "release", "plan", "--llm-changelog").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir)
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release plan failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				planPath := filepath.Join(ecosystemDir, ".grove", "release_plan.json")
				ctx.Set("release_plan_path", planPath)
				return nil
			}),
			harness.NewStep("Simulate Approval", func(ctx *harness.Context) error {
				planPath := ctx.Get("release_plan_path").(string)
				ecosystemDir := ctx.Get("ecosystem_dir").(string)

				// Load and approve the plan
				planData, err := os.ReadFile(planPath)
				if err != nil {
					return err
				}

				var plan ReleasePlan
				if err := json.Unmarshal(planData, &plan); err != nil {
					return err
				}

				for i := range plan.Repos {
					plan.Repos[i].Selected = true
					plan.Repos[i].Status = "Approved"
				}

				// Copy staged changelogs and commit
				stagingDir := filepath.Join(ctx.RootDir, ".grove", "release_staging")
				for _, repo := range []string{"lib-a", "app-b"} {
					stagedChangelog := filepath.Join(stagingDir, repo, "CHANGELOG.md")
					repoChangelog := filepath.Join(ecosystemDir, repo, "CHANGELOG.md")
					
					content, err := os.ReadFile(stagedChangelog)
					if err != nil {
						return err
					}
					
					if err := os.WriteFile(repoChangelog, content, 0644); err != nil {
						return err
					}

					repoDir := filepath.Join(ecosystemDir, repo)
					git := func(args ...string) error {
						cmd := ctx.Command("git", args...).Dir(repoDir)
						res := cmd.Run()
						return res.Error
					}

					if err := git("add", "CHANGELOG.md"); err != nil {
						return err
					}
					if err := git("commit", "-m", "docs: update changelog"); err != nil {
						return err
					}
				}

				modifiedPlanData, err := json.Marshal(plan)
				if err != nil {
					return err
				}

				return os.WriteFile(planPath, modifiedPlanData, 0644)
			}),
			harness.NewStep("Run 'grove release apply' with failing CI", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)

				// Set environment for FAILING CI
				cmd := ctx.Command("grove", "release", "apply").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir).
					Env("GH_MOCK_CI_STATUS", "failure")
				
				res := cmd.Run()
				// We expect this to fail
				if res.Error == nil {
					return fmt.Errorf("grove release apply should have failed with CI failure")
				}

				return nil
			}),
			harness.NewStep("Run 'grove release undo-tag'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)

				cmd := ctx.Command("grove", "release", "undo-tag").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir)
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release undo-tag failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				// Verify tags were removed
				// In the real implementation, we'd check the .mock-git/TAGS file
				
				return nil
			}),
			harness.NewStep("Run 'grove release clear-plan'", func(ctx *harness.Context) error {
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				planPath := ctx.Get("release_plan_path").(string)

				cmd := ctx.Command("grove", "release", "clear-plan").
					Dir(ecosystemDir).
					Env("HOME", ctx.RootDir)
				
				res := cmd.Run()
				if res.Error != nil {
					return fmt.Errorf("grove release clear-plan failed: %v\nOutput: %s", res.Error, res.Stdout)
				}

				// Assert that release_plan.json is deleted
				if _, err := os.Stat(planPath); !os.IsNotExist(err) {
					return fmt.Errorf("release_plan.json should be deleted after clear-plan")
				}

				// Assert that staging directory is deleted
				stagingDir := filepath.Join(ctx.RootDir, ".grove", "release_staging")
				if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
					return fmt.Errorf("staging directory should be deleted after clear-plan")
				}

				return nil
			}),
		},
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(substr)] == substr ||
		len(s) > len(substr) && s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && len(substr) > 0 && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}