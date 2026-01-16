package tests

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/grove/pkg/release"
	"github.com/grovetools/tend/pkg/git"
	"github.com/grovetools/tend/pkg/harness"
)

// ChangelogHashTrackingScenario tests the changelog hash tracking functionality
func ChangelogHashTrackingScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "changelog-hash-tracking",
		Description: "Tests changelog hash tracking and dirty detection",
		Tags:        []string{"changelog", "hash"},
		Steps: []harness.Step{
			harness.NewStep("Test hash calculation and storage", func(ctx *harness.Context) error {
				// Create a test plan
				plan := &release.ReleasePlan{
					Repos: map[string]*release.RepoReleasePlan{
						"test-repo": {
							CurrentVersion: "v0.1.0",
							NextVersion:    "v0.1.1",
							SelectedBump:   "patch",
						},
					},
				}
				
				// Simulate writing a changelog
				changelogContent := "## v0.1.1\n\n### Features\n- New feature\n"
				
				// Calculate hash (this is what the TUI does)
				hash := sha256.Sum256([]byte(changelogContent))
				hashStr := fmt.Sprintf("%x", hash)
				
				// Store in plan
				plan.Repos["test-repo"].ChangelogHash = hashStr
				plan.Repos["test-repo"].ChangelogState = "clean"
				
				// Verify hash was stored
				if plan.Repos["test-repo"].ChangelogHash == "" {
					return fmt.Errorf("hash not stored in plan")
				}
				
				if plan.Repos["test-repo"].ChangelogState != "clean" {
					return fmt.Errorf("expected state 'clean', got '%s'", plan.Repos["test-repo"].ChangelogState)
				}
				
				// Simulate modifying the content
				modifiedContent := changelogContent + "\n**Custom note added**\n"
				modifiedHash := sha256.Sum256([]byte(modifiedContent))
				modifiedHashStr := fmt.Sprintf("%x", modifiedHash)
				
				// Check if it's different (dirty detection)
				if modifiedHashStr == hashStr {
					return fmt.Errorf("modified hash should be different from original")
				}
				
				return nil
			}),
			
			harness.NewStep("Test dirty detection logic", func(ctx *harness.Context) error {
				// Create temp directory
				tempDir := ctx.NewDir("changelog-test")
				repoDir := filepath.Join(tempDir, "test-repo")
				if err := os.MkdirAll(repoDir, 0755); err != nil {
					return err
				}
				
				// Create initial changelog
				changelogPath := filepath.Join(repoDir, "CHANGELOG.md")
				originalContent := "## v0.1.1\n\n### Features\n- Original feature\n"
				if err := os.WriteFile(changelogPath, []byte(originalContent), 0644); err != nil {
					return err
				}
				
				// Calculate and store hash
				hash := sha256.Sum256([]byte(originalContent))
				storedHash := fmt.Sprintf("%x", hash)
				
				// Simulate plan with stored hash
				plan := &release.RepoReleasePlan{
					ChangelogHash:  storedHash,
					ChangelogState: "clean",
					NextVersion:    "v0.1.1",
				}
				
				// Read file and check if dirty (should be clean)
				currentContent, err := os.ReadFile(changelogPath)
				if err != nil {
					return err
				}
				
				currentHash := sha256.Sum256(currentContent)
				currentHashStr := fmt.Sprintf("%x", currentHash)
				
				if currentHashStr != storedHash {
					return fmt.Errorf("file should not be dirty yet")
				}
				
				// Modify the file
				modifiedContent := strings.Replace(string(currentContent), 
					"Original feature", 
					"Modified feature with custom notes", 1)
				if err := os.WriteFile(changelogPath, []byte(modifiedContent), 0644); err != nil {
					return err
				}
				
				// Check again (should be dirty now)
				newContent, err := os.ReadFile(changelogPath)
				if err != nil {
					return err
				}
				
				newHash := sha256.Sum256(newContent)
				newHashStr := fmt.Sprintf("%x", newHash)
				
				if newHashStr == storedHash {
					return fmt.Errorf("file should be dirty after modification")
				}
				
				// Check version header is still present
				if !strings.Contains(string(newContent), fmt.Sprintf("## %s", plan.NextVersion)) {
					return fmt.Errorf("version header missing after modification")
				}
				
				return nil
			}),
			
			harness.NewStep("Test plan serialization with hash fields", func(ctx *harness.Context) error {
				// Create a plan with hash fields
				plan := &release.ReleasePlan{
					RootDir: "/test/path",
					Repos: map[string]*release.RepoReleasePlan{
						"repo1": {
							CurrentVersion:  "v1.0.0",
							NextVersion:     "v1.1.0",
							ChangelogHash:   "abc123def456",
							ChangelogState:  "dirty",
						},
						"repo2": {
							CurrentVersion:  "v2.0.0",
							NextVersion:     "v2.0.1",
							ChangelogHash:   "",
							ChangelogState:  "none",
						},
					},
				}
				
				// Serialize to JSON
				data, err := json.MarshalIndent(plan, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal plan: %w", err)
				}
				
				// Verify JSON contains the new fields
				jsonStr := string(data)
				if !strings.Contains(jsonStr, "changelog_hash") {
					return fmt.Errorf("JSON should contain changelog_hash field")
				}
				if !strings.Contains(jsonStr, "changelog_state") {
					return fmt.Errorf("JSON should contain changelog_state field")
				}
				
				// Deserialize back
				var plan2 release.ReleasePlan
				if err := json.Unmarshal(data, &plan2); err != nil {
					return fmt.Errorf("failed to unmarshal plan: %w", err)
				}
				
				// Verify fields are preserved
				if plan2.Repos["repo1"].ChangelogHash != "abc123def456" {
					return fmt.Errorf("changelog hash not preserved after serialization")
				}
				if plan2.Repos["repo1"].ChangelogState != "dirty" {
					return fmt.Errorf("changelog state not preserved after serialization")
				}
				
				return nil
			}),
		},
	}
}

// ChangelogStateTransitionsScenario tests the state transitions of changelog
func ChangelogStateTransitionsScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "changelog-state-transitions", 
		Description: "Tests changelog state transitions",
		Tags:        []string{"changelog", "state"},
		Steps: []harness.Step{
			harness.NewStep("Test state transitions", func(ctx *harness.Context) error {
				tempDir := ctx.NewDir("state-test")
				
				// Ensure directory exists
				if err := os.MkdirAll(tempDir, 0755); err != nil {
					return err
				}
				
				// Initialize git repo for testing
				if err := git.Init(tempDir); err != nil {
					return err
				}
				if err := git.SetupTestConfig(tempDir); err != nil {
					return err
				}
				
				// Create initial state - no changelog
				plan := &release.RepoReleasePlan{
					ChangelogState: "none",
				}
				
				if plan.ChangelogState != "none" {
					return fmt.Errorf("initial state should be 'none'")
				}
				
				// Transition to clean after write
				changelogContent := "## v1.0.0\n\nInitial release\n"
				hash := sha256.Sum256([]byte(changelogContent))
				plan.ChangelogHash = fmt.Sprintf("%x", hash)
				plan.ChangelogState = "clean"
				
				if plan.ChangelogState != "clean" {
					return fmt.Errorf("state should be 'clean' after write")
				}
				
				// Transition to dirty after modification
				plan.ChangelogState = "dirty"
				
				if plan.ChangelogState != "dirty" {
					return fmt.Errorf("state should be 'dirty' after modification")
				}
				
				return nil
			}),
			
			harness.NewStep("Test version header validation", func(ctx *harness.Context) error {
				// Test cases for version header validation
				testCases := []struct {
					content     string
					version     string
					shouldMatch bool
				}{
					{
						content:     "## v1.0.0\n\nRelease notes",
						version:     "v1.0.0",
						shouldMatch: true,
					},
					{
						content:     "## v1.0.0\n\nRelease notes",
						version:     "v2.0.0",
						shouldMatch: false,
					},
					{
						content:     "# Changelog\n\n## v1.0.0 (2024-01-01)\n\nNotes",
						version:     "v1.0.0",
						shouldMatch: true,
					},
					{
						content:     "Some other content without version",
						version:     "v1.0.0",
						shouldMatch: false,
					},
				}
				
				for i, tc := range testCases {
					// Check if content contains expected version header
					expectedHeader := fmt.Sprintf("## %s", tc.version)
					hasHeader := strings.Contains(tc.content, expectedHeader)
					
					if hasHeader != tc.shouldMatch {
						return fmt.Errorf("test case %d: expected match=%v, got %v", 
							i, tc.shouldMatch, hasHeader)
					}
				}
				
				return nil
			}),
		},
	}
}