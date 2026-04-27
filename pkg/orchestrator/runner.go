package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func (o *Orchestrator) reportTask(ctx context.Context, job TaskJob, exitCode int, commitHash string, durationMs int64) {
	if o.DaemonClient == nil || !o.DaemonClient.IsRunning() {
		return
	}
	_ = o.DaemonClient.ReportTask(ctx, job.Name, o.Options.Verb, exitCode, commitHash, durationMs)
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

func (o *Orchestrator) executeJobs(ctx context.Context, jobs []TaskJob, numWorkers int, env []string, states map[string]WorkspaceState, eventsChan chan<- TaskEvent) {
	jobsChan := make(chan TaskJob, len(jobs))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once

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
						Type: "finish",
						Result: &TaskResult{
							Job: job,
							Err: runCtx.Err(),
						},
					}
					continue
				default:
				}

				eventsChan <- TaskEvent{Job: job, Type: "start"}
				start := time.Now()

				var cmd *exec.Cmd
				if len(job.Command) == 0 {
					cmd = exec.CommandContext(runCtx, "make", o.Options.Verb)
				} else {
					cmd = exec.CommandContext(runCtx, job.Command[0], job.Command[1:]...) //nolint:gosec // commands from trusted build config
				}
				cmd.Dir = job.Path
				cmd.Env = prependJobBinDir(env, job.Path)

				stdoutPipe, _ := cmd.StdoutPipe()
				stderrPipe, _ := cmd.StderrPipe()

				var outputBuf bytes.Buffer
				multiReader := io.MultiReader(stdoutPipe, stderrPipe)
				scanner := bufio.NewScanner(multiReader)

				var streamWg sync.WaitGroup
				streamWg.Add(1)
				go func() {
					defer streamWg.Done()
					for scanner.Scan() {
						line := scanner.Text()
						outputBuf.WriteString(line + "\n")
						eventsChan <- TaskEvent{
							Job:        job,
							Type:       "output",
							OutputLine: line,
						}
					}
				}()

				err := cmd.Start()
				if err != nil {
					eventsChan <- TaskEvent{
						Job:  job,
						Type: "finish",
						Result: &TaskResult{
							Job:      job,
							Err:      err,
							Duration: time.Since(start),
						},
					}
					once.Do(cancel)
					continue
				}

				streamWg.Wait()
				err = cmd.Wait()
				duration := time.Since(start)

				result := TaskResult{
					Job:      job,
					Output:   outputBuf.Bytes(),
					Err:      err,
					Duration: duration,
				}
				eventsChan <- TaskEvent{Job: job, Type: "finish", Result: &result}

				exitCode := 0
				if err != nil {
					exitCode = 1
					once.Do(cancel)
				}

				if s, ok := states[job.Name]; ok {
					o.reportTask(runCtx, job, exitCode, s.CommitHash, duration.Milliseconds())
				}
			}
		}()
	}

	for _, job := range jobs {
		jobsChan <- job
	}
	close(jobsChan)
	wg.Wait()
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
