package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Target is a cross-compilation target for `grove build --target`.
//
// The orchestrator never sets GOOS/GOARCH directly: Makefiles run `go run`
// codegen steps that must stay native. Instead it injects GROVE_TARGET_*
// variables; each repo's Makefile opts in and applies them only to its final
// `go build`, and owns its own cgo story (CGO_ENABLED + $(GROVE_TARGET_CC)).
// The orchestrator carries no per-repo cgo knowledge.
type Target struct {
	GOOS   string
	GOARCH string
}

// ParseTarget parses a "<goos>/<goarch>" pair (e.g. "linux/amd64").
func ParseTarget(s string) (Target, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return Target{}, fmt.Errorf("invalid --target %q: expected <goos>/<goarch>, e.g. linux/amd64", s)
	}
	return Target{GOOS: strings.TrimSpace(parts[0]), GOARCH: strings.TrimSpace(parts[1])}, nil
}

// IsZero reports whether no target was requested at all.
func (t Target) IsZero() bool { return t.GOOS == "" && t.GOARCH == "" }

// IsNative reports whether the target matches the host toolchain — a native
// no-op: no env injection, normal cache keys.
func (t Target) IsNative() bool { return t.GOOS == runtime.GOOS && t.GOARCH == runtime.GOARCH }

// String renders the canonical "<goos>/<goarch>" form.
func (t Target) String() string { return t.GOOS + "/" + t.GOARCH }

// Pair renders the "<goos>_<goarch>" form used in paths and cache keys.
func (t Target) Pair() string { return t.GOOS + "_" + t.GOARCH }

// OutDir is the repo-relative directory cross-built binaries land in.
// NEVER plain bin/: the host's native binaries there are live symlink
// targets — a foreign-arch binary clobbering them bricks the local daemon.
func (t Target) OutDir() string { return "bin/" + t.Pair() }

// zigTriple maps the target to a `zig cc` triple for cgo cross-compiles.
// Unmapped targets return "" — fine for pure-Go repos, which need no CC.
func (t Target) zigTriple() string {
	switch t.String() {
	case "linux/amd64":
		return "x86_64-linux-gnu"
	case "linux/arm64":
		return "aarch64-linux-gnu"
	}
	return ""
}

// Env returns the GROVE_TARGET_* variables a cross-targeted build injects.
// Makefiles opt in: apply GROVE_TARGET_GOOS/GOARCH only to the final
// `go build`, emit binaries into GROVE_BUILD_OUT, and wire GROVE_TARGET_CC/
// CXX through their own CGO_ENABLED handling when they need cgo.
func (t Target) Env() []string {
	env := []string{
		"GROVE_TARGET_GOOS=" + t.GOOS,
		"GROVE_TARGET_GOARCH=" + t.GOARCH,
		"GROVE_BUILD_OUT=" + t.OutDir(),
	}
	if triple := t.zigTriple(); triple != "" {
		// -fno-sanitize=undefined: zig cc defaults UBSan ON for C code, and
		// go-sqlite3's sqlite3-binding.c then fails the cross link with
		// dozens of undefined __ubsan_handle_* symbols.
		env = append(env,
			"GROVE_TARGET_CC=zig cc -target "+triple+" -fno-sanitize=undefined",
			"GROVE_TARGET_CXX=zig c++ -target "+triple+" -fno-sanitize=undefined",
		)
	}
	return env
}

// sqliteModulePath is the cgo sqlite module whose cross-compilation needs the
// include shim below.
const sqliteModulePath = "github.com/mattn/go-sqlite3"

// CgoCflags resolves the GROVE_TARGET_CGO_CFLAGS value for a cross build:
// "-I<shim-dir> -I<go-sqlite3-module-dir>". sqlite-vec's header does
// #include "sqlite3.h", which the macOS SDK provides natively but zig's linux
// sysroot doesn't — and go-sqlite3 >=1.14.40 ships only sqlite3-binding.h, so
// a one-line shim header bridges the two. The shim lives under
// <containerDir>/.grove/cross-include (sibling of the depgraph.json cache).
//
// This var must NOT be exported as plain CGO_CFLAGS: that leaks into native
// `go run` codegen prereqs and breaks them. Makefiles consume
// GROVE_TARGET_CGO_CFLAGS inside their cross-scoped env only.
//
// Returns ("", nil) when the module dir cannot be resolved (no container,
// go-sqlite3 not in the module graph, go list failure) — pure-Go stacks need
// no shim, so the var is simply omitted. Errors are only for shim-write
// failures; callers treat them as non-fatal (warn + omit).
func (t Target) CgoCflags(containerDir string) (string, error) {
	if containerDir == "" {
		return "", nil
	}
	modDir := sqliteModuleDir(containerDir)
	if modDir == "" {
		return "", nil
	}
	return t.cgoCflagsWithModDir(containerDir, modDir)
}

// cgoCflagsWithModDir is the I/O core of CgoCflags with the go list step
// already resolved (split out for tests).
func (t Target) cgoCflagsWithModDir(containerDir, modDir string) (string, error) {
	shimDir := filepath.Join(containerDir, ".grove", "cross-include")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(shimDir, "sqlite3.h"), []byte("#include \"sqlite3-binding.h\"\n"), 0o644); err != nil {
		return "", err
	}
	return "-I" + shimDir + " -I" + modDir, nil
}

// sqliteModuleDir resolves go-sqlite3's module cache dir via
// `go list -m -f '{{.Dir}}' github.com/mattn/go-sqlite3` in dir (go.work
// resolves it there). "" when the command fails or the module is absent.
func sqliteModuleDir(dir string) string {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", sqliteModulePath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// OutDirHasExecutable reports whether repoPath/<OutDir()> contains at least
// one regular executable file — the compliance probe for Makefiles that
// ignore GROVE_BUILD_OUT.
func (t Target) OutDirHasExecutable(repoPath string) bool {
	entries, err := os.ReadDir(filepath.Join(repoPath, t.OutDir()))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().Perm()&0o111 != 0 {
			return true
		}
	}
	return false
}
