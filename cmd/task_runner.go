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
	cmd.Flags().Bool("affected", false, "Only run on workspaces with uncommitted changes")
	cmd.Flags().Bool("no-cache", false, "Ignore cached task results")
	cmd.Flags().IntP("jobs", "j", runtime.NumCPU(), "Number of parallel workers")
	cmd.Flags().String("filter", "", "Glob pattern to include only matching projects")
	cmd.Flags().String("exclude", "", "Comma-separated glob patterns to exclude projects")
	cmd.Flags().Bool("fail-fast", false, "Stop immediately when one task fails")
	cmd.Flags().Bool("dry-run", false, "Show what would run without executing")
	cmd.Flags().BoolP("verbose", "v", false, "Stream raw output instead of using the TUI")
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
	verbose, _ := cmd.Flags().GetBool("verbose")
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
			Pretty(fmt.Sprintf("No projects to run after filtering.")).
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
			Verb:         verb,
			Strategy:     orch.StrategyFlat,
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

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		verbose = true
	}

	if dryRun {
		return runTaskDryRun(opts, verb, taskJobs, waves, configMap, hasWaves)
	}

	if opts.JSONOutput {
		return runJSONTaskWaves(o, verb, waves)
	}

	if verbose {
		return runVerboseTaskWaves(o, verb, waves)
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
	verbose, _ := cmd.Flags().GetBool("verbose")
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
			Field("filter", filter).
			Field("exclude", exclude).
			Pretty(fmt.Sprintf("No projects to %s after filtering.", verb)).
			Emit()
		return nil
	}

	var taskJobs []orch.TaskJob
	configMap := make(map[string]*config.Config)

	for _, wsPath := range workspaces {
		cfg, loadErr := config.LoadFrom(wsPath)
		name := filepath.Base(wsPath)
		resolved := orch.ResolveCommand(cfg, verb)

		taskJobs = append(taskJobs, orch.TaskJob{
			Name:    name,
			Path:    wsPath,
			Command: resolved,
		})

		if loadErr == nil {
			configMap[name] = cfg
		}
	}

	waves := orch.SortIntoWaves(taskJobs, configMap)
	hasWaves := len(waves) > 1

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
			Verb:         verb,
			Strategy:     strategy,
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

	// Store flags in package vars for TUI/verbose callbacks
	buildFailFast = failFast
	buildInteractive = interactive
	buildJobs = jobs

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		verbose = true
	}

	if dryRun {
		return runTaskDryRun(opts, verb, taskJobs, waves, configMap, hasWaves)
	}

	if opts.JSONOutput {
		return runJSONTaskWaves(o, verb, waves)
	}

	if verbose || hasWaves {
		if hasWaves && !verbose {
			taskUlog.Info("Using verbose mode for wave-based execution").
				Pretty(fmt.Sprintf("Running %s in waves due to build_after dependencies...", verb)).
				Emit()
		}
		return runVerboseTaskWaves(o, verb, waves)
	}

	return runTuiTask(o, verb, taskJobs)
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

func runVerboseTaskWaves(o *orch.Orchestrator, verb string, waves [][]orch.TaskJob) error {
	var successCount, failCount, skipCount int
	totalJobs := 0
	for _, wave := range waves {
		totalJobs += len(wave)
	}

	pretty := logging.NewPrettyLogger()
	pretty.Progress(fmt.Sprintf("Running %s on %d projects in %d wave(s) (using %d workers)", verb, totalJobs, len(waves), buildJobs))
	pretty.Blank()

	completedJobs := 0
	for waveIdx, waveJobs := range waves {
		if len(waves) > 1 {
			pretty.Progress(fmt.Sprintf("Wave %d/%d (%d projects)", waveIdx+1, len(waves), len(waveJobs)))
		}

		ctx := context.Background()
		events := o.RunWithEvents(ctx, waveJobs)

		for event := range events {
			switch event.Type {
			case "cached":
				if event.Result != nil {
					completedJobs++
					successCount++
					progress := fmt.Sprintf("[%d/%d]", completedJobs, totalJobs)
					taskUlog.Progress("Cached project").
						Field("name", event.Result.Job.Name).
						Field("completed", completedJobs).
						Field("total", totalJobs).
						Pretty(fmt.Sprintf("\n%s %s (cached)", progress, event.Result.Job.Name)).
						Emit()
				}
			case "finish":
				if event.Result == nil {
					continue
				}
				result := event.Result
				completedJobs++
				progress := fmt.Sprintf("[%d/%d]", completedJobs, totalJobs)

				taskUlog.Progress("Running task").
					Field("name", result.Job.Name).
					Field("completed", completedJobs).
					Field("total", totalJobs).
					Pretty(fmt.Sprintf("\n%s %s %s...", progress, verb, result.Job.Name)).
					Emit()
				pretty.Divider()

				if len(result.Output) > 0 {
					os.Stdout.Write(result.Output)
				}

				if result.Skipped {
					skipCount++
					pretty.Status("warning", fmt.Sprintf("Skipped (%v)", result.Duration.Round(time.Millisecond)))
				} else if result.Err != nil {
					failCount++
					pretty.Status("error", fmt.Sprintf("Failed (%v)", result.Duration.Round(time.Millisecond)))
					if result.Err.Error() != "exit status 1" && result.Err.Error() != "exit status 2" {
						pretty.ErrorPretty("Error", result.Err)
					}
					if buildFailFast {
						pretty.Blank()
						pretty.Divider()
						pretty.InfoPretty(fmt.Sprintf("%s stopped (fail-fast). Success: %d, Skipped: %d, Failed: %d", verb, successCount, skipCount, failCount))
						return fmt.Errorf("%d %s tasks failed", failCount, verb)
					}
				} else {
					successCount++
					pretty.Status("success", fmt.Sprintf("Success (%v)", result.Duration.Round(time.Millisecond)))
				}
			}
		}
	}

	pretty.Blank()
	pretty.Divider()
	label := strings.ToUpper(verb[:1]) + verb[1:]
	pretty.InfoPretty(fmt.Sprintf("%s finished. Success: %d, Skipped: %d, Failed: %d", label, successCount, skipCount, failCount))
	if failCount > 0 {
		return fmt.Errorf("%d %s tasks failed", failCount, verb)
	}
	return nil
}

func runTuiTask(o *orch.Orchestrator, verb string, jobs []orch.TaskJob) error {
	return runTuiBuild(o, verb, jobs)
}
