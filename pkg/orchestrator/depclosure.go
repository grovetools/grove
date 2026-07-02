package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/grovetools/core/config"
)

// DepGraph holds each workspace member's direct workspace dependencies as
// derived from the actual Go import graph (`go list -deps`). It feeds wave
// ordering (via synthesized build_after edges, see DeriveWorkspaceBuildAfter)
// and exposes the transitive Closure of a member for artifact-input hashing.
type DepGraph struct {
	deps map[string][]string
}

// Direct returns member's direct workspace dependencies (sorted). It returns
// nil for members with no derived dependencies (including non-Go members).
func (g *DepGraph) Direct(member string) []string {
	if g == nil {
		return nil
	}
	return g.deps[member]
}

// Closure returns the transitive workspace dependencies of member, sorted.
// The member itself is never included.
func (g *DepGraph) Closure(member string) []string {
	if g == nil {
		return nil
	}
	seen := make(map[string]bool)
	var walk func(string)
	walk = func(m string) {
		for _, d := range g.deps[m] {
			if !seen[d] {
				seen[d] = true
				walk(d)
			}
		}
	}
	walk(member)
	delete(seen, member)
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// goListFunc resolves the set of module paths imported (directly or
// transitively) by the Go packages in dir. The second return is false on
// failure. Production code uses listImportedModules (native.go); tests inject
// a fake.
type goListFunc func(dir string) (map[string]bool, bool)

// DeriveWorkspaceBuildAfter derives build_after edges for EVERY Go member of
// the workspace from the actual import graph and merges them into configs
// (supplementing any declared build_after, which remains the mechanism for
// non-Go members). It returns the dependency graph for Closure queries.
//
// The import graph comes from `go list -deps` rather than go.mod: in a go.work
// workspace a member can import another member's packages without listing it
// in go.mod (see the DeriveNativeBuildAfter-era rationale preserved on
// listImportedModules). This unifies the native-artifact (zig/CGO) ordering:
// import edges are a superset of the old provider edges, and when `go list`
// fails for a Go member we conservatively order it after every native-artifact
// provider so CGO links never race the provider's build.
//
// The derived graph is cached at <container>/.grove/depgraph.json keyed by
// hash(go.work + all member go.mod contents); on a key match the go list pass
// is skipped entirely.
func DeriveWorkspaceBuildAfter(jobs []TaskJob, configs map[string]*config.Config) *DepGraph {
	return deriveWorkspaceBuildAfter(jobs, configs, listImportedModules, workspaceContainer(jobs))
}

// deriveWorkspaceBuildAfter is the injectable core of DeriveWorkspaceBuildAfter.
// container may be "" to disable caching.
func deriveWorkspaceBuildAfter(jobs []TaskJob, configs map[string]*config.Config, list goListFunc, container string) *DepGraph {
	var cachePath, key string
	if container != "" {
		cachePath = filepath.Join(container, ".grove", "depgraph.json")
		key = depGraphCacheKey(container, jobs)
	}

	g := loadDepGraphCache(cachePath, key)
	if g == nil {
		var complete bool
		g, complete = buildDepGraph(jobs, list)
		// Only persist fully-resolved graphs: a graph containing
		// conservative fallback edges (go list failure) must not shadow a
		// future successful derivation under the same key.
		if complete && cachePath != "" {
			saveDepGraphCache(cachePath, key, g)
		}
	}

	type memberDeps struct {
		name string
		deps []string
	}
	edges := g.buildAfterEdges(nativeProviders(jobs))
	items := make([]memberDeps, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, memberDeps{name: job.Name, deps: edges[job.Name]})
	}
	applyBuildAfterEdges(configs, items, func(m memberDeps) (string, []string) {
		return m.name, m.deps
	})
	return g
}

// buildAfterEdges returns the derived edges suitable for wave scheduling.
// Two reductions are applied, in order:
//
//  1. A native provider's edges to non-providers are dropped. A Go member
//     needs no BUILT artifact from its workspace deps (go.work compiles them
//     from source), so a provider's own imports impose no real ordering —
//     but its consumers must wait for its native lib. In the real workspace
//     compositor <-> tuimux import each other; without this reduction the
//     provider would land in a cycle with its CGO consumers and the
//     provider-first guarantee would be lost. This preserves the old
//     DeriveNativeBuildAfter placement (providers scheduled first).
//
//  2. Intra-cycle edges are removed. Module-level import cycles are real in a
//     go.work workspace (e.g. core <-> tend <-> tuimux) and have no valid
//     build order; feeding them to SortIntoWaves verbatim would trip its
//     cycle fallback and collapse the ENTIRE graph into a single wave.
//     Condensing strongly connected components keeps the schedulable
//     (inter-component) edges: cycle members simply share a wave, exactly as
//     they did before any edge was derived between them.
//
// Closure and affected-expansion are unaffected — they operate on the full
// graph.
func (g *DepGraph) buildAfterEdges(providers map[string]bool) map[string][]string {
	sched := make(map[string][]string, len(g.deps))
	for member, deps := range g.deps {
		if !providers[member] {
			sched[member] = deps
			continue
		}
		var kept []string
		for _, dep := range deps {
			if providers[dep] {
				kept = append(kept, dep)
			}
		}
		sched[member] = kept
	}

	comp := stronglyConnectedComponents(sched)
	edges := make(map[string][]string, len(sched))
	for member, deps := range sched {
		var kept []string
		for _, dep := range deps {
			if comp[member] != comp[dep] {
				kept = append(kept, dep)
			}
		}
		edges[member] = kept
	}
	return edges
}

// nativeProviders returns the set of members that produce native build
// artifacts (see producesNativeArtifact).
func nativeProviders(jobs []TaskJob) map[string]bool {
	p := make(map[string]bool)
	for _, job := range jobs {
		if producesNativeArtifact(job.Path) {
			p[job.Name] = true
		}
	}
	return p
}

// stronglyConnectedComponents assigns a component id to every node of the
// dependency graph (Tarjan). Nodes that appear only as dependency targets are
// included. Two members share an id iff they lie on a common import cycle.
func stronglyConnectedComponents(deps map[string][]string) map[string]int {
	index := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	comp := make(map[string]int)
	var stack []string
	next, nComps := 0, 0

	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = next
		lowlink[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range deps[v] {
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && index[w] < lowlink[v] {
				lowlink[v] = index[w]
			}
		}

		if lowlink[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = nComps
				if w == v {
					break
				}
			}
			nComps++
		}
	}

	// Deterministic iteration order for reproducible component ids.
	nodes := make([]string, 0, len(deps))
	for v := range deps {
		nodes = append(nodes, v)
	}
	sort.Strings(nodes)
	for _, v := range nodes {
		if _, seen := index[v]; !seen {
			strongconnect(v)
		}
	}
	return comp
}

// buildDepGraph runs the go list pass for every Go member (bounded by
// maxGoListWorkers) and translates imported module paths into member
// dependencies. The bool return reports whether every member resolved cleanly;
// members whose go list failed fall back to depending on every native-artifact
// provider (never under-schedule a CGO link).
func buildDepGraph(jobs []TaskJob, list goListFunc) (*DepGraph, bool) {
	memberByModule := make(map[string]string, len(jobs))
	var goMembers []TaskJob
	providers := nativeProviders(jobs)
	for _, job := range jobs {
		if modPath := readModulePath(filepath.Join(job.Path, "go.mod")); modPath != "" {
			memberByModule[modPath] = job.Name
			goMembers = append(goMembers, job)
		}
	}

	deps := make(map[string][]string, len(goMembers))
	complete := true
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxGoListWorkers())
	)
	for _, job := range goMembers {
		job := job
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var memberDeps []string
			imported, ok := list(job.Path)
			if ok {
				for modPath, member := range memberByModule {
					if member != job.Name && imported[modPath] {
						memberDeps = append(memberDeps, member)
					}
				}
			} else {
				for p := range providers {
					if p != job.Name {
						memberDeps = append(memberDeps, p)
					}
				}
			}
			sort.Strings(memberDeps)
			mu.Lock()
			deps[job.Name] = memberDeps
			if !ok {
				complete = false
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return &DepGraph{deps: deps}, complete
}

// workspaceContainer returns the directory containing all workspace members
// (the ecosystem/worktree root where go.work lives), or "" when the members do
// not share a single parent (caching is skipped in that case).
func workspaceContainer(jobs []TaskJob) string {
	if len(jobs) == 0 {
		return ""
	}
	container := filepath.Dir(jobs[0].Path)
	for _, job := range jobs[1:] {
		if filepath.Dir(job.Path) != container {
			return ""
		}
	}
	return container
}

// depGraphCacheKey hashes the inputs the derived graph depends on: the
// container's go.work plus every member's name and go.mod content. Any change
// to workspace membership or module requirements invalidates the cache.
func depGraphCacheKey(container string, jobs []TaskJob) string {
	h := sha256.New()
	if data, err := os.ReadFile(filepath.Join(container, "go.work")); err == nil {
		h.Write(data)
	}
	h.Write([]byte{0})
	sorted := make([]TaskJob, len(jobs))
	copy(sorted, jobs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, job := range sorted {
		h.Write([]byte(job.Name))
		h.Write([]byte{0})
		if data, err := os.ReadFile(filepath.Join(job.Path, "go.mod")); err == nil {
			h.Write(data)
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// depGraphCacheFile is the on-disk shape of .grove/depgraph.json.
type depGraphCacheFile struct {
	Key  string              `json:"key"`
	Deps map[string][]string `json:"deps"`
}

// loadDepGraphCache returns the cached graph when the stored key matches, nil
// otherwise (missing, unreadable, corrupt, or stale).
func loadDepGraphCache(path, key string) *DepGraph {
	if path == "" || key == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c depGraphCacheFile
	if err := json.Unmarshal(data, &c); err != nil || c.Key != key || c.Deps == nil {
		return nil
	}
	return &DepGraph{deps: c.Deps}
}

// saveDepGraphCache persists the graph atomically (tmp file in the same
// directory + rename) so concurrent grove invocations never observe a partial
// write. Failures are silent: the cache is a pure optimization.
func saveDepGraphCache(path, key string, g *DepGraph) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(depGraphCacheFile{Key: key, Deps: g.deps})
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".depgraph-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
	}
}
