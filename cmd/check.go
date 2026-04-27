package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	orch "github.com/grovetools/grove/pkg/orchestrator"

	"github.com/grovetools/grove/pkg/discovery"
)

func newCheckCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("check", "Run full validation pipeline (fmt, vet, lint, test) across ecosystem")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return executePipeline(cmd, []string{"fmt", "vet", "lint", "test"})
	}
	cmd.SilenceUsage = true
	addTaskFlags(cmd)
	return cmd
}

func executePipeline(cmd *cobra.Command, pipeline []string) error {
	opts := cli.GetOptions(cmd)

	affected, _ := cmd.Flags().GetBool("affected")
	noCache, _ := cmd.Flags().GetBool("no-cache")
	jobs, _ := cmd.Flags().GetInt("jobs")
	filter, _ := cmd.Flags().GetString("filter")
	exclude, _ := cmd.Flags().GetString("exclude")
	failFast, _ := cmd.Flags().GetBool("fail-fast")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	interactive, _ := cmd.Flags().GetBool("interactive")

	projects, _, err := DiscoverTargetProjects()
	if err != nil {
		return fmt.Errorf("failed to discover projects: %w", err)
	}

	var workspaces []string
	for _, p := range projects {
		workspaces = append(workspaces, p.Path)
	}

	if filter != "" {
		workspaces = discovery.FilterWorkspaces(workspaces, filter)
	}
	if exclude != "" {
		workspaces = applyExcludeFilter(workspaces, exclude)
	}

	if len(workspaces) == 0 {
		fmt.Println("No projects to check after filtering.")
		return nil
	}

	var taskJobs []orch.TaskJob
	configMap := make(map[string]*config.Config)

	for _, wsPath := range workspaces {
		cfg, loadErr := config.LoadFrom(wsPath)
		name := filepath.Base(wsPath)

		taskJobs = append(taskJobs, orch.TaskJob{
			Name: name,
			Path: wsPath,
		})

		if loadErr == nil {
			configMap[name] = cfg
		}
	}

	var binDirs []string
	for _, wsPath := range workspaces {
		binDirs = append(binDirs, filepath.Join(wsPath, "bin"))
	}
	runOpts := &orch.RunOptions{ExtraPathDirs: binDirs}

	client := daemon.New()
	var stateProvider orch.StateProvider
	if client.IsRunning() {
		stateProvider = &orch.DaemonStateProvider{Client: client}
	} else {
		stateProvider = &orch.LocalStateProvider{}
	}

	o := &orch.Orchestrator{
		Options: orch.OrchestratorOptions{
			Pipeline:     pipeline,
			Strategy:     orch.StrategyWaveSorted,
			AffectedOnly: affected,
			NoCache:      noCache,
			Jobs:         jobs,
			FailFast:     failFast,
		},
		RunOpts:       runOpts,
		StateProvider: stateProvider,
		DaemonClient:  client,
		Configs:       configMap,
	}

	buildFailFast = failFast
	buildInteractive = interactive
	buildJobs = jobs

	if dryRun {
		waves := orch.SortIntoWaves(taskJobs, configMap)
		return runTaskDryRun(opts, "check", taskJobs, waves, configMap, len(waves) > 1)
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))

	if opts.JSONOutput || !isTTY {
		return runJSONPipeline(o, pipeline, taskJobs)
	}

	return runTuiPipeline(o, pipeline, taskJobs)
}

func runJSONPipeline(o *orch.Orchestrator, pipeline []string, jobs []orch.TaskJob) error {
	type VerbResult struct {
		Verb       string `json:"verb"`
		Success    bool   `json:"success"`
		Skipped    bool   `json:"skipped,omitempty"`
		Duration   string `json:"duration,omitempty"`
		Error      string `json:"error,omitempty"`
		Output     string `json:"output,omitempty"`
		Cached     bool   `json:"cached,omitempty"`
		SkipReason string `json:"reason,omitempty"`
	}
	type WorkspaceResult struct {
		Workspace string       `json:"workspace"`
		Pipeline  []VerbResult `json:"pipeline"`
	}

	ctx := context.Background()
	events := o.RunWithEvents(ctx, jobs)

	// Collect results per workspace
	wsResults := make(map[string]*WorkspaceResult)
	for _, job := range jobs {
		wsResults[job.Name] = &WorkspaceResult{
			Workspace: job.Name,
			Pipeline:  make([]VerbResult, 0, len(pipeline)),
		}
	}

	for event := range events {
		if event.Type != "finish" && event.Type != "cached" {
			continue
		}
		if event.Result == nil {
			continue
		}
		r := event.Result
		ws, ok := wsResults[r.Job.Name]
		if !ok {
			continue
		}

		vr := VerbResult{
			Verb:     r.Verb,
			Duration: r.Duration.Round(time.Millisecond).String(),
			Cached:   r.Cached,
		}

		if r.Cached {
			vr.Success = true
		} else if r.Skipped {
			vr.Skipped = true
			if r.Err != nil {
				vr.SkipReason = r.Err.Error()
			}
		} else if r.Err != nil {
			vr.Success = false
			vr.Error = r.Err.Error()
			vr.Output = string(r.Output)
		} else {
			vr.Success = true
		}

		ws.Pipeline = append(ws.Pipeline, vr)
	}

	var results []WorkspaceResult
	fullyPassed, partialFailed, fullyFailed := 0, 0, 0
	for _, job := range jobs {
		ws := wsResults[job.Name]
		results = append(results, *ws)

		failures := 0
		for _, vr := range ws.Pipeline {
			if !vr.Success && !vr.Skipped && !vr.Cached {
				failures++
			}
		}
		switch {
		case failures == 0:
			fullyPassed++
		case failures == len(ws.Pipeline):
			fullyFailed++
		default:
			partialFailed++
		}
	}

	output := map[string]any{
		"results": results,
		"summary": map[string]int{
			"total_workspaces": len(jobs),
			"fully_passed":     fullyPassed,
			"partially_failed": partialFailed,
			"fully_failed":     fullyFailed,
		},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		return err
	}

	if partialFailed+fullyFailed > 0 {
		return fmt.Errorf("%d workspace(s) had failures", partialFailed+fullyFailed)
	}
	return nil
}

// runTuiPipeline renders per-repo pipeline progress using the existing build TUI.
// Each pipeline verb emits events that the TUI consumes.
func runTuiPipeline(o *orch.Orchestrator, _ []string, jobs []orch.TaskJob) error {
	return runTuiBuild(o, "check", jobs)
}
