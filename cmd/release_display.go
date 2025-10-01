package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	grovelogging "github.com/mattsolo1/grove-core/logging"
)

// Define reusable styles for the release command
var (
	// Base styles
	releaseTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("212")).
				MarginBottom(1)

	releaseSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("141")).
				MarginTop(1).
				MarginBottom(1)

	releaseSuccessStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("42"))

	releaseWarningStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("214"))

	releaseErrorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("196"))

	releaseInfoStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("247"))

	releaseHighlightStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("42"))

	releaseDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// Box styles for different phases
	phaseBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(1, 2)

	successBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("42")).
			Padding(1, 2)

	// Progress indicators
	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212"))

	releaseCheckmarkStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("42"))

	crossStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
)

// Initialize loggers for display functions
var (
	log       = grovelogging.NewLogger("grove-meta")
	prettyLog = grovelogging.NewPrettyLogger()
)

// Phase display helpers
func displayPhase(title string) {
	prettyLog.InfoPretty(fmt.Sprintf("🚀 %s", title))
}

func displaySection(title string) {
	prettyLog.InfoPretty(title)
}

func displaySuccess(message string) {
	prettyLog.Success(message)
}

func displayWarning(message string) {
	prettyLog.WarnPretty(message)
}

func displayError(message string) {
	prettyLog.ErrorPretty(message, nil)
}

func displayInfo(message string) {
	prettyLog.InfoPretty(message)
}

// Progress display helpers
func displayProgress(message string) {
	prettyLog.InfoPretty(fmt.Sprintf("◆ %s", message))
}

func displayComplete(message string) {
	prettyLog.Success(message)
}

func displayFailed(message string) {
	prettyLog.ErrorPretty(message, nil)
}

// Create a styled pre-flight checks table
func displayPreflightTable(headers []string, rows [][]string) {
	re := lipgloss.NewRenderer(os.Stdout)

	baseStyle := re.NewStyle().Padding(0, 1)
	headerStyle := baseStyle.Copy().Bold(true).Foreground(lipgloss.Color("255"))

	// Style each row based on status
	styledRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			styledRows = append(styledRows, row)
			continue
		}

		// Check status column (index 2)
		status := row[2]
		issues := ""
		if len(row) > 3 {
			issues = row[3]
		}

		var styledRow []string
		if strings.Contains(status, "✓") {
			// Clean status
			if issues != "" && strings.Contains(issues, "will push") {
				// Has pending push
				styledRow = []string{
					row[0],
					row[1],
					successStyle.Render(status),
					releaseInfoStyle.Render(issues),
				}
			} else {
				// Completely clean
				styledRow = []string{
					row[0],
					row[1],
					successStyle.Render(status),
					releaseDimStyle.Render(issues),
				}
			}
		} else if strings.Contains(status, "○") || strings.Contains(status, "Changelog") {
			// Changelog-only status (orange)
			styledRow = []string{
				row[0],
				row[1],
				releaseWarningStyle.Render(status),
				releaseInfoStyle.Render(issues),
			}
		} else if strings.Contains(status, "✗") {
			// Dirty status
			styledRow = []string{
				errorStyle.Render(row[0]),
				errorStyle.Render(row[1]),
				errorStyle.Render(status),
				errorStyle.Render(issues),
			}
		} else {
			// Unknown/error status
			styledRow = []string{
				releaseWarningStyle.Render(row[0]),
				releaseWarningStyle.Render(row[1]),
				releaseWarningStyle.Render(status),
				releaseWarningStyle.Render(issues),
			}
		}

		styledRows = append(styledRows, styledRow)
	}

	// Create table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(styledRows...)

	// Apply header styling
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return headerStyle
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	prettyLog.InfoPretty(t.String())
}

// Create a progress box for release operations
func displayReleaseProgress(title string, items []string) {
	content := []string{
		releaseTitleStyle.Render(title),
		"",
	}
	content = append(content, items...)

	box := phaseBoxStyle.Render(strings.Join(content, "\n"))
	prettyLog.InfoPretty(box)
}

// Display final success message
func displayFinalSuccess(version string, repoCount int) {
	content := []string{
		successStyle.Render("✅ Release Successfully Created"),
		"",
		fmt.Sprintf("New ecosystem version: %s", releaseHighlightStyle.Render(version)),
		fmt.Sprintf("Repositories released: %s", releaseHighlightStyle.Render(fmt.Sprintf("%d", repoCount))),
		"",
		"GitHub Actions will now:",
		"  • Build and release each tool independently",
		"  • Create GitHub releases with artifacts",
		"  • Update release notes",
		"",
		releaseInfoStyle.Render("Monitor the individual tool releases in their respective repositories."),
	}

	box := successBoxStyle.Render(strings.Join(content, "\n"))
	prettyLog.Blank()
	prettyLog.InfoPretty(box)
}

// Display release summary with better formatting
func displayReleaseSummary(releaseLevels [][]string, versions map[string]string, currentVersions map[string]string, hasChanges map[string]bool) {
	displaySection("📋 Release Order (by dependency level)")

	levelCount := 0
	for _, level := range releaseLevels {
		// Check if this level has any repos with changes
		var reposInLevel []string
		for _, repo := range level {
			if _, hasVersion := versions[repo]; hasVersion && hasChanges[repo] {
				reposInLevel = append(reposInLevel, repo)
			}
		}

		if len(reposInLevel) > 0 {
			levelCount++
			prettyLog.Blank()
			prettyLog.InfoPretty(fmt.Sprintf("Level %d (can release in parallel):", levelCount))

			for _, repo := range reposInLevel {
				current := currentVersions[repo]
				if current == "" {
					current = "-"
				}
				proposed := versions[repo]
				increment := getVersionIncrement(current, proposed)

				prettyLog.InfoPretty(fmt.Sprintf("  • %s: %s → %s (%s)",
					repo,
					releaseDimStyle.Render(current),
					releaseHighlightStyle.Render(proposed),
					releaseInfoStyle.Render(increment)))
			}
		}
	}

	if levelCount == 1 {
		displayInfo("\n📝 Note: All repositories are independent and will be released in parallel.")
	}
}
