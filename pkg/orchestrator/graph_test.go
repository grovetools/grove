package orchestrator

import (
	"reflect"
	"testing"

	"github.com/grovetools/core/config"
)

// TestFilterAffected_SeedsFromDirtyAndDivergence verifies that --affected
// selection seeds from IsDirty OR DivergesFromMain and, under the wave-sorted
// strategy, expands through the reverse dependency graph.
func TestFilterAffected_SeedsFromDirtyAndDivergence(t *testing.T) {
	jobs := []TaskJob{
		{Name: "core"},
		{Name: "grove"},
		{Name: "daemon"},
		{Name: "nav"},
	}
	configs := map[string]*config.Config{
		"grove":  {Name: "grove", BuildAfter: []string{"core"}},
		"daemon": {Name: "daemon", BuildAfter: []string{"grove"}},
		"nav":    {Name: "nav"},
	}

	// core is clean but has commits not on main; everything else is pristine.
	states := map[string]WorkspaceState{
		"core":   {IsDirty: false, DivergesFromMain: true},
		"grove":  {},
		"daemon": {},
		"nav":    {},
	}

	got := FilterAffected(jobs, states, configs, nil, StrategyWaveSorted)
	var names []string
	for _, j := range got {
		names = append(names, j.Name)
	}
	if want := []string{"core", "grove", "daemon"}; !reflect.DeepEqual(names, want) {
		t.Errorf("divergent seed selection = %v, want %v", names, want)
	}

	// Flat strategy: no dependency expansion, just the changed set.
	got = FilterAffected(jobs, states, configs, nil, StrategyFlat)
	if len(got) != 1 || got[0].Name != "core" {
		t.Errorf("flat divergent selection = %v, want [core]", got)
	}

	// Dirty seeding still works alongside divergence.
	states["nav"] = WorkspaceState{IsDirty: true}
	got = FilterAffected(jobs, states, configs, nil, StrategyWaveSorted)
	names = nil
	for _, j := range got {
		names = append(names, j.Name)
	}
	if want := []string{"core", "grove", "daemon", "nav"}; !reflect.DeepEqual(names, want) {
		t.Errorf("dirty+divergent selection = %v, want %v", names, want)
	}

	// Nothing changed -> nothing selected.
	clean := map[string]WorkspaceState{"core": {}, "grove": {}, "daemon": {}, "nav": {}}
	if got := FilterAffected(jobs, clean, configs, nil, StrategyWaveSorted); len(got) != 0 {
		t.Errorf("clean selection = %v, want empty", got)
	}
}

// TestFilterAffected_ExpandsThroughFullGraph verifies that dependents are found
// through the full derived import graph, not just the (cycle-condensed)
// build_after edges in configs: a cycle partner of a changed member has no
// config edge to it, yet is still affected by it.
func TestFilterAffected_ExpandsThroughFullGraph(t *testing.T) {
	jobs := []TaskJob{
		{Name: "core"},
		{Name: "tend"},
		{Name: "nav"},
		{Name: "website"},
	}
	// core <-> tend cycle: buildAfterEdges condensed their mutual edges away,
	// so configs carry none. nav imports tend. website is unrelated.
	configs := map[string]*config.Config{
		"nav": {Name: "nav", BuildAfter: []string{"tend"}},
	}
	graph := &DepGraph{deps: map[string][]string{
		"core": {"tend"},
		"tend": {"core"},
		"nav":  {"tend"},
	}}
	states := map[string]WorkspaceState{
		"core":    {DivergesFromMain: true},
		"tend":    {},
		"nav":     {},
		"website": {},
	}

	got := FilterAffected(jobs, states, configs, graph, StrategyWaveSorted)
	var names []string
	for _, j := range got {
		names = append(names, j.Name)
	}
	if want := []string{"core", "tend", "nav"}; !reflect.DeepEqual(names, want) {
		t.Errorf("full-graph expansion = %v, want %v (cycle partner + its dependents, unrelated excluded)", names, want)
	}
}
