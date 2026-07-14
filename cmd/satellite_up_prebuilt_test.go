package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/grove/cmd/satelliteassets"
)

// gitInitStackRepo makes a one-commit git repo at dir on the poc branch, the
// minimum localRepoTip/localRepoDirty need.
func gitInitStackRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := gitOutput(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "grove-satellite-poc")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c1")
	return run("rev-parse", "HEAD")
}

// TestSatellitePrebuiltStackReposCoverRequiredBinaries pins the fixed ship set:
// it must map to every binary the source bootstrap guarantees (grove, groved,
// flow, nb, treemux, tuimux from step 4; grove-syncd from step 6) — groved via
// the daemon repo, grove-syncd via the sync repo.
func TestSatellitePrebuiltStackReposCoverRequiredBinaries(t *testing.T) {
	for _, want := range []string{"grove", "daemon", "flow", "nb", "treemux", "tuimux", "sync"} {
		if !containsString(satellitePrebuiltStackRepos, want) {
			t.Errorf("satellitePrebuiltStackRepos missing %q (required for the grove stack)", want)
		}
	}
	if containsString(satellitePrebuiltStackRepos, "compositor") {
		t.Errorf("compositor is a library with no binaries — it must NOT be in the prebuilt ship set")
	}
}

// TestValidateSatellitePrebuiltStack covers the pre-terraform fail-fast gate:
// every stack repo must be a git checkout and the grove-syncd unit must be
// present. Fake .git dirs + a unit file suffice (validate only stats them).
func TestValidateSatellitePrebuiltStack(t *testing.T) {
	root := t.TempDir()

	// Nothing present → error naming the first missing repo.
	if err := validateSatellitePrebuiltStack(root); err == nil {
		t.Fatalf("expected error for an empty ecosystem dir")
	}

	// Create a .git marker for every stack repo, plus compositor (built first
	// for its zig libs, validated too).
	for _, r := range append([]string{"compositor"}, satellitePrebuiltStackRepos...) {
		if err := os.MkdirAll(filepath.Join(root, r, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Repos present but the unit is still missing → error mentions grove-syncd.
	err := validateSatellitePrebuiltStack(root)
	if err == nil || !strings.Contains(err.Error(), "grove-syncd") {
		t.Fatalf("expected grove-syncd unit error, got %v", err)
	}

	// Ship the unit → validation passes.
	unit := filepath.Join(root, filepath.FromSlash(satelliteSyncdUnitRel))
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateSatellitePrebuiltStack(root); err != nil {
		t.Fatalf("validate with all repos + unit: %v", err)
	}

	// Removing one repo's .git fails again.
	if err := os.RemoveAll(filepath.Join(root, "sync", ".git")); err != nil {
		t.Fatal(err)
	}
	if err := validateSatellitePrebuiltStack(root); err == nil {
		t.Fatalf("expected error after removing sync/.git")
	}
}

// TestSatellitePrebuiltStackDeltas builds the fresh-VM ship set from real local
// git repos: every stack repo forced at its tip, with dirtiness tracked.
func TestSatellitePrebuiltStackDeltas(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	shas := map[string]string{}
	for _, r := range satellitePrebuiltStackRepos {
		shas[r] = gitInitStackRepo(t, filepath.Join(root, r))
	}
	// Make one repo dirty.
	if err := os.WriteFile(filepath.Join(root, "sync", "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	updates, dirty, err := satellitePrebuiltStackDeltas(root)
	if err != nil {
		t.Fatalf("satellitePrebuiltStackDeltas: %v", err)
	}
	if len(updates) != len(satellitePrebuiltStackRepos) {
		t.Fatalf("got %d deltas, want %d", len(updates), len(satellitePrebuiltStackRepos))
	}
	seen := map[string]repoDelta{}
	for _, d := range updates {
		seen[d.Repo] = d
		if d.Status != deltaStatusForced {
			t.Errorf("%s status = %q, want forced", d.Repo, d.Status)
		}
		if d.Branch != "grove-satellite-poc" {
			t.Errorf("%s branch = %q, want grove-satellite-poc", d.Repo, d.Branch)
		}
		if d.LocalSHA != shas[d.Repo] {
			t.Errorf("%s LocalSHA = %q, want %q", d.Repo, d.LocalSHA, shas[d.Repo])
		}
	}
	if !dirty["sync"] {
		t.Errorf("sync should be dirty")
	}
	if dirty["grove"] {
		t.Errorf("grove should be clean")
	}
}

// TestBootstrapScriptPrebuiltRequiresSyncdUnit exercises the embedded script's
// new flag guard: --prebuilt without --syncd-unit is a usage error (exit 2),
// hit before any SSH so the check is fast + offline.
func TestBootstrapScriptPrebuiltRequiresSyncdUnit(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	data, err := satelliteassets.BootstrapScript()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "satellite-bootstrap.sh")
	if err := os.WriteFile(script, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, script, "vm-dest", "--prebuilt")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for --prebuilt without --syncd-unit; output:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("expected exit code 2, got %v; output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "requires --syncd-unit") {
		t.Fatalf("expected 'requires --syncd-unit' message, got:\n%s", out)
	}
}

// TestBootstrapScriptEmbedsPrebuiltBranches guards the embedded script's
// prebuilt mode: it must parse and carry the skip/install markers so a future
// edit can't silently drop them.
func TestBootstrapScriptEmbedsPrebuiltBranches(t *testing.T) {
	data, err := satelliteassets.BootstrapScript()
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	assertBashParses(t, script)
	for _, marker := range []string{
		"--prebuilt)",
		"--syncd-unit)",
		"requires --syncd-unit",
		"preparing empty ecosystem root (repos arrive via 'grove satellite repos push')",
		"prebuilt binaries OK",
		"GROVE_SYNCD_UNIT",
	} {
		if !strings.Contains(script, marker) {
			t.Errorf("embedded bootstrap script missing prebuilt marker %q", marker)
		}
	}
}
