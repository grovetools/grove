package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/grovetools/core/config"
)

// writeMember creates a workspace member directory under container. modPath ""
// creates a non-Go member (no go.mod).
func writeMember(t *testing.T, container, name, modPath string) TaskJob {
	t.Helper()
	dir := filepath.Join(container, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if modPath != "" {
		content := "module " + modPath + "\n\ngo 1.22\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return TaskJob{Name: name, Path: dir}
}

// markNativeProvider makes a member detectable by producesNativeArtifact.
func markNativeProvider(t *testing.T, job TaskJob) {
	t.Helper()
	zigDir := filepath.Join(job.Path, "zig")
	if err := os.MkdirAll(zigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zigDir, "build.zig"), []byte("// zig"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeGoList returns a goListFunc backed by a per-directory result table and a
// thread-safe call counter. Directories absent from the table fail.
func fakeGoList(results map[string]map[string]bool) (goListFunc, *int, *sync.Mutex) {
	var mu sync.Mutex
	calls := 0
	fn := func(dir string) (map[string]bool, bool) {
		mu.Lock()
		calls++
		mu.Unlock()
		mods, ok := results[dir]
		return mods, ok
	}
	return fn, &calls, &mu
}

// TestDeriveWorkspaceBuildAfter_EdgesAndClosure verifies that import-graph
// derived edges land in BuildAfter for every Go member (not just native
// consumers), that declared build_after on non-Go members survives untouched,
// and that Closure walks transitively.
func TestDeriveWorkspaceBuildAfter_EdgesAndClosure(t *testing.T) {
	container := t.TempDir()
	core := writeMember(t, container, "core", "example.com/core")
	grove := writeMember(t, container, "grove", "example.com/grove")
	daemon := writeMember(t, container, "daemon", "example.com/daemon")
	compositor := writeMember(t, container, "compositor", "example.com/compositor")
	nav := writeMember(t, container, "nav", "example.com/nav")
	website := writeMember(t, container, "website", "") // non-Go member
	markNativeProvider(t, compositor)

	jobs := []TaskJob{core, grove, daemon, compositor, nav, website}
	configs := map[string]*config.Config{
		"website": {Name: "website", BuildAfter: []string{"grove"}},
	}

	list, _, _ := fakeGoList(map[string]map[string]bool{
		core.Path:  {"example.com/core": true, "golang.org/x/mod": true},
		grove.Path: {"example.com/grove": true, "example.com/core": true},
		// daemon's listing deliberately omits core so the Closure test
		// exercises transitive expansion rather than go list's own
		// transitivity.
		daemon.Path:     {"example.com/daemon": true, "example.com/grove": true},
		compositor.Path: {"example.com/compositor": true, "example.com/core": true},
		nav.Path:        {"example.com/nav": true, "example.com/compositor": true, "example.com/core": true},
	})

	g := deriveWorkspaceBuildAfter(jobs, configs, list, container)

	wantEdges := map[string][]string{
		"grove":  {"core"},
		"daemon": {"grove"},
		"nav":    {"compositor", "core"},
	}
	for name, want := range wantEdges {
		cfg := configs[name]
		if cfg == nil {
			t.Fatalf("no config synthesized for %s", name)
		}
		if !reflect.DeepEqual(cfg.BuildAfter, want) {
			t.Errorf("%s.BuildAfter = %v, want %v", name, cfg.BuildAfter, want)
		}
	}
	if cfg, ok := configs["core"]; ok && len(cfg.BuildAfter) != 0 {
		t.Errorf("core.BuildAfter = %v, want none", cfg.BuildAfter)
	}
	// compositor is a native provider: its own imports impose no scheduling
	// edge (it needs no built artifact from its deps), keeping it in the
	// earliest possible wave as DeriveNativeBuildAfter did.
	if cfg, ok := configs["compositor"]; ok && len(cfg.BuildAfter) != 0 {
		t.Errorf("compositor.BuildAfter = %v, want none (provider schedules early)", cfg.BuildAfter)
	}
	if got := configs["website"].BuildAfter; !reflect.DeepEqual(got, []string{"grove"}) {
		t.Errorf("website.BuildAfter = %v, want declared [grove] preserved", got)
	}

	if got := g.Closure("daemon"); !reflect.DeepEqual(got, []string{"core", "grove"}) {
		t.Errorf("Closure(daemon) = %v, want [core grove]", got)
	}
	if got := g.Closure("nav"); !reflect.DeepEqual(got, []string{"compositor", "core"}) {
		t.Errorf("Closure(nav) = %v, want [compositor core]", got)
	}
	if got := g.Closure("core"); len(got) != 0 {
		t.Errorf("Closure(core) = %v, want empty", got)
	}

	// Wave ordering: core before grove before daemon.
	waves := SortIntoWaves(jobs, configs)
	pos := map[string]int{}
	for i, wave := range waves {
		for _, j := range wave {
			pos[j.Name] = i
		}
	}
	if !(pos["core"] < pos["grove"] && pos["grove"] < pos["daemon"]) {
		t.Errorf("wave order wrong: core=%d grove=%d daemon=%d", pos["core"], pos["grove"], pos["daemon"])
	}
	if !(pos["compositor"] < pos["nav"]) {
		t.Errorf("compositor wave %d should precede nav wave %d", pos["compositor"], pos["nav"])
	}
}

// TestDeriveWorkspaceBuildAfter_CyclesShareAWave mirrors the real workspace
// topology: core <-> tend import each other, and the native provider
// (compositor) and one of its CGO consumers (tuimux) also import each other.
// Module-level cycles have no valid build order; verbatim edges would trip
// SortIntoWaves' fallback and collapse the ENTIRE graph into one wave, losing
// the provider-before-consumer guarantee. Expected: provider out-edges and
// intra-cycle edges are dropped for scheduling (cycle members share a wave),
// edges in and out of cycles survive, and Closure still reports cycle partners.
func TestDeriveWorkspaceBuildAfter_CyclesShareAWave(t *testing.T) {
	container := t.TempDir()
	core := writeMember(t, container, "core", "example.com/core")
	tend := writeMember(t, container, "tend", "example.com/tend")
	compositor := writeMember(t, container, "compositor", "example.com/compositor")
	tuimux := writeMember(t, container, "tuimux", "example.com/tuimux")
	markNativeProvider(t, compositor)

	jobs := []TaskJob{core, tend, compositor, tuimux}
	configs := map[string]*config.Config{}

	list, _, _ := fakeGoList(map[string]map[string]bool{
		// core <-> tend cycle.
		core.Path: {"example.com/core": true, "example.com/tend": true},
		tend.Path: {"example.com/tend": true, "example.com/core": true},
		// compositor <-> tuimux cycle: the provider imports one of its own
		// CGO consumers.
		compositor.Path: {"example.com/compositor": true, "example.com/core": true, "example.com/tuimux": true},
		tuimux.Path:     {"example.com/tuimux": true, "example.com/compositor": true, "example.com/core": true, "example.com/tend": true},
	})

	g := deriveWorkspaceBuildAfter(jobs, configs, list, container)

	if cfg, ok := configs["core"]; ok && len(cfg.BuildAfter) != 0 {
		t.Errorf("core.BuildAfter = %v, want none (intra-cycle edge dropped)", cfg.BuildAfter)
	}
	if cfg, ok := configs["tend"]; ok && len(cfg.BuildAfter) != 0 {
		t.Errorf("tend.BuildAfter = %v, want none (intra-cycle edge dropped)", cfg.BuildAfter)
	}
	if cfg, ok := configs["compositor"]; ok && len(cfg.BuildAfter) != 0 {
		t.Errorf("compositor.BuildAfter = %v, want none (provider schedules early)", cfg.BuildAfter)
	}
	// tuimux must still wait for the provider's native lib, plus the core/tend
	// component.
	if got := configs["tuimux"].BuildAfter; !reflect.DeepEqual(got, []string{"compositor", "core", "tend"}) {
		t.Errorf("tuimux.BuildAfter = %v, want [compositor core tend]", got)
	}

	// Closure keeps the full graph, including cycle partners, without looping.
	if got := g.Closure("core"); !reflect.DeepEqual(got, []string{"tend"}) {
		t.Errorf("Closure(core) = %v, want [tend]", got)
	}
	if got := g.Closure("compositor"); !reflect.DeepEqual(got, []string{"core", "tend", "tuimux"}) {
		t.Errorf("Closure(compositor) = %v, want [core tend tuimux]", got)
	}

	// Waves: {core,tend,compositor} first, then tuimux — never one blob.
	waves := SortIntoWaves(jobs, configs)
	pos := map[string]int{}
	for i, wave := range waves {
		for _, j := range wave {
			pos[j.Name] = i
		}
	}
	if pos["core"] != pos["tend"] {
		t.Errorf("cycle members should share a wave: core=%d tend=%d", pos["core"], pos["tend"])
	}
	if !(pos["compositor"] < pos["tuimux"]) {
		t.Errorf("provider wave %d should precede CGO consumer wave %d", pos["compositor"], pos["tuimux"])
	}
}

// TestDeriveWorkspaceBuildAfter_GoListFailureFallsBackToProviders verifies the
// conservative CGO ordering: a Go member whose go list fails is ordered after
// every native-artifact provider, and the incomplete graph is not cached.
func TestDeriveWorkspaceBuildAfter_GoListFailureFallsBackToProviders(t *testing.T) {
	container := t.TempDir()
	compositor := writeMember(t, container, "compositor", "example.com/compositor")
	broken := writeMember(t, container, "broken", "example.com/broken")
	markNativeProvider(t, compositor)

	jobs := []TaskJob{compositor, broken}
	configs := map[string]*config.Config{}

	list, _, _ := fakeGoList(map[string]map[string]bool{
		compositor.Path: {"example.com/compositor": true},
		// broken.Path absent -> go list failure
	})

	deriveWorkspaceBuildAfter(jobs, configs, list, container)

	if got := configs["broken"].BuildAfter; !reflect.DeepEqual(got, []string{"compositor"}) {
		t.Errorf("broken.BuildAfter = %v, want fallback [compositor]", got)
	}
	if _, err := os.Stat(filepath.Join(container, ".grove", "depgraph.json")); !os.IsNotExist(err) {
		t.Errorf("incomplete graph must not be cached (stat err = %v)", err)
	}
}

// TestDepGraphCache_HitAndInvalidation verifies that a complete graph is
// persisted, that a key match skips the go list pass entirely while yielding
// identical edges, and that changing a member's go.mod invalidates the key.
func TestDepGraphCache_HitAndInvalidation(t *testing.T) {
	container := t.TempDir()
	core := writeMember(t, container, "core", "example.com/core")
	grove := writeMember(t, container, "grove", "example.com/grove")
	jobs := []TaskJob{core, grove}

	results := map[string]map[string]bool{
		core.Path:  {"example.com/core": true},
		grove.Path: {"example.com/grove": true, "example.com/core": true},
	}

	// Cold: computes and caches.
	list1, calls1, mu1 := fakeGoList(results)
	configs1 := map[string]*config.Config{}
	deriveWorkspaceBuildAfter(jobs, configs1, list1, container)
	mu1.Lock()
	if *calls1 != 2 {
		t.Fatalf("cold run: go list calls = %d, want 2", *calls1)
	}
	mu1.Unlock()
	if _, err := os.Stat(filepath.Join(container, ".grove", "depgraph.json")); err != nil {
		t.Fatalf("expected cache file after complete derivation: %v", err)
	}

	// Warm: key matches, go list never runs, edges identical.
	list2, calls2, mu2 := fakeGoList(results)
	configs2 := map[string]*config.Config{}
	g := deriveWorkspaceBuildAfter(jobs, configs2, list2, container)
	mu2.Lock()
	if *calls2 != 0 {
		t.Fatalf("warm run: go list calls = %d, want 0", *calls2)
	}
	mu2.Unlock()
	if got := configs2["grove"].BuildAfter; !reflect.DeepEqual(got, []string{"core"}) {
		t.Errorf("warm grove.BuildAfter = %v, want [core]", got)
	}
	if got := g.Closure("grove"); !reflect.DeepEqual(got, []string{"core"}) {
		t.Errorf("warm Closure(grove) = %v, want [core]", got)
	}

	// go.mod change: key mismatch, recompute.
	newMod := "module example.com/grove\n\ngo 1.22\n\nrequire example.com/other v1.0.0\n"
	if err := os.WriteFile(filepath.Join(grove.Path, "go.mod"), []byte(newMod), 0o644); err != nil {
		t.Fatal(err)
	}
	list3, calls3, mu3 := fakeGoList(results)
	configs3 := map[string]*config.Config{}
	deriveWorkspaceBuildAfter(jobs, configs3, list3, container)
	mu3.Lock()
	if *calls3 != 2 {
		t.Fatalf("after go.mod change: go list calls = %d, want 2 (cache must invalidate)", *calls3)
	}
	mu3.Unlock()
}

// TestDepGraphCache_GoWorkChangeInvalidates verifies that editing go.work at
// the container root invalidates the cache key.
func TestDepGraphCache_GoWorkChangeInvalidates(t *testing.T) {
	container := t.TempDir()
	core := writeMember(t, container, "core", "example.com/core")
	jobs := []TaskJob{core}
	if err := os.WriteFile(filepath.Join(container, "go.work"), []byte("go 1.22\n\nuse ./core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := map[string]map[string]bool{core.Path: {"example.com/core": true}}

	list1, _, _ := fakeGoList(results)
	deriveWorkspaceBuildAfter(jobs, map[string]*config.Config{}, list1, container)

	if err := os.WriteFile(filepath.Join(container, "go.work"), []byte("go 1.22\n\nuse (\n\t./core\n\t./grove\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list2, calls2, mu2 := fakeGoList(results)
	deriveWorkspaceBuildAfter(jobs, map[string]*config.Config{}, list2, container)
	mu2.Lock()
	defer mu2.Unlock()
	if *calls2 != 1 {
		t.Fatalf("after go.work change: go list calls = %d, want 1 (cache must invalidate)", *calls2)
	}
}

// TestWorkspaceContainer verifies container detection: siblings share a parent;
// members under different parents disable caching.
func TestWorkspaceContainer(t *testing.T) {
	container := t.TempDir()
	a := writeMember(t, container, "a", "")
	b := writeMember(t, container, "b", "")
	if got := workspaceContainer([]TaskJob{a, b}); got != container {
		t.Errorf("workspaceContainer = %q, want %q", got, container)
	}
	elsewhere := TaskJob{Name: "c", Path: filepath.Join(t.TempDir(), "c")}
	if got := workspaceContainer([]TaskJob{a, elsewhere}); got != "" {
		t.Errorf("workspaceContainer with split parents = %q, want \"\"", got)
	}
	if got := workspaceContainer(nil); got != "" {
		t.Errorf("workspaceContainer(nil) = %q, want \"\"", got)
	}
}
