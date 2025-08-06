package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-meta/pkg/aggregator"
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

var (
	currentTableView bool
)

func NewWorkspaceCurrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show notebook current notes for all workspaces",
		Long:  "Display current notes (from nb list --type current) for each workspace in the monorepo",
		RunE:  runWorkspaceCurrent,
	}

	cmd.Flags().BoolVar(&currentTableView, "table", false, "Show all current notes in a single table ordered by date")

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

		// Check if table view is requested
		if currentTableView {
			return renderCurrentTable(results)
		}

		// Default grouped view
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

// renderCurrentTable renders all current notes in a single table sorted by date
func renderCurrentTable(results map[string][]CurrentNote) error {
	// Collect all notes with workspace info
	type noteWithWorkspace struct {
		Note      CurrentNote
		Workspace string
	}
	
	var allNotes []noteWithWorkspace
	for wsName, notes := range results {
		for _, note := range notes {
			allNotes = append(allNotes, noteWithWorkspace{
				Note:      note,
				Workspace: wsName,
			})
		}
	}
	
	if len(allNotes) == 0 {
		fmt.Println(noCurrentStyle.Render("No current notes found in any workspace."))
		return nil
	}
	
	// Sort by ModifiedAt descending (most recent first)
	sort.Slice(allNotes, func(i, j int) bool {
		// If one has zero time, put it at the end
		if allNotes[i].Note.ModifiedAt.IsZero() && !allNotes[j].Note.ModifiedAt.IsZero() {
			return false
		}
		if !allNotes[i].Note.ModifiedAt.IsZero() && allNotes[j].Note.ModifiedAt.IsZero() {
			return true
		}
		return allNotes[i].Note.ModifiedAt.After(allNotes[j].Note.ModifiedAt)
	})
	
	// Create table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers("WORKSPACE", "TITLE", "ID", "MODIFIED").
		Width(100). // Set a reasonable total width
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return lipgloss.NewStyle().
					Foreground(lipgloss.Color("255")).
					Bold(true).
					Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})
	
	// Add rows
	for _, nw := range allNotes {
		note := nw.Note
		
		// Format time
		timeStr := ""
		if !note.ModifiedAt.IsZero() {
			timeStr = formatTimeAgo(note.ModifiedAt)
			// Highlight recent modifications (within last day)
			if time.Since(note.ModifiedAt) < 24*time.Hour {
				timeStr = "● " + timeStr
			}
		}
		
		// Truncate ID to 8 characters
		idStr := note.ID
		if len(idStr) > 8 {
			idStr = idStr[:8]
		}
		
		// Format title
		title := note.Title
		if title == "" {
			title = "(untitled)"
		}
		
		t.Row(
			nw.Workspace,
			title,
			idStr,
			timeStr,
		)
	}
	
	fmt.Println(t)
	return nil
}