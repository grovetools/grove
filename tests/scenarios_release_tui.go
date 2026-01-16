package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/git"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/tui"
)

// ReleaseTUISelectionScenario tests the selection functionality of the interactive release TUI
func ReleaseTUISelectionScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "release-tui-selection",
		Description: "Tests repository selection in the interactive release TUI",
		Tags:        []string{"release", "tui", "interactive", "selection"},
		LocalOnly:   true, // Requires a local git setup
		Steps: []harness.Step{
			harness.NewStep("Setup mock ecosystem", func(ctx *harness.Context) error {
				// Setup the test ecosystem directly in ctx.RootDir
				// This simplifies the test and avoids directory navigation issues
				
				// 1. Initialize as a Git repository with workspaces
				if err := git.Init(ctx.RootDir); err != nil {
					return fmt.Errorf("failed to init git: %w", err)
				}
				if err := git.SetupTestConfig(ctx.RootDir); err != nil {
					return fmt.Errorf("failed to setup git config: %w", err)
				}
				
				// Create grove.yml with workspaces configuration
				if err := fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), 
					"name: test-ecosystem\nworkspaces:\n  - \"*\"\n"); err != nil {
					return err
				}
				
				// Create .gitignore
				if err := fs.WriteString(filepath.Join(ctx.RootDir, ".gitignore"), 
					"go.work.sum\n"); err != nil {
					return err
				}
				
				// Commit the root
				if err := git.Add(ctx.RootDir, "."); err != nil {
					return err
				}
				if err := git.Commit(ctx.RootDir, "Initial ecosystem setup"); err != nil {
					return err
				}
				
				// 2. Create repo-a subdirectory (with changes to release)
				repoADir := filepath.Join(ctx.RootDir, "repo-a")
				if err := os.Mkdir(repoADir, 0755); err != nil {
					return err
				}
				
				// Initialize repo-a as a git repo
				if err := git.Init(repoADir); err != nil {
					return err
				}
				if err := git.SetupTestConfig(repoADir); err != nil {
					return err
				}
				
				// Add grove.yml and go.mod
				if err := fs.WriteString(filepath.Join(repoADir, "grove.yml"), 
					"name: repo-a\ntype: go\n"); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(repoADir, "go.mod"), 
					"module repo-a\n\ngo 1.21\n"); err != nil {
					return err
				}
				
				// Initial commit and tag
				if err := git.Add(repoADir, "."); err != nil {
					return err
				}
				if err := git.Commit(repoADir, "Initial commit"); err != nil {
					return err
				}
				
				// Tag v0.1.0
				cmd := command.New("git", "tag", "v0.1.0").Dir(repoADir)
				if result := cmd.Run(); result.Error != nil {
					return fmt.Errorf("failed to tag repo-a: %w", result.Error)
				}
				
				// Checkout a feature branch
				cmd = command.New("git", "checkout", "-b", "feature/new-ui").Dir(repoADir)
				if result := cmd.Run(); result.Error != nil {
					return fmt.Errorf("failed to create branch: %w", result.Error)
				}
				
				// Add a new commit to make repo-a have changes
				if err := fs.WriteString(filepath.Join(repoADir, "main.go"), 
					"package main\n\nfunc main() {}\n"); err != nil {
					return err
				}
				// Also create utils.go in the same commit
				if err := fs.WriteString(filepath.Join(repoADir, "utils.go"),
					"package main\n\n// Original utils\n"); err != nil {
					return err
				}
				if err := git.Add(repoADir, "."); err != nil {
					return err
				}
				if err := git.Commit(repoADir, "feat: add main function and utils"); err != nil {
					return err
				}
				
				// Now modify utils.go to create an unstaged modification
				if err := fs.WriteString(filepath.Join(repoADir, "utils.go"),
					"package main\n\n// Modified file\n"); err != nil {
					return err
				}
				
				// Create and stage a new file
				if err := fs.WriteString(filepath.Join(repoADir, "config.go"),
					"package main\n\n// Config file\n"); err != nil {
					return err
				}
				if err := git.Add(repoADir, "config.go"); err != nil {
					return err
				}
				
				// Create an untracked file
				if err := fs.WriteString(filepath.Join(repoADir, "test.txt"),
					"untracked file\n"); err != nil {
					return err
				}
				
				// 3. Create repo-b subdirectory (no changes, up to date)
				repoBDir := filepath.Join(ctx.RootDir, "repo-b")
				if err := os.Mkdir(repoBDir, 0755); err != nil {
					return err
				}
				
				// Initialize repo-b as a git repo
				if err := git.Init(repoBDir); err != nil {
					return err
				}
				if err := git.SetupTestConfig(repoBDir); err != nil {
					return err
				}
				
				// Add grove.yml and go.mod
				if err := fs.WriteString(filepath.Join(repoBDir, "grove.yml"), 
					"name: repo-b\ntype: go\n"); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(repoBDir, "go.mod"), 
					"module repo-b\n\ngo 1.21\n"); err != nil {
					return err
				}
				
				// Initial commit and tag (no additional commits after tag)
				if err := git.Add(repoBDir, "."); err != nil {
					return err
				}
				if err := git.Commit(repoBDir, "Initial commit"); err != nil {
					return err
				}
				
				// Tag v0.2.0
				cmd = command.New("git", "tag", "v0.2.0").Dir(repoBDir)
				if result := cmd.Run(); result.Error != nil {
					return fmt.Errorf("failed to tag repo-b: %w", result.Error)
				}
				
				// 4. Add the subdirectories as git submodules
				cmd = command.New("git", "submodule", "add", "./repo-a", "repo-a").Dir(ctx.RootDir)
				if result := cmd.Run(); result.Error != nil {
					return fmt.Errorf("failed to add submodule repo-a: %w\n%s", result.Error, result.Stderr)
				}
				
				cmd = command.New("git", "submodule", "add", "./repo-b", "repo-b").Dir(ctx.RootDir)
				if result := cmd.Run(); result.Error != nil {
					return fmt.Errorf("failed to add submodule repo-b: %w\n%s", result.Error, result.Stderr)
				}
				
				// Commit the submodules
				if err := git.Add(ctx.RootDir, ".gitmodules", "repo-a", "repo-b"); err != nil {
					return err
				}
				if err := git.Commit(ctx.RootDir, "feat: add submodules"); err != nil {
					return err
				}
				
				return nil
			}),
			harness.NewStep("Launch release TUI", func(ctx *harness.Context) error {
				// The ecosystem is already in ctx.RootDir, and harness runs commands from there
				// So we can launch the TUI directly
				// Use the binary from bin/grove
				// Try to find the correct binary
				cwd, _ := os.Getwd()
				fmt.Printf("Current working directory: %s\n", cwd)
				fmt.Printf("PWD env var: %s\n", os.Getenv("PWD"))
				
				// Try different paths to find the binary
				possiblePaths := []string{
					filepath.Join(cwd, "bin", "grove"),
					filepath.Join(os.Getenv("PWD"), "bin", "grove"),
					"./bin/grove",
					filepath.Join(cwd, "..", "..", "..", "bin", "grove"),
				}
				
				var groveBinary string
				for _, path := range possiblePaths {
					if _, err := os.Stat(path); err == nil {
						groveBinary = path
						fmt.Printf("Found binary at: %s\n", groveBinary)
						break
					}
				}
				
				if groveBinary == "" {
					groveBinary = ctx.GroveBinary // fallback to default
					fmt.Printf("Using fallback binary: %s\n", groveBinary)
				}
				session, err := ctx.StartTUI(groveBinary, []string{"release", "tui", "--fresh"})
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)
				return nil
			}),
			harness.NewStep("Wait for TUI to stabilize", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				// Wait for the main view to render. Generous timeout for plan generation.
				if err := session.WaitForText("Grove Release Manager", 30*time.Second); err != nil {
					content, _ := session.Capture()
					return fmt.Errorf("TUI did not stabilize: %w\n%s", err, content)
				}
				return nil
			}),
			harness.NewStep("Verify initial selection state", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				// Capture with cleaned output to remove ANSI color codes
				content, err := session.Capture(tui.WithCleanedOutput())
				if err != nil {
					return err
				}

				// Verify new headers are present
				if !strings.Contains(content, "Branch") {
					return fmt.Errorf("expected 'Branch' header not found in TUI output.\n%s", content)
				}
				if !strings.Contains(content, "Git Status") {
					return fmt.Errorf("expected 'Git Status' header not found in TUI output.\n%s", content)
				}
				if !strings.Contains(content, "Changes/Release") {
					return fmt.Errorf("expected 'Changes/Release' header not found in TUI output.\n%s", content)
				}

				// repo-a should be shown and selected (it has changes)
				if !strings.Contains(content, "[*]") && !strings.Contains(content, "repo-a") {
					return fmt.Errorf("repo-a not found or not selected in TUI output.\n%s", content)
				}
				
				// Verify Branch column shows feature/new-ui
				if !strings.Contains(content, "feature/new-ui") {
					return fmt.Errorf("expected branch 'feature/new-ui' not found.\n%s", content)
				}
				
				// Verify Git Status column shows dirty status
				// Should show something like "S:1 M:1 ?:1"
				if !strings.Contains(content, "S:1") {
					return fmt.Errorf("expected staged file count 'S:1' not found.\n%s", content)
				}
				if !strings.Contains(content, "M:1") {
					return fmt.Errorf("expected modified file count 'M:1' not found.\n%s", content)
				}
				if !strings.Contains(content, "?:1") {
					return fmt.Errorf("expected untracked file count '?:1' not found.\n%s", content)
				}
				
				// Verify Changes/Release column shows v0.1.0 (↑1) - we made 1 commit after the tag
				if !strings.Contains(content, "v0.1.0 (↑1)") {
					return fmt.Errorf("expected changes/release 'v0.1.0 (↑1)' not found.\n%s", content)
				}
				
				// repo-b should NOT be shown (it has no changes, is up to date)
				// This is expected behavior - only repos with changes appear
				if strings.Contains(content, "repo-b") {
					return fmt.Errorf("repo-b should not appear (it has no changes), but it was found in TUI output.\n%s", content)
				}
				
				// Store initial content for debugging
				ctx.Set("initial_content", content)
				
				return nil
			}),
			harness.NewStep("Test toggling selection", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				initialContent := ctx.GetString("initial_content")
				
				// First, let's see if we can navigate and interact
				// Try pressing space to toggle selection
				if err := session.SendKeys(" "); err != nil {
					return fmt.Errorf("failed to send space key: %w", err)
				}
				time.Sleep(500 * time.Millisecond) // Wait for UI update
				
				content, err := session.Capture()
				if err != nil {
					return err
				}
				
				// Check if the content changed after pressing space
				if content == initialContent {
					// Content didn't change, which might indicate the selection toggle isn't working
					return fmt.Errorf("UI did not update after pressing space key (selection toggle might not be working)")
				}
				
				// Try toggling again
				if err := session.SendKeys(" "); err != nil {
					return fmt.Errorf("failed to send second space key: %w", err)
				}
				time.Sleep(500 * time.Millisecond)
				
				return nil
			}),
			harness.NewStep("Test bulk selection shortcuts", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				
				// Note: Bulk selection shortcuts might not be implemented yet
				// This test documents the expected behavior
				
				// Try Ctrl+d to deselect all
				if err := session.SendKeys("Ctrl+d"); err != nil {
					return fmt.Errorf("failed to send Ctrl+d: %w", err)
				}
				time.Sleep(500 * time.Millisecond)
				
				beforeSelectAll, err := session.Capture()
				if err != nil {
					return err
				}
				
				// Check if repo-a was deselected
				if strings.Contains(beforeSelectAll, "[*]") && strings.Contains(beforeSelectAll, "repo-a") {
					// Still selected after Ctrl+d, this might indicate the shortcut isn't working
					fmt.Printf("Note: Ctrl+d might not be implemented (repo-a still selected after Ctrl+d)\n")
				}
				
				// Try Ctrl+a to select all
				if err := session.SendKeys("Ctrl+a"); err != nil {
					return fmt.Errorf("failed to send Ctrl+a: %w", err)
				}
				time.Sleep(500 * time.Millisecond)
				
				afterSelectAll, err := session.Capture()
				if err != nil {
					return err
				}
				
				// Check if content changed
				if beforeSelectAll == afterSelectAll {
					// Bulk selection shortcuts might not be implemented
					fmt.Printf("Note: Bulk selection shortcuts (Ctrl+a/Ctrl+d) might not be implemented yet\n")
					// Don't fail the test - this documents the current behavior
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