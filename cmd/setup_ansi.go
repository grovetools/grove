package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/tui/theme"
)

// ANSI art generation helpers for the setup wizard
// These provide visual previews of what the configured tools will look like

// renderGmuxView generates a styled representation of gmux sz output
// showing an ecosystem with several projects, some with git modifications
func renderGmuxView(projectName string, isNew bool, width int) string {
	var content strings.Builder

	// Styles
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true)
	dirtyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))
	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("141"))
	newStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("46")).
		Bold(true)
	mutedStyle := theme.DefaultTheme.Muted

	// Simulated gmux output header
	content.WriteString(headerStyle.Render("grove-projects"))
	content.WriteString(mutedStyle.Render(" (ecosystem)"))
	content.WriteString("\n")
	content.WriteString(strings.Repeat("─", min(width-8, 60)))
	content.WriteString("\n")

	// Project entries
	projects := []struct {
		name   string
		branch string
		dirty  bool
	}{
		{"grove-core", "main", false},
		{"grove-flow", "feature/jobs", true},
		{"grove-nb", "main", false},
		{"api-server", "fix/auth-bug", true},
	}

	for _, p := range projects {
		prefix := "  "
		suffix := ""

		if p.dirty {
			suffix = dirtyStyle.Render(" *")
		}

		branchInfo := branchStyle.Render(fmt.Sprintf("[%s]", p.branch))

		content.WriteString(fmt.Sprintf("%s%s %s%s\n", prefix, p.name, branchInfo, suffix))
	}

	// Add the new project if specified
	if projectName != "" {
		prefix := "  "
		marker := ""
		if isNew {
			marker = newStyle.Render(" (new)")
		}
		content.WriteString(fmt.Sprintf("%s%s%s\n", prefix, newStyle.Render(projectName), marker))
	}

	// Box it
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(min(width-12, 50))

	return boxStyle.Render(content.String())
}

// renderNbView generates a styled representation of the nb tui interface
func renderNbView(width int) string {
	var content strings.Builder

	// Styles
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true)
	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("255"))
	tagStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("141"))
	dateStyle := theme.DefaultTheme.Muted

	content.WriteString(headerStyle.Render("Notes"))
	content.WriteString(dateStyle.Render(" - default workspace"))
	content.WriteString("\n")
	content.WriteString(strings.Repeat("─", min(width-8, 50)))
	content.WriteString("\n")

	// Sample notes
	notes := []struct {
		title    string
		tags     []string
		date     string
		selected bool
	}{
		{"Project kickoff meeting", []string{"meeting"}, "2h ago", true},
		{"API design notes", []string{"design", "api"}, "1d ago", false},
		{"Bug investigation: auth flow", []string{"bug"}, "2d ago", false},
		{"Weekly standup notes", []string{"meeting"}, "3d ago", false},
	}

	for _, n := range notes {
		line := fmt.Sprintf("  %s", n.title)
		for _, tag := range n.tags {
			line += " " + tagStyle.Render("#"+tag)
		}
		line += " " + dateStyle.Render(n.date)

		if n.selected {
			content.WriteString(selectedStyle.Render(line))
		} else {
			content.WriteString(line)
		}
		content.WriteString("\n")
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(min(width-12, 55))

	return boxStyle.Render(content.String())
}

// renderNoteCreationExample shows a sample nb new command and resulting file structure
func renderNoteCreationExample(notebookPath string, width int) string {
	var content strings.Builder

	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39"))
	pathStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))
	mutedStyle := theme.DefaultTheme.Muted

	// Command example
	content.WriteString(cmdStyle.Render("$ nb new \"Initial project plan\""))
	content.WriteString("\n\n")

	// Tree view of result
	content.WriteString(mutedStyle.Render("Creates:"))
	content.WriteString("\n")
	content.WriteString(pathStyle.Render(notebookPath))
	content.WriteString("\n")
	content.WriteString(mutedStyle.Render("└── default/"))
	content.WriteString("\n")
	content.WriteString(mutedStyle.Render("    └── "))
	content.WriteString(pathStyle.Render("initial-project-plan.md"))
	content.WriteString("\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(min(width-12, 60))

	return boxStyle.Render(content.String())
}

// renderTmuxConfig shows the exact tmux configuration that will be created
func renderTmuxConfig(width int) string {
	var content strings.Builder

	commentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true)
	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39"))

	content.WriteString(commentStyle.Render("# Key bindings added:\n"))
	content.WriteString("\n")

	bindings := []struct {
		key  string
		desc string
	}{
		{"C-p", "Flow status popup"},
		{"C-f", "Gmux session switcher"},
		{"M-v", "Context (cx) viewer"},
		{"C-n", "Notes TUI"},
		{"C-e", "Core editor"},
	}

	for _, b := range bindings {
		content.WriteString(keyStyle.Render(fmt.Sprintf("%-6s", b.key)))
		content.WriteString(cmdStyle.Render(b.desc))
		content.WriteString("\n")
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(min(width-12, 45))

	return boxStyle.Render(content.String())
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
