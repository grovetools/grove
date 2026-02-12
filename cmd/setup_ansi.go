package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/theme"
)

// ANSI art generation helpers for the setup wizard
// These provide visual previews of what the configured tools will look like

// renderNavPreview renders a preview of the ecosystem structure in nav
// Shows just Workspace and Path columns with proper alignment
// The ecosystemPath and optional newProjectName are used to generate the preview
func renderNavPreview(ecosystemPath string, newProjectName string, width int) string {
	t := theme.DefaultTheme

	// Resolve root info from path
	rootPath := ecosystemPath
	if rootPath == "" {
		rootPath = "~/Code/my-projects"
	}

	// Clean the path and get root name
	cleanRoot := filepath.Clean(rootPath)
	rootName := filepath.Base(cleanRoot)
	if rootName == "." || rootName == "/" || rootName == "" {
		rootName = "my-projects"
	}

	// Use minimal layout for very narrow screens
	if width < 60 {
		return renderMinimalNavPreview(rootName, rootPath, newProjectName)
	}

	var sb strings.Builder

	// Calculate column widths - use more space for workspace to fit names
	wsColWidth := 20
	pathColWidth := width - wsColWidth - 10 // Account for borders and spacing
	if pathColWidth < 20 {
		pathColWidth = 20
	}
	if pathColWidth > 45 {
		pathColWidth = 45
	}

	// Build the table using lipgloss styles
	borderStyle := t.Muted
	headerStyle := t.Bold
	ecoStyle := t.Highlight
	projectStyle := t.Normal
	pathStyle := t.Muted

	// Helper to truncate a name if needed (before adding prefix/icon)
	truncateName := func(name string, maxLen int) string {
		if len(name) > maxLen {
			return name[:maxLen-1] + "…"
		}
		return name
	}

	// Helper to pad string to width
	padTo := func(s string, w int) string {
		// Count visible length (approximate - icons are ~2 chars wide visually)
		return fmt.Sprintf("%-*s", w, s)
	}

	// Table header
	sb.WriteString(borderStyle.Render("  ╭" + strings.Repeat("─", wsColWidth+2) + "┬" + strings.Repeat("─", pathColWidth+2) + "╮") + "\n")
	sb.WriteString(borderStyle.Render("  │ ") + headerStyle.Render(padTo("WORKSPACE", wsColWidth)) + borderStyle.Render(" │ ") + headerStyle.Render(padTo("PATH", pathColWidth)) + borderStyle.Render(" │") + "\n")
	sb.WriteString(borderStyle.Render("  ├" + strings.Repeat("─", wsColWidth+2) + "┼" + strings.Repeat("─", pathColWidth+2) + "┤") + "\n")

	// Root row (ecosystem) - icon takes ~2 visual chars + 1 space = 3
	displayName := truncateName(rootName, wsColWidth-3)
	rootDisplay := padTo(theme.IconTree+" "+displayName, wsColWidth)
	pathDisplay := truncateName(rootPath, pathColWidth)
	sb.WriteString(borderStyle.Render("  │ ") + ecoStyle.Render(rootDisplay) + borderStyle.Render(" │ ") + pathStyle.Render(padTo(pathDisplay, pathColWidth)) + borderStyle.Render(" │") + "\n")

	// Generic example projects
	exampleProjects := []struct {
		name   string
		prefix string
	}{
		{"project-a", "├─"},
		{"project-b", "├─"},
	}

	// If new project is being added, use it as the last one
	if newProjectName != "" {
		exampleProjects = append(exampleProjects, struct {
			name   string
			prefix string
		}{newProjectName, "└─"})
		// Fix the previous last item's prefix
		exampleProjects[len(exampleProjects)-2].prefix = "├─"
	} else {
		exampleProjects = append(exampleProjects, struct {
			name   string
			prefix string
		}{"project-c", "└─"})
	}

	for _, proj := range exampleProjects {
		// prefix (3) + icon (2) + space (1) = 6 chars of overhead
		projNameTrunc := truncateName(proj.name, wsColWidth-6)
		projDisplay := padTo(proj.prefix+" "+theme.IconRepo+" "+projNameTrunc, wsColWidth)
		projPath := truncateName(filepath.Join(rootPath, proj.name), pathColWidth)

		var nameStyle lipgloss.Style
		if proj.name == newProjectName && newProjectName != "" {
			nameStyle = t.Success // Highlight new project
		} else {
			nameStyle = projectStyle
		}

		sb.WriteString(borderStyle.Render("  │ ") + nameStyle.Render(projDisplay) + borderStyle.Render(" │ ") + pathStyle.Render(padTo(projPath, pathColWidth)) + borderStyle.Render(" │") + "\n")
	}

	// Table footer
	sb.WriteString(borderStyle.Render("  ╰" + strings.Repeat("─", wsColWidth+2) + "┴" + strings.Repeat("─", pathColWidth+2) + "╯") + "\n")

	// Legend
	sb.WriteString(t.Muted.Render(fmt.Sprintf("  Icons: %s ecosystem • %s project", theme.IconTree, theme.IconRepo)))

	return sb.String()
}

// renderMinimalNavPreview renders a simple list for very narrow screens
func renderMinimalNavPreview(rootName, rootPath, newProjectName string) string {
	t := theme.DefaultTheme

	var sb strings.Builder
	sb.WriteString(t.Highlight.Render(theme.IconTree+" "+rootName) + "\n")
	sb.WriteString(t.Muted.Render("  "+rootPath) + "\n")
	sb.WriteString("  ├─ " + theme.IconRepo + " project-a\n")
	sb.WriteString("  ├─ " + theme.IconRepo + " project-b\n")
	if newProjectName != "" {
		sb.WriteString("  └─ " + t.Success.Render(theme.IconRepo+" "+newProjectName) + " (new)\n")
	} else {
		sb.WriteString("  └─ " + theme.IconRepo + " project-c\n")
	}
	return sb.String()
}

// renderGmuxView is kept for backward compatibility, delegates to renderNavPreview
// TODO: Remove once all callers are updated
func renderGmuxView(ecosystemName string, projectName string, isNew bool, width int) string {
	// Convert ecosystem name to a path for the new function
	rootPath := "~/Code/" + ecosystemName
	if ecosystemName == "" {
		rootPath = "~/Code/my-projects"
	}
	return renderNavPreview(rootPath, projectName, width)
}

// renderMinimalGmuxView is kept for backward compatibility
func renderMinimalGmuxView(ecosystemName, projectName string) string {
	rootPath := "~/Code/" + ecosystemName
	if ecosystemName == "" {
		rootPath = "~/Code/my-projects"
	}
	return renderMinimalNavPreview(ecosystemName, rootPath, projectName)
}

// renderNotebookPreview renders a simple preview of the notebook directory structure
func renderNotebookPreview(notebookPath string, width int) string {
	t := theme.DefaultTheme

	if notebookPath == "" {
		notebookPath = "~/notebooks"
	}

	var sb strings.Builder

	// Show a simple tree structure
	sb.WriteString("  " + t.Highlight.Render(theme.IconNote+" "+notebookPath) + "\n")
	sb.WriteString("  " + t.Muted.Render("├── grovetools/") + "\n")
	sb.WriteString("  " + t.Muted.Render("│   ├── inbox/") + "\n")
	sb.WriteString("  " + t.Muted.Render("│   └── plans/") + "\n")
	sb.WriteString("  " + t.Muted.Render("├── my-projects/") + "\n")
	sb.WriteString("  " + t.Muted.Render("│   └── inbox/") + "\n")
	sb.WriteString("  " + t.Muted.Render("└── global/") + "\n")
	sb.WriteString("  " + t.Muted.Render("    └── inbox/") + "\n")
	sb.WriteString("\n")
	sb.WriteString("  " + t.Muted.Render("Each workspace gets its own folder for notes and plans."))

	return sb.String()
}

// renderNbView returns a captured nb tui tree view with ANSI styling
// The workspace name is substituted into the template
func renderNbView(workspaceName string, width int) string {
	wsName := workspaceName
	if wsName == "" {
		wsName = "my-project"
	}

	// Use a simpler view if width is very constrained
	if width < 50 {
		template := "  \x1b[1mnb > %s\x1b[0m\n" +
			"\n" +
			"   \x1b[93m▶ \x1b[39m󰇧 \x1b[3;4mglobal\x1b[0m\n" +
			"      \x1b[3m%s\x1b[0m\n" +
			"     \x1b[2m│ \x1b[0m\x1b[93m󰚇\x1b[39m inbox\x1b[2m (2)\x1b[0m\n" +
			"     \x1b[2m│ \x1b[0m\x1b[34m󰠡\x1b[39m \x1b[3mplans\x1b[0m\x1b[2m (2)\x1b[0m\n" +
			"     \x1b[2m└ \x1b[0m\x1b[32m󰄳\x1b[39m completed\x1b[2m (2)\x1b[0m\n"

		return fmt.Sprintf(template, wsName, wsName)
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
		{"C-f", "Nav session switcher"},
		{"M-p", "Nav key manager"},
		{"M-h", "Nav session history"},
		{"M-w", "Nav window selector"},
		{"M-v", "Context (cx) viewer"},
		{"C-n", "Notes TUI"},
		{"M-s", "Hooks sessions"},
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
