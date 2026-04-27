package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/tui/components/table"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/util/pathutil"
	"github.com/spf13/cobra"
)

var canonicalVerbOrder = []string{"build", "fmt", "vet", "lint", "test", "test-e2e"}

func newStatusCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("status", "Show ecosystem status matrix")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd)
	}
	cmd.SilenceUsage = true
	cmd.Flags().Bool("errors", false, "Show error summaries for failed tasks")
	return cmd
}

func runStatus(cmd *cobra.Command) error {
	opts := cli.GetOptions(cmd)
	showErrors, _ := cmd.Flags().GetBool("errors")

	client := daemon.New()
	if !client.IsRunning() {
		return fmt.Errorf("daemon is not running; status requires groved")
	}

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

	// Filter to scoped workspaces
	var scoped []*models.EnrichedWorkspace
	for _, ws := range workspaces {
		if ws.WorkspaceNode == nil {
			continue
		}
		wsNorm, _ := pathutil.NormalizeForLookup(ws.Path)
		if scopePaths[wsNorm] {
			scoped = append(scoped, ws)
		}
	}

	if len(scoped) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	// Discover all verb columns dynamically
	displayVerbs := discoverVerbs(scoped)

	if opts.JSONOutput {
		return runStatusJSON(scoped, displayVerbs)
	}

	return runStatusTable(scoped, displayVerbs, showErrors)
}

func discoverVerbs(workspaces []*models.EnrichedWorkspace) []string {
	verbSet := make(map[string]bool)
	for _, ws := range workspaces {
		for verb := range ws.TaskResults {
			verbSet[verb] = true
		}
	}

	var displayVerbs []string
	for _, v := range canonicalVerbOrder {
		if verbSet[v] {
			displayVerbs = append(displayVerbs, v)
			delete(verbSet, v)
		}
	}

	var extra []string
	for v := range verbSet {
		extra = append(extra, v)
	}
	sort.Strings(extra)
	displayVerbs = append(displayVerbs, extra...)

	return displayVerbs
}

func verbDisplayName(verb string) string {
	parts := strings.Split(verb, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}

func runStatusTable(scoped []*models.EnrichedWorkspace, displayVerbs []string, showErrors bool) error {
	t := theme.DefaultTheme

	headers := []string{"Workspace", "Branch", "Dirty"}
	for _, v := range displayVerbs {
		headers = append(headers, verbDisplayName(v))
	}

	type failure struct {
		workspace string
		verb      string
		summary   string
	}
	var failures []failure
	var rows [][]string

	for _, ws := range scoped {
		name := filepath.Base(ws.Path)
		branch := "-"
		if ws.GitStatus != nil && ws.GitStatus.StatusInfo != nil && ws.GitStatus.StatusInfo.Branch != "" {
			branch = ws.GitStatus.StatusInfo.Branch
		}

		dirty := t.Success.Render("No")
		if ws.GitStatus != nil && ws.GitStatus.IsDirty {
			dirty = t.Error.Render("Yes")
		}

		row := []string{name, branch, dirty}
		for _, v := range displayVerbs {
			row = append(row, formatTaskResult(t, ws.TaskResults, v))

			if showErrors && ws.TaskResults != nil {
				if tr, ok := ws.TaskResults[v]; ok && tr != nil && tr.ExitCode != 0 && tr.ErrorSummary != "" {
					failures = append(failures, failure{workspace: name, verb: v, summary: tr.ErrorSummary})
				}
			}
		}
		rows = append(rows, row)
	}

	tbl := table.NewStyledTable().
		Headers(headers...).
		Rows(rows...)

	fmt.Println(tbl.Render())

	if showErrors {
		if len(failures) > 0 {
			fmt.Println("\nFailures:")
			for _, f := range failures {
				firstLine := f.summary
				if idx := strings.Index(firstLine, "\n"); idx >= 0 {
					firstLine = firstLine[:idx]
				}
				fmt.Printf("  %s/%s: %s\n", f.workspace, f.verb, firstLine)
			}
		}

		// Show scenario-level failures from TestReports
		for _, ws := range scoped {
			name := filepath.Base(ws.Path)
			for verb, report := range ws.TestReports {
				if report == nil || report.Summary.Failed == 0 {
					continue
				}
				fmt.Printf("\n  %s/%s: %d/%d failed\n", name, verb, report.Summary.Failed, report.Summary.Total)
				for _, sc := range report.Scenarios {
					if sc.Status != "fail" {
						continue
					}
					detail := sc.Name
					if sc.FailedStep != "" {
						detail += fmt.Sprintf(" (step %q)", sc.FailedStep)
					}
					if sc.Error != "" {
						detail += ": " + sc.Error
					}
					fmt.Printf("    %s\n", detail)
				}
			}
		}

		// Show slowest scenarios across all workspaces
		type slowEntry struct {
			workspace string
			scenario  string
			duration  time.Duration
		}
		var all []slowEntry
		for _, ws := range scoped {
			name := filepath.Base(ws.Path)
			for _, report := range ws.TestReports {
				if report == nil {
					continue
				}
				for _, sc := range report.Scenarios {
					all = append(all, slowEntry{
						workspace: name,
						scenario:  sc.Name,
						duration:  time.Duration(sc.DurationMs) * time.Millisecond,
					})
				}
			}
		}
		if len(all) > 0 {
			sort.Slice(all, func(i, j int) bool { return all[i].duration > all[j].duration })
			limit := 3
			if len(all) < limit {
				limit = len(all)
			}
			fmt.Println("\nSlowest scenarios:")
			for _, e := range all[:limit] {
				fmt.Printf("  %s/%s: %s\n", e.workspace, e.scenario, e.duration.Round(time.Second))
			}
		}
	}

	return nil
}

func runStatusJSON(scoped []*models.EnrichedWorkspace, displayVerbs []string) error {
	type JSONTaskResult struct {
		ExitCode     int    `json:"exit_code"`
		DurationMs   int64  `json:"duration_ms"`
		Timestamp    string `json:"timestamp"`
		Cached       bool   `json:"cached,omitempty"`
		ErrorSummary string `json:"error_summary,omitempty"`
	}
	type JSONWorkspace struct {
		Name        string                        `json:"name"`
		Path        string                        `json:"path"`
		Branch      string                        `json:"branch"`
		Dirty       bool                          `json:"dirty"`
		Results     map[string]*JSONTaskResult    `json:"results"`
		TestReports map[string]*models.TestReport `json:"test_reports,omitempty"`
	}

	var output []JSONWorkspace
	for _, ws := range scoped {
		name := filepath.Base(ws.Path)
		branch := ""
		if ws.GitStatus != nil && ws.GitStatus.StatusInfo != nil {
			branch = ws.GitStatus.StatusInfo.Branch
		}
		dirty := ws.GitStatus != nil && ws.GitStatus.IsDirty

		results := make(map[string]*JSONTaskResult)
		for _, v := range displayVerbs {
			if ws.TaskResults == nil {
				results[v] = nil
				continue
			}
			tr, ok := ws.TaskResults[v]
			if !ok || tr == nil {
				results[v] = nil
				continue
			}
			results[v] = &JSONTaskResult{
				ExitCode:     tr.ExitCode,
				DurationMs:   tr.DurationMs,
				Timestamp:    tr.Timestamp.Format(time.RFC3339),
				ErrorSummary: tr.ErrorSummary,
			}
		}

		output = append(output, JSONWorkspace{
			Name:        name,
			Path:        ws.Path,
			Branch:      branch,
			Dirty:       dirty,
			Results:     results,
			TestReports: ws.TestReports,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"workspaces": output,
	})
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
