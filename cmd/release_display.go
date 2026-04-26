package cmd

import (
	"fmt"
	"strings"

	tablecomponent "github.com/grovetools/core/tui/components/table"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/tui/theme"
)

// Define reusable styles for the release command
var (
	// Base styles
	releaseWarningStyle = theme.DefaultTheme.Warning.
				Bold(true)

	releaseInfoStyle = theme.DefaultTheme.Info

	releaseDimStyle = theme.DefaultTheme.Muted
)

// Initialize loggers for display functions
var (
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
