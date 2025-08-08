package tests

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-tend/internal/harness"
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/git"
)

// ConventionalCommitsScenario tests the full lifecycle of conventional commits and changelog generation
func ConventionalCommitsScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "conventional-commits",
		Description: "Tests git hooks installation, conventional commit enforcement, and changelog generation",
		Tags:        []string{"conventional-commits", "changelog", "git-hooks"},
		Steps: []harness.Step{
			{
				Name:        "Setup test repository",
				Description: "Creates a new Git repository with initial files",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.NewDir("test-repo")
					
					if err := git.Init(repoDir); err != nil {
						return fmt.Errorf("failed to initialize git repository: %w", err)
					}
					
					// Configure Git for testing
					if err := git.SetupTestConfig(repoDir); err != nil {
						return fmt.Errorf("failed to setup git config: %w", err)
					}
					
					// Create initial files
					if err := fs.WriteString(filepath.Join(repoDir, "README.md"), "# Test Repository\n\nTesting conventional commits."); err != nil {
						return err
					}
					if err := fs.WriteString(filepath.Join(repoDir, "go.mod"), "module test\n\ngo 1.21"); err != nil {
						return err
					}
					
					// Initial commit
					if err := git.Add(repoDir, "."); err != nil {
						return err
					}
					if err := git.Commit(repoDir, "Initial commit"); err != nil {
						return err
					}
					
					ctx.Set("repo_dir", repoDir)
					return nil
				},
			},
			{
				Name:        "Install git hooks",
				Description: "Installs conventional commit hooks in the repository",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					cmd := command.New(ctx.GroveBinary, "git-hooks", "install").Dir(repoDir)
					result := cmd.Run()
					
					if result.Error != nil {
						return fmt.Errorf("failed to install hooks: %w\nStderr: %s", result.Error, result.Stderr)
					}
					
					hookPath := filepath.Join(repoDir, ".git", "hooks", "commit-msg")
					if !fs.Exists(hookPath) {
						return fmt.Errorf("commit-msg hook was not created at %s", hookPath)
					}
					
					ctx.ShowCommandOutput("grove git-hooks install", result.Stdout, result.Stderr)
					ctx.Set("hook_path", hookPath)
					return nil
				},
			},
			{
				Name:        "Test invalid commit rejection",
				Description: "Verifies that non-conventional commits are rejected",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					// Create a file to commit
					if err := fs.WriteString(filepath.Join(repoDir, "main.go"), "package main\n\nfunc main() {}"); err != nil {
						return err
					}
					
					if err := git.Add(repoDir, "main.go"); err != nil {
						return err
					}
					
					// Attempt invalid commit
					err := git.Commit(repoDir, "This is not a conventional commit")
					if err == nil {
						return fmt.Errorf("expected commit to fail with invalid message, but it succeeded")
					}
					
					// Verify error message contains expected text
					if !strings.Contains(err.Error(), "INVALID COMMIT MESSAGE") {
						return fmt.Errorf("expected error to contain 'INVALID COMMIT MESSAGE', got: %v", err)
					}
					
					return nil
				},
			},
			{
				Name:        "Make valid conventional commits",
				Description: "Creates various types of conventional commits",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					// Series of commits to test
					commits := []struct {
						file    string
						content string
						message string
					}{
						{
							file:    "main.go",
							content: "package main\n\nfunc main() {\n\tprintln(\"Hello, Grove!\")\n}",
							message: "feat: add main.go with hello world functionality",
						},
						{
							file:    "utils.go",
							content: "package main\n\nimport \"strings\"\n\nfunc FormatName(name string) string {\n\treturn strings.Title(name)\n}",
							message: "feat(utils): add name formatting utility function",
						},
						{
							file:    "main.go",
							content: "package main\n\nfunc main() {\n\tname := FormatName(\"grove\")\n\tprintln(\"Hello, \" + name + \"!\")\n}",
							message: "fix: update main to use FormatName function",
						},
						{
							file:    "utils.go",
							content: "package main\n\nimport \"strings\"\n\nfunc FormatName(prefix, name string) string {\n\treturn prefix + strings.Title(name)\n}",
							message: "feat!: add prefix parameter to FormatName function\n\nBREAKING CHANGE: FormatName now requires two parameters",
						},
						{
							file:    "README.md",
							content: "# Test Repository\n\n## Usage\n\n```go\ngo run .\n```\n\n## Features\n\n- Name formatting\n- Conventional commits",
							message: "docs: add usage instructions and feature list to README",
						},
						{
							file:    ".gitignore",
							content: "*.exe\n*.test\n*.out",
							message: "chore: add .gitignore file",
						},
					}
					
					// Make each commit
					for _, c := range commits {
						if err := fs.WriteString(filepath.Join(repoDir, c.file), c.content); err != nil {
							return fmt.Errorf("failed to write %s: %w", c.file, err)
						}
						
						if err := git.Add(repoDir, c.file); err != nil {
							return fmt.Errorf("failed to add %s: %w", c.file, err)
						}
						
						if err := git.Commit(repoDir, c.message); err != nil {
							return fmt.Errorf("failed to commit '%s': %w", c.message, err)
						}
					}
					
					// Add one more empty commit for performance
					cmd := command.New("git", "commit", "--allow-empty", "-m", "perf: optimize string concatenation").Dir(repoDir)
					result := cmd.Run()
					if result.Error != nil {
						return fmt.Errorf("failed to make empty commit: %w", result.Error)
					}
					
					return nil
				},
			},
			{
				Name:        "Tag initial release",
				Description: "Tags the first commit as v0.1.0",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					// Get the first commit hash
					cmd := command.New("git", "rev-list", "--max-parents=0", "HEAD").Dir(repoDir)
					result := cmd.Run()
					if result.Error != nil {
						return fmt.Errorf("failed to get first commit: %w", result.Error)
					}
					
					firstCommit := strings.TrimSpace(result.Stdout)
					
					// Tag it
					cmd = command.New("git", "tag", "-a", "v0.1.0", firstCommit, "-m", "Initial release").Dir(repoDir)
					result = cmd.Run()
					if result.Error != nil {
						return fmt.Errorf("failed to tag initial release: %w", result.Error)
					}
					
					return nil
				},
			},
			{
				Name:        "Generate changelog for v0.2.0",
				Description: "Uses grove release changelog to generate a changelog",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					cmd := command.New(ctx.GroveBinary, "release", "changelog", ".", "--version", "v0.2.0").Dir(repoDir)
					result := cmd.Run()
					
					if result.Error != nil {
						return fmt.Errorf("changelog generation failed: %w\nStderr: %s", result.Error, result.Stderr)
					}
					
					// Verify CHANGELOG.md was created
					changelogPath := filepath.Join(repoDir, "CHANGELOG.md")
					if !fs.Exists(changelogPath) {
						return fmt.Errorf("CHANGELOG.md was not created")
					}
					
					// Read and verify content
					content, err := fs.ReadString(changelogPath)
					if err != nil {
						return fmt.Errorf("failed to read changelog: %w", err)
					}
					
					// Check for expected sections
					expectedSections := []string{
						"## v0.2.0",
						"### 💥 BREAKING CHANGES",
						"### Features",
						"### Bug Fixes",
						"### Documentation",
						"### Chores",
						"### Performance Improvements",
						"**utils:**", // Scoped commit
						"add prefix parameter to FormatName function", // Breaking change
					}
					
					for _, expected := range expectedSections {
						if !strings.Contains(content, expected) {
							return fmt.Errorf("changelog missing expected content: %s", expected)
						}
					}
					
					ctx.ShowCommandOutput("Changelog content", content, "")
					return nil
				},
			},
			{
				Name:        "Commit changelog and tag v0.2.0",
				Description: "Commits the changelog and creates release tag",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					if err := git.Add(repoDir, "CHANGELOG.md"); err != nil {
						return err
					}
					
					if err := git.Commit(repoDir, "docs(changelog): update CHANGELOG.md for v0.2.0"); err != nil {
						return err
					}
					
					cmd := command.New("git", "tag", "-a", "v0.2.0", "-m", "Release v0.2.0").Dir(repoDir)
					result := cmd.Run()
					if result.Error != nil {
						return fmt.Errorf("failed to tag v0.2.0: %w", result.Error)
					}
					
					return nil
				},
			},
			{
				Name:        "Generate changelog for v0.3.0",
				Description: "Tests changelog prepending for multiple releases",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					// Add another commit
					if err := fs.WriteString(filepath.Join(repoDir, "config.go"), "package main\n\ntype Config struct {\n\tPrefix string\n}"); err != nil {
						return err
					}
					
					if err := git.Add(repoDir, "config.go"); err != nil {
						return err
					}
					
					if err := git.Commit(repoDir, "feat(config): add configuration struct"); err != nil {
						return err
					}
					
					// Generate changelog for v0.3.0
					cmd := command.New(ctx.GroveBinary, "release", "changelog", ".", "--version", "v0.3.0").Dir(repoDir)
					result := cmd.Run()
					
					if result.Error != nil {
						return fmt.Errorf("changelog generation for v0.3.0 failed: %w", result.Error)
					}
					
					// Read and verify content
					content, err := fs.ReadString(filepath.Join(repoDir, "CHANGELOG.md"))
					if err != nil {
						return fmt.Errorf("failed to read updated changelog: %w", err)
					}
					
					// Verify both versions are present
					if !strings.Contains(content, "## v0.3.0") {
						return fmt.Errorf("changelog missing v0.3.0 section")
					}
					if !strings.Contains(content, "## v0.2.0") {
						return fmt.Errorf("changelog missing v0.2.0 section")
					}
					
					// Verify v0.3.0 comes before v0.2.0 (prepended)
					idx1 := strings.Index(content, "## v0.3.0")
					idx2 := strings.Index(content, "## v0.2.0")
					if idx1 > idx2 {
						return fmt.Errorf("v0.3.0 should be prepended before v0.2.0")
					}
					
					return nil
				},
			},
			{
				Name:        "Uninstall git hooks",
				Description: "Removes the conventional commit hooks",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					hookPath := ctx.Get("hook_path").(string)
					
					cmd := command.New(ctx.GroveBinary, "git-hooks", "uninstall").Dir(repoDir)
					result := cmd.Run()
					
					if result.Error != nil {
						return fmt.Errorf("failed to uninstall hooks: %w", result.Error)
					}
					
					// Verify hook was removed
					if fs.Exists(hookPath) {
						return fmt.Errorf("commit-msg hook still exists after uninstall")
					}
					
					ctx.ShowCommandOutput("grove git-hooks uninstall", result.Stdout, result.Stderr)
					return nil
				},
			},
			{
				Name:        "Test non-conventional commit after uninstall",
				Description: "Verifies hooks are no longer enforced",
				Func: func(ctx *harness.Context) error {
					repoDir := ctx.Get("repo_dir").(string)
					
					// Should be able to make non-conventional commit now
					cmd := command.New("git", "commit", "--allow-empty", "-m", "This is not conventional but should work now").Dir(repoDir)
					result := cmd.Run()
					
					if result.Error != nil {
						return fmt.Errorf("commit should succeed after hook uninstall: %w", result.Error)
					}
					
					return nil
				},
			},
		},
	}
}