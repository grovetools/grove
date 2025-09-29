package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
			// Note: We'll need to handle this in runReleasePlan
			if isRC {
				// This would be handled by setting a Type field in the plan
				fmt.Println("Generating Release Candidate plan...")
			}
			
			plan, err := runReleasePlan(ctx)
			if err != nil {
				return err
			}
			
			// Set the plan type based on the --rc flag
			if isRC {
				plan.Type = "rc"
			} else {
				plan.Type = "full"
			}
			
			// Save the plan again with the type
			if err := release.SavePlan(plan); err != nil {
				return fmt.Errorf("failed to save release plan type: %w", err)
			}
			
			// Display summary
			fmt.Println("\n✅ Release plan generated successfully!")
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
	cmd.Flags().BoolVar(&releasePush, "push", false, "Push changes to remote repositories")
	cmd.Flags().BoolVar(&releaseSkipParent, "skip-parent", false, "Skip parent repository updates")
	
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
			
			fmt.Println("✅ Release plan cleared successfully")
			return nil
		},
	}
	
	return cmd
}

// newReleaseUndoTagCmd creates the 'grove release undo-tag' subcommand
func newReleaseUndoTagCmd() *cobra.Command {
	var tagName string
	var allRepos bool
	
	cmd := &cobra.Command{
		Use:   "undo-tag [tag]",
		Short: "Remove a tag that hasn't been pushed yet",
		Long: `Remove a local tag that was created but hasn't been pushed to remote.

This is useful if you need to fix something after tagging but before pushing.

Examples:
  grove release undo-tag v1.2.3              # Remove specific tag from current repo
  grove release undo-tag --all v1.2.3        # Remove tag from all repositories
  grove release undo-tag --last              # Remove the last created tag`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			
			fmt.Printf("Removing tag: %s\n", tagName)
			
			if allRepos {
				// TODO: Iterate through all repos and remove the tag
				fmt.Println("Removing from all repositories...")
				return fmt.Errorf("--all flag not yet implemented")
			} else {
				// Remove from current repository
				gitCmd := exec.Command("git", "tag", "-d", tagName)
				output, err := gitCmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("failed to delete tag: %w\n%s", err, string(output))
				}
				
				fmt.Printf("✅ Successfully removed tag %s\n", tagName)
			}
			
			return nil
		},
	}
	
	cmd.Flags().BoolVar(&allRepos, "all", false, "Remove tag from all repositories")
	cmd.Flags().Bool("last", false, "Remove the last created tag")
	
	return cmd
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