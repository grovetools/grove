package cmd

import (
	"context"
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
	ctx := context.Background()
	ulog.Info("Release phase").
		Field("title", title).
		Pretty(fmt.Sprintf("%s %s", theme.IconSparkle, title)).
		Log(ctx)
}

func displaySection(title string) {
	ctx := context.Background()
	ulog.Info("Release section").
		Field("title", title).
		Pretty(title).
		Log(ctx)
}

func displaySuccess(message string) {
	ctx := context.Background()
	ulog.Success("Release operation successful").
		Field("message", message).
		Pretty(theme.IconSuccess + " " + message).
		Log(ctx)
}

func displayWarning(message string) {
	ctx := context.Background()
	ulog.Warn("Release warning").
		Field("message", message).
		Pretty(theme.IconWarning + " " + message).
		Log(ctx)
}

func displayError(message string) {
	ctx := context.Background()
	ulog.Error("Release error").
		Field("message", message).
		Pretty(theme.IconError + " " + message).
		Log(ctx)
}

func displayInfo(message string) {
	ctx := context.Background()
	ulog.Info("Release information").
		Field("message", message).
		Pretty(message).
		Log(ctx)
}

// Progress display helpers
func displayProgress(message string) {
	ctx := context.Background()
	ulog.Progress("Release in progress").
		Field("message", message).
		Pretty(fmt.Sprintf("%s %s", theme.IconWorktree, message)).
		Log(ctx)
}

func displayComplete(message string) {
	ctx := context.Background()
	ulog.Success("Release operation completed").
		Field("message", message).
		Pretty(theme.IconSuccess + " " + message).
		Log(ctx)
}

func displayFailed(message string) {
	ctx := context.Background()
	ulog.Error("Release operation failed").
		Field("message", message).
		Pretty(theme.IconError + " " + message).
		Log(ctx)
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

	ctx := context.Background()
	ulog.Info("Release preflight table").
		Field("row_count", len(styledRows)).
		Field("headers", headers).
		Pretty(t.String()).
		Log(ctx)
}

// Create a progress box for release operations
func displayReleaseProgress(title string, items []string) {
	content := []string{
		releaseTitleStyle.Render(title),
		"",
	}
	content = append(content, items...)

	box := phaseBoxStyle.Render(strings.Join(content, "\n"))
	ctx := context.Background()
	ulog.Info("Release progress").
		Field("title", title).
		Field("item_count", len(items)).
		Pretty(box).
		Log(ctx)
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
	ctx := context.Background()
	ulog.Info("Release completed").Pretty("\n").PrettyOnly().Log(ctx)
	ulog.Success("Release successfully created").
		Field("version", version).
		Field("repo_count", repoCount).
		Pretty(box).
		Log(ctx)
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
			ctx := context.Background()
			ulog.Info("Release level separator").Pretty("\n").PrettyOnly().Log(ctx)
			ulog.Info("Release level").
				Field("level", levelCount).
				Field("repo_count", len(reposInLevel)).
				Pretty(fmt.Sprintf("Level %d (can release in parallel):", levelCount)).
				Log(ctx)

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
					Log(ctx)
			}
		}
	}

	if levelCount == 1 {
		displayInfo(fmt.Sprintf("\n%s Note: All repositories are independent and will be released in parallel.", theme.IconNote))
	}
}
