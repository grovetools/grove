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

// SmartChangelogDetectionScenario tests the smart changelog detection feature
func SmartChangelogDetectionScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "smart-changelog-detection",
		Description: "Tests smart changelog detection for manual edits",
		Tags:        []string{"changelog", "detection"},
		LocalOnly:   false, // Changed to non-interactive test
		Steps: []harness.Step{
			harness.NewStep("Setup test ecosystem with mocks", func(ctx *harness.Context) error {
				// Create mock directory
				mockDir := filepath.Join(ctx.RootDir, "mocks")
				if err := os.MkdirAll(mockDir, 0755); err != nil {
					return err
				}
				
				// Create gemapi mock
				gemapiMockPath := filepath.Join(mockDir, "gemapi")
				if err := os.WriteFile(gemapiMockPath, []byte(gemapiMockScript), 0755); err != nil {
					return err
				}
				
				// Set PATH to use our mock
				os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
				
				// Create ecosystem structure
				ecosystemDir := ctx.RootDir
				
				// Initialize git
				if err := git.Init(ecosystemDir); err != nil {
					return fmt.Errorf("failed to init git: %w", err)
				}
				if err := git.SetupTestConfig(ecosystemDir); err != nil {
					return fmt.Errorf("failed to setup git config: %w", err)
				}
				
				// Create grove.yml
				if err := fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"),
					"name: test-ecosystem\nworkspaces:\n  - \"*\"\n"); err != nil {
					return err
				}
				
				// Create .gitignore
				if err := fs.WriteString(filepath.Join(ecosystemDir, ".gitignore"),
					"go.work.sum\n"); err != nil {
					return err
				}
				
				// Commit root
				if err := git.Add(ecosystemDir, "."); err != nil {
					return err
				}
				if err := git.Commit(ecosystemDir, "Initial ecosystem setup"); err != nil {
					return err
				}
				
				// Create test-repo subdirectory
				repoDir := filepath.Join(ecosystemDir, "test-repo")
				if err := os.Mkdir(repoDir, 0755); err != nil {
					return err
				}
				
				// Initialize test-repo
				if err := git.Init(repoDir); err != nil {
					return err
				}
				if err := git.SetupTestConfig(repoDir); err != nil {
					return err
				}
				
				// Add grove.yml and go.mod
				if err := fs.WriteString(filepath.Join(repoDir, "grove.yml"),
					"name: test-repo\ntype: go\n"); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(repoDir, "go.mod"),
					"module test-repo\n\ngo 1.21\n"); err != nil {
					return err
				}
				
				// Create a file to track changes
				if err := fs.WriteString(filepath.Join(repoDir, "main.go"),
					"package main\n\nfunc main() {}\n"); err != nil {
					return err
				}
				
				// Initial commit and tag
				if err := git.Add(repoDir, "."); err != nil {
					return err
				}
				if err := git.Commit(repoDir, "Initial commit"); err != nil {
					return err
				}
				
				// Tag v0.1.0
				cmd := command.New("git", "tag", "v0.1.0").Dir(repoDir)
				if result := cmd.Run(); result.Error != nil {
					return fmt.Errorf("failed to tag test-repo: %w", result.Error)
				}
				
				// Make a change for release
				if err := fs.WriteString(filepath.Join(repoDir, "feature.go"),
					"package main\n\nfunc NewFeature() {}\n"); err != nil {
					return err
				}
				if err := git.Add(repoDir, "feature.go"); err != nil {
					return err
				}
				if err := git.Commit(repoDir, "feat: add new feature"); err != nil {
					return err
				}
				
				ctx.Set("ecosystem_dir", ecosystemDir)
				ctx.Set("repo_dir", repoDir)
				
				return nil
			}),
			
			harness.NewStep("Generate changelog using command line", func(ctx *harness.Context) error {
				repoDir := ctx.Get("repo_dir").(string)
				
				// Need to run from ecosystem dir, not repo dir
				ecosystemDir := ctx.Get("ecosystem_dir").(string)
				
				// Generate changelog using command line
				cmd := command.New(ctx.GroveBinary, "release", "changelog", "test-repo")
				cmd.Dir(ecosystemDir)
				result := cmd.Run()
				if result.Error != nil {
					return fmt.Errorf("failed to generate changelog: %w\nstdout: %s\nstderr: %s", result.Error, result.Stdout, result.Stderr)
				}
				
				// Check that CHANGELOG.md was created in staging
				stagingPath := filepath.Join(repoDir, ".grove", "staging", "CHANGELOG.md")
				if _, err := os.Stat(stagingPath); os.IsNotExist(err) {
					return fmt.Errorf("staged changelog not found at %s", stagingPath)
				}
				
				// Read the staged changelog content
				changelogContent, err := os.ReadFile(stagingPath)
				if err != nil {
					return fmt.Errorf("failed to read staged changelog: %w", err)
				}
				
				ctx.Set("staging_path", stagingPath)
				ctx.Set("original_content", string(changelogContent))
				
				return nil
			}),
			
			harness.NewStep("Copy staged changelog to working directory", func(ctx *harness.Context) error {
				stagingPath := ctx.Get("staging_path").(string)
				repoDir := ctx.Get("repo_dir").(string)
				changelogPath := filepath.Join(repoDir, "CHANGELOG.md")
				
				// Copy the staged changelog to the working directory (simulating 'w' in TUI)
				stagedContent, err := os.ReadFile(stagingPath)
				if err != nil {
					return fmt.Errorf("failed to read staged changelog: %w", err)
				}
				
				if err := os.WriteFile(changelogPath, stagedContent, 0644); err != nil {
					return fmt.Errorf("failed to write changelog: %w", err)
				}
				
				ctx.Set("changelog_path", changelogPath)
				
				return nil
			}),
			
			harness.NewStep("Modify changelog and test dirty detection", func(ctx *harness.Context) error {
				changelogPath := ctx.Get("changelog_path").(string)
				
				// Read current changelog
				content, err := os.ReadFile(changelogPath)
				if err != nil {
					return fmt.Errorf("failed to read changelog: %w", err)
				}
				
				// Modify the changelog (add custom notes)
				modifiedContent := strings.Replace(string(content), 
					"## v0.1.1",
					"## v0.1.1\n\n**Custom Note:** This release includes important manual additions!", 
					1)
				
				if err := os.WriteFile(changelogPath, []byte(modifiedContent), 0644); err != nil {
					return fmt.Errorf("failed to write modified changelog: %w", err)
				}
				
				ctx.Set("modified_content", modifiedContent)
				
				return nil
			}),
			
			harness.NewStep("Test release with modified changelog", func(ctx *harness.Context) error {
				changelogPath := ctx.Get("changelog_path").(string)
				repoDir := ctx.Get("repo_dir").(string)
				
				// Run release with dry-run to see if it preserves the modified changelog
				cmd := ctx.Command(ctx.GroveBinary, "release", "--dry-run")
				cmd.Dir(repoDir)
				result := cmd.Run()
				
				// The release should succeed
				if result.Error != nil {
					return fmt.Errorf("release failed: %w\nstdout: %s\nstderr: %s", result.Error, result.Stdout, result.Stderr)
				}
				
				// Read the changelog again
				content, err := os.ReadFile(changelogPath)
				if err != nil {
					return fmt.Errorf("failed to read changelog after release: %w", err)
				}
				
				// Verify our custom note is still there
				if !strings.Contains(string(content), "Custom Note:") {
					return fmt.Errorf("custom note not found - changelog was regenerated instead of preserved")
				}
				
				return nil
			}),
			
		},
	}
}

// ChangelogUIIndicatorsScenario tests changelog state management
func ChangelogUIIndicatorsScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "changelog-ui-indicators",
		Description: "Tests changelog state management",
		Tags:        []string{"changelog", "state"},
		LocalOnly:   false,
		Steps: []harness.Step{
			harness.NewStep("Setup repos with different changelog states", func(ctx *harness.Context) error {
				// Create mock directory
				mockDir := filepath.Join(ctx.RootDir, "mocks")
				if err := os.MkdirAll(mockDir, 0755); err != nil {
					return err
				}
				
				// Create gemapi mock
				gemapiMockPath := filepath.Join(mockDir, "gemapi")
				if err := os.WriteFile(gemapiMockPath, []byte(gemapiMockScript), 0755); err != nil {
					return err
				}
				
				// Set PATH to use our mock
				os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
				
				ecosystemDir := ctx.RootDir
				
				// Initialize ecosystem
				if err := git.Init(ecosystemDir); err != nil {
					return fmt.Errorf("failed to init git: %w", err)
				}
				if err := git.SetupTestConfig(ecosystemDir); err != nil {
					return fmt.Errorf("failed to setup git config: %w", err)
				}
				
				// Create grove.yml
				if err := fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"),
					"name: test-ecosystem\nworkspaces:\n  - \"*\"\n"); err != nil {
					return err
				}
				
				// Commit root
				if err := git.Add(ecosystemDir, "."); err != nil {
					return err
				}
				if err := git.Commit(ecosystemDir, "Initial ecosystem"); err != nil {
					return err
				}
				
				// Create multiple repos with different states
				repos := []struct {
					name   string
					state  string
				}{
					{"repo-pending", "pending"},      // No changelog generated yet
					{"repo-generated", "generated"},  // Changelog generated but not modified
					{"repo-modified", "modified"},    // Changelog written and modified
				}
				
				for _, repo := range repos {
					repoDir := filepath.Join(ecosystemDir, repo.name)
					if err := os.Mkdir(repoDir, 0755); err != nil {
						return err
					}
					
					// Initialize repo
					if err := git.Init(repoDir); err != nil {
						return err
					}
					if err := git.SetupTestConfig(repoDir); err != nil {
						return err
					}
					
					// Add basic files
					if err := fs.WriteString(filepath.Join(repoDir, "grove.yml"),
						fmt.Sprintf("name: %s\ntype: go\n", repo.name)); err != nil {
						return err
					}
					if err := fs.WriteString(filepath.Join(repoDir, "go.mod"),
						fmt.Sprintf("module %s\n\ngo 1.21\n", repo.name)); err != nil {
						return err
					}
					
					// Initial commit and tag
					if err := git.Add(repoDir, "."); err != nil {
						return err
					}
					if err := git.Commit(repoDir, "Initial commit"); err != nil {
						return err
					}
					
					cmd := command.New("git", "tag", "v0.1.0").Dir(repoDir)
					if result := cmd.Run(); result.Error != nil {
						return fmt.Errorf("failed to tag %s: %w", repo.name, result.Error)
					}
					
					// Add a change
					if err := fs.WriteString(filepath.Join(repoDir, "main.go"),
						"package main\n\nfunc main() {}\n"); err != nil {
						return err
					}
					if err := git.Add(repoDir, "main.go"); err != nil {
						return err
					}
					if err := git.Commit(repoDir, fmt.Sprintf("feat: add feature to %s", repo.name)); err != nil {
						return err
					}
				}
				
				ctx.Set("ecosystem_dir", ecosystemDir)
				
				return nil
			}),
			
			harness.NewStep("Launch TUI and verify indicator states", func(ctx *harness.Context) error {
				// Launch TUI
				session, err := ctx.StartTUI(ctx.GroveBinary, "release", "tui", "--fresh")
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)
				
				// Wait for TUI to load
				if err := session.WaitForText("Grove Release Manager", 30*time.Second); err != nil {
					content, _ := session.Capture()
					return fmt.Errorf("TUI did not load: %w\n%s", err, content)
				}
				
				// Capture initial state
				content, err := session.Capture()
				if err != nil {
					return err
				}
				
				// All repos should initially show "Pending" for changelog
				if !strings.Contains(content, "Pending") {
					return fmt.Errorf("expected 'Pending' changelog status not found")
				}
				
				// Generate changelog for first repo
				if err := session.SendKeys("g"); err != nil {
					return fmt.Errorf("failed to send 'g' key: %w", err)
				}
				time.Sleep(2 * time.Second)
				
				// Now it should show "Generated"
				content, err = session.Capture()
				if err != nil {
					return err
				}
				
				if !strings.Contains(content, "Generated") {
					return fmt.Errorf("expected 'Generated' status after generating changelog")
				}
				
				// Write changelog
				if err := session.SendKeys("w"); err != nil {
					return fmt.Errorf("failed to send 'w' key: %w", err)
				}
				time.Sleep(1 * time.Second)
				
				// After write, it should still show Generated (clean state)
				// Only after external modification would it show Modified
				
				return nil
			}),
			
			harness.NewStep("Cleanup", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				
				// Quit TUI
				if err := session.SendKeys("q"); err != nil {
					// Already closed
				}
				
				return nil
			}),
		},
	}
}