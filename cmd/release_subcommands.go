package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/release"
	"github.com/spf13/cobra"
)

// newReleasePlanCmd creates the 'grove release plan' subcommand
func newReleasePlanCmd() *cobra.Command {
	var isRC bool
	
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate a release plan for the ecosystem",
		Long: `Generate a release plan that analyzes all repositories for changes
and suggests appropriate version bumps.

The plan is saved to ~/.grove/release_plan.json and can be:
- Reviewed and modified with 'grove release tui'
- Applied with 'grove release apply'
- Cleared with 'grove release clear-plan'

Use --rc flag to create a Release Candidate plan that skips changelog
and documentation updates.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Clear any existing plan first
			if err := release.ClearPlan(); err != nil {
				return fmt.Errorf("failed to clear existing plan: %w", err)
			}

			// Set the RC flag if requested
			if isRC {
				fmt.Println("Generating Release Candidate plan...")
			}

			plan, err := runReleasePlan(ctx, isRC)
			if err != nil {
				// If runReleasePlan returns a "no changes" error, we should inform the user and exit cleanly.
				if strings.Contains(err.Error(), "no repositories have changes") {
					fmt.Println("No repositories have changes since their last release. Nothing to plan.")
					return nil
				}
				return err
			}

			// The plan type is now set within runReleasePlan, so we just need to save it.
			if err := release.SavePlan(plan); err != nil {
				return fmt.Errorf("failed to save release plan: %w", err)
			}
			
			// Display summary
			fmt.Println("\n" + theme.IconSuccess + " Release plan generated successfully!")
			fmt.Printf("   Plan saved to: ~/.grove/release_plan.json\n")
			
			// Count repos with changes
			reposWithChanges := 0
			for _, repo := range plan.Repos {
				if repo.Selected {
					reposWithChanges++
				}
			}
			fmt.Printf("   Repositories with changes: %d\n", reposWithChanges)
			
			fmt.Println("\nNext steps:")
			fmt.Println("1. Review and modify: grove release tui")
			fmt.Println("2. Apply the release: grove release apply")
			
			return nil
		},
	}
	
	cmd.Flags().BoolVar(&isRC, "rc", false, "Generate a Release Candidate plan (skip docs/changelogs)")
	
	// Add the same repo selection flags as main release command
	cmd.Flags().StringSliceVar(&releaseRepos, "repos", []string{}, "Only release specified repositories")
	cmd.Flags().BoolVar(&releaseWithDeps, "with-deps", false, "Include all dependencies of specified repositories")
	cmd.Flags().StringSliceVar(&releaseMajor, "major", []string{}, "Repositories to receive major version bump")
	cmd.Flags().StringSliceVar(&releaseMinor, "minor", []string{}, "Repositories to receive minor version bump")
	cmd.Flags().StringSliceVar(&releasePatch, "patch", []string{}, "Repositories to receive patch version bump")
	cmd.Flags().BoolVar(&releaseLLMChangelog, "llm-changelog", false, "Generate changelog using an LLM")
	
	return cmd
}

// newReleaseApplyCmd creates the 'grove release apply' subcommand
func newReleaseApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Execute a previously generated release plan",
		Long: `Execute a release plan that was previously generated with 'grove release plan'
and reviewed with 'grove release tui'.

This command will:
1. Load the plan from ~/.grove/release_plan.json
2. Execute the release for all approved repositories
3. Create tags and push changes if configured
4. Clear the plan upon successful completion

Use --dry-run to preview what would be done without making changes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			
			// Check if a plan exists
			plan, err := release.LoadPlan()
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no release plan found - run 'grove release plan' first")
				}
				return fmt.Errorf("failed to load release plan: %w", err)
			}
			
			// Check that at least one repo is approved
			hasApproved := false
			for _, repo := range plan.Repos {
				if repo.Selected && repo.Status == "Approved" {
					hasApproved = true
					break
				}
			}
			
			if !hasApproved {
				return fmt.Errorf("no repositories are approved for release - run 'grove release tui' to review and approve")
			}
			
			// Execute the release
			return runReleaseApply(ctx)
		},
	}
	
	cmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "Print commands without executing them")
	cmd.Flags().BoolVar(&releasePush, "push", true, "Push changes to remote repositories (default: true)")
	cmd.Flags().BoolVar(&releaseSkipParent, "skip-parent", false, "Skip parent repository updates")
	cmd.Flags().BoolVar(&releaseSkipCI, "skip-ci", false, "Skip CI waits after changelog updates (still waits for release workflows)")
	cmd.Flags().BoolVar(&releaseResume, "resume", false, "Only process repos that haven't completed successfully")
	cmd.Flags().BoolVar(&releaseSyncDeps, "sync-deps", false, "Sync grove dependencies to latest versions before releasing")
	
	return cmd
}

// newReleaseClearPlanCmd creates the 'grove release clear-plan' subcommand
func newReleaseClearPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear-plan",
		Short: "Clear the current release plan",
		Long: `Clear the current release plan and staging directory.

This removes:
- The release plan at ~/.grove/release_plan.json
- The staging directory at ~/.grove/release_staging/

Use this to abort a release in progress and start fresh.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if a plan exists
			plan, err := release.LoadPlan()
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No release plan to clear")
					return nil
				}
				return fmt.Errorf("failed to check for existing plan: %w", err)
			}
			
			// Show what will be cleared
			fmt.Println("This will clear:")
			fmt.Printf("- Release plan created at: %s\n", plan.CreatedAt.Format("2006-01-02 15:04:05"))
			
			repoCount := 0
			for _, repo := range plan.Repos {
				if repo.Selected {
					repoCount++
				}
			}
			fmt.Printf("- %d repositories in the plan\n", repoCount)
			fmt.Println("- All staged changelogs")
			
			// Confirm
			fmt.Print("\nAre you sure you want to clear the release plan? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			response = strings.TrimSpace(strings.ToLower(response))
			
			if response != "y" && response != "yes" {
				fmt.Println("Cancelled")
				return nil
			}
			
			// Clear the plan
			if err := release.ClearPlan(); err != nil {
				return fmt.Errorf("failed to clear plan: %w", err)
			}
			
			fmt.Println(theme.IconSuccess + " Release plan cleared successfully")
			return nil
		},
	}
	
	return cmd
}

// newReleaseUndoTagCmd creates the 'grove release undo-tag' subcommand
func newReleaseUndoTagCmd() *cobra.Command {
	var tagName string
	var remote bool
	var force bool
	var fromPlan bool
	
	cmd := &cobra.Command{
		Use:   "undo-tag [tag]",
		Short: "Remove tags locally and optionally from remote",
		Long: `Remove tags that were created during a release.

This is useful if you need to fix something after tagging.

Examples:
  grove release undo-tag v1.2.3              # Remove specific tag from current repo
  grove release undo-tag v1.2.3 --remote     # Also remove from origin
  grove release undo-tag --from-plan         # Remove all tags from release plan
  grove release undo-tag --from-plan --remote --force  # Force remove from remote too`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			
			if fromPlan {
				// Load the release plan
				plan, err := release.LoadPlan()
				if err != nil {
					if os.IsNotExist(err) {
						return fmt.Errorf("no release plan found - cannot undo tags without a plan")
					}
					return fmt.Errorf("failed to load release plan: %w", err)
				}
				
				// Confirm the action
				if !force {
					fmt.Println("This will remove the following tags:")
					for repoName, repo := range plan.Repos {
						if repo.Selected && repo.NextVersion != "" {
							fmt.Printf("  - %s: %s", repoName, repo.NextVersion)
							if remote {
								fmt.Print(" (local and remote)")
							} else {
								fmt.Print(" (local only)")
							}
							fmt.Println()
						}
					}
					
					fmt.Print("\nAre you sure? [y/N]: ")
					var response string
					fmt.Scanln(&response)
					response = strings.TrimSpace(strings.ToLower(response))
					
					if response != "y" && response != "yes" {
						fmt.Println("Cancelled")
						return nil
					}
				}
				
				// Track results
				var successCount, failCount int
				var errors []string
				
				// Process each repository
				for repoName, repo := range plan.Repos {
					if !repo.Selected || repo.NextVersion == "" {
						continue
					}
					
					// Find the repository path
					repoPath := findRepositoryPath(repoName)
					if repoPath == "" {
						errors = append(errors, fmt.Sprintf("%s: could not find repository path", repoName))
						failCount++
						continue
					}
					
					version := repo.NextVersion
					
					// Delete local tag
					deleteCmd := exec.CommandContext(ctx, "git", "tag", "-d", version)
					deleteCmd.Dir = repoPath
					output, err := deleteCmd.CombinedOutput()
					if err != nil {
						if !strings.Contains(string(output), "not found") {
							errors = append(errors, fmt.Sprintf("%s: failed to delete local tag: %s", repoName, string(output)))
							failCount++
							continue
						}
						// Tag doesn't exist locally, that's ok
					} else {
						fmt.Printf("  ✓ Removed local tag %s from %s\n", version, repoName)
					}
					
					// Delete remote tag if requested
					if remote {
						pushCmd := exec.CommandContext(ctx, "git", "push", "origin", ":refs/tags/"+version)
						pushCmd.Dir = repoPath
						output, err := pushCmd.CombinedOutput()
						if err != nil {
							if !strings.Contains(string(output), "not found") {
								errors = append(errors, fmt.Sprintf("%s: failed to delete remote tag: %s", repoName, string(output)))
								failCount++
								continue
							}
							// Tag doesn't exist remotely, that's ok
						} else {
							fmt.Printf("  ✓ Removed remote tag %s from %s\n", version, repoName)
						}
					}
					
					successCount++
				}
				
				// Report results
				fmt.Printf("\n%s Successfully processed %d repositories\n", theme.IconSuccess, successCount)
				if failCount > 0 {
					fmt.Printf("%s Failed on %d repositories:\n", theme.IconError, failCount)
					for _, err := range errors {
						fmt.Printf("  - %s\n", err)
					}
					return fmt.Errorf("some operations failed")
				}
				
				return nil
			}
			
			// Single tag mode
			if len(args) > 0 {
				tagName = args[0]
			}
			
			if tagName == "" {
				// Get the last tag
				gitCmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
				output, err := gitCmd.Output()
				if err != nil {
					return fmt.Errorf("failed to find last tag: %w", err)
				}
				tagName = strings.TrimSpace(string(output))
			}
			
			// Confirm if not forced
			if !force && remote {
				fmt.Printf("This will remove tag %s locally", tagName)
				if remote {
					fmt.Print(" and from remote")
				}
				fmt.Print(". Continue? [y/N]: ")
				
				var response string
				fmt.Scanln(&response)
				response = strings.TrimSpace(strings.ToLower(response))
				
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}
			
			fmt.Printf("Removing tag: %s\n", tagName)
			
			// Remove from current repository
			gitCmd := exec.Command("git", "tag", "-d", tagName)
			output, err := gitCmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed to delete tag: %w\n%s", err, string(output))
			}
			
			fmt.Printf("%s Successfully removed local tag %s\n", theme.IconSuccess, tagName)
			
			// Remove from remote if requested
			if remote {
				pushCmd := exec.Command("git", "push", "origin", ":refs/tags/"+tagName)
				output, err := pushCmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("failed to delete remote tag: %w\n%s", err, string(output))
				}
				fmt.Printf("%s Successfully removed remote tag %s\n", theme.IconSuccess, tagName)
			}
			
			return nil
		},
	}
	
	cmd.Flags().BoolVar(&remote, "remote", false, "Also delete tags from origin")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&fromPlan, "from-plan", false, "Remove tags for all repos in the release plan")
	
	return cmd
}

// findRepositoryPath tries to locate a repository by name
func findRepositoryPath(repoName string) string {
	// Try common patterns
	patterns := []string{
		repoName,                                              // Current directory subdirectory
		filepath.Join("..", repoName),                        // Sibling directory
		filepath.Join("..", "..", "grove-ecosystem", repoName), // Grove ecosystem structure
	}
	
	for _, pattern := range patterns {
		if info, err := os.Stat(pattern); err == nil && info.IsDir() {
			// Check if it's a git repository
			gitPath := filepath.Join(pattern, ".git")
			if _, err := os.Stat(gitPath); err == nil {
				absPath, _ := filepath.Abs(pattern)
				return absPath
			}
		}
	}
	
	// If not found, return empty string
	return ""
}

// newReleaseRollbackCmd creates the 'grove release rollback' subcommand
func newReleaseRollbackCmd() *cobra.Command {
	var commits int
	var mode string
	var push bool
	var forcePush bool
	
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback commits in repositories from the release plan",
		Long: `Rollback recent commits in repositories that are part of the release plan.

This command helps recover from failed releases by resetting repositories
to a previous state. It reads the release plan to know which repositories
to operate on.

Examples:
  grove release rollback                    # Rollback 1 commit (mixed mode)
  grove release rollback --commits 2        # Rollback 2 commits
  grove release rollback --hard             # Hard reset (loses changes)
  grove release rollback --soft --push      # Soft reset and push
  grove release rollback --push --force     # Force push after rollback`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			
			// Load the release plan
			plan, err := release.LoadPlan()
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no release plan found - rollback requires a plan to know which repos to operate on")
				}
				return fmt.Errorf("failed to load release plan: %w", err)
			}
			
			// Validate mode
			validModes := map[string]bool{"hard": true, "soft": true, "mixed": true}
			if !validModes[mode] {
				return fmt.Errorf("invalid mode %q - must be hard, soft, or mixed", mode)
			}
			
			// Warn about destructive operation
			fmt.Printf("%s This will rollback %d commit(s) using %s reset\n", theme.IconWarning, commits, mode)
			if mode == "hard" {
				fmt.Println("   WARNING: Hard reset will LOSE all uncommitted changes!")
			}
			
			// List affected repositories
			fmt.Println("\nAffected repositories:")
			repoCount := 0
			for repoName, repo := range plan.Repos {
				if repo.Selected {
					fmt.Printf("  - %s", repoName)
					if repo.NextVersion != "" {
						fmt.Printf(" (was releasing %s)", repo.NextVersion)
					}
					fmt.Println()
					repoCount++
				}
			}
			
			if repoCount == 0 {
				fmt.Println("No repositories selected in the plan")
				return nil
			}
			
			// Confirm the action
			fmt.Print("\nAre you sure you want to rollback? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			response = strings.TrimSpace(strings.ToLower(response))
			
			if response != "y" && response != "yes" {
				fmt.Println("Cancelled")
				return nil
			}
			
			// Create backup tag with timestamp
			backupTag := fmt.Sprintf("backup-%d", time.Now().Unix())
			fmt.Printf("\nCreating backup tags with prefix: %s\n", backupTag)
			
			// Track results
			var successCount, failCount int
			var errors []string
			rollbackState := make(map[string]string) // Track what we rolled back
			
			// Process each repository
			for repoName, repo := range plan.Repos {
				if !repo.Selected {
					continue
				}
				
				// Find the repository path
				repoPath := findRepositoryPath(repoName)
				if repoPath == "" {
					errors = append(errors, fmt.Sprintf("%s: could not find repository path", repoName))
					failCount++
					continue
				}
				
				fmt.Printf("\nProcessing %s...\n", repoName)
				
				// Create backup tag first
				backupRepoTag := fmt.Sprintf("%s-%s", backupTag, repoName)
				tagCmd := exec.CommandContext(ctx, "git", "tag", backupRepoTag, "-m", fmt.Sprintf("Backup before rollback"))
				tagCmd.Dir = repoPath
				if output, err := tagCmd.CombinedOutput(); err != nil {
					// Don't fail if tag already exists, just warn
					if !strings.Contains(string(output), "already exists") {
						fmt.Printf("  %s Could not create backup tag: %s\n", theme.IconWarning, strings.TrimSpace(string(output)))
					}
				} else {
					fmt.Printf("  ✓ Created backup tag: %s\n", backupRepoTag)
				}
				
				// Check for uncommitted changes if using hard reset
				if mode == "hard" {
					statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
					statusCmd.Dir = repoPath
					if output, _ := statusCmd.Output(); len(output) > 0 {
						fmt.Printf("  %s Warning: %s has uncommitted changes that will be lost!\n", theme.IconWarning, repoName)
					}
				}
				
				// Perform the reset
				resetArgs := []string{"reset", "--" + mode, fmt.Sprintf("HEAD~%d", commits)}
				resetCmd := exec.CommandContext(ctx, "git", resetArgs...)
				resetCmd.Dir = repoPath
				output, err := resetCmd.CombinedOutput()
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s: reset failed: %s", repoName, string(output)))
					failCount++
					continue
				}
				
				// Get the new HEAD commit for tracking
				headCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
				headCmd.Dir = repoPath
				if headOutput, err := headCmd.Output(); err == nil {
					rollbackState[repoName] = strings.TrimSpace(string(headOutput))
				}
				
				fmt.Printf("  ✓ Rolled back %d commit(s) (%s mode)\n", commits, mode)
				
				// Push if requested
				if push {
					// Check if we need force push
					needsForce := false
					
					// Check if local branch has diverged from remote
					statusCmd := exec.CommandContext(ctx, "git", "status", "-sb")
					statusCmd.Dir = repoPath
					if statusOutput, err := statusCmd.Output(); err == nil {
						// If we've rolled back, we'll likely be behind and need force
						if strings.Contains(string(statusOutput), "behind") || commits > 0 {
							needsForce = true
						}
					}
					
					if needsForce && !forcePush {
						errors = append(errors, fmt.Sprintf("%s: needs force push but --force not provided", repoName))
						fmt.Printf("  %s Skipping push: force push required but --force not provided\n", theme.IconWarning)
						failCount++
						continue
					}
					
					// Perform the push
					pushArgs := []string{"push", "origin", "HEAD"}
					if needsForce && forcePush {
						pushArgs = append(pushArgs, "--force-with-lease")
						fmt.Printf("  → Force pushing to origin...\n")
					} else {
						fmt.Printf("  → Pushing to origin...\n")
					}
					
					pushCmd := exec.CommandContext(ctx, "git", pushArgs...)
					pushCmd.Dir = repoPath
					output, err := pushCmd.CombinedOutput()
					if err != nil {
						errors = append(errors, fmt.Sprintf("%s: push failed: %s", repoName, string(output)))
						failCount++
						continue
					}
					
					if needsForce && forcePush {
						fmt.Printf("  ✓ Force pushed to origin\n")
					} else {
						fmt.Printf("  ✓ Pushed to origin\n")
					}
				}
				
				successCount++
			}
			
			// Save rollback state for potential recovery
			if len(rollbackState) > 0 {
				if err := saveRollbackState(rollbackState); err != nil {
					fmt.Printf("\n%s Could not save rollback state: %v\n", theme.IconWarning, err)
				} else {
					fmt.Printf("\n%s Rollback state saved to ~/.grove/release_rollback.json\n", theme.IconNote)
				}
			}
			
			// Report results
			fmt.Printf("\n%s Successfully rolled back %d repositories\n", theme.IconSuccess, successCount)
			if failCount > 0 {
				fmt.Printf("%s Failed on %d repositories:\n", theme.IconError, failCount)
				for _, err := range errors {
					fmt.Printf("  - %s\n", err)
				}
				return fmt.Errorf("some rollback operations failed")
			}
			
			fmt.Printf("\n%s To recover, use the backup tags created with prefix: %s\n", theme.IconLightbulb, backupTag)
			
			return nil
		},
	}
	
	cmd.Flags().IntVar(&commits, "commits", 1, "Number of commits to roll back")
	cmd.Flags().StringVar(&mode, "mode", "mixed", "Reset mode: hard, soft, or mixed")
	cmd.Flags().BoolVar(&push, "push", false, "Push the rollback to origin")
	cmd.Flags().BoolVar(&forcePush, "force", false, "Allow force push if needed")
	
	// Add shortcuts for modes
	cmd.Flags().Bool("hard", false, "Shortcut for --mode=hard")
	cmd.Flags().Bool("soft", false, "Shortcut for --mode=soft")
	cmd.Flags().Bool("mixed", false, "Shortcut for --mode=mixed")
	
	// Handle mode shortcuts
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("hard") {
			mode = "hard"
		} else if cmd.Flags().Changed("soft") {
			mode = "soft"
		} else if cmd.Flags().Changed("mixed") {
			mode = "mixed"
		}
		return nil
	}
	
	return cmd
}

// saveRollbackState saves information about the rollback for potential recovery
func saveRollbackState(state map[string]string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	
	groveDir := filepath.Join(home, ".grove")
	if err := os.MkdirAll(groveDir, 0755); err != nil {
		return err
	}
	
	statePath := filepath.Join(groveDir, "release_rollback.json")
	
	// Create rollback record
	record := map[string]interface{}{
		"timestamp":    time.Now().Format(time.RFC3339),
		"repositories": state,
	}
	
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(statePath, data, 0644)
}

// newReleaseReviewCmd creates an alias for the TUI command
func newReleaseReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review and modify the release plan (alias for 'tui')",
		Long: `Launch the interactive TUI to review and modify the release plan.

This is an alias for 'grove release tui'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			return runReleaseTUI(ctx)
		},
	}
	
	return cmd
}