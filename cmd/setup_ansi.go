package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/theme"
)

// ANSI art generation helpers for the setup wizard
// These provide visual previews of what the configured tools will look like

// renderGmuxView returns a captured gmux sessionize view with ANSI styling
// The ecosystem name and optional new project name are substituted into the template
func renderGmuxView(ecosystemName string, projectName string, isNew bool, width int) string {
	ecoName := ecosystemName
	if ecoName == "" {
		ecoName = "my-projects"
	}

	// Pad ecosystem name to 14 chars for alignment in header and table
	paddedEcoName := fmt.Sprintf("%-14s", ecoName)
	if len(paddedEcoName) > 14 {
		paddedEcoName = paddedEcoName[:14]
	}

	// Captured from real gmux sz output - using \x1b for escape character
	// Shows a minimal ecosystem with sample projects
	var template string

	if projectName != "" {
		// Template with new project highlighted
		paddedProjectName := fmt.Sprintf("%-12s", projectName)
		if len(paddedProjectName) > 12 {
			paddedProjectName = paddedProjectName[:12]
		}

		template = "\x1b[1m\x1b[36m[Focus: %s]\x1b[0m > \x1b[38;5;240mPress / to filter...\x1b[39m\n" +
			"\n" +
			"  \x1b[90m╭──────────────────┬──────────┬─────────┬───────────────┬──────────────────────────────────────╮\x1b[39m\n" +
			"  \x1b[90m│\x1b[39m \x1b[1mWORKSPACE\x1b[0m        \x1b[90m│\x1b[39m \x1b[1m BRANCH\x1b[0m \x1b[90m│\x1b[39m \x1b[1m󰊢 GIT\x1b[0m   \x1b[90m│\x1b[39m \x1b[1m󰊢 CHANGES\x1b[0m     \x1b[90m│\x1b[39m \x1b[1mPATH\x1b[0m                                 \x1b[90m│\x1b[39m\n" +
			"  \x1b[90m├──────────────────┼──────────┼─────────┼───────────────┼──────────────────────────────────────┤\x1b[39m\n" +
			"  \x1b[90m│\x1b[39m \x1b[1m\x1b[36m\x1b[0m%s \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[32m*\x1b[0m       \x1b[90m│\x1b[39m -             \x1b[90m│\x1b[39m \x1b[2m~/Code/%s\x1b[0m                    \x1b[90m│\x1b[39m\n" +
			"  \x1b[90m│\x1b[39m ├─ \x1b[2m\x1b[0mgrove-core   \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[36m↑1\x1b[0m      \x1b[90m│\x1b[39m -             \x1b[90m│\x1b[39m \x1b[2m~/Code/%s/grove-core\x1b[0m         \x1b[90m│\x1b[39m\n" +
			"  \x1b[90m│\x1b[39m ├─ \x1b[2m\x1b[0mgrove-flow   \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[33mx\x1b[0m \x1b[1m\x1b[36m↑2\x1b[0m    \x1b[90m│\x1b[39m \x1b[1m\x1b[33mM:1\x1b[0m \x1b[1m\x1b[32m+6\x1b[0m \x1b[1m\x1b[31m-2\x1b[0m    \x1b[90m│\x1b[39m \x1b[2m~/Code/%s/grove-flow\x1b[0m         \x1b[90m│\x1b[39m\n" +
			"  \x1b[90m│\x1b[39m └─ \x1b[1m\x1b[32m\x1b[0m%s \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[32m(new)\x1b[0m   \x1b[90m│\x1b[39m -             \x1b[90m│\x1b[39m \x1b[2m~/Code/%s/%s\x1b[0m  \x1b[90m│\x1b[39m\n" +
			"  \x1b[90m╰──────────────────┴──────────┴─────────┴───────────────┴──────────────────────────────────────╯\x1b[39m\n" +
			"  \x1b[2mIcons: \x1b[1m\x1b[36m\x1b[0m current • \x1b[1m\x1b[93m\x1b[0m active • \x1b[0m ecosystem • \x1b[0m repo\x1b[0m"

		return fmt.Sprintf(template, ecoName, paddedEcoName, ecoName, ecoName, ecoName, paddedProjectName, ecoName, projectName)
	}

	// Template without new project
	template = "\x1b[1m\x1b[36m[Focus: %s]\x1b[0m > \x1b[38;5;240mPress / to filter...\x1b[39m\n" +
		"\n" +
		"  \x1b[90m╭──────────────────┬──────────┬─────────┬───────────────┬──────────────────────────────────────╮\x1b[39m\n" +
		"  \x1b[90m│\x1b[39m \x1b[1mWORKSPACE\x1b[0m        \x1b[90m│\x1b[39m \x1b[1m BRANCH\x1b[0m \x1b[90m│\x1b[39m \x1b[1m󰊢 GIT\x1b[0m   \x1b[90m│\x1b[39m \x1b[1m󰊢 CHANGES\x1b[0m     \x1b[90m│\x1b[39m \x1b[1mPATH\x1b[0m                                 \x1b[90m│\x1b[39m\n" +
		"  \x1b[90m├──────────────────┼──────────┼─────────┼───────────────┼──────────────────────────────────────┤\x1b[39m\n" +
		"  \x1b[90m│\x1b[39m \x1b[1m\x1b[36m\x1b[0m%s \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[32m*\x1b[0m       \x1b[90m│\x1b[39m -             \x1b[90m│\x1b[39m \x1b[2m~/Code/%s\x1b[0m                    \x1b[90m│\x1b[39m\n" +
		"  \x1b[90m│\x1b[39m ├─ \x1b[2m\x1b[0mgrove-core   \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[36m↑1\x1b[0m      \x1b[90m│\x1b[39m -             \x1b[90m│\x1b[39m \x1b[2m~/Code/%s/grove-core\x1b[0m         \x1b[90m│\x1b[39m\n" +
		"  \x1b[90m│\x1b[39m ├─ \x1b[2m\x1b[0mgrove-flow   \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[33mx\x1b[0m \x1b[1m\x1b[36m↑2\x1b[0m    \x1b[90m│\x1b[39m \x1b[1m\x1b[33mM:1\x1b[0m \x1b[1m\x1b[32m+6\x1b[0m \x1b[1m\x1b[31m-2\x1b[0m    \x1b[90m│\x1b[39m \x1b[2m~/Code/%s/grove-flow\x1b[0m         \x1b[90m│\x1b[39m\n" +
		"  \x1b[90m│\x1b[39m └─ \x1b[2m\x1b[0mapi-server   \x1b[90m│\x1b[39m \x1b[2m\x1b[0mmain   \x1b[90m│\x1b[39m \x1b[1m\x1b[32m*\x1b[0m       \x1b[90m│\x1b[39m \x1b[1m\x1b[32m+21\x1b[0m \x1b[1m\x1b[31m-5\x1b[0m       \x1b[90m│\x1b[39m \x1b[2m~/Code/%s/api-server\x1b[0m         \x1b[90m│\x1b[39m\n" +
		"  \x1b[90m╰──────────────────┴──────────┴─────────┴───────────────┴──────────────────────────────────────╯\x1b[39m\n" +
		"  \x1b[2mIcons: \x1b[1m\x1b[36m\x1b[0m current • \x1b[1m\x1b[93m\x1b[0m active • \x1b[0m ecosystem • \x1b[0m repo\x1b[0m"

	return fmt.Sprintf(template, ecoName, paddedEcoName, ecoName, ecoName, ecoName, ecoName)
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
