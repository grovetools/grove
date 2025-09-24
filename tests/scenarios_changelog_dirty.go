package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/git"
	"github.com/mattsolo1/grove-tend/pkg/harness"
	"github.com/mattsolo1/grove-tend/pkg/tui"
)

// ReleaseTUIChangelogWorkflowScenario tests the full TUI workflow for changelogs.
func ReleaseTUIChangelogWorkflowScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "release-tui-changelog-workflow",
		Description: "Tests generating and writing a changelog via the release TUI",
		Tags:        []string{"release", "tui", "changelog"},
		LocalOnly:   true, // Requires interactive TUI session
		Steps: []harness.Step{
			setupTestRepoWithChangesStep("tui-workflow-repo"),
			harness.NewStep("Launch TUI and wait for it to load", func(ctx *harness.Context) error {
				session, err := ctx.StartTUI(ctx.GroveBinary, "release", "tui", "--fresh")
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)
				return session.WaitForText("Grove Release Manager", 30*time.Second)
			}),
			harness.NewStep("Verify initial 'Pending' changelog status", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				return session.AssertContains("Pending")
			}),
			harness.NewStep("Generate changelog and verify UI update", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				// Send the generate key
				if err := session.SendKeys("g"); err != nil {
					return fmt.Errorf("failed to send 'g' key: %w", err)
				}
				// Wait for the LLM call to complete and UI to update
				return session.WaitForText("Generated", 15*time.Second)
			}),
			harness.NewStep("Write changelog and verify file creation", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				if err := session.SendKeys("w"); err != nil {
					return fmt.Errorf("failed to send 'w' key: %w", err)
				}
				// Wait a moment for the file to be written
				time.Sleep(2 * time.Second)
				// Check if the file exists
				changelogPath := filepath.Join(ctx.RootDir, "tui-workflow-repo", "CHANGELOG.md")
				if !fs.Exists(changelogPath) {
					return fmt.Errorf("changelog file not created at %s", changelogPath)
				}
				return nil
			}),
			harness.NewStep("Verify changelog content on disk", func(ctx *harness.Context) error {
				changelogPath := filepath.Join(ctx.RootDir, "tui-workflow-repo", "CHANGELOG.md")
				content, err := fs.ReadString(changelogPath)
				if err != nil {
					return fmt.Errorf("failed to read changelog: %w", err)
				}
				if !strings.Contains(content, "## v0.1.1") {
					return fmt.Errorf("changelog does not contain expected version header")
				}
				return nil
			}),
			harness.NewStep("Quit TUI", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				return session.SendKeys("q")
			}),
		},
	}
}

// ReleaseTUIChangelogDirtyStateScenario tests the TUI's ability to detect a manually modified changelog.
func ReleaseTUIChangelogDirtyStateScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "release-tui-changelog-dirty-state",
		Description: "Tests detection of manually modified changelogs in the TUI",
		Tags:        []string{"release", "tui", "changelog"},
		LocalOnly:   true,
		Steps: []harness.Step{
			setupTestRepoWithChangesStep("dirty-repo"),
			harness.NewStep("Launch TUI and generate changelog", func(ctx *harness.Context) error {
				session, err := ctx.StartTUI(ctx.GroveBinary, "release", "tui", "--fresh")
				if err != nil {
					return err
				}
				ctx.Set("tui_session", session)
				if err := session.WaitForText("Grove Release Manager", 30*time.Second); err != nil {
					return err
				}
				// Generate and write the initial changelog
				if err := session.SendKeys("g"); err != nil {
					return err
				}
				if err := session.WaitForText("Generated", 15*time.Second); err != nil {
					return err
				}
				if err := session.SendKeys("w"); err != nil {
					return err
				}
				// Wait for file to be written
				time.Sleep(2 * time.Second)
				changelogPath := filepath.Join(ctx.RootDir, "dirty-repo", "CHANGELOG.md")
				if !fs.Exists(changelogPath) {
					return fmt.Errorf("changelog file not created")
				}
				return nil
			}),
			harness.NewStep("Manually modify the changelog file", func(ctx *harness.Context) error {
				changelogPath := filepath.Join(ctx.RootDir, "dirty-repo", "CHANGELOG.md")
				content, err := fs.ReadString(changelogPath)
				if err != nil {
					return err
				}
				modifiedContent := content + "\n**Manual Edit:** This is a critical note.\n"
				return fs.WriteString(changelogPath, modifiedContent)
			}),
			harness.NewStep("Verify TUI shows 'Modified' status", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				// Send a key press to force a potential UI refresh
				if err := session.SendKeys("down", "up"); err != nil {
					return err
				}
				return session.WaitForText("Modified", 5*time.Second)
			}),
			harness.NewStep("Quit TUI", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				return session.SendKeys("q")
			}),
		},
	}
}

// setupTestRepoWithChangesStep is a helper to create a consistent test repo for changelog scenarios.
func setupTestRepoWithChangesStep(repoName string) harness.Step {
	return harness.NewStep("Setup test repository with changes", func(ctx *harness.Context) error {
		// Create ecosystem structure
		ecosystemDir := ctx.RootDir
		
		// Initialize git for ecosystem
		if err := git.Init(ecosystemDir); err != nil {
			return fmt.Errorf("failed to init ecosystem git: %w", err)
		}
		if err := git.SetupTestConfig(ecosystemDir); err != nil {
			return fmt.Errorf("failed to setup ecosystem git config: %w", err)
		}
		
		// Create grove.yml for ecosystem
		if err := fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"),
			"name: test-ecosystem\nworkspaces:\n  - \"*\"\n"); err != nil {
			return err
		}
		
		// Commit ecosystem root
		if err := git.Add(ecosystemDir, "."); err != nil {
			return err
		}
		if err := git.Commit(ecosystemDir, "Initial ecosystem setup"); err != nil {
			return err
		}
		
		// Create test repo subdirectory
		repoDir := filepath.Join(ecosystemDir, repoName)
		if err := os.Mkdir(repoDir, 0755); err != nil {
			return err
		}
		
		// Create mock gemapi
		mockDir := ctx.NewDir("mocks")
		gemapiMockPath := filepath.Join(mockDir, "gemapi")
		if err := fs.WriteString(gemapiMockPath, gemapiMockScript); err != nil {
			return err
		}
		if err := os.Chmod(gemapiMockPath, 0755); err != nil {
			return err
		}
		os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
		
		// Init Git repo
		if err := git.Init(repoDir); err != nil {
			return err
		}
		if err := git.SetupTestConfig(repoDir); err != nil {
			return err
		}
		
		// Create files
		if err := fs.WriteString(filepath.Join(repoDir, "grove.yml"), 
			fmt.Sprintf("name: %s\ntype: go\n", repoName)); err != nil {
			return err
		}
		if err := fs.WriteString(filepath.Join(repoDir, "go.mod"), 
			fmt.Sprintf("module %s\ngo 1.21", repoName)); err != nil {
			return err
		}
		
		// Initial commit and tag
		if err := git.Add(repoDir, "."); err != nil {
			return err
		}
		if err := git.Commit(repoDir, "Initial commit"); err != nil {
			return err
		}
		if result := command.New("git", "tag", "v0.1.0").Dir(repoDir).Run(); result.Error != nil {
			return result.Error
		}
		
		// Add a new commit to create changes for the release
		if err := fs.WriteString(filepath.Join(repoDir, "main.go"), 
			"package main\nfunc main() {}"); err != nil {
			return err
		}
		if err := git.Add(repoDir, "."); err != nil {
			return err
		}
		if err := git.Commit(repoDir, "feat: add main function"); err != nil {
			return err
		}
		
		return nil
	})
}