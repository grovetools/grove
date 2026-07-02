package orchestrator

import "github.com/grovetools/core/config"

// BuildReverseGraph inverts the BuildAfter dependency graph.
// If A has BuildAfter=[B], then B changing means A is affected: reverseGraph[B] = [A].
func BuildReverseGraph(configs map[string]*config.Config) map[string][]string {
	reverse := make(map[string][]string)
	for name, cfg := range configs {
		if cfg == nil {
			continue
		}
		for _, dep := range cfg.BuildAfter {
			reverse[dep] = append(reverse[dep], name)
		}
	}
	return reverse
}

// FilterAffected returns only the jobs whose workspaces are changed — dirty or
// divergent from main (committed-but-unmerged) — plus, for the wave-sorted
// strategy, the jobs that transitively depend on a changed workspace.
//
// Dependents are found through the union of the declared build_after edges
// (configs) and the full derived import graph (graph, may be nil). The full
// graph matters: the scheduling edges written into configs have import-cycle
// edges condensed away (see buildAfterEdges), but a cycle partner of a changed
// member is still affected by it.
func FilterAffected(jobs []TaskJob, states map[string]WorkspaceState, configs map[string]*config.Config, graph *DepGraph, strategy ConcurrencyStrategy) []TaskJob {
	changed := make(map[string]bool)
	for _, job := range jobs {
		if s, ok := states[job.Name]; ok && (s.IsDirty || s.DivergesFromMain) {
			changed[job.Name] = true
		}
	}

	if strategy == StrategyFlat {
		return filterBySet(jobs, changed)
	}

	// Wave-sorted: expand changed set through reverse dependency graph
	reverse := BuildReverseGraph(configs)
	if graph != nil {
		for member, deps := range graph.deps {
			for _, dep := range deps {
				reverse[dep] = append(reverse[dep], member)
			}
		}
	}
	affected := make(map[string]bool)
	var walk func(name string)
	walk = func(name string) {
		if affected[name] {
			return
		}
		affected[name] = true
		for _, dep := range reverse[name] {
			walk(dep)
		}
	}
	for name := range changed {
		walk(name)
	}

	return filterBySet(jobs, affected)
}

func filterBySet(jobs []TaskJob, set map[string]bool) []TaskJob {
	var result []TaskJob
	for _, job := range jobs {
		if set[job.Name] {
			result = append(result, job)
		}
	}
	return result
}

// SortIntoWaves organizes task jobs into waves based on BuildAfter dependencies.
// Jobs within a wave can run in parallel; waves must run sequentially.
func SortIntoWaves(jobs []TaskJob, configs map[string]*config.Config) [][]TaskJob {
	nameSet := make(map[string]bool)
	for _, job := range jobs {
		nameSet[job.Name] = true
	}

	deps := make(map[string][]string)
	for _, job := range jobs {
		if cfg, ok := configs[job.Name]; ok && len(cfg.BuildAfter) > 0 {
			var validDeps []string
			for _, dep := range cfg.BuildAfter {
				if nameSet[dep] {
					validDeps = append(validDeps, dep)
				}
			}
			deps[job.Name] = validDeps
		}
	}

	built := make(map[string]bool)
	var waves [][]TaskJob
	remaining := len(jobs)

	for remaining > 0 {
		var wave []TaskJob
		for _, job := range jobs {
			if built[job.Name] {
				continue
			}
			canBuild := true
			for _, dep := range deps[job.Name] {
				if !built[dep] {
					canBuild = false
					break
				}
			}
			if canBuild {
				wave = append(wave, job)
			}
		}

		if len(wave) == 0 && remaining > 0 {
			for _, job := range jobs {
				if !built[job.Name] {
					wave = append(wave, job)
				}
			}
		}

		for _, job := range wave {
			built[job.Name] = true
			remaining--
		}
		if len(wave) > 0 {
			waves = append(waves, wave)
		}
	}

	return waves
}
