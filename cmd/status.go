package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/tui/components/table"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/util/pathutil"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("status", "Show ecosystem status matrix")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd)
	}
	cmd.SilenceUsage = true
	return cmd
}

func runStatus(_ *cobra.Command) error {
	client := daemon.New()
	if !client.IsRunning() {
		return fmt.Errorf("daemon is not running; status requires groved")
	}

	// Scope to the current ecosystem/project
	projects, _, err := DiscoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	scopePaths := make(map[string]bool, len(projects))
	for _, p := range projects {
		norm, err := pathutil.NormalizeForLookup(p.Path)
		if err != nil {
			norm = p.Path
		}
		scopePaths[norm] = true
	}

	ctx := context.Background()
	workspaces, err := client.GetEnrichedWorkspaces(ctx, &models.EnrichmentOptions{
		FetchGitStatus: true,
	})
	if err != nil {
		return fmt.Errorf("failed to get workspace status: %w", err)
	}

	t := theme.DefaultTheme

	headers := []string{"Workspace", "Branch", "Dirty", "Last Fmt", "Last Lint", "Last Check"}
	var rows [][]string

	for _, ws := range workspaces {
		if ws.WorkspaceNode == nil {
			continue
		}
		wsNorm, _ := pathutil.NormalizeForLookup(ws.Path)
		if !scopePaths[wsNorm] {
			continue
		}

		name := filepath.Base(ws.Path)

		branch := "-"
		if ws.GitStatus != nil && ws.GitStatus.StatusInfo != nil && ws.GitStatus.StatusInfo.Branch != "" {
			branch = ws.GitStatus.StatusInfo.Branch
		}

		dirty := t.Success.Render("No")
		if ws.GitStatus != nil && ws.GitStatus.IsDirty {
			dirty = t.Error.Render("Yes")
		}

		fmtResult := formatTaskResult(t, ws.TaskResults, "fmt")
		lintResult := formatTaskResult(t, ws.TaskResults, "lint")
		checkResult := formatTaskResult(t, ws.TaskResults, "check")

		rows = append(rows, []string{name, branch, dirty, fmtResult, lintResult, checkResult})
	}

	if len(rows) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	tbl := table.NewStyledTable().
		Headers(headers...).
		Rows(rows...)

	fmt.Println(tbl.Render())
	return nil
}

func formatTaskResult(t *theme.Theme, results map[string]*models.TaskResult, verb string) string {
	if results == nil {
		return "-"
	}
	tr, ok := results[verb]
	if !ok || tr == nil {
		return "-"
	}

	ago := timeAgo(tr.Timestamp)
	if tr.ExitCode == 0 {
		return t.Success.Render(fmt.Sprintf("✓ %s", ago))
	}
	return t.Error.Render(fmt.Sprintf("✗ %s", ago))
}

func timeAgo(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := time.Since(ts)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
