package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// p2TildeCodeRoot creates a git repo with one origin remote at $HOME/<rel> and
// returns the DECLARED spelling ("~/<rel>") that roots.toml would hold.
//
// P1 copies legacy paths verbatim into roots.toml, so tilde spellings there are
// not a hypothetical: the Machine A cutover file carried four of them.
func p2TildeCodeRoot(t *testing.T, home, rel, remote string) string {
	t.Helper()
	dir := filepath.Join(home, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", remote)
	return "~/" + rel
}

// TestP2MigrationDerivesSubjectFromATildeDeclaredCodeRoot is the Instance 1
// regression.
//
// rootForName fed the DECLARED path straight to canonicalPath, whose
// filepath.Abs does not reject a tilde — it prefixes the process cwd and
// returns "<cwd>/~/code/alpha". EvalSymlinks then fails on the nonexistent
// result and the Clean fallback hands it downstream anyway. No error is raised
// at any point.
//
// SubjectForCodeRoot is then asked about a directory that cannot exist:
// FindEcosystemManifest stats no grove.toml, `git rev-parse --show-toplevel`
// errors so FromRemotes sees nothing, and the canonical-remote rule is skipped.
// The migration falls through to MintLocal and stamps a throwaway
// "local:<ULID>" — permanently, since a stamp is authoritative forever.
//
// Every existing fixture in this package writes an absolute t.TempDir() path,
// so none of them can reach this branch. That is precisely why the bug reached
// a real cutover.
func TestP2MigrationDerivesSubjectFromATildeDeclaredCodeRoot(t *testing.T) {
	dir, nb := p2Sandbox(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	declared := p2TildeCodeRoot(t, home, filepath.Join("code", "alpha"), "git@github.com:grovetools/core.git")
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "[roots.alpha]\npath = "+quoteTOML(declared)+"\nnotebook = \"nb\"\n")
	config.ResetLoadCache()
	p2StubCommands(t)

	got := p2PlanSubject(t, nb)
	if want := "github.com/grovetools/core"; got != want {
		t.Fatalf("subject = %q, want %q — a declared code-root spelling was resolved against the process cwd, "+
			"so the canonical remote was never found and a throwaway local subject was minted", got, want)
	}
}

// TestCanonicalPathResolvesDeclaredSpellings fixes the defect at its own level
// rather than at one call site.
//
// canonicalPath has six callers and rootForName is only one of them. The one
// that matters most is writeP2MachineConfig, which keys machine.Subjects by
// this result: a ghost path there means the machine's identity table records a
// directory that does not exist, on an operation whose output is permanent.
// Expanding inside canonicalPath closes all six at once.
func TestCanonicalPathResolvesDeclaredSpellings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "code", "alpha")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	// The physical answer, so a symlinked TMPDIR does not make this brittle.
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	if got := canonicalPath("~/code/alpha"); got != want {
		t.Errorf("canonicalPath(%q) = %q, want %q", "~/code/alpha", got, want)
	}
	// Idempotent: the callers already passing absolute paths are unaffected.
	if got := canonicalPath(real); got != want {
		t.Errorf("canonicalPath(%q) = %q, want %q", real, got, want)
	}
	if got := canonicalPath("~/code/alpha"); strings.Contains(got, "~") {
		t.Errorf("canonicalPath left a declared spelling in place: %q", got)
	}
}

// TestCanonicalPathNeverPrefixesTheProcessCwd states the failure directly: the
// silent part of this bug class is that a wrong answer LOOKS like a path.
func TestCanonicalPathNeverPrefixesTheProcessCwd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalPath("~/notebooks/does-not-exist")
	if strings.HasPrefix(got, cwd) {
		t.Fatalf("canonicalPath resolved a declared path against the process cwd: %q", got)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("canonicalPath returned a non-absolute path: %q", got)
	}
}
