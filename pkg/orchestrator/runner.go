package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/taskexec"
)

type RunOptions struct {
	ExtraPathDirs []string
}

type Orchestrator struct {
	Options       OrchestratorOptions
	RunOpts       *RunOptions
	StateProvider StateProvider
	DaemonClient  daemon.Client
	// BuildClient, together with Options.RemoteExec, routes job execution
	// through the global daemon's machine-wide build queue instead of the
	// local worker pool. daemon.Client satisfies it. When the daemon is
	// unreachable (or predates the build queue) execution transparently
	// falls back to the local pool.
	BuildClient BuildClient
	Configs     map[string]*config.Config
	// DepGraph is the full derived import graph (DeriveWorkspaceBuildAfter).
	// --affected expansion uses it alongside the declared/scheduling edges in
	// Configs so import-cycle partners of a changed member are still selected.
	DepGraph *DepGraph

	// Remote-exec state: one submission group per orchestrator run, plus
	// a latch that disables remote exec after the first failed submit.
	remoteMu        sync.Mutex
	remoteDisabled  bool
	remoteSubmitted bool
	groupID         string

	// Cross-cgo state: GROVE_TARGET_CGO_CFLAGS is resolved at most once per
	// run (go list + shim write are I/O); "" means unavailable/omitted.
	crossCgoOnce  sync.Once
	crossCgoFlags string
}

// crossTarget returns the active cross-compilation target, or false when the
// run is native (no target, or a target equal to the host).
func (o *Orchestrator) crossTarget() (Target, bool) {
	t := o.Options.Target
	if t.IsZero() || t.IsNative() {
		return Target{}, false
	}
	return t, true
}

// verbKey is the TaskResults map key for a verb: the verb itself natively,
// "<verb>@<goos>_<goarch>" under a cross target. Both cache lookup and
// storage (reportTaskVerb) go through it so cross and native builds never
// invalidate or false-hit each other.
func (o *Orchestrator) verbKey(verb string) string {
	if t, ok := o.crossTarget(); ok {
		return verb + "@" + t.Pair()
	}
	return verb
}

func (o *Orchestrator) isCacheHit(job TaskJob, states map[string]WorkspaceState) bool {
	return o.isCacheHitForVerb(job, o.Options.Verb, states)
}

func (o *Orchestrator) isCacheHitForVerb(job TaskJob, verb string, states map[string]WorkspaceState) bool {
	if o.Options.NoCache {
		return false
	}
	s, ok := states[job.Name]
	if !ok || s.IsDirty || s.TaskResults == nil {
		return false
	}
	tr, ok := s.TaskResults[o.verbKey(verb)]
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
		jobs, states = o.filterAffected(ctx, jobs, states)
	}

	if o.Options.Strategy == StrategyWaveSorted {
		return o.runWaves(ctx, jobs, states)
	}
	return o.runFlat(ctx, jobs, states)
}

// filterAffected reduces jobs to the --affected selection (dirty or divergent
// from main, plus dependents under the wave-sorted strategy), falling back to
// local git state when the daemon returned nothing. It returns the (possibly
// freshly fetched) states so callers keep using them for cache-hit checks.
func (o *Orchestrator) filterAffected(ctx context.Context, jobs []TaskJob, states map[string]WorkspaceState) ([]TaskJob, map[string]WorkspaceState) {
	if len(states) == 0 {
		local := &LocalStateProvider{}
		states, _ = local.GetState(ctx, jobPaths(jobs))
	}
	if states == nil {
		return jobs, nil
	}
	return FilterAffected(jobs, states, o.Configs, o.DepGraph, o.Options.Strategy), states
}

// AffectedJobs returns the subset of jobs that --affected would run, using the
// orchestrator's state provider (with local-git fallback). Used by dry-run so
// the reported selection matches what a real run would execute.
func (o *Orchestrator) AffectedJobs(ctx context.Context, jobs []TaskJob) []TaskJob {
	var states map[string]WorkspaceState
	if o.StateProvider != nil {
		states, _ = o.StateProvider.GetState(ctx, jobPaths(jobs))
	}
	jobs, _ = o.filterAffected(ctx, jobs, states)
	return jobs
}

func (o *Orchestrator) runFlat(ctx context.Context, jobs []TaskJob, states map[string]WorkspaceState) <-chan TaskEvent {
	numWorkers := o.Options.Jobs
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	env := o.buildJobEnv(jobs)
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

	env := o.buildJobEnv(jobs)
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

	// Remote exec: if this run was aborted (Ctrl+C or fail-fast), tell the
	// daemon to kill this group's running process groups and drain its
	// queued jobs — dropping the SSE streams alone leaves them running.
	if runCtx.Err() != nil {
		o.cancelRemoteGroup()
	}
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

		output, duration, err, started := o.runProcess(ctx, job, verb, command, env, eventsChan)
		if !started {
			// The process (or remote submission) never ran — report the
			// failure without a task-result report, mirroring the local
			// start-error path.
			eventsChan <- TaskEvent{
				Job: job, Verb: verb, Type: "finish",
				Result: &TaskResult{Job: job, Verb: verb, Err: err, Duration: duration},
			}
			if o.Options.FailFast {
				once.Do(cancel)
			}
			if isPipeline {
				o.emitSkippedVerbs(job, verbs[vi+1:], verb, eventsChan)
			}
			return
		}

		skipped := false
		if err != nil && taskexec.IsMakeTargetMissing(command, string(output)) {
			err = nil
			skipped = true
		}

		// Cross-target compliance probe: a successful cross build whose
		// GROVE_BUILD_OUT dir holds no executable came from a Makefile that
		// ignores the injection — its bin/ may now hold foreign-arch
		// binaries, and it cannot participate in prebuilt deploys. Warn
		// hard (on the event stream for the TUI, on stderr for JSON/non-TTY
		// paths, and in the stored output) but do not fail the build.
		if t, cross := o.crossTarget(); cross && verb == "build" && err == nil && !skipped {
			if !t.OutDirHasExecutable(job.Path) {
				warn := fmt.Sprintf("WARNING: %s built for %s but produced no executable in %s — its Makefile ignores GROVE_BUILD_OUT; it will be excluded from prebuilt deploys", job.Name, t, t.OutDir())
				eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "output", OutputLine: warn}
				fmt.Fprintln(os.Stderr, warn)
				output = append(output, []byte("\n"+warn+"\n")...)
			}
		}

		result := TaskResult{
			Job:      job,
			Verb:     verb,
			Output:   output,
			Err:      err,
			Duration: duration,
			Skipped:  skipped,
		}
		eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "finish", Result: &result}

		exitCode := 0
		errSummary := ""
		if err != nil {
			exitCode = 1
			errSummary = extractErrorSummary(string(output))
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
	// Stored under verbKey so a cross-targeted result never masquerades as
	// (or clobbers) the native one — lookup (isCacheHitForVerb) matches.
	_ = o.DaemonClient.ReportTask(ctx, job.Path, o.verbKey(verb), exitCode, commitHash, durationMs, errorSummary)
}

// runProcess executes a single verb for a job, preferring the daemon's
// machine-wide build queue when remote exec is enabled and falling back to
// the local process path when it is not available. The returned bool is
// false when the process (or remote submission) never started.
func (o *Orchestrator) runProcess(ctx context.Context, job TaskJob, verb string, command, env []string, eventsChan chan<- TaskEvent) ([]byte, time.Duration, error, bool) {
	if o.remoteExecEnabled() {
		output, duration, err, started := o.runRemoteProcess(ctx, job, verb, command, env, eventsChan)
		if err == nil || !isRemoteUnavailable(err) {
			return output, duration, err, started
		}
		// Daemon unreachable, LocalClient, or a daemon that predates the
		// build queue — run everything locally from here on.
		o.disableRemoteExec()
	}
	return o.runLocalProcess(ctx, job, verb, command, env, eventsChan)
}

// runLocalProcess runs one verb in-process through the shared taskexec
// helper (the daemon's build queue executes through the same helper, so
// behavior is identical whichever side runs the job).
func (o *Orchestrator) runLocalProcess(ctx context.Context, job TaskJob, verb string, command, env []string, eventsChan chan<- TaskEvent) ([]byte, time.Duration, error, bool) {
	eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "start"}
	start := time.Now()

	task := taskexec.New(ctx, taskexec.Options{
		Command: command,
		Verb:    verb,
		Dir:     job.Path,
		Env:     prependJobBinDir(env, job.Path),
		OnOutput: func(line string) {
			eventsChan <- TaskEvent{
				Job:        job,
				Verb:       verb,
				Type:       "output",
				OutputLine: line,
			}
		},
	})
	if err := task.Start(); err != nil {
		return nil, time.Since(start), err, false
	}
	output, err := task.Wait()
	return output, time.Since(start), err, true
}

// buildJobEnv assembles the job environment: the standard env plus, under a
// cross target, the GROVE_TARGET_* injection (including the resolved
// GROVE_TARGET_CGO_CFLAGS when go-sqlite3 is in the module graph). A native
// target injects nothing — the Makefiles' default (native) build path stays
// untouched.
func (o *Orchestrator) buildJobEnv(jobs []TaskJob) []string {
	env := buildEnv(o.RunOpts)
	if t, ok := o.crossTarget(); ok {
		env = append(env, t.Env()...)
		if flags := o.crossCgoCflags(t, jobs); flags != "" {
			env = append(env, "GROVE_TARGET_CGO_CFLAGS="+flags)
		}
	}
	return env
}

// crossCgoCflags resolves Target.CgoCflags for the run's workspace container
// once per run. Failures are non-fatal: warn and omit the var (the affected
// repo's cross link will say the rest).
func (o *Orchestrator) crossCgoCflags(t Target, jobs []TaskJob) string {
	o.crossCgoOnce.Do(func() {
		flags, err := t.CgoCflags(workspaceContainer(jobs))
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: GROVE_TARGET_CGO_CFLAGS unavailable (%v) — sqlite cgo repos may fail their cross link\n", err)
			return
		}
		o.crossCgoFlags = flags
	})
	return o.crossCgoFlags
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
