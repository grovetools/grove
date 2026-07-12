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

	// Discover all verb columns dynamically
	displayVerbs := discoverVerbs(scoped)

	// Net-new federated surfaces (C17): fetch remote/local jobs and satellite
	// health from the global daemon. These are SOFT — an older groved without
	// the endpoints returns an error and we simply skip the section rather than
	// failing the whole command (mirrors the errEndpointNotFound tolerance in
	// core/pkg/daemon/remote.go).
	//
	// NOTE: fetched even when zero workspaces were discovered — satellite
	// health and federated jobs come from the daemon, not from workspaces, so
	// their sections must render regardless (the zero-workspaces message is
	// handled inside runStatusTable).
	jobs := softListJobs(ctx, client)
	sats := softSatelliteStatuses(ctx, client)

	if opts.JSONOutput {
		return runStatusJSON(scoped, displayVerbs, jobs, sats)
	}

	return runStatusTable(scoped, displayVerbs, showErrors, jobs, sats)
}

// softListJobs fetches jobs, swallowing errors so `grove status` still renders
// against an older daemon that lacks the endpoint.
func softListJobs(ctx context.Context, client daemon.Client) []*models.JobInfo {
	jobs, err := client.ListJobs(ctx, models.JobFilter{})
	if err != nil {
		return nil
	}
	return jobs
}

// softSatelliteStatuses fetches satellite health, swallowing errors (older
// groved → skip the section).
func softSatelliteStatuses(ctx context.Context, client daemon.Client) map[string]*models.SatelliteStatus {
	sats, err := client.GetSatelliteStatuses(ctx)
	if err != nil {
		return nil
	}
	return sats
}

// jobIsRecent keeps non-terminal jobs plus jobs that reached a terminal state
// within the last hour, so the Jobs section shows live and just-finished work.
func jobIsRecent(job *models.JobInfo) bool {
	switch job.Status {
	case "completed", "failed", "cancelled":
		if job.CompletedAt == nil {
			return false
		}
		return time.Since(*job.CompletedAt) <= time.Hour
	default:
		return true
	}
}

// renderJobsSection prints the federated Jobs table (C17). Origin renders the
// satellite registry name, or "local" when empty. Remote-supplied strings were
// already sanitized at the collector boundary (C9), so no re-sanitization here.
func renderJobsSection(jobs []*models.JobInfo) {
	var rows [][]string
	for _, job := range jobs {
		if job == nil || !jobIsRecent(job) {
			continue
		}
		name := job.JobFile
		if name == "" {
			name = job.ID
		}
		origin := job.Origin
		if origin == "" {
			origin = "local"
		}
		rows = append(rows, []string{name, job.PlanName, job.Status, origin, timeAgo(job.SubmittedAt)})
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	tbl := table.NewStyledTable().
		Headers("Job", "Plan", "Status", "Origin", "Age").
		Rows(rows...)
	fmt.Println("\nJobs:")
	fmt.Println(tbl.Render())
}

// renderSatellitesSection prints satellite connection health (C17), only when
// at least one satellite is known.
func renderSatellitesSection(sats map[string]*models.SatelliteStatus) {
	if len(sats) == 0 {
		return
	}
	names := make([]string, 0, len(sats))
	for n := range sats {
		names = append(names, n)
	}
	sort.Strings(names)
	var rows [][]string
	for _, name := range names {
		st := sats[name]
		if st == nil {
			continue
		}
		since := "-"
		if !st.Since.IsZero() {
			since = timeAgo(st.Since)
		}
		rows = append(rows, []string{name, st.State, st.Addr, since, st.LastError})
	}
	if len(rows) == 0 {
		return
	}
	tbl := table.NewStyledTable().
		Headers("Satellite", "State", "Addr", "Since", "Last Error").
		Rows(rows...)
	fmt.Println("\nSatellites:")
	fmt.Println(tbl.Render())
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

func runStatusTable(scoped []*models.EnrichedWorkspace, displayVerbs []string, showErrors bool, jobs []*models.JobInfo, sats map[string]*models.SatelliteStatus) error {
	// Zero workspaces is not a reason to skip the federated sections: satellite
	// health and jobs come from the daemon, not from workspace discovery.
	// Empty jobs/sats still render nothing, preserving the plain
	// "No workspaces found." output when nothing federated is configured.
	if len(scoped) == 0 {
		fmt.Println("No workspaces found.")
		renderJobsSection(jobs)
		renderSatellitesSection(sats)
		return nil
	}

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

	// Federated Jobs + Satellites sections (C17). Empty inputs render nothing.
	renderJobsSection(jobs)
	renderSatellitesSection(sats)

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

func runStatusJSON(scoped []*models.EnrichedWorkspace, displayVerbs []string, jobs []*models.JobInfo, sats map[string]*models.SatelliteStatus) error {
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

	// Jobs (C17): emit the federated rows the Store holds, unmodified. Origin
	// serializes automatically (empty == local).
	type JSONJob struct {
		ID          string `json:"id"`
		JobFile     string `json:"job_file"`
		PlanName    string `json:"plan_name,omitempty"`
		Status      string `json:"status"`
		Origin      string `json:"origin,omitempty"`
		SubmittedAt string `json:"submitted_at,omitempty"`
	}
	jobsOut := make([]JSONJob, 0, len(jobs))
	for _, job := range jobs {
		if job == nil {
			continue
		}
		submitted := ""
		if !job.SubmittedAt.IsZero() {
			submitted = job.SubmittedAt.Format(time.RFC3339)
		}
		jobsOut = append(jobsOut, JSONJob{
			ID:          job.ID,
			JobFile:     job.JobFile,
			PlanName:    job.PlanName,
			Status:      job.Status,
			Origin:      job.Origin,
			SubmittedAt: submitted,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"workspaces": output,
		"jobs":       jobsOut,
		"satellites": sats,
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
