package orchestrator

import "time"

type ConcurrencyStrategy string

const (
	StrategyFlat       ConcurrencyStrategy = "flat"
	StrategyWaveSorted ConcurrencyStrategy = "wave-sorted"
)

type OrchestratorOptions struct {
	Verb         string
	Pipeline     []string
	Strategy     ConcurrencyStrategy
	AffectedOnly bool
	NoCache      bool
	Jobs         int
	JSONOutput   bool
	FailFast     bool
	// RemoteExec routes job execution through the global daemon's
	// machine-wide build queue (requires a BuildClient on the
	// Orchestrator). With remote exec, Jobs caps this invocation's
	// in-flight submissions while the daemon's max_parallel remains the
	// authoritative host-wide cap. Falls back to the local worker pool
	// when the daemon is unreachable or predates the build queue.
	RemoteExec bool
	// Target is the cross-compilation target (grove build --target). When
	// set and non-native, jobs get the GROVE_TARGET_* env injected and
	// task-result cache keys become "<verb>@<goos>_<goarch>" so cross and
	// native builds never invalidate or false-hit each other. A zero or
	// native target is a no-op.
	Target Target
}

type TaskJob struct {
	Name    string
	Path    string
	Command []string
}

type TaskResult struct {
	Job      TaskJob
	Verb     string
	Output   []byte
	Err      error
	Duration time.Duration
	Cached   bool
	Skipped  bool
}

type TaskEvent struct {
	Job        TaskJob
	Verb       string
	Type       string // "start", "finish", "output", "cached"
	Result     *TaskResult
	OutputLine string
}
