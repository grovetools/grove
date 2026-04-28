package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
)

type RunOptions struct {
	ExtraPathDirs []string
}

type Orchestrator struct {
	Options       OrchestratorOptions
	RunOpts       *RunOptions
	StateProvider StateProvider
	DaemonClient  daemon.Client
	Configs       map[string]*config.Config
}

func (o *Orchestrator) isCacheHit(job TaskJob, states map[string]WorkspaceState) bool {
	if o.Options.NoCache {
		return false
	}
	s, ok := states[job.Name]
	if !ok || s.IsDirty || s.TaskResults == nil {
		return false
	}
	tr, ok := s.TaskResults[o.Options.Verb]
	if !ok || tr == nil {
		return false
	}
	return tr.ExitCode == 0 && tr.CommitHash == s.CommitHash
}

func (o *Orchestrator) isCacheHitForVerb(job TaskJob, verb string, states map[string]WorkspaceState) bool {
	if o.Options.NoCache {
		return false
	}
	s, ok := states[job.Name]
	if !ok || s.IsDirty || s.TaskResults == nil {
		return false
	}
	tr, ok := s.TaskResults[verb]
	if !ok || tr == nil {
		return false
	}
	return tr.ExitCode == 0 && tr.CommitHash == s.CommitHash
}

// RunWithResults runs tasks and returns all results. Convenience wrapper for non-TUI callers.
func (o *Orchestrator) RunWithResults(ctx context.Context, jobs []TaskJob) ([]TaskResult, error) {
	events := o.RunWithEvents(ctx, jobs)
	var results []TaskResult
	for event := range events {
		if event.Type == "finish" || event.Type == "cached" {
			if event.Result != nil {
				results = append(results, *event.Result)
			}
		}
	}
	return results, nil
}

// RunWithEvents runs tasks and returns a channel of events for TUI consumption.
func (o *Orchestrator) RunWithEvents(ctx context.Context, jobs []TaskJob) <-chan TaskEvent {
	var states map[string]WorkspaceState
	if o.StateProvider != nil {
		states, _ = o.StateProvider.GetState(ctx, jobPaths(jobs))
	}

	if o.Options.AffectedOnly {
		if len(states) == 0 {
			// Fallback to local git if daemon returned nothing
			local := &LocalStateProvider{}
			states, _ = local.GetState(ctx, jobPaths(jobs))
		}
		if states != nil {
			jobs = FilterAffected(jobs, states, o.Configs, o.Options.Strategy)
		}
	}

	if o.Options.Strategy == StrategyWaveSorted {
		return o.runWaves(ctx, jobs, states)
	}
	return o.runFlat(ctx, jobs, states)
}

func (o *Orchestrator) runFlat(ctx context.Context, jobs []TaskJob, states map[string]WorkspaceState) <-chan TaskEvent {
	numWorkers := o.Options.Jobs
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	env := buildEnv(o.RunOpts)
	eventsChan := make(chan TaskEvent, len(jobs)*3)

	var execJobs []TaskJob
	for _, job := range jobs {
		if o.isCacheHit(job, states) {
			eventsChan <- TaskEvent{
				Job:  job,
				Type: "cached",
				Result: &TaskResult{
					Job:    job,
					Cached: true,
				},
			}
		} else {
			execJobs = append(execJobs, job)
		}
	}

	if len(execJobs) == 0 {
		close(eventsChan)
		return eventsChan
	}

	go func() {
		defer close(eventsChan)
		o.executeJobs(ctx, execJobs, numWorkers, env, states, eventsChan)
	}()
	return eventsChan
}

func (o *Orchestrator) runWaves(ctx context.Context, jobs []TaskJob, states map[string]WorkspaceState) <-chan TaskEvent {
	waves := SortIntoWaves(jobs, o.Configs)
	numWorkers := o.Options.Jobs
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	env := buildEnv(o.RunOpts)
	eventsChan := make(chan TaskEvent, len(jobs)*3)

	go func() {
		defer close(eventsChan)
		for _, wave := range waves {
			var execJobs []TaskJob
			for _, job := range wave {
				if o.isCacheHit(job, states) {
					eventsChan <- TaskEvent{
						Job:  job,
						Type: "cached",
						Result: &TaskResult{
							Job:    job,
							Cached: true,
						},
					}
				} else {
					execJobs = append(execJobs, job)
				}
			}
			if len(execJobs) > 0 {
				o.executeJobs(ctx, execJobs, numWorkers, env, states, eventsChan)
			}
		}
	}()
	return eventsChan
}

// pipelineVerbs returns the list of verbs to execute for a job.
// If Pipeline is set, returns it; otherwise returns a single-element slice of Verb.
func (o *Orchestrator) pipelineVerbs() []string {
	if len(o.Options.Pipeline) > 0 {
		return o.Options.Pipeline
	}
	return []string{o.Options.Verb}
}

func (o *Orchestrator) executeJobs(ctx context.Context, jobs []TaskJob, numWorkers int, env []string, states map[string]WorkspaceState, eventsChan chan<- TaskEvent) {
	jobsChan := make(chan TaskJob, len(jobs))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once

	verbs := o.pipelineVerbs()
	isPipeline := len(o.Options.Pipeline) > 0

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsChan {
				select {
				case <-runCtx.Done():
					eventsChan <- TaskEvent{
						Job:  job,
						Verb: verbs[0],
						Type: "finish",
						Result: &TaskResult{
							Job:  job,
							Verb: verbs[0],
							Err:  runCtx.Err(),
						},
					}
					continue
				default:
				}

				o.executeJobVerbs(runCtx, job, verbs, isPipeline, env, states, eventsChan, &once, cancel)
			}
		}()
	}

	for _, job := range jobs {
		jobsChan <- job
	}
	close(jobsChan)
	wg.Wait()
}

func (o *Orchestrator) executeJobVerbs(ctx context.Context, job TaskJob, verbs []string, isPipeline bool, env []string, states map[string]WorkspaceState, eventsChan chan<- TaskEvent, once *sync.Once, cancel context.CancelFunc) {
	for vi, verb := range verbs {
		select {
		case <-ctx.Done():
			for _, remaining := range verbs[vi:] {
				eventsChan <- TaskEvent{
					Job: job, Verb: remaining, Type: "finish",
					Result: &TaskResult{Job: job, Verb: remaining, Err: ctx.Err()},
				}
			}
			return
		default:
		}

		// Per-verb cache check in pipeline mode
		if isPipeline && o.isCacheHitForVerb(job, verb, states) {
			eventsChan <- TaskEvent{
				Job: job, Verb: verb, Type: "cached",
				Result: &TaskResult{Job: job, Verb: verb, Cached: true},
			}
			continue
		}

		// Resolve command for this verb
		var command []string
		if isPipeline {
			cfg := o.Configs[job.Name]
			command = ResolveCommand(cfg, verb)
		} else {
			command = job.Command
		}

		eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "start"}
		start := time.Now()

		var cmd *exec.Cmd
		if len(command) == 0 {
			cmd = exec.CommandContext(ctx, "make", verb)
		} else {
			cmd = exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // commands from trusted build config
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			// Kill the entire process group so make's children (tend run -p, etc.)
			// also receive SIGTERM and can run their cleanup.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		cmd.WaitDelay = 10 * time.Second
		cmd.Dir = job.Path
		cmd.Env = prependJobBinDir(env, job.Path)

		stdoutPipe, _ := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout

		var outputBuf bytes.Buffer
		scanner := bufio.NewScanner(stdoutPipe)

		var streamWg sync.WaitGroup
		streamWg.Add(1)
		go func() {
			defer streamWg.Done()
			for scanner.Scan() {
				line := scanner.Text()
				outputBuf.WriteString(line + "\n")
				eventsChan <- TaskEvent{
					Job:        job,
					Verb:       verb,
					Type:       "output",
					OutputLine: line,
				}
			}
		}()

		err := cmd.Start()
		if err != nil {
			eventsChan <- TaskEvent{
				Job: job, Verb: verb, Type: "finish",
				Result: &TaskResult{Job: job, Verb: verb, Err: err, Duration: time.Since(start)},
			}
			if o.Options.FailFast {
				once.Do(cancel)
			}
			if isPipeline {
				o.emitSkippedVerbs(job, verbs[vi+1:], verb, eventsChan)
			}
			return
		}

		streamWg.Wait()
		err = cmd.Wait()
		duration := time.Since(start)

		skipped := false
		if err != nil && isMakeTargetMissing(command, outputBuf.String()) {
			err = nil
			skipped = true
		}

		result := TaskResult{
			Job:      job,
			Verb:     verb,
			Output:   outputBuf.Bytes(),
			Err:      err,
			Duration: duration,
			Skipped:  skipped,
		}
		eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "finish", Result: &result}

		exitCode := 0
		errSummary := ""
		if err != nil {
			exitCode = 1
			errSummary = extractErrorSummary(outputBuf.String())
			if o.Options.FailFast {
				once.Do(cancel)
			}
		}

		commitHash := ""
		if s, ok := states[job.Name]; ok {
			commitHash = s.CommitHash
		}
		o.reportTaskVerb(ctx, job, verb, exitCode, commitHash, duration.Milliseconds(), errSummary)

		if err != nil && isPipeline {
			o.emitSkippedVerbs(job, verbs[vi+1:], verb, eventsChan)
			return
		}
	}
}

func (o *Orchestrator) emitSkippedVerbs(job TaskJob, remaining []string, failedVerb string, eventsChan chan<- TaskEvent) {
	for _, v := range remaining {
		eventsChan <- TaskEvent{
			Job: job, Verb: v, Type: "finish",
			Result: &TaskResult{
				Job:     job,
				Verb:    v,
				Skipped: true,
				Err:     fmt.Errorf("skipped: %s failed", failedVerb),
			},
		}
	}
}

func (o *Orchestrator) reportTaskVerb(ctx context.Context, job TaskJob, verb string, exitCode int, commitHash string, durationMs int64, errorSummary string) {
	if o.DaemonClient == nil || !o.DaemonClient.IsRunning() {
		return
	}
	_ = o.DaemonClient.ReportTask(ctx, job.Path, verb, exitCode, commitHash, durationMs, errorSummary)
}

func isMakeTargetMissing(command []string, output string) bool {
	isMake := len(command) == 0 || command[0] == "make"
	if !isMake {
		return false
	}
	return strings.Contains(output, "No rule to make target") ||
		strings.Contains(output, "no rule to make target")
}

func buildEnv(opts *RunOptions) []string {
	env := os.Environ()
	env = append(env, "TERM=xterm-256color")

	if opts != nil && len(opts.ExtraPathDirs) > 0 {
		pathPrefix := strings.Join(opts.ExtraPathDirs, string(os.PathListSeparator))
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				env[i] = "PATH=" + pathPrefix + string(os.PathListSeparator) + e[5:]
				return env
			}
		}
		env = append(env, "PATH="+pathPrefix)
	}

	return env
}

// prependJobBinDir returns a copy of env with the job's own bin/ directory
// prepended to PATH so that project-local binaries (e.g. tend, the project
// binary under test) are found before identically-named binaries from other
// workspaces that may also appear in ExtraPathDirs.
func prependJobBinDir(env []string, jobPath string) []string {
	binDir := filepath.Join(jobPath, "bin")
	out := make([]string, len(env))
	copy(out, env)
	for i, e := range out {
		if strings.HasPrefix(e, "PATH=") {
			out[i] = "PATH=" + binDir + string(os.PathListSeparator) + e[5:]
			return out
		}
	}
	out = append(out, "PATH="+binDir)
	return out
}

func extractErrorSummary(output string) string {
	const maxLen = 200
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	// Take last few non-empty lines
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 5; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			tail = append([]string{line}, tail...)
		}
	}
	summary := strings.Join(tail, "\n")
	if len(summary) > maxLen {
		summary = summary[len(summary)-maxLen:]
	}
	return summary
}

func jobPaths(jobs []TaskJob) []string {
	paths := make([]string, len(jobs))
	for i, j := range jobs {
		paths[i] = j.Path
	}
	return paths
}

// TaskResultToModels converts our internal TaskResult list to models for external consumption.
func TaskResultToModels(results []TaskResult) []*models.TaskResult {
	var out []*models.TaskResult
	for _, r := range results {
		exitCode := 0
		if r.Err != nil {
			exitCode = 1
		}
		out = append(out, &models.TaskResult{
			ExitCode:   exitCode,
			DurationMs: r.Duration.Milliseconds(),
			Timestamp:  time.Now(),
		})
	}
	return out
}
