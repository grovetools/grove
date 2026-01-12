package build

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// BuildJob represents a single project to be built.
type BuildJob struct {
	Name    string   // e.g., "grove-core"
	Path    string   // Absolute path to the project directory
	Command []string // The build command to execute, e.g., ["make", "build"]
}

// RunOptions contains optional configuration for build runs
type RunOptions struct {
	// ExtraPathDirs are prepended to PATH for all build commands
	// This allows built tools to be available for subsequent builds
	ExtraPathDirs []string
}

// BuildResult contains the outcome of a single build job.
type BuildResult struct {
	Job      BuildJob
	Output   []byte // Combined stdout and stderr
	Err      error
	Duration time.Duration
}

// BuildEvent represents an event during the build process
type BuildEvent struct {
	Job        BuildJob
	Type       string // "start", "finish", or "output"
	Result     *BuildResult // nil for start and output events
	OutputLine string       // For "output" events
}

// Run executes a list of build jobs concurrently and returns a channel of results.
func Run(ctx context.Context, jobs []BuildJob, numWorkers int, continueOnError bool) <-chan BuildResult {
	return RunWithOptions(ctx, jobs, numWorkers, continueOnError, nil)
}

// RunWithOptions executes build jobs with additional options like extra PATH directories.
func RunWithOptions(ctx context.Context, jobs []BuildJob, numWorkers int, continueOnError bool, opts *RunOptions) <-chan BuildResult {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	// Build environment with extra PATH if provided
	env := buildEnv(opts)

	jobsChan := make(chan BuildJob, len(jobs))
	resultsChan := make(chan BuildResult, len(jobs))

	// If we don't continue on error, we need a cancellable context.
	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsChan {
				select {
				case <-runCtx.Done():
					// A previous job failed and we are not continuing on error.
					resultsChan <- BuildResult{Job: job, Err: runCtx.Err()}
					continue
				default:
					// Proceed with the build.
				}

				start := time.Now()

				var cmd *exec.Cmd
				if len(job.Command) == 0 {
					cmd = exec.CommandContext(runCtx, "make", "build")
				} else {
					cmd = exec.CommandContext(runCtx, job.Command[0], job.Command[1:]...)
				}

				cmd.Dir = job.Path
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				duration := time.Since(start)

				result := BuildResult{
					Job:      job,
					Output:   output,
					Err:      err,
					Duration: duration,
				}
				resultsChan <- result

				if err != nil && !continueOnError {
					once.Do(cancel) // Cancel all other running jobs
				}
			}
		}()
	}

	// Feed the jobs to the workers.
	for _, job := range jobs {
		jobsChan <- job
	}
	close(jobsChan)

	// Close the results channel once all workers are done.
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	return resultsChan
}

// buildEnv creates the environment for build commands, including extra PATH dirs
func buildEnv(opts *RunOptions) []string {
	env := os.Environ()
	env = append(env, "TERM=xterm-256color")

	if opts != nil && len(opts.ExtraPathDirs) > 0 {
		// Find and modify PATH
		pathPrefix := strings.Join(opts.ExtraPathDirs, string(os.PathListSeparator))
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				env[i] = "PATH=" + pathPrefix + string(os.PathListSeparator) + e[5:]
				return env
			}
		}
		// PATH not found, add it
		env = append(env, "PATH="+pathPrefix)
	}

	return env
}

// RunWithEvents executes build jobs and returns a channel of events (start and finish)
func RunWithEvents(ctx context.Context, jobs []BuildJob, numWorkers int, continueOnError bool) <-chan BuildEvent {
	return RunWithEventsAndOptions(ctx, jobs, numWorkers, continueOnError, nil)
}

// RunWithEventsAndOptions executes build jobs with options and returns a channel of events
func RunWithEventsAndOptions(ctx context.Context, jobs []BuildJob, numWorkers int, continueOnError bool, opts *RunOptions) <-chan BuildEvent {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	// Build environment with extra PATH if provided
	env := buildEnv(opts)

	jobsChan := make(chan BuildJob, len(jobs))
	eventsChan := make(chan BuildEvent, len(jobs)*2) // *2 for start and finish events

	// If we don't continue on error, we need a cancellable context.
	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsChan {
				select {
				case <-runCtx.Done():
					// A previous job failed and we are not continuing on error.
					eventsChan <- BuildEvent{
						Job:  job,
						Type: "finish",
						Result: &BuildResult{
							Job: job,
							Err: runCtx.Err(),
						},
					}
					continue
				default:
					// Proceed with the build.
				}

				// Send start event
				eventsChan <- BuildEvent{Job: job, Type: "start"}

				start := time.Now()

				var cmd *exec.Cmd
				if len(job.Command) == 0 {
					cmd = exec.CommandContext(runCtx, "make", "build")
				} else {
					cmd = exec.CommandContext(runCtx, job.Command[0], job.Command[1:]...)
				}

				cmd.Dir = job.Path
				cmd.Env = env

				// Get pipes for stdout and stderr
				stdoutPipe, _ := cmd.StdoutPipe()
				stderrPipe, _ := cmd.StderrPipe()

				// Buffer to capture full output
				var outputBuf bytes.Buffer

				// MultiReader to read from both pipes
				multiReader := io.MultiReader(stdoutPipe, stderrPipe)

				// Scanner to read line by line
				scanner := bufio.NewScanner(multiReader)

				// Goroutine to stream output
				var streamWg sync.WaitGroup
				streamWg.Add(1)
				go func() {
					defer streamWg.Done()
					for scanner.Scan() {
						line := scanner.Text()
						outputBuf.WriteString(line + "\n")
						eventsChan <- BuildEvent{
							Job:        job,
							Type:       "output",
							OutputLine: line,
						}
					}
				}()

				err := cmd.Start()
				if err != nil {
					// Send finish event with start error
					eventsChan <- BuildEvent{
						Job:  job,
						Type: "finish",
						Result: &BuildResult{
							Job:      job,
							Err:      err,
							Duration: time.Since(start),
						},
					}
					if !continueOnError {
						once.Do(cancel)
					}
					continue
				}

				// Wait for streaming to finish, then for command to exit
				streamWg.Wait()
				err = cmd.Wait()

				duration := time.Since(start)

				result := BuildResult{
					Job:      job,
					Output:   outputBuf.Bytes(),
					Err:      err,
					Duration: duration,
				}

				// Send finish event
				eventsChan <- BuildEvent{
					Job:    job,
					Type:   "finish",
					Result: &result,
				}

				if err != nil && !continueOnError {
					once.Do(cancel) // Cancel all other running jobs
				}
			}
		}()
	}

	// Feed the jobs to the workers.
	for _, job := range jobs {
		jobsChan <- job
	}
	close(jobsChan)

	// Close the events channel once all workers are done.
	go func() {
		wg.Wait()
		close(eventsChan)
	}()

	return eventsChan
}