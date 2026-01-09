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
	log  = grovelogging.NewLogger("grove-meta")
	ulog = grovelogging.NewUnifiedLogger("grove-meta.release")
)

// Phase display helpers
func displayPhase(title string) {
	ulog.Info("Release phase").
		Field("title", title).
		Pretty(fmt.Sprintf("%s %s", theme.IconSparkle, title)).
		Emit()
}

func displaySection(title string) {
	ulog.Info("Release section").
		Field("title", title).
		Pretty(title).
		Emit()
}

func displaySuccess(message string) {
	ulog.Success("Release operation successful").
		Field("message", message).
		Pretty(theme.IconSuccess + " " + message).
		Emit()
}

func displayWarning(message string) {
	ulog.Warn("Release warning").
		Field("message", message).
		Pretty(theme.IconWarning + " " + message).
		Emit()
}

func displayError(message string) {
	ulog.Error("Release error").
		Field("message", message).
		Pretty(theme.IconError + " " + message).
		Emit()
}

func displayInfo(message string) {
	ulog.Info("Release information").
		Field("message", message).
		Pretty(message).
		Emit()
}

// Progress display helpers
func displayProgress(message string) {
	ulog.Progress("Release in progress").
		Field("message", message).
		Pretty(fmt.Sprintf("%s %s", theme.IconWorktree, message)).
		Emit()
}

func displayComplete(message string) {
	ulog.Success("Release operation completed").
		Field("message", message).
		Pretty(theme.IconSuccess + " " + message).
		Emit()
}

func displayFailed(message string) {
	ulog.Error("Release operation failed").
		Field("message", message).
		Pretty(theme.IconError + " " + message).
		Emit()
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

	ulog.Info("Release preflight table").
		Field("row_count", len(styledRows)).
		Field("headers", headers).
		Pretty(t.String()).
		Emit()
}

// Create a progress box for release operations
func displayReleaseProgress(title string, items []string) {
	content := []string{
		releaseTitleStyle.Render(title),
		"",
	}
	content = append(content, items...)

	box := phaseBoxStyle.Render(strings.Join(content, "\n"))
	ulog.Info("Release progress").
		Field("title", title).
		Field("item_count", len(items)).
		Pretty(box).
		Emit()
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
	ulog.Info("Release completed").Pretty("\n").PrettyOnly().Emit()
	ulog.Success("Release successfully created").
		Field("version", version).
		Field("repo_count", repoCount).
		Pretty(box).
		Emit()
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
			ulog.Info("Release level separator").Pretty("\n").PrettyOnly().Emit()
			ulog.Info("Release level").
				Field("level", levelCount).
				Field("repo_count", len(reposInLevel)).
				Pretty(fmt.Sprintf("Level %d (can release in parallel):", levelCount)).
				Emit()

			for _, repo := range reposInLevel {
				current := currentVersions[repo]
				if current == "" {
					current = "-"
				}
				proposed := versions[repo]
				increment := getVersionIncrement(current, proposed)

				prettyMsg := fmt.Sprintf("  • %s: %s → %s (%s)",
					repo,
					releaseDimStyle.Render(current),
					releaseHighlightStyle.Render(proposed),
					releaseInfoStyle.Render(increment))

				ulog.Info("Release repository").
					Field("repo", repo).
					Field("current_version", current).
					Field("proposed_version", proposed).
					Field("increment", increment).
					Pretty(prettyMsg).
					Emit()
			}
		}
	}

	if levelCount == 1 {
		displayInfo(fmt.Sprintf("\n%s Note: All repositories are independent and will be released in parallel.", theme.IconNote))
	}
}
