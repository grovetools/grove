package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-meta/pkg/aggregator"
	"github.com/spf13/cobra"
)

var (
	// Styles for issues display
	issuesHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205")).
				MarginTop(1).
				MarginBottom(0)

	issueTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))

	issueMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)

	issueBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(0, 1).
			MarginLeft(2).
			MarginBottom(1)

	noIssuesStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242")).
			Italic(true)
)

// Note represents a simplified version of a notebook note
type Note struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Path       string    `json:"path"`
	ModifiedAt time.Time `json:"modified_at"`
	CreatedAt  time.Time `json:"created_at"`
	Type       string    `json:"type"`
}

func NewWorkspaceIssuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issues",
		Short: "Show notebook issues for all workspaces",
		Long:  "Display issues (from nb list --type issues) for each workspace in the monorepo",
		RunE:  runWorkspaceIssues,
	}

	return cmd
}

func runWorkspaceIssues(cmd *cobra.Command, args []string) error {
	// Collector function to get issues for each workspace
	collector := func(workspacePath string, workspaceName string) ([]Note, error) {
		// Run nb list --type issues --json
		nbCmd := exec.Command("nb", "list", "--type", "issues", "--json")
		nbCmd.Dir = workspacePath

		output, err := nbCmd.Output()
		if err != nil {
			// nb might not be available in this workspace
			return []Note{}, nil
		}

		// Parse JSON output
		var notes []Note
		if err := json.Unmarshal(output, &notes); err != nil {
			return nil, fmt.Errorf("failed to parse nb output: %w", err)
		}

		return notes, nil
	}

	// Renderer function to display the results
	renderer := func(results map[string][]Note) error {
		if len(results) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		// Sort workspace names for consistent output
		var workspaceNames []string
		hasAnyIssues := false
		for name, issues := range results {
			workspaceNames = append(workspaceNames, name)
			if len(issues) > 0 {
				hasAnyIssues = true
			}
		}
		sortStrings(workspaceNames)

		if !hasAnyIssues {
			fmt.Println(noIssuesStyle.Render("No issues found in any workspace."))
			return nil
		}

		for _, wsName := range workspaceNames {
			issues := results[wsName]

			// Skip workspaces with no issues
			if len(issues) == 0 {
				continue
			}

			// Print workspace header
			header := issuesHeaderStyle.Render(fmt.Sprintf("🐛 %s (%d issues)", wsName, len(issues)))
			fmt.Println(header)

			// Build content for this workspace
			var lines []string
			for _, issue := range issues {
				line := formatIssue(issue)
				lines = append(lines, line)
			}

			// Join and box the content
			content := strings.Join(lines, "\n\n")
			boxed := issueBoxStyle.Render(content)
			fmt.Println(boxed)
		}

		return nil
	}

	return aggregator.Run(collector, renderer)
}

func formatIssue(note Note) string {
	// Format title
	title := issueTitleStyle.Render(note.Title)
	if title == "" {
		title = issueTitleStyle.Render("(untitled)")
	}

	// Format metadata
	timeAgo := formatTimeAgo(note.ModifiedAt)
	meta := issueMetaStyle.Render(fmt.Sprintf("%s • %s", note.ID[:8], timeAgo))

	return fmt.Sprintf("%s\n%s", title, meta)
}

func formatTimeAgo(t time.Time) string {
	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
