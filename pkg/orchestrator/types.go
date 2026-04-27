package orchestrator

import "time"

type ConcurrencyStrategy string

const (
	StrategyFlat       ConcurrencyStrategy = "flat"
	StrategyWaveSorted ConcurrencyStrategy = "wave-sorted"
)

type OrchestratorOptions struct {
	Verb         string
	Strategy     ConcurrencyStrategy
	AffectedOnly bool
	NoCache      bool
	Jobs         int
	JSONOutput   bool
}

type TaskJob struct {
	Name    string
	Path    string
	Command []string
}

type TaskResult struct {
	Job      TaskJob
	Output   []byte
	Err      error
	Duration time.Duration
	Cached   bool
}

type TaskEvent struct {
	Job        TaskJob
	Type       string // "start", "finish", "output", "cached"
	Result     *TaskResult
	OutputLine string
}
