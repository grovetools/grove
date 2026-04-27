package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/tui/components/table"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/util/pathutil"
	"github.com/spf13/cobra"
)

func newTestReportCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("test-report", "Show structured test results from the daemon")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runTestReport(cmd)
	}
	cmd.SilenceUsage = true
	cmd.Flags().Int("slowest", 0, "Show top N slowest scenarios")
	cmd.Flags().Bool("failing", false, "Show only failing scenarios")
	cmd.Flags().String("workspace", "", "Filter to a specific workspace")
	return cmd
}

func runTestReport(cmd *cobra.Command) error {
	opts := cli.GetOptions(cmd)
	slowest, _ := cmd.Flags().GetInt("slowest")
	failing, _ := cmd.Flags().GetBool("failing")
	wsFilter, _ := cmd.Flags().GetString("workspace")

	client := daemon.New()
	if !client.IsRunning() {
		return fmt.Errorf("daemon is not running; test-report requires groved")
	}

	projects, _, err := DiscoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	scopePaths := make(map[string]bool, len(projects))
	for _, p := range projects {
		norm, _ := pathutil.NormalizeForLookup(p.Path)
		scopePaths[norm] = true
	}

	ctx := context.Background()
	workspaces, err := client.GetEnrichedWorkspaces(ctx, &models.EnrichmentOptions{})
	if err != nil {
		return fmt.Errorf("failed to get workspace data: %w", err)
	}

	var scoped []*models.EnrichedWorkspace
	for _, ws := range workspaces {
		if ws.WorkspaceNode == nil {
			continue
		}
		wsNorm, _ := pathutil.NormalizeForLookup(ws.Path)
		if !scopePaths[wsNorm] {
			continue
		}
		if wsFilter != "" && filepath.Base(ws.Path) != wsFilter {
			continue
		}
		scoped = append(scoped, ws)
	}

	type scenarioEntry struct {
		Workspace string
		Verb      string
		Scenario  models.ScenarioResult
	}

	var entries []scenarioEntry
	for _, ws := range scoped {
		name := filepath.Base(ws.Path)
		for _, report := range ws.TestReports {
			if report == nil {
				continue
			}
			for _, sc := range report.Scenarios {
				if failing && sc.Status != "fail" {
					continue
				}
				entries = append(entries, scenarioEntry{
					Workspace: name,
					Verb:      report.Verb,
					Scenario:  sc,
				})
			}
		}
	}

	if len(entries) == 0 {
		fmt.Println("No test reports available.")
		return nil
	}

	if opts.JSONOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	if slowest > 0 {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Scenario.DurationMs > entries[j].Scenario.DurationMs
		})
		if len(entries) > slowest {
			entries = entries[:slowest]
		}
		fmt.Printf("Top %d slowest scenarios:\n", len(entries))
		for _, e := range entries {
			d := time.Duration(e.Scenario.DurationMs) * time.Millisecond
			fmt.Printf("  %s/%s: %s (%s)\n", e.Workspace, e.Scenario.Name, d.Round(time.Second), e.Verb)
		}
		return nil
	}

	t := theme.DefaultTheme
	tbl := table.NewStyledTable().Headers("Workspace", "Verb", "Scenario", "Status", "Duration")

	for _, e := range entries {
		d := time.Duration(e.Scenario.DurationMs) * time.Millisecond
		status := t.Success.Render("pass")
		if e.Scenario.Status == "fail" {
			status = t.Error.Render("fail")
		} else if e.Scenario.Status == "skip" {
			status = "skip"
		}
		tbl.Row(e.Workspace, e.Verb, e.Scenario.Name, status, d.Round(time.Millisecond).String())
	}

	fmt.Println(tbl.Render())

	if failing {
		for _, e := range entries {
			if e.Scenario.Error != "" {
				fmt.Printf("\n  %s/%s/%s", e.Workspace, e.Verb, e.Scenario.Name)
				if e.Scenario.FailedStep != "" {
					fmt.Printf(" (step %q)", e.Scenario.FailedStep)
				}
				fmt.Printf(": %s\n", e.Scenario.Error)
			}
		}
	}

	return nil
}
