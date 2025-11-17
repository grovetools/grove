package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tablecomponent "github.com/mattsolo1/grove-core/tui/components/table"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/theme"
)

// Define reusable styles for the release command
var (
	// Base styles
	releaseTitleStyle = theme.DefaultTheme.Header.Copy().
				MarginBottom(1)

	releaseSectionStyle = theme.DefaultTheme.Header.Copy().
				MarginTop(1).
				MarginBottom(1)

	releaseSuccessStyle = theme.DefaultTheme.Success.Copy().
				Bold(true)

	releaseWarningStyle = theme.DefaultTheme.Warning.Copy().
				Bold(true)

	releaseErrorStyle = theme.DefaultTheme.Error.Copy().
				Bold(true)

	releaseInfoStyle = theme.DefaultTheme.Info

	releaseHighlightStyle = theme.DefaultTheme.Success.Copy().
				Bold(true)

	releaseDimStyle = theme.DefaultTheme.Muted

	// Box styles for different phases
	phaseBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
			Padding(1, 2)

	successBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.DefaultTheme.Success.GetForeground()).
			Padding(1, 2)

	// Progress indicators
	spinnerStyle = theme.DefaultTheme.Highlight

	releaseCheckmarkStyle = theme.DefaultTheme.Success

	crossStyle = theme.DefaultTheme.Error
)

// Initialize loggers for display functions
var (
	log       = grovelogging.NewLogger("grove-meta")
	prettyLog = grovelogging.NewPrettyLogger()
)

// Phase display helpers
func displayPhase(title string) {
	prettyLog.InfoPretty(fmt.Sprintf("%s %s", theme.IconSparkle, title))
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
	prettyLog.InfoPretty(fmt.Sprintf("%s %s", theme.IconWorktree, message))
}

func displayComplete(message string) {
	prettyLog.Success(message)
}

func displayFailed(message string) {
	prettyLog.ErrorPretty(message, nil)
}

// Create a styled pre-flight checks table
func displayPreflightTable(headers []string, rows [][]string) {

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
		if strings.Contains(status, theme.IconSuccess) {
			// Clean status
			if issues != "" && strings.Contains(issues, "will push") {
				// Has pending push
				styledRow = []string{
					row[0],
					row[1],
					theme.DefaultTheme.Success.Render(status),
					releaseInfoStyle.Render(issues),
				}
			} else {
				// Completely clean
				styledRow = []string{
					row[0],
					row[1],
					theme.DefaultTheme.Success.Render(status),
					releaseDimStyle.Render(issues),
				}
			}
		} else if strings.Contains(status, theme.IconUnselect) || strings.Contains(status, "Changelog") {
			// Changelog-only status (orange)
			styledRow = []string{
				row[0],
				row[1],
				releaseWarningStyle.Render(status),
				releaseInfoStyle.Render(issues),
			}
		} else if strings.Contains(status, theme.IconError) {
			// Dirty status
			styledRow = []string{
				theme.DefaultTheme.Error.Render(row[0]),
				theme.DefaultTheme.Error.Render(row[1]),
				theme.DefaultTheme.Error.Render(status),
				theme.DefaultTheme.Error.Render(issues),
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

	// Create styled table
	t := tablecomponent.NewStyledTable().
		Headers(headers...).
		Rows(styledRows...)

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
		theme.DefaultTheme.Success.Render(theme.IconSuccess + " Release Successfully Created"),
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
	displaySection(fmt.Sprintf("%s Release Order (by dependency level)", theme.IconPlan))

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
		displayInfo(fmt.Sprintf("\n%s Note: All repositories are independent and will be released in parallel.", theme.IconNote))
	}
}
