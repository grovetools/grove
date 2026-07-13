package orchestrator

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/models"
)

func TestParseTarget(t *testing.T) {
	tgt, err := ParseTarget("linux/amd64")
	if err != nil {
		t.Fatalf("ParseTarget(linux/amd64): %v", err)
	}
	if tgt.GOOS != "linux" || tgt.GOARCH != "amd64" {
		t.Errorf("parsed = %+v", tgt)
	}
	if tgt.String() != "linux/amd64" {
		t.Errorf("String = %q", tgt.String())
	}
	if tgt.Pair() != "linux_amd64" {
		t.Errorf("Pair = %q", tgt.Pair())
	}
	// OutDir is NEVER plain bin/ — native binaries there are live symlink
	// targets.
	if tgt.OutDir() != "bin/linux_amd64" {
		t.Errorf("OutDir = %q", tgt.OutDir())
	}
	if tgt.IsZero() {
		t.Error("parsed target must not be zero")
	}

	for _, bad := range []string{"", "linux", "linux/", "/amd64", "linux/amd64/v3", " / "} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q): expected error", bad)
		}
	}

	if !(Target{}).IsZero() {
		t.Error("zero Target must report IsZero")
	}
	native := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if !native.IsNative() {
		t.Error("host target must report IsNative")
	}
	if (Target{GOOS: "plan9", GOARCH: "mips"}).IsNative() {
		t.Error("plan9/mips should not be native on any test host")
	}
}

func TestTargetEnv(t *testing.T) {
	tgt := Target{GOOS: "linux", GOARCH: "amd64"}
	env := strings.Join(tgt.Env(), "\n")
	for _, want := range []string{
		"GROVE_TARGET_GOOS=linux",
		"GROVE_TARGET_GOARCH=amd64",
		"GROVE_BUILD_OUT=bin/linux_amd64",
		// -fno-sanitize=undefined: zig cc defaults UBSan on for C code,
		// which breaks go-sqlite3's cross link (__ubsan_handle_* undefined).
		"GROVE_TARGET_CC=zig cc -target x86_64-linux-gnu -fno-sanitize=undefined",
		"GROVE_TARGET_CXX=zig c++ -target x86_64-linux-gnu -fno-sanitize=undefined",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("Env missing %q:\n%s", want, env)
		}
	}
	// GOOS/GOARCH must never be set directly: Makefile `go run` codegen
	// steps stay native, each Makefile opts in via GROVE_TARGET_*.
	for _, e := range tgt.Env() {
		if strings.HasPrefix(e, "GOOS=") || strings.HasPrefix(e, "GOARCH=") {
			t.Errorf("Env must not set %s directly", e)
		}
	}

	// linux/arm64 maps to the aarch64 triple.
	if env := strings.Join(Target{GOOS: "linux", GOARCH: "arm64"}.Env(), "\n"); !strings.Contains(env, "aarch64-linux-gnu") {
		t.Errorf("arm64 Env missing aarch64 triple:\n%s", env)
	}

	// Unmapped targets get no CC/CXX (pure-Go repos need none).
	env = strings.Join(Target{GOOS: "windows", GOARCH: "amd64"}.Env(), "\n")
	if strings.Contains(env, "GROVE_TARGET_CC") || strings.Contains(env, "GROVE_TARGET_CXX") {
		t.Errorf("unmapped target must not emit CC/CXX:\n%s", env)
	}
}

// TestVerbKeyCacheKeying proves cross and native builds never share cache
// entries: under a cross target both lookup and storage use
// "<verb>@<goos>_<goarch>"; native (zero or host-equal) targets use the verb.
func TestVerbKeyCacheKeying(t *testing.T) {
	cross := Target{GOOS: "plan9", GOARCH: "mips"} // never the test host
	native := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}

	o := &Orchestrator{Options: OrchestratorOptions{Verb: "build", Target: cross}}
	if got := o.verbKey("build"); got != "build@plan9_mips" {
		t.Errorf("cross verbKey = %q", got)
	}
	if got := (&Orchestrator{Options: OrchestratorOptions{Verb: "build"}}).verbKey("build"); got != "build" {
		t.Errorf("zero-target verbKey = %q", got)
	}
	// A --target equal to the host is a native no-op: plain key, no env.
	oNative := &Orchestrator{Options: OrchestratorOptions{Verb: "build", Target: native}}
	if got := oNative.verbKey("build"); got != "build" {
		t.Errorf("native-target verbKey = %q", got)
	}
	if _, isCross := oNative.crossTarget(); isCross {
		t.Error("host-equal target must not report as cross")
	}

	// Cache lookup honors the keyed entry and ignores the native one.
	job := TaskJob{Name: "grove"}
	states := map[string]WorkspaceState{
		"grove": {
			CommitHash: "abc",
			TaskResults: map[string]*models.TaskResult{
				"build": {ExitCode: 0, CommitHash: "abc"},
			},
		},
	}
	if o.isCacheHit(job, states) {
		t.Error("cross build must not false-hit the native cache entry")
	}
	states["grove"].TaskResults["build@plan9_mips"] = &models.TaskResult{ExitCode: 0, CommitHash: "abc"}
	if !o.isCacheHit(job, states) {
		t.Error("cross build should hit its own keyed entry")
	}
	// And the native orchestrator keeps hitting the plain key.
	if !oNative.isCacheHit(job, states) {
		t.Error("native build should hit the plain verb entry")
	}
}

func TestTargetOutDirHasExecutable(t *testing.T) {
	tgt := Target{GOOS: "linux", GOARCH: "amd64"}
	repo := t.TempDir()

	// Missing dir → no.
	if tgt.OutDirHasExecutable(repo) {
		t.Error("missing out dir must not report an executable")
	}

	out := filepath.Join(repo, "bin", "linux_amd64")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	// Non-executable file → no.
	if err := os.WriteFile(filepath.Join(out, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tgt.OutDirHasExecutable(repo) {
		t.Error("non-executable file must not count")
	}
	// Subdirectory → no.
	if err := os.MkdirAll(filepath.Join(out, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if tgt.OutDirHasExecutable(repo) {
		t.Error("directories must not count")
	}
	// Regular executable → yes.
	if err := os.WriteFile(filepath.Join(out, "grove"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !tgt.OutDirHasExecutable(repo) {
		t.Error("executable file should count")
	}
}

// TestBuildJobEnvTargetInjection: the job env carries GROVE_TARGET_* only
// under a cross target — a native or zero target injects nothing.
func TestBuildJobEnvTargetInjection(t *testing.T) {
	hasTargetVar := func(env []string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, "GROVE_TARGET_") || strings.HasPrefix(e, "GROVE_BUILD_OUT=") {
				return true
			}
		}
		return false
	}
	if hasTargetVar((&Orchestrator{}).buildJobEnv(nil)) {
		t.Error("zero target must not inject GROVE_TARGET_*")
	}
	native := &Orchestrator{Options: OrchestratorOptions{Target: Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}}
	if hasTargetVar(native.buildJobEnv(nil)) {
		t.Error("native target must not inject GROVE_TARGET_*")
	}
	cross := &Orchestrator{Options: OrchestratorOptions{Target: Target{GOOS: "plan9", GOARCH: "mips"}}}
	if !hasTargetVar(cross.buildJobEnv(nil)) {
		t.Error("cross target must inject GROVE_TARGET_*")
	}
}

// TestCgoCflagsShim covers the GROVE_TARGET_CGO_CFLAGS plumbing: the shim
// header is written under <container>/.grove/cross-include with exactly the
// go-sqlite3 bridging include, the flags reference shim dir then module dir,
// and unresolvable containers/modules omit the value entirely.
func TestCgoCflagsShim(t *testing.T) {
	tgt := Target{GOOS: "linux", GOARCH: "amd64"}

	// No container → omitted, no error.
	if flags, err := tgt.CgoCflags(""); err != nil || flags != "" {
		t.Errorf("empty container = (%q, %v), want omitted", flags, err)
	}
	// A container with no Go module context → go list fails → omitted.
	if flags, err := tgt.CgoCflags(t.TempDir()); err != nil || flags != "" {
		t.Errorf("module-less container = (%q, %v), want omitted", flags, err)
	}

	// Resolved module dir → shim written + both -I flags, shim first.
	container := t.TempDir()
	modDir := "/fake/gopath/pkg/mod/github.com/mattn/go-sqlite3@v1.14.42"
	flags, err := tgt.cgoCflagsWithModDir(container, modDir)
	if err != nil {
		t.Fatalf("cgoCflagsWithModDir: %v", err)
	}
	shimDir := filepath.Join(container, ".grove", "cross-include")
	if want := "-I" + shimDir + " -I" + modDir; flags != want {
		t.Errorf("flags = %q, want %q", flags, want)
	}
	data, err := os.ReadFile(filepath.Join(shimDir, "sqlite3.h"))
	if err != nil {
		t.Fatalf("shim not written: %v", err)
	}
	if string(data) != "#include \"sqlite3-binding.h\"\n" {
		t.Errorf("shim content = %q", data)
	}

	// The env var must be the GROVE_TARGET_-scoped one; plain CGO_CFLAGS
	// would leak into native `go run` codegen prereqs. Only inspect the
	// entries the orchestrator ADDS (the inherited environ may carry its
	// own CGO_CFLAGS).
	o := &Orchestrator{Options: OrchestratorOptions{Target: tgt}}
	o.crossCgoOnce.Do(func() { o.crossCgoFlags = flags }) // pre-resolve: no go list in tests
	added := o.buildJobEnv(nil)[len(buildEnv(o.RunOpts)):]
	var found bool
	for _, e := range added {
		if strings.HasPrefix(e, "CGO_CFLAGS=") {
			t.Errorf("plain CGO_CFLAGS must never be injected: %s", e)
		}
		if e == "GROVE_TARGET_CGO_CFLAGS="+flags {
			found = true
		}
	}
	if !found {
		t.Error("GROVE_TARGET_CGO_CFLAGS not injected under a cross target")
	}
}
