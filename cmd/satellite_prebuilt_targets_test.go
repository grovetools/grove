package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

// TestPrebuiltRepoLimitsMirrorMakefiles keeps the exception table honest: for
// every constrained repo, the table must list exactly the targets that repo's
// Makefile declares in CROSS_TARGETS. Drift between the two is the whole bug —
// grove shipped agent to linux/amd64 for months after agent/Makefile started
// refusing it, and every gcp provision died on the resulting build failure
// AFTER the VM was billable. When a Makefile learns (or loses) an arch, this
// fails until the table follows.
func TestPrebuiltRepoLimitsMirrorMakefiles(t *testing.T) {
	root := ecosystemRootForTest(t)
	for repo, limit := range prebuiltRepoLimits {
		if len(limit.Targets) == 0 {
			continue
		}
		mk := filepath.Join(root, repo, "Makefile")
		data, err := os.ReadFile(mk)
		if err != nil {
			t.Skipf("%s not readable (%v) — not running inside a full ecosystem worktree", mk, err)
		}
		declared := parseCrossTargetsMakeVar(string(data))
		if len(declared) == 0 {
			t.Errorf("%s/Makefile declares no %s, but prebuiltRepoLimits[%q] constrains it to %v — one of the two is stale",
				repo, crossTargetsMakeVar, repo, limit.Targets)
			continue
		}
		if strings.Join(sortedCopy(declared), ",") != strings.Join(sortedCopy(limit.Targets), ",") {
			t.Errorf("%s/Makefile %s = %v, prebuiltRepoLimits[%q].Targets = %v — they must match",
				repo, crossTargetsMakeVar, declared, repo, limit.Targets)
		}
	}
}

// parseCrossTargetsMakeVar reads the "CROSS_TARGETS := a/b c/d" declaration out
// of a Makefile. nil when absent.
func parseCrossTargetsMakeVar(makefile string) []string {
	for _, line := range strings.Split(makefile, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), crossTargetsMakeVar)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		for _, op := range []string{":=", "?=", "="} {
			if v, ok := strings.CutPrefix(rest, op); ok {
				return strings.Fields(v)
			}
		}
	}
	return nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// ecosystemRootForTest resolves the ecosystem worktree root (the dir holding
// grove/, agent/, ...) from the test's working dir, or skips.
func ecosystemRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Skip(err)
	}
	// cmd/ -> grove/ -> <ecosystem root>
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "grove", "Makefile")); err != nil {
		t.Skipf("no ecosystem worktree above %s", wd)
	}
	return root
}

// withPrebuiltRepoLimit installs a limit for repo for the duration of the test,
// so the arch-exclusion paths stay covered no matter what the real table says.
func withPrebuiltRepoLimit(t *testing.T, repo string, limit prebuiltRepoLimit) {
	t.Helper()
	saved, had := prebuiltRepoLimits[repo]
	prebuiltRepoLimits[repo] = limit
	t.Cleanup(func() {
		if had {
			prebuiltRepoLimits[repo] = saved
		} else {
			delete(prebuiltRepoLimits, repo)
		}
	})
}

// TestPrebuiltRepoSupportsTarget covers the table lookup: constrained repos
// answer per target, unconstrained ones answer yes to everything.
func TestPrebuiltRepoSupportsTarget(t *testing.T) {
	arm := orch.Target{GOOS: "linux", GOARCH: "arm64"}
	amd := orch.Target{GOOS: "linux", GOARCH: "amd64"}

	if !prebuiltRepoSupportsTarget("grove", amd) || !prebuiltRepoSupportsTarget("grove", arm) {
		t.Errorf("grove is unconstrained — it must support every target")
	}
	// The two satellite arches in service: the Tart guest and the gcp VM.
	// agent/Makefile's CROSS_TARGETS declares both (mirrored by the table).
	if !prebuiltRepoSupportsTarget("agent", arm) || !prebuiltRepoSupportsTarget("agent", amd) {
		t.Errorf("agent must support both satellite arches; agent/Makefile declares them in %s", crossTargetsMakeVar)
	}
	// ...but the table is exhaustive, so an arch nobody builds for is refused.
	if prebuiltRepoSupportsTarget("agent", orch.Target{GOOS: "linux", GOARCH: "riscv64"}) {
		t.Errorf("agent must not claim a target its Makefile refuses")
	}
	if !prebuiltRepoOptional("agent") {
		t.Errorf("agent must be optional: grove-agent is not in satellite-bootstrap.sh's required binaries")
	}
	for _, r := range []string{"grove", "daemon", "flow", "nb", "treemux", "tuimux", "sync"} {
		if prebuiltRepoOptional(r) {
			t.Errorf("%s produces a binary satellite-bootstrap.sh requires — it must not be optional", r)
		}
	}
}

// TestSplitPrebuiltReposForTarget checks the partition keeps input order and
// routes each repo by its declared limit.
func TestSplitPrebuiltReposForTarget(t *testing.T) {
	withPrebuiltRepoLimit(t, "agent", prebuiltRepoLimit{Targets: []string{"linux/arm64"}, Optional: true})
	repos := []string{"grove", "agent", "daemon"}

	ship, unsupported := splitPrebuiltReposForTarget(repos, orch.Target{GOOS: "linux", GOARCH: "amd64"})
	if strings.Join(ship, ",") != "grove,daemon" {
		t.Errorf("linux/amd64 ship = %v, want [grove daemon]", ship)
	}
	if strings.Join(unsupported, ",") != "agent" {
		t.Errorf("linux/amd64 unsupported = %v, want [agent]", unsupported)
	}

	ship, unsupported = splitPrebuiltReposForTarget(repos, orch.Target{GOOS: "linux", GOARCH: "arm64"})
	if strings.Join(ship, ",") != "grove,agent,daemon" {
		t.Errorf("linux/arm64 ship = %v, want the whole input", ship)
	}
	if len(unsupported) != 0 {
		t.Errorf("linux/arm64 unsupported = %v, want none", unsupported)
	}
}

// TestPartitionOptionalRepos splits a drop list into what breaks a satellite
// and what merely degrades it.
func TestPartitionOptionalRepos(t *testing.T) {
	required, optional := partitionOptionalRepos([]string{"agent", "sync", "grove"})
	if strings.Join(required, ",") != "sync,grove" {
		t.Errorf("required = %v, want [sync grove]", required)
	}
	if strings.Join(optional, ",") != "agent" {
		t.Errorf("optional = %v, want [agent]", optional)
	}
}

// TestSatellitePrebuiltShipSetForSatelliteTargets is the ticket's acceptance
// check at the ship-set selection level: both satellite arches — gcp's
// linux/amd64 (the `up --prebuilt` default, which used to abort every
// provision) and the Tart guest's linux/arm64 — resolve to the FULL stack,
// with nothing excluded and no error.
func TestSatellitePrebuiltShipSetForSatelliteTargets(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		target := orch.Target{GOOS: "linux", GOARCH: arch}
		ship, excluded, err := satellitePrebuiltShipSet(target)
		if err != nil {
			t.Fatalf("%s ship set must resolve — a satellite arch in service: %v", target, err)
		}
		for _, want := range satellitePrebuiltStackRepos {
			if !containsString(ship, want) {
				t.Errorf("%s ship set is missing %q (excluded = %v)", target, want, excluded)
			}
		}
		if len(excluded) != 0 {
			t.Errorf("%s excluded = %v, want none", target, excluded)
		}
	}
}

// TestSatellitePrebuiltShipSetExcludesUnbuildableOptional covers the mechanism
// itself against a synthetic limit: an OPTIONAL repo that cannot target the VM
// arch leaves the ship set — reported, never silently — instead of failing the
// deploy on a build that was never going to succeed.
func TestSatellitePrebuiltShipSetExcludesUnbuildableOptional(t *testing.T) {
	withPrebuiltRepoLimit(t, "agent", prebuiltRepoLimit{Targets: []string{"linux/arm64"}, Optional: true})

	target := orch.Target{GOOS: "linux", GOARCH: "amd64"}
	ship, excluded, err := satellitePrebuiltShipSet(target)
	if err != nil {
		t.Fatalf("an optional repo that cannot build must not fail the ship set: %v", err)
	}
	for _, want := range []string{"grove", "daemon", "flow", "nb", "treemux", "tuimux", "sync"} {
		if !containsString(ship, want) {
			t.Errorf("ship set is missing required repo %q", want)
		}
	}
	if containsString(ship, "agent") {
		t.Errorf("ship set still contains agent, whose Makefile refuses %s", target)
	}
	if !containsString(excluded, "agent") {
		t.Errorf("agent must be REPORTED as excluded, not silently dropped; excluded = %v", excluded)
	}
	if msg := unsupportedPrebuiltReposMessage(excluded, target); !strings.Contains(msg, "agent") || !strings.Contains(msg, "linux/amd64") {
		t.Errorf("exclusion message must name the repo and the target, got %q", msg)
	}
}

// TestSatellitePrebuiltShipSetRequiredRepoUnbuildable proves the fail-fast
// stance survives: if a REQUIRED repo ever cannot target the VM's arch, the
// ship set errors (pre-terraform, before the VM is billable) instead of
// provisioning a satellite that cannot boot.
func TestSatellitePrebuiltShipSetRequiredRepoUnbuildable(t *testing.T) {
	withPrebuiltRepoLimit(t, "sync", prebuiltRepoLimit{Targets: []string{"linux/arm64"}})

	_, _, err := satellitePrebuiltShipSet(orch.Target{GOOS: "linux", GOARCH: "amd64"})
	if err == nil {
		t.Fatalf("expected an error when a required repo cannot target the VM arch")
	}
	if !strings.Contains(err.Error(), "sync") {
		t.Errorf("error must name the offending repo, got %v", err)
	}
}
