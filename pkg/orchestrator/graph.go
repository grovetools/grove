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

// FilterAffected returns only the jobs whose workspaces are dirty or transitively
// depended upon by a dirty workspace (wave-sorted strategy only).
func FilterAffected(jobs []TaskJob, states map[string]WorkspaceState, configs map[string]*config.Config, strategy ConcurrencyStrategy) []TaskJob {
	dirty := make(map[string]bool)
	for _, job := range jobs {
		if s, ok := states[job.Name]; ok && s.IsDirty {
			dirty[job.Name] = true
		}
	}

	if strategy == StrategyFlat {
		return filterBySet(jobs, dirty)
	}

	// Wave-sorted: expand dirty set through reverse dependency graph
	reverse := BuildReverseGraph(configs)
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
	for name := range dirty {
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
