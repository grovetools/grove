package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/tui/theme"
)

// ANSI art generation helpers for the setup wizard
// These provide visual previews of what the configured tools will look like

// renderGmuxView generates a styled representation of gmux table view
// showing an ecosystem with projects in table format like the real gmux TUI
func renderGmuxView(ecosystemName string, projectName string, isNew bool, width int) string {
	var content strings.Builder

	// Styles
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true)
	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("141"))
	successStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("46"))
	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))
	newStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("46")).
		Bold(true)
	mutedStyle := theme.DefaultTheme.Muted
	borderStyle := mutedStyle

	// Use ecosystem name or default
	ecoName := ecosystemName
	if ecoName == "" {
		ecoName = "my-projects"
	}

	// Filter prompt
	content.WriteString(mutedStyle.Render("[Focus: "))
	content.WriteString(headerStyle.Render(ecoName))
	content.WriteString(mutedStyle.Render("] > Press / to filter..."))
	content.WriteString("\n\n")

	// Table header
	content.WriteString(borderStyle.Render("  ╭───┬────┬───────────────┬──────────────┬───────┬───────────╮\n"))
	content.WriteString(borderStyle.Render("  │"))
	content.WriteString(mutedStyle.Render(" K "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" CX "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" WORKSPACE     "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render("  BRANCH      "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" 󰊢 GIT "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" 󰊢 CHANGES "))
	content.WriteString(borderStyle.Render("│\n"))
	content.WriteString(borderStyle.Render("  ├───┼────┼───────────────┼──────────────┼───────┼───────────┤\n"))

	// Ecosystem row
	content.WriteString(borderStyle.Render("󰜴 │   │    │"))
	content.WriteString(headerStyle.Render(fmt.Sprintf("  %-12s", ecoName)))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" -            "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" -     "))
	content.WriteString(borderStyle.Render("│"))
	content.WriteString(mutedStyle.Render(" -         "))
	content.WriteString(borderStyle.Render("│\n"))

	// Project entries
	projects := []struct {
		name    string
		branch  string
		git     string
		changes string
		isLast  bool
	}{
		{"grove-core", "main", "↑1", "-", false},
		{"grove-flow", "feat/jobs", "✗", "N:1", false},
		{"api-server", "main", "✓", "M:1 +2 -1", false},
	}

	for _, p := range projects {
		treeChar := "├─"
		if p.isLast && projectName == "" {
			treeChar = "└─"
		}

		content.WriteString(borderStyle.Render("  │   │    │ "))
		content.WriteString(mutedStyle.Render(treeChar + " "))
		content.WriteString(fmt.Sprintf("%-10s", p.name))
		content.WriteString(borderStyle.Render("│"))
		content.WriteString(branchStyle.Render(fmt.Sprintf("  %-12s", p.branch)))
		content.WriteString(borderStyle.Render("│"))

		// Git status with color
		gitStatus := p.git
		if gitStatus == "✓" {
			gitStatus = successStyle.Render(" ✓    ")
		} else if gitStatus == "✗" {
			gitStatus = warningStyle.Render(" ✗    ")
		} else {
			gitStatus = warningStyle.Render(fmt.Sprintf(" %-5s", p.git))
		}
		content.WriteString(gitStatus)
		content.WriteString(borderStyle.Render("│"))
		content.WriteString(mutedStyle.Render(fmt.Sprintf(" %-9s", p.changes)))
		content.WriteString(borderStyle.Render("│\n"))
	}

	// Add the new project if specified
	if projectName != "" {
		content.WriteString(borderStyle.Render("  │   │    │ "))
		content.WriteString(mutedStyle.Render("└─ "))
		content.WriteString(newStyle.Render(fmt.Sprintf("%-10s", projectName)))
		content.WriteString(borderStyle.Render("│"))
		content.WriteString(branchStyle.Render("  main        "))
		content.WriteString(borderStyle.Render("│"))
		content.WriteString(newStyle.Render(" (new)"))
		content.WriteString(borderStyle.Render("│"))
		content.WriteString(mutedStyle.Render(" -         "))
		content.WriteString(borderStyle.Render("│\n"))
	}

	content.WriteString(borderStyle.Render("  ╰───┴────┴───────────────┴──────────────┴───────┴───────────╯\n"))

	// Legend
	content.WriteString(mutedStyle.Render("Icons:  current •  active •  ecosystem •  repo"))

	return content.String()
}

// renderNbView returns a captured nb tui tree view with ANSI styling
// The workspace name is substituted into the template
func renderNbView(workspaceName string, width int) string {
	wsName := workspaceName
	if wsName == "" {
		wsName = "my-project"
	}

	// Captured from real nb tui output - using \x1b for escape character
	template := "  \x1b[1mnb > %s\x1b[0m\n" +
		"\n" +
		"   \x1b[93m▶ \x1b[39m󰇧 \x1b[3;4mglobal\x1b[0m\n" +
		"      \x1b[3m%s\x1b[0m\n" +
		"     \x1b[2m│ \x1b[0m\x1b[93m󰚇\x1b[39m inbox\x1b[2m (2)\x1b[0m\n" +
		"     \x1b[2m│ \x1b[0m\x1b[31m\x1b[39m issues\x1b[2m (3)\x1b[0m\n" +
		"     \x1b[2m│ \x1b[0m\x1b[34m󰠡\x1b[39m \x1b[3mplans\x1b[0m\x1b[2m (2)\x1b[0m\n" +
		"     \x1b[2m│ └ \x1b[0m\x1b[34m󰦖 \x1b[3m\x1b[39minitial-setup\x1b[0m\x1b[2m [\x1b[3mnote:\x1b[0m ← setup]\x1b[2m (3)\x1b[0m\n" +
		"     \x1b[2m│ \x1b[0m\x1b[34m󰔟\x1b[39m \x1b[3min_progress\x1b[0m\x1b[2m (1)\x1b[0m\n" +
		"     \x1b[2m│ └ \x1b[0m\x1b[34m󰡯\x1b[39m 20260106-kickoff.md\x1b[2m [\x1b[3mplan:\x1b[0m → setup]\n" +
		"     \x1b[2m└ \x1b[0m\x1b[32m󰄳\x1b[39m completed\x1b[2m (2)\x1b[0m\n" +
		"\n" +
		"  \x1b[2m6 notes shown | Press \x1b[0m\x1b[1m\x1b[93m?\x1b[0m\x1b[2m for help\x1b[0m"

	return fmt.Sprintf(template, wsName, wsName)
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
