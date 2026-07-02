package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/grovetools/core/pkg/models"
)

// BuildClient is the daemon-client subset used for remote build execution
// through the global daemon's machine-wide build queue. daemon.Client
// satisfies it.
type BuildClient interface {
	SubmitBuild(ctx context.Context, req models.BuildJobRequest) (string, error)
	CancelBuild(ctx context.Context, groupID string) error
	StreamBuildEvents(ctx context.Context, jobID string) (<-chan models.BuildJobEvent, error)
}

// errRemoteUnavailable signals that a job could not be handed to the daemon
// build queue at all (daemon unreachable, LocalClient, or a daemon that
// predates the endpoint). Submission is atomic, so the caller can safely
// re-run the job on the local pool.
var errRemoteUnavailable = errors.New("remote build execution unavailable")

func isRemoteUnavailable(err error) bool {
	return errors.Is(err, errRemoteUnavailable)
}

// remoteExecEnabled reports whether jobs should be submitted to the daemon
// build queue rather than run on the local worker pool.
func (o *Orchestrator) remoteExecEnabled() bool {
	if !o.Options.RemoteExec || o.BuildClient == nil {
		return false
	}
	o.remoteMu.Lock()
	defer o.remoteMu.Unlock()
	return !o.remoteDisabled
}

// disableRemoteExec latches remote exec off for the rest of this run after
// a failed submission, so every subsequent job goes straight to the local
// pool instead of re-probing a dead daemon.
func (o *Orchestrator) disableRemoteExec() {
	o.remoteMu.Lock()
	o.remoteDisabled = true
	o.remoteMu.Unlock()
}

// buildGroupID lazily generates the submission group shared by every job
// of this orchestrator run — the unit of cancellation on Ctrl+C/fail-fast.
func (o *Orchestrator) buildGroupID() string {
	o.remoteMu.Lock()
	defer o.remoteMu.Unlock()
	if o.groupID == "" {
		o.groupID = fmt.Sprintf("grove-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return o.groupID
}

func (o *Orchestrator) markRemoteSubmitted() {
	o.remoteMu.Lock()
	o.remoteSubmitted = true
	o.remoteMu.Unlock()
}

// cancelRemoteGroup asks the daemon to kill this run's running process
// groups and drain its queued jobs. Called after an aborted run; a no-op
// when nothing was ever submitted.
func (o *Orchestrator) cancelRemoteGroup() {
	o.remoteMu.Lock()
	groupID := o.groupID
	submitted := o.remoteSubmitted
	o.remoteMu.Unlock()
	if !submitted || o.BuildClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = o.BuildClient.CancelBuild(ctx, groupID)
}

// runRemoteProcess executes one job verb through the daemon's machine-wide
// build queue, mapping the remote event stream 1:1 onto the same TaskEvent
// types the local pool emits ("start" on the daemon's started event,
// "output" per line; the caller emits "finish"). Returns
// errRemoteUnavailable when the submission itself failed so the caller can
// fall back to the local pool.
func (o *Orchestrator) runRemoteProcess(ctx context.Context, job TaskJob, verb string, command, env []string, eventsChan chan<- TaskEvent) ([]byte, time.Duration, error, bool) {
	jobID, err := o.BuildClient.SubmitBuild(ctx, models.BuildJobRequest{
		Workspace: job.Name,
		Dir:       job.Path,
		Command:   command,
		Env:       prependJobBinDir(env, job.Path),
		GroupID:   o.buildGroupID(),
		Verb:      verb,
	})
	if err != nil {
		// Any submit failure is safe to retry locally: no job was queued.
		return nil, 0, errRemoteUnavailable, false
	}
	o.markRemoteSubmitted()

	events, err := o.BuildClient.StreamBuildEvents(ctx, jobID)
	if err != nil {
		// The job is queued but unobservable; report a failure rather than
		// risking a second, concurrent local run of the same job.
		eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "start"}
		return nil, 0, fmt.Errorf("stream build events for %s: %w", job.Name, err), true
	}

	var outputBuf bytes.Buffer
	start := time.Now()
	for ev := range events {
		switch ev.Event {
		case models.BuildEventStarted:
			// The job left the daemon's queue — this is the moment that
			// corresponds to the local pool's pre-exec "start" event.
			start = time.Now()
			eventsChan <- TaskEvent{Job: job, Verb: verb, Type: "start"}
		case models.BuildEventOutput:
			outputBuf.WriteString(ev.Line + "\n")
			eventsChan <- TaskEvent{
				Job:        job,
				Verb:       verb,
				Type:       "output",
				OutputLine: ev.Line,
			}
		case models.BuildEventFinished:
			duration := time.Duration(ev.DurationMs) * time.Millisecond
			var runErr error
			switch {
			case ev.Cancelled:
				runErr = context.Canceled
			case ev.ExitCode != 0 || ev.Error != "":
				msg := ev.Error
				if msg == "" {
					msg = fmt.Sprintf("exit status %d", ev.ExitCode)
				}
				runErr = errors.New(msg)
			}
			return outputBuf.Bytes(), duration, runErr, true
		}
	}

	// Stream closed without a terminal event: our context was cancelled or
	// the daemon went away mid-build.
	if ctx.Err() != nil {
		return outputBuf.Bytes(), time.Since(start), ctx.Err(), true
	}
	return outputBuf.Bytes(), time.Since(start), fmt.Errorf("build event stream for %s closed before completion", job.Name), true
}
