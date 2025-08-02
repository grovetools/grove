package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/git"
	"github.com/grovepm/grove/pkg/aggregator"
	"github.com/spf13/cobra"
)

var (
	// Styles for worktree display
	worktreeHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("99")).
		MarginTop(1).
		MarginBottom(0)

	worktreePathStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("241"))

	worktreeBranchStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("33")).
		Bold(true)

	worktreeCleanStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("34"))

	worktreeDirtyStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("208"))

	worktreeErrorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("196"))

	worktreeBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		MarginLeft(2)
)

func NewWorkspaceWorktreesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktrees",
		Short: "Show git worktrees for all workspaces",
		Long:  "Display git worktrees for each workspace in the monorepo with their status",
		RunE:  runWorkspaceWorktrees,
	}

	return cmd
}

func runWorkspaceWorktrees(cmd *cobra.Command, args []string) error {
	// Collector function to get worktrees for each workspace
	collector := func(workspacePath string, workspaceName string) ([]git.WorktreeWithStatus, error) {
		return git.ListWorktreesWithStatus(workspacePath)
	}

	// Renderer function to display the results
	renderer := func(results map[string][]git.WorktreeWithStatus) error {
		if len(results) == 0 {
			fmt.Println("No workspaces found with worktrees.")
			return nil
		}

		// Sort workspace names for consistent output
		var workspaceNames []string
		for name := range results {
			workspaceNames = append(workspaceNames, name)
		}
		sortStrings(workspaceNames)

		for _, wsName := range workspaceNames {
			worktrees := results[wsName]
			
			// Skip workspaces with only one worktree (the main one)
			if len(worktrees) <= 1 {
				continue
			}

			// Print workspace header
			header := worktreeHeaderStyle.Render(fmt.Sprintf("📁 %s", wsName))
			fmt.Println(header)

			// Build content for this workspace
			var lines []string
			for _, wt := range worktrees {
				line := formatWorktree(wt)
				lines = append(lines, line)
			}

			// Join and box the content
			content := strings.Join(lines, "\n")
			boxed := worktreeBoxStyle.Render(content)
			fmt.Println(boxed)
		}

		// If no workspaces have additional worktrees
		hasAnyWorktrees := false
		for _, worktrees := range results {
			if len(worktrees) > 1 {
				hasAnyWorktrees = true
				break
			}
		}

		if !hasAnyWorktrees {
			fmt.Println("\nNo workspaces have additional worktrees.")
		}

		return nil
	}

	return aggregator.Run(collector, renderer)
}

func formatWorktree(wt git.WorktreeWithStatus) string {
	// Get relative path from current directory
	cwd, _ := os.Getwd()
	relPath, err := filepath.Rel(cwd, wt.Path)
	if err != nil {
		relPath = wt.Path
	}

	// Format path
	pathStr := worktreePathStyle.Render(relPath)

	// Format branch
	branchStr := worktreeBranchStyle.Render(wt.Branch)
	if wt.Branch == "" {
		branchStr = worktreeBranchStyle.Render("(no branch)")
	}

	// Format status
	var statusStr string
	if wt.Status != nil {
		if wt.Status.IsDirty {
			counts := []string{}
			if wt.Status.StagedCount > 0 {
				counts = append(counts, fmt.Sprintf("S:%d", wt.Status.StagedCount))
			}
			if wt.Status.ModifiedCount > 0 {
				counts = append(counts, fmt.Sprintf("M:%d", wt.Status.ModifiedCount))
			}
			if wt.Status.UntrackedCount > 0 {
				counts = append(counts, fmt.Sprintf("?:%d", wt.Status.UntrackedCount))
			}
			statusStr = worktreeDirtyStyle.Render(fmt.Sprintf("● %s", strings.Join(counts, " ")))
		} else {
			statusStr = worktreeCleanStyle.Render("✓ Clean")
		}
	} else {
		statusStr = worktreeErrorStyle.Render("⚠ Unknown")
	}

	// Combine all parts
	return fmt.Sprintf("%-50s %-20s %s", pathStr, branchStr, statusStr)
}

// Helper function to sort strings (since it's not in the standard library pre-1.21)
func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}