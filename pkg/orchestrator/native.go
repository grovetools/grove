package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/grovetools/core/config"
	"golang.org/x/mod/modfile"
)

// producesNativeArtifact reports whether a workspace member produces a native
// (non-Go) build artifact that its Go consumers link at build time.
//
// Today the only such member is `compositor`, which builds libcompositor.a /
// libgrove-compositor-ext.a via its `zig` make target (see compositor/Makefile),
// and whose consumers CGO-link the static lib (compositor/compositor.go:
// `#cgo LDFLAGS: -L${SRCDIR}/zig/zig-out/lib -lcompositor`). Because that lib is
// produced by compositor's own build command rather than by `go build` of the
// consumer, a consumer scheduled in the same wave as compositor fails to link on
// a fresh worktree ("ld: library 'compositor' not found"). We detect such
// members by the presence of a zig build (zig/build.zig); future native
// providers using a different toolchain can extend this probe.
func producesNativeArtifact(path string) bool {
	_, err := os.Stat(filepath.Join(path, "zig", "build.zig"))
	return err == nil
}

// DeriveNativeBuildAfter augments configs with synthetic build_after edges so
// that members producing native artifacts are scheduled into an earlier wave
// than the members that CGO-link those artifacts.
//
// The consumer relationship is discovered from the *actual* Go import graph
// (`go list -deps`), not from go.mod. In a go.work workspace a member can import
// another member's packages — and thus link its native lib — without listing it
// in go.mod (e.g. grove transitively imports compositor through the embedded TUI
// command packages while grove/go.mod mentions neither). go.mod is therefore an
// unreliable signal here.
//
// It mutates the *config.Config values in configs (appending to BuildAfter),
// creating a minimal config for members that had no loaded config so the edge
// survives into SortIntoWaves. It is a no-op when no member produces a native
// artifact (the common case for ecosystems without compositor), so the
// `go list` pre-pass only runs for builds that actually include a provider.
func DeriveNativeBuildAfter(jobs []TaskJob, configs map[string]*config.Config) {
	// memberByModule maps a module path to the member that defines it; provider
	// records the module path of each native-artifact provider member.
	memberByModule := make(map[string]string, len(jobs))
	provider := make(map[string]string) // member name -> module path
	for _, job := range jobs {
		modPath := readModulePath(filepath.Join(job.Path, "go.mod"))
		if modPath != "" {
			memberByModule[modPath] = job.Name
		}
		if producesNativeArtifact(job.Path) {
			provider[job.Name] = modPath
		}
	}
	if len(provider) == 0 {
		return
	}

	// providerModule maps each provider's module path back to its member name so
	// we can translate imported module paths into build_after edges.
	providerModule := make(map[string]string, len(provider))
	for name, modPath := range provider {
		if modPath != "" {
			providerModule[modPath] = name
		}
	}

	// For each non-provider member, resolve the providers it links and record
	// the edges. go list runs concurrently; on error we conservatively treat the
	// member as depending on every provider so we never under-schedule.
	type edgeResult struct {
		consumer string
		deps     []string
	}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxGoListWorkers())
		results []edgeResult
	)
	for _, job := range jobs {
		if _, isProvider := provider[job.Name]; isProvider {
			continue
		}
		job := job
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			deps := consumedProviders(job.Path, providerModule, provider)
			mu.Lock()
			results = append(results, edgeResult{consumer: job.Name, deps: deps})
			mu.Unlock()
		}()
	}
	wg.Wait()

	applyBuildAfterEdges(configs, results, func(r edgeResult) (string, []string) {
		return r.consumer, r.deps
	})
}

// consumedProviders returns the provider member names whose modules are imported
// (directly or transitively) by the member at path. On any `go list` failure it
// conservatively returns all providers so the member is still ordered after them.
func consumedProviders(path string, providerModule map[string]string, allProviders map[string]string) []string {
	imported, ok := listImportedModules(path)
	if !ok {
		all := make([]string, 0, len(allProviders))
		for name := range allProviders {
			all = append(all, name)
		}
		return all
	}
	var deps []string
	for mod, name := range providerModule {
		if imported[mod] {
			deps = append(deps, name)
		}
	}
	return deps
}

// listImportedModules runs `go list -deps` in dir and returns the set of module
// paths reachable from its packages. The second return is false on error.
func listImportedModules(dir string) (map[string]bool, bool) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	mods := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			mods[line] = true
		}
	}
	return mods, true
}

// applyBuildAfterEdges adds the derived build_after edges to configs, creating a
// minimal config for any member that had none. It is the pure core of the
// derivation and is exercised directly by tests.
func applyBuildAfterEdges[T any](configs map[string]*config.Config, items []T, edges func(T) (string, []string)) {
	for _, item := range items {
		consumer, deps := edges(item)
		if len(deps) == 0 {
			continue
		}
		cfg := configs[consumer]
		if cfg == nil {
			cfg = &config.Config{Name: consumer}
			configs[consumer] = cfg
		}
		for _, dep := range deps {
			if dep != consumer && !containsString(cfg.BuildAfter, dep) {
				cfg.BuildAfter = append(cfg.BuildAfter, dep)
			}
		}
	}
}

// readModulePath returns the module path declared in a go.mod file, or "" on
// any read/parse error.
func readModulePath(goModPath string) string {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	return modfile.ModulePath(data)
}

func maxGoListWorkers() int {
	n := runtime.NumCPU()
	if n < 2 {
		return 2
	}
	if n > 8 {
		return 8
	}
	return n
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
