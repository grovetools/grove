package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovepm/grove/pkg/aggregator"
	"github.com/spf13/cobra"
)

var (
	// Styles for current notes display
	currentHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("213")).
		MarginTop(1).
		MarginBottom(0)

	currentTitleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))

	currentMetaStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Italic(true)

	currentBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("213")).
		Padding(0, 1).
		MarginLeft(2).
		MarginBottom(1)

	noCurrentStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")).
		Italic(true)
)

// Note represents a simplified version of a notebook note
type CurrentNote struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Path       string    `json:"path"`
	ModifiedAt time.Time `json:"modified_at"`
	CreatedAt  time.Time `json:"created_at"`
	Type       string    `json:"type"`
}

func NewWorkspaceCurrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show notebook current notes for all workspaces",
		Long:  "Display current notes (from nb list --type current) for each workspace in the monorepo",
		RunE:  runWorkspaceCurrent,
	}

	return cmd
}

func runWorkspaceCurrent(cmd *cobra.Command, args []string) error {
	// Collector function to get current notes for each workspace
	collector := func(workspacePath string, workspaceName string) ([]CurrentNote, error) {
		// Run nb list --type current --json
		nbCmd := exec.Command("nb", "list", "--type", "current", "--json")
		nbCmd.Dir = workspacePath
		
		output, err := nbCmd.Output()
		if err != nil {
			// nb might not be available in this workspace
			return []CurrentNote{}, nil
		}

		// Parse JSON output
		var notes []CurrentNote
		if err := json.Unmarshal(output, &notes); err != nil {
			return nil, fmt.Errorf("failed to parse nb output: %w", err)
		}

		return notes, nil
	}

	// Renderer function to display the results
	renderer := func(results map[string][]CurrentNote) error {
		if len(results) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		// Sort workspace names for consistent output
		var workspaceNames []string
		hasAnyCurrent := false
		for name, notes := range results {
			workspaceNames = append(workspaceNames, name)
			if len(notes) > 0 {
				hasAnyCurrent = true
			}
		}
		sortStrings(workspaceNames)

		if !hasAnyCurrent {
			fmt.Println(noCurrentStyle.Render("No current notes found in any workspace."))
			return nil
		}

		for _, wsName := range workspaceNames {
			notes := results[wsName]
			
			// Skip workspaces with no current notes
			if len(notes) == 0 {
				continue
			}

			// Print workspace header
			header := currentHeaderStyle.Render(fmt.Sprintf("📌 %s (%d current)", wsName, len(notes)))
			fmt.Println(header)

			// Build content for this workspace
			var lines []string
			for _, note := range notes {
				line := formatCurrentNote(note)
				lines = append(lines, line)
			}

			// Join and box the content
			content := strings.Join(lines, "\n\n")
			boxed := currentBoxStyle.Render(content)
			fmt.Println(boxed)
		}

		return nil
	}

	return aggregator.Run(collector, renderer)
}

func formatCurrentNote(note CurrentNote) string {
	// Format title
	title := currentTitleStyle.Render(note.Title)
	if title == "" {
		title = currentTitleStyle.Render("(untitled)")
	}

	// Format metadata
	timeAgo := formatTimeAgo(note.ModifiedAt)
	meta := currentMetaStyle.Render(fmt.Sprintf("%s • %s", note.ID[:8], timeAgo))

	return fmt.Sprintf("%s\n%s", title, meta)
}