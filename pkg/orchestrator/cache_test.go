package orchestrator

import (
	"testing"

	"github.com/grovetools/core/pkg/models"
)

// depGraphFixture models the real shape that produced the bug: flow imports
// core through go.work, nav imports neither.
func depGraphFixture() *DepGraph {
	return &DepGraph{deps: map[string][]string{
		"flow":   {"core"},
		"daemon": {"core"},
		"core":   nil,
		"nav":    nil,
	}}
}

func cacheStates(coreCommit string) map[string]WorkspaceState {
	return map[string]WorkspaceState{
		"core": {CommitHash: coreCommit},
		"flow": {CommitHash: "flow1"},
		"nav":  {CommitHash: "nav1"},
	}
}

// recordRun stores the cache token the orchestrator would report for job under
// verb "build", exactly as executeJobVerbs does.
func recordRun(o *Orchestrator, job TaskJob, states map[string]WorkspaceState) {
	s := states[job.Name]
	s.TaskResults = map[string]*models.TaskResult{
		"build": {ExitCode: 0, CommitHash: o.cacheToken(s.CommitHash, job, states)},
	}
	states[job.Name] = s
}

// TestCacheHit_SiblingCommitInvalidates is the regression test for the ticket:
// `grove build` reported flow cached after core — which flow links via go.work
// — got a new commit, so the flow binary shipped without the core change.
func TestCacheHit_SiblingCommitInvalidates(t *testing.T) {
	o := &Orchestrator{
		Options:  OrchestratorOptions{Verb: "build"},
		DepGraph: depGraphFixture(),
	}
	flow := TaskJob{Name: "flow"}
	nav := TaskJob{Name: "nav"}

	states := cacheStates("core1")
	recordRun(o, flow, states)
	recordRun(o, nav, states)

	if !o.isCacheHit(flow, states) {
		t.Fatal("flow should be cached when nothing has changed")
	}

	// core commits. flow depends on it, nav does not.
	states["core"] = WorkspaceState{CommitHash: "core2"}
	if o.isCacheHit(flow, states) {
		t.Error("flow must rebuild after a commit in its go.work sibling core")
	}
	if !o.isCacheHit(nav, states) {
		t.Error("nav does not depend on core and must stay cached")
	}

	// Rebuilding flow against the new core re-validates its cache.
	recordRun(o, flow, states)
	if !o.isCacheHit(flow, states) {
		t.Error("flow should be cached again once rebuilt against the new core")
	}
}

// TestCacheHit_DirtySiblingNeverHits covers the uncommitted half: a dirty
// sibling has no hash identity, so nothing downstream can be proven current —
// and the result recorded while it was dirty must not validate it once clean.
func TestCacheHit_DirtySiblingNeverHits(t *testing.T) {
	o := &Orchestrator{
		Options:  OrchestratorOptions{Verb: "build"},
		DepGraph: depGraphFixture(),
	}
	flow := TaskJob{Name: "flow"}

	states := cacheStates("core1")
	states["core"] = WorkspaceState{CommitHash: "core1", IsDirty: true}
	recordRun(o, flow, states)

	if o.isCacheHit(flow, states) {
		t.Error("flow must not hit the cache while core is dirty")
	}

	// core's edits are staged into a commit; the dirty-run result is stale.
	states["core"] = WorkspaceState{CommitHash: "core2"}
	if o.isCacheHit(flow, states) {
		t.Error("a result recorded against dirty core must not survive its commit")
	}

	// core's edits are reverted instead: still a different token than the
	// dirty run recorded, so flow rebuilds rather than trusting it.
	states["core"] = WorkspaceState{CommitHash: "core1"}
	if o.isCacheHit(flow, states) {
		t.Error("a result recorded against dirty core must not survive its revert")
	}
}

// TestCacheToken_LeafKeepsBareCommit pins the compatibility guarantee: members
// with no known workspace dependencies (and every run without a dep graph)
// still record a bare commit hash, so results cached before this change and
// results from repos with no siblings keep hitting.
func TestCacheToken_LeafKeepsBareCommit(t *testing.T) {
	states := cacheStates("core1")

	withGraph := &Orchestrator{Options: OrchestratorOptions{Verb: "build"}, DepGraph: depGraphFixture()}
	if got := withGraph.cacheToken("core1", TaskJob{Name: "core"}, states); got != "core1" {
		t.Errorf("leaf member token = %q, want bare commit", got)
	}

	noGraph := &Orchestrator{Options: OrchestratorOptions{Verb: "build"}}
	if got := noGraph.cacheToken("flow1", TaskJob{Name: "flow"}, states); got != "flow1" {
		t.Errorf("token without a dep graph = %q, want bare commit", got)
	}

	// A dep the run knows nothing about (filtered out of the selection)
	// contributes nothing rather than forcing a permanent miss.
	if got := withGraph.cacheToken("flow1", TaskJob{Name: "flow"}, map[string]WorkspaceState{
		"flow": {CommitHash: "flow1"},
	}); got != "flow1" {
		t.Errorf("token with no known deps = %q, want bare commit", got)
	}
}
