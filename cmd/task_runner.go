package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	orch "github.com/grovetools/grove/pkg/orchestrator"

	"github.com/grovetools/grove/pkg/discovery"
)

func addTaskFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("affected", false, "Only run on workspaces that are dirty or diverge from main, plus their dependents")
	cmd.Flags().Bool("no-cache", false, "Ignore cached task results")
	cmd.Flags().IntP("jobs", "j", runtime.NumCPU(), "Number of parallel workers")
	cmd.Flags().String("filter", "", "Glob pattern to include only matching projects")
	cmd.Flags().String("exclude", "", "Comma-separated glob patterns to exclude projects")
	cmd.Flags().Bool("fail-fast", false, "Stop immediately when one task fails")
	cmd.Flags().Bool("dry-run", false, "Show what would run without executing")
	cmd.Flags().BoolP("interactive", "i", false, "Keep TUI open after completion for inspection")
}

// executeTaskWithCommand runs a raw command across workspaces using the orchestrator.
// Used by `grove run --parallel` where the command is user-provided, not resolved from config.
func executeTaskWithCommand(cmd *cobra.Command, verb string, rawCommand []string) error {
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
		taskUlog.Info("No projects to run").
			Field("verb", verb).
			Pretty("No projects to run after filtering.").
			Emit()
		return nil
	}

	var taskJobs []orch.TaskJob
	configMap := make(map[string]*config.Config)

	for _, wsPath := range workspaces {
		cfg, loadErr := config.LoadFrom(wsPath)
		name := filepath.Base(wsPath)

		taskJobs = append(taskJobs, orch.TaskJob{
			Name:    name,
			Path:    wsPath,
			Command: rawCommand,
		})

		if loadErr == nil {
			configMap[name] = cfg
		}
	}

	waves := orch.SortIntoWaves(taskJobs, configMap)
	hasWaves := len(waves) > 1

	o := newTaskOrchestrator(orch.OrchestratorOptions{
		Verb:         verb,
		Strategy:     orch.StrategyFlat,
		AffectedOnly: affected,
		NoCache:      noCache,
		Jobs:         jobs,
		FailFast:     failFast,
		RemoteExec:   true,
	}, workspaces, taskJobs, configMap)

	buildFailFast = failFast
	buildInteractive = interactive
	buildJobs = jobs

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))

	if dryRun {
		if affected {
			taskJobs = o.AffectedJobs(context.Background(), taskJobs)
			waves = orch.SortIntoWaves(taskJobs, configMap)
			hasWaves = len(waves) > 1
		}
		return runTaskDryRun(opts, verb, taskJobs, waves, configMap, hasWaves)
	}

	if opts.JSONOutput || !isTTY {
		return runJSONTaskWaves(o, verb, waves)
	}

	return runTuiTask(o, verb, taskJobs)
}

func executeTask(cmd *cobra.Command, verb string, strategy orch.ConcurrencyStrategy) error {
	opts := cli.GetOptions(cmd)

	affected, _ := cmd.Flags().GetBool("affected")
	noCache, _ := cmd.Flags().GetBool("no-cache")
	jobs, _ := cmd.Flags().GetInt("jobs")
	filter, _ := cmd.Flags().GetString("filter")
	exclude, _ := cmd.Flags().GetString("exclude")
	failFast, _ := cmd.Flags().GetBool("fail-fast")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	interactive, _ := cmd.Flags().GetBool("interactive")

	// --target is registered on the build command only; other verbs have no
	// such flag and stay native.
	var target orch.Target
	if f := cmd.Flags().Lookup("target"); f != nil && f.Value.String() != "" {
		t, err := orch.ParseTarget(f.Value.String())
		if err != nil {
			return err
		}
		target = t
		if !t.IsNative() {
			fmt.Printf("cross-compiling for %s → %s\n", t, t.OutDir())
		}
	}

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
		taskUlog.Info("No projects to run").
			Field("verb", verb).
			Field("filter", filter).
			Field("exclude", exclude).
			Pretty(fmt.Sprintf("No projects to %s after filtering.", verb)).
			Emit()
		return nil
	}

	taskJobs, configMap := makeVerbTaskJobs(verb, workspaces)
	waves := orch.SortIntoWaves(taskJobs, configMap)
	hasWaves := len(waves) > 1

	o := newTaskOrchestrator(orch.OrchestratorOptions{
		Verb:         verb,
		Strategy:     strategy,
		AffectedOnly: affected,
		NoCache:      noCache,
		Jobs:         jobs,
		FailFast:     failFast,
		RemoteExec:   true,
		Target:       target,
	}, workspaces, taskJobs, configMap)

	// Store flags in package vars for TUI/verbose callbacks
	buildFailFast = failFast
	buildInteractive = interactive
	buildJobs = jobs

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))

	if dryRun {
		if affected {
			taskJobs = o.AffectedJobs(context.Background(), taskJobs)
			waves = orch.SortIntoWaves(taskJobs, configMap)
			hasWaves = len(waves) > 1
		}
		return runTaskDryRun(opts, verb, taskJobs, waves, configMap, hasWaves)
	}

	if opts.JSONOutput || !isTTY {
		return runJSONTaskWaves(o, verb, waves)
	}

	return runTuiTask(o, verb, taskJobs)
}

// makeVerbTaskJobs resolves the verb's command per workspace into TaskJobs and
// returns the loaded configs (workspaces without a loadable config still get a
// job, just no config entry).
func makeVerbTaskJobs(verb string, workspaces []string) ([]orch.TaskJob, map[string]*config.Config) {
	var taskJobs []orch.TaskJob
	configMap := make(map[string]*config.Config)
	for _, wsPath := range workspaces {
		cfg, loadErr := config.LoadFrom(wsPath)
		name := filepath.Base(wsPath)
		taskJobs = append(taskJobs, orch.TaskJob{
			Name:    name,
			Path:    wsPath,
			Command: orch.ResolveCommand(cfg, verb),
		})
		if loadErr == nil {
			configMap[name] = cfg
		}
	}
	return taskJobs, configMap
}

// newTaskOrchestrator wires the standard task orchestrator around the given
// jobs: per-workspace bin/ dirs on PATH, the global daemon as state provider
// and build queue (degrading to local state + local pool when unreachable),
// and the derived import dep graph.
func newTaskOrchestrator(options orch.OrchestratorOptions, workspaces []string, taskJobs []orch.TaskJob, configMap map[string]*config.Config) *orch.Orchestrator {
	var binDirs []string
	for _, wsPath := range workspaces {
		binDirs = append(binDirs, filepath.Join(wsPath, "bin"))
	}

	// Global daemon client: the global daemon is the single owner of
	// TaskResults and the machine-wide build queue. Auto-starts groved;
	// when that fails we still degrade to LocalStateProvider + local pool.
	client := daemon.NewGlobalClient()
	var stateProvider orch.StateProvider
	if client.IsRunning() {
		stateProvider = &orch.DaemonStateProvider{Client: client}
	} else {
		stateProvider = &orch.LocalStateProvider{}
	}

	return &orch.Orchestrator{
		Options:       options,
		RunOpts:       &orch.RunOptions{ExtraPathDirs: binDirs},
		StateProvider: stateProvider,
		DaemonClient:  client,
		BuildClient:   client,
		Configs:       configMap,
		DepGraph:      orch.DeriveWorkspaceBuildAfter(taskJobs, configMap),
	}
}

// BuildReposForTarget builds the named repos under sourceDir for target via
// the standard orchestrator: wave-ordered, parallel, cached per
// "build@<goos>_<goarch>". This is the local-build engine behind
// `grove satellite upgrade --prebuilt` (and the answer to "why doesn't
// satellite upgrade use grove build" — now it does). Results come back
// per-repo; a failed repo is one failed TaskResult, never an error.
func BuildReposForTarget(ctx context.Context, sourceDir string, repos []string, target orch.Target, jobs int) ([]orch.TaskResult, error) {
	var workspaces []string
	for _, r := range repos {
		workspaces = append(workspaces, filepath.Join(sourceDir, r))
	}
	taskJobs, configMap := makeVerbTaskJobs("build", workspaces)
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	o := newTaskOrchestrator(orch.OrchestratorOptions{
		Verb:       "build",
		Strategy:   orch.StrategyWaveSorted,
		Jobs:       jobs,
		FailFast:   false,
		RemoteExec: true,
		Target:     target,
	}, workspaces, taskJobs, configMap)
	return o.RunWithResults(ctx, taskJobs)
}

var taskUlog = logging.NewUnifiedLogger("grove-meta.task")

func runTaskDryRun(opts cli.CommandOptions, verb string, jobs []orch.TaskJob, waves [][]orch.TaskJob, configMap map[string]*config.Config, hasWaves bool) error {
	if opts.JSONOutput {
		result := map[string]any{
			"mode":  "dry-run",
			"verb":  verb,
			"waves": len(waves),
			"total": len(jobs),
		}
		if hasWaves {
			waveData := make([][]string, len(waves))
			for i, wave := range waves {
				for _, job := range wave {
					waveData[i] = append(waveData[i], job.Name)
				}
			}
			result["execution_order"] = waveData
		} else {
			var names []string
			for _, job := range jobs {
				names = append(names, job.Name)
			}
			result["projects"] = names
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	taskUlog.Info("Dry run").
		Field("verb", verb).
		Field("total", len(jobs)).
		Field("waves", len(waves)).
		Pretty(fmt.Sprintf("Projects that would run '%s':", verb)).
		Emit()

	if hasWaves {
		for i, wave := range waves {
			taskUlog.Info("Execution wave").
				Field("wave", i+1).
				Field("count", len(wave)).
				Pretty(fmt.Sprintf("\nWave %d:", i+1)).
				Emit()
			for _, job := range wave {
				deps := ""
				if cfg, ok := configMap[job.Name]; ok && len(cfg.BuildAfter) > 0 {
					deps = fmt.Sprintf(" (after: %s)", strings.Join(cfg.BuildAfter, ", "))
				}
				taskUlog.Info("Task job").
					Field("name", job.Name).
					Field("path", job.Path).
					Pretty(fmt.Sprintf("  - %s%s", job.Name, deps)).
					Emit()
			}
		}
	} else {
		for i, job := range jobs {
			taskUlog.Info("Task job").
				Field("index", i+1).
				Field("name", job.Name).
				Field("path", job.Path).
				Pretty(fmt.Sprintf("  %d. %s (%s)", i+1, job.Name, job.Path)).
				Emit()
		}
	}
	taskUlog.Info("Dry run summary").
		Field("verb", verb).
		Field("total", len(jobs)).
		Field("waves", len(waves)).
		Pretty(fmt.Sprintf("\nTotal: %d projects in %d wave(s)", len(jobs), len(waves))).
		Emit()
	return nil
}

func runJSONTaskWaves(o *orch.Orchestrator, verb string, waves [][]orch.TaskJob) error {
	type JSONResult struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Wave     int    `json:"wave"`
		Success  bool   `json:"success"`
		Skipped  bool   `json:"skipped,omitempty"`
		Duration string `json:"duration"`
		Error    string `json:"error,omitempty"`
		Output   string `json:"output,omitempty"`
		Cached   bool   `json:"cached,omitempty"`
	}

	var results []JSONResult
	var successCount, failCount, skipCount int

	for waveIdx, waveJobs := range waves {
		ctx := context.Background()
		events := o.RunWithEvents(ctx, waveJobs)

		for event := range events {
			if event.Type != "finish" && event.Type != "cached" {
				continue
			}
			if event.Result == nil {
				continue
			}
			r := event.Result
			jr := JSONResult{
				Name:     r.Job.Name,
				Path:     r.Job.Path,
				Wave:     waveIdx + 1,
				Duration: r.Duration.Round(time.Millisecond).String(),
				Cached:   r.Cached,
			}

			if r.Skipped {
				skipCount++
				jr.Skipped = true
				jr.Success = true
			} else if r.Err != nil {
				failCount++
				jr.Success = false
				jr.Error = r.Err.Error()
				jr.Output = string(r.Output)

				if buildFailFast {
					results = append(results, jr)
					return outputTaskJSONResults(results, verb, successCount, failCount, len(waves))
				}
			} else {
				successCount++
				jr.Success = true
			}
			results = append(results, jr)
		}
	}

	return outputTaskJSONResults(results, verb, successCount, failCount, len(waves))
}

func outputTaskJSONResults[T any](results []T, verb string, successCount, failCount, totalWaves int) error {
	output := map[string]any{
		"mode":    verb,
		"jobs":    buildJobs,
		"waves":   totalWaves,
		"results": results,
		"summary": map[string]int{
			"total":   len(results),
			"success": successCount,
			"failed":  failCount,
		},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		return err
	}

	if failCount > 0 {
		return fmt.Errorf("%d %s tasks failed", failCount, verb)
	}
	return nil
}

func runTuiTask(o *orch.Orchestrator, verb string, jobs []orch.TaskJob) error {
	return runTuiBuild(o, verb, jobs)
}
