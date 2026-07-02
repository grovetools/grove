package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
)

// fakeBuildClient scripts the daemon build-queue client surface.
type fakeBuildClient struct {
	mu        sync.Mutex
	submitted []models.BuildJobRequest
	cancelled []string
	submitErr error
	jobEvents map[string][]models.BuildJobEvent // keyed by workspace name
	nextJobID int
	jobIDToWs map[string]string
}

func newFakeBuildClient() *fakeBuildClient {
	return &fakeBuildClient{
		jobEvents: make(map[string][]models.BuildJobEvent),
		jobIDToWs: make(map[string]string),
	}
}

func (f *fakeBuildClient) SubmitBuild(ctx context.Context, req models.BuildJobRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return "", f.submitErr
	}
	f.submitted = append(f.submitted, req)
	f.nextJobID++
	id := "job-" + req.Workspace
	f.jobIDToWs[id] = req.Workspace
	return id, nil
}

func (f *fakeBuildClient) CancelBuild(ctx context.Context, groupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, groupID)
	return nil
}

func (f *fakeBuildClient) StreamBuildEvents(ctx context.Context, jobID string) (<-chan models.BuildJobEvent, error) {
	f.mu.Lock()
	ws := f.jobIDToWs[jobID]
	events := f.jobEvents[ws]
	f.mu.Unlock()

	ch := make(chan models.BuildJobEvent, len(events)+1)
	for _, ev := range events {
		ev.JobID = jobID
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func remoteOrchestrator(client BuildClient) *Orchestrator {
	return &Orchestrator{
		Options: OrchestratorOptions{
			Verb:       "build",
			Strategy:   StrategyFlat,
			Jobs:       2,
			RemoteExec: true,
		},
		BuildClient: client,
	}
}

func collectTaskEvents(events <-chan TaskEvent) (starts, outputs, finishes []TaskEvent) {
	for ev := range events {
		switch ev.Type {
		case "start":
			starts = append(starts, ev)
		case "output":
			outputs = append(outputs, ev)
		case "finish":
			finishes = append(finishes, ev)
		}
	}
	return starts, outputs, finishes
}

func TestRemoteExecMapsEventsOntoTaskEvents(t *testing.T) {
	fake := newFakeBuildClient()
	fake.jobEvents["ws1"] = []models.BuildJobEvent{
		{Event: models.BuildEventQueued},
		{Event: models.BuildEventStarted},
		{Event: models.BuildEventOutput, Line: "compiling"},
		{Event: models.BuildEventOutput, Line: "done"},
		{Event: models.BuildEventFinished, ExitCode: 0, DurationMs: 42},
	}

	o := remoteOrchestrator(fake)
	jobs := []TaskJob{{Name: "ws1", Path: t.TempDir(), Command: []string{"make", "build"}}}
	starts, outputs, finishes := collectTaskEvents(o.RunWithEvents(context.Background(), jobs))

	if len(starts) != 1 {
		t.Errorf("expected 1 start event, got %d", len(starts))
	}
	if len(outputs) != 2 || outputs[0].OutputLine != "compiling" {
		t.Errorf("unexpected output events: %+v", outputs)
	}
	if len(finishes) != 1 {
		t.Fatalf("expected 1 finish event, got %d", len(finishes))
	}
	res := finishes[0].Result
	if res == nil || res.Err != nil {
		t.Errorf("expected successful result, got %+v", res)
	}
	if !strings.Contains(string(res.Output), "compiling") {
		t.Errorf("result output not accumulated from remote stream: %q", string(res.Output))
	}

	if len(fake.submitted) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(fake.submitted))
	}
	req := fake.submitted[0]
	if req.GroupID == "" {
		t.Error("submission missing group ID")
	}
	if len(req.Env) == 0 {
		t.Error("submission missing resolved env")
	}
	if req.Verb != "build" || req.Workspace != "ws1" {
		t.Errorf("unexpected submission: %+v", req)
	}
}

func TestRemoteExecMapsFailureExitCode(t *testing.T) {
	fake := newFakeBuildClient()
	fake.jobEvents["ws1"] = []models.BuildJobEvent{
		{Event: models.BuildEventStarted},
		{Event: models.BuildEventOutput, Line: "boom"},
		{Event: models.BuildEventFinished, ExitCode: 2, Error: "exit status 2"},
	}

	o := remoteOrchestrator(fake)
	jobs := []TaskJob{{Name: "ws1", Path: t.TempDir(), Command: []string{"make", "build"}}}
	_, _, finishes := collectTaskEvents(o.RunWithEvents(context.Background(), jobs))

	if len(finishes) != 1 || finishes[0].Result == nil {
		t.Fatalf("expected 1 finish event with result, got %+v", finishes)
	}
	if finishes[0].Result.Err == nil {
		t.Error("expected error result for exit code 2")
	}
}

func TestRemoteExecMissingMakeTargetMapsToSkipped(t *testing.T) {
	fake := newFakeBuildClient()
	fake.jobEvents["ws1"] = []models.BuildJobEvent{
		{Event: models.BuildEventStarted},
		{Event: models.BuildEventOutput, Line: "make: *** No rule to make target `lint'.  Stop."},
		{Event: models.BuildEventFinished, ExitCode: 2, Error: "exit status 2"},
	}

	o := remoteOrchestrator(fake)
	// Empty command → default `make <verb>`, which is eligible for the
	// missing-target skip classification.
	jobs := []TaskJob{{Name: "ws1", Path: t.TempDir()}}
	_, _, finishes := collectTaskEvents(o.RunWithEvents(context.Background(), jobs))

	if len(finishes) != 1 || finishes[0].Result == nil {
		t.Fatalf("expected 1 finish event with result, got %+v", finishes)
	}
	res := finishes[0].Result
	if !res.Skipped || res.Err != nil {
		t.Errorf("expected skipped result for missing make target, got %+v", res)
	}
}

func TestRemoteExecFallsBackToLocalPoolWhenUnsupported(t *testing.T) {
	fake := newFakeBuildClient()
	fake.submitErr = daemon.ErrNotSupported

	o := remoteOrchestrator(fake)
	jobs := []TaskJob{{Name: "ws1", Path: t.TempDir(), Command: []string{"sh", "-c", "echo local-ran"}}}
	_, _, finishes := collectTaskEvents(o.RunWithEvents(context.Background(), jobs))

	if len(finishes) != 1 || finishes[0].Result == nil {
		t.Fatalf("expected 1 finish event, got %+v", finishes)
	}
	res := finishes[0].Result
	if res.Err != nil {
		t.Fatalf("local fallback failed: %v", res.Err)
	}
	if !strings.Contains(string(res.Output), "local-ran") {
		t.Errorf("expected local execution output, got %q", string(res.Output))
	}
	if !o.remoteDisabled {
		t.Error("remote exec should be latched off after a failed submit")
	}
}

func TestRemoteExecCancelsGroupOnFailFast(t *testing.T) {
	fake := newFakeBuildClient()
	fake.jobEvents["ws1"] = []models.BuildJobEvent{
		{Event: models.BuildEventStarted},
		{Event: models.BuildEventFinished, ExitCode: 1, Error: "exit status 1"},
	}
	fake.jobEvents["ws2"] = []models.BuildJobEvent{
		{Event: models.BuildEventStarted},
		{Event: models.BuildEventFinished, ExitCode: 0},
	}

	o := remoteOrchestrator(fake)
	o.Options.FailFast = true
	o.Options.Jobs = 1 // serialize so the failure cancels before ws2 runs

	jobs := []TaskJob{
		{Name: "ws1", Path: t.TempDir(), Command: []string{"make", "build"}},
		{Name: "ws2", Path: t.TempDir(), Command: []string{"make", "build"}},
	}
	collectTaskEvents(o.RunWithEvents(context.Background(), jobs))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.cancelled) == 0 {
		t.Error("expected CancelBuild after fail-fast abort")
	}
}
