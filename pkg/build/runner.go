package build

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// BuildJob represents a single project to be built.
type BuildJob struct {
	Name string // e.g., "grove-core"
	Path string // Absolute path to the project directory
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
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

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
				cmd := exec.CommandContext(runCtx, "make", "build")
				cmd.Dir = job.Path
				// Set environment to ensure consistent behavior
				cmd.Env = append(os.Environ(), "TERM=xterm-256color")
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

// RunWithEvents executes build jobs and returns a channel of events (start and finish)
func RunWithEvents(ctx context.Context, jobs []BuildJob, numWorkers int, continueOnError bool) <-chan BuildEvent {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

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
				cmd := exec.CommandContext(runCtx, "make", "build")
				cmd.Dir = job.Path
				cmd.Env = append(os.Environ(), "TERM=xterm-256color")

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