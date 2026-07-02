package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
//
// Ordering of consumers after providers is handled by the general import-graph
// derivation (DeriveWorkspaceBuildAfter, depclosure.go): a consumer that
// CGO-links the provider necessarily imports its Go packages, so the provider
// edge is a subset of the derived import edges. The one case imports cannot
// show — a member whose `go list` fails — falls back to depending on every
// native provider so we never under-schedule a CGO link.
func producesNativeArtifact(path string) bool {
	_, err := os.Stat(filepath.Join(path, "zig", "build.zig"))
	return err == nil
}

// listImportedModules runs `go list -deps` in dir and returns the set of module
// paths reachable from its packages. The second return is false on error.
//
// The *actual* import graph is used rather than go.mod: in a go.work workspace
// a member can import another member's packages — and thus link its native
// lib — without listing it in go.mod (e.g. grove transitively imports
// compositor through the embedded TUI command packages while grove/go.mod
// mentions neither). go.mod is therefore an unreliable signal here.
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
