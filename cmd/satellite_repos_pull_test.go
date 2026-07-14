package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestComputeSatellitePullDelta pins the pull delta's status selection: a VM
// head already in the local OBJECT STORE is up-to-date (even when it is not
// the local tip), an unknown VM head is a fetch, missing/errored VM repos are
// skipped — and the delta carries the VM's branch (detached mapped to "").
func TestComputeSatellitePullDelta(t *testing.T) {
	repos := []string{"same", "known", "new", "detachednew", "absent", "broken"}
	local := map[string]repoTip{
		"same":        {SHA: "c1", Branch: "main"},
		"known":       {SHA: "c2", Branch: "main"},
		"new":         {SHA: "c3", Branch: "main"},
		"detachednew": {SHA: "c4", Branch: "main"},
		"absent":      {SHA: "c5", Branch: "main"},
		"broken":      {SHA: "c6", Branch: "main"},
	}
	remote := map[string]satelliteRemoteHead{
		"same":        {SHA: "c1", Branch: "main"},   // VM == local tip
		"known":       {SHA: "old2", Branch: "main"}, // != tip but object present (pulled earlier / ancestor)
		"new":         {SHA: "vm3", Branch: "agent-work"},
		"detachednew": {SHA: "vm4", Branch: ""}, // detached VM HEAD
		"absent":      {SHA: "MISSING"},
		"broken":      {SHA: "ERROR"},
	}
	hasObject := func(repo, sha string) bool { return repo == "same" || repo == "known" }

	deltas := computeSatellitePullDelta(repos, local, remote, hasObject)
	byRepo := map[string]repoDelta{}
	for _, d := range deltas {
		byRepo[d.Repo] = d
	}
	want := map[string]string{
		"same":        deltaStatusUpToDate,
		"known":       deltaStatusUpToDate,
		"new":         deltaStatusFetch,
		"detachednew": deltaStatusFetch,
		"absent":      deltaStatusMissingRemote,
		"broken":      deltaStatusRemoteError,
	}
	for repo, status := range want {
		if byRepo[repo].Status != status {
			t.Errorf("pull delta[%s].Status = %q, want %q", repo, byRepo[repo].Status, status)
		}
	}
	// The fetch set is exactly the unknown-object repos, in input order.
	if names := strings.Join(deltaRepoNames(deltasWithStatus(deltas, deltaStatusFetch)), ","); names != "new,detachednew" {
		t.Fatalf("pull fetch set = %q, want new,detachednew", names)
	}
	// Branch carries the VM's branch (the ref-mapping input).
	if byRepo["new"].Branch != "agent-work" {
		t.Errorf("new.Branch = %q, want agent-work", byRepo["new"].Branch)
	}
	if byRepo["detachednew"].Branch != "" {
		t.Errorf("detachednew.Branch = %q, want \"\" (detached)", byRepo["detachednew"].Branch)
	}
	if byRepo["new"].RemoteSHA != "vm3" || byRepo["new"].LocalSHA != "c3" {
		t.Errorf("new shas = %q/%q, want c3/vm3", byRepo["new"].LocalSHA, byRepo["new"].RemoteSHA)
	}
}

// TestRemoteHeadsBranchesScriptAndParse checks the extended probe script's
// content (per-repo sha + abbrev-ref with sentinels) and the parser's decode,
// including the detached→"" and MISSING placeholder mappings.
func TestRemoteHeadsBranchesScriptAndParse(t *testing.T) {
	script := buildRemoteHeadsBranchesScript("~/code/grovetools", []string{"grove", "nb"})
	for _, want := range []string{
		`CODE="$HOME/code/grovetools"`,
		`rev-parse HEAD 2>/dev/null || echo ERROR`,
		`rev-parse --abbrev-ref HEAD 2>/dev/null || echo ERROR`,
		`echo "grove MISSING -"`,
		`echo "nb MISSING -"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("probe script missing %q:\n%s", want, script)
		}
	}
	assertBashParses(t, script)

	out := `grove aaa111 agent-work
detachedrepo bbb222 HEAD
nested ccc333 feature/x
gone MISSING -
odd ERROR ERROR
noise line
`
	heads := parseRemoteHeadsBranches(out)
	cases := map[string]satelliteRemoteHead{
		"grove":        {SHA: "aaa111", Branch: "agent-work"},
		"detachedrepo": {SHA: "bbb222", Branch: ""},
		"nested":       {SHA: "ccc333", Branch: "feature/x"},
		"gone":         {SHA: "MISSING", Branch: ""},
		"odd":          {SHA: "ERROR", Branch: ""},
	}
	for repo, want := range cases {
		if got := heads[repo]; got != want {
			t.Errorf("parsed[%s] = %+v, want %+v", repo, got, want)
		}
	}
	if _, ok := heads["noise"]; ok {
		t.Errorf("malformed line must be ignored: %+v", heads)
	}
}

// TestSatellitePullRef pins the VM-branch → local-ref mapping (branch,
// nested branch, detached) and the off-the-wire validation.
func TestSatellitePullRef(t *testing.T) {
	good := map[string]string{
		"agent-work": "refs/satellite/sat1/agent-work",
		"feature/x":  "refs/satellite/sat1/feature/x",
		"":           "refs/satellite/sat1/detached",
		"v1.2":       "refs/satellite/sat1/v1.2",
	}
	for branch, want := range good {
		got, err := satellitePullRef("sat1", branch)
		if err != nil || got != want {
			t.Errorf("satellitePullRef(sat1, %q) = %q, %v; want %q", branch, got, err, want)
		}
	}
	for _, bad := range []string{"a..b", ".hidden", "x/.y", "x//y", "a/", "/a", "x.lock", "a b", "x;y", "$(boom)"} {
		if ref, err := satellitePullRef("sat1", bad); err == nil {
			t.Errorf("satellitePullRef(sat1, %q) = %q, want error", bad, ref)
		}
	}
}

// TestBuildSatelliteReposPullBundleScript checks the VM bundle script's
// content contract: fresh stage recreation, the ancestor-checked base loop
// with full-bundle fallback, per-repo failure isolation, nonzero exit with
// the stage kept — and that non-hex "bases" never reach the generated shell.
func TestBuildSatelliteReposPullBundleScript(t *testing.T) {
	reqs := []satellitePullBundleReq{
		{Repo: "grove", Bases: []string{"aaa111", "bbb222"}},
		{Repo: "nb", Bases: nil},
		{Repo: "evil", Bases: []string{"$(rm -rf /)", "ccc333"}},
	}
	script := buildSatelliteReposPullBundleScript("~/code/grovetools", "/tmp/pull-stage", reqs)
	for _, want := range []string{
		"set -uo pipefail", // NOT -e: per-repo isolation
		`CODE="$HOME/code/grovetools"`,
		`STAGE="/tmp/pull-stage"`,
		`rm -rf "$STAGE" && mkdir -p "$STAGE" || exit 1`, // fresh stage every run
		`git -C "$dir" merge-base --is-ancestor "$base" HEAD`,
		`spec="$base..HEAD"`,
		`git -C "$dir" bundle create "$STAGE/$repo.bundle" "$spec"`,
		"bundle_repo grove aaa111 bbb222",
		"bundle_repo nb\n",
		"bundle_repo evil ccc333", // the injection candidate is dropped
		`mark_failed "$repo"`,
		"=== repos pull bundle summary ===",
		"exit 1",
		"repos pull bundles ready",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("pull bundle script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "rm -rf /)") {
		t.Errorf("non-hex base leaked into the script:\n%s", script)
	}
	// Read-only towards the checkouts: no checkout/reset/fetch of VM repos.
	for _, forbid := range []string{"checkout", "reset --hard", "git -C \"$dir\" fetch"} {
		if strings.Contains(script, forbid) {
			t.Errorf("pull bundle script must not contain %q:\n%s", forbid, script)
		}
	}
	assertBashParses(t, script)
}

// --- engine execution against a local fake transport ---

// localPullTransport satisfies satelliteReposTransport by executing the
// generated scripts with local bash and copying files instead of scp'ing —
// the pull engine's full loop without a VM. failScpFor simulates a per-repo
// transfer failure.
type localPullTransport struct {
	t          *testing.T
	scripts    []string
	commands   []string
	failScpFor map[string]bool
}

func (l *localPullTransport) dest() string { return "local-fake" }

func (l *localPullTransport) outputScript(script string) (string, error) {
	l.t.Helper()
	assertBashParses(l.t, script)
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	return string(out), err
}

func (l *localPullTransport) runScript(script string) error {
	l.t.Helper()
	l.scripts = append(l.scripts, script)
	assertBashParses(l.t, script)
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	} else {
		l.t.Logf("runScript output:\n%s", out)
	}
	return nil
}

func (l *localPullTransport) runCommand(command string) error {
	l.commands = append(l.commands, command)
	return exec.Command("bash", "-c", command).Run()
}

func (l *localPullTransport) scpFrom(remotePaths []string, localDir string) error {
	for _, p := range remotePaths {
		if l.failScpFor[strings.TrimSuffix(filepath.Base(p), ".bundle")] {
			return fmt.Errorf("simulated transfer failure for %s", p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(localDir, filepath.Base(p)), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// TestSatelliteReposPullEngineExecution drives the whole pull engine against
// real throwaway git repos with a local fake transport: dry-run transfers
// nothing, a real pull fetches new VM commits into refs/satellite/<name>/…
// (branch and detached ref mapping) without touching local branches/worktree,
// a re-pull is an idempotent no-op, the next pull after more VM commits rides
// an incremental bundle seeded from the previous satellite ref, per-repo
// transfer failures are isolated (stage kept), and the push interlock flips
// from held to shippable once the pull landed.
func TestSatelliteReposPullEngineExecution(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	root := t.TempDir()
	laptop := filepath.Join(root, "laptop")
	vmCode := filepath.Join(root, "vm-code")
	stage := filepath.Join(root, "pull-stage")

	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitOutput(dir, args...)
		if err != nil {
			t.Fatalf("git %v in %s: %v", args, dir, err)
		}
		return out
	}
	commit := func(dir, file, content, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		git(dir, "add", ".")
		git(dir, "commit", "-m", msg)
		return git(dir, "rev-parse", "HEAD")
	}
	mkRepo := func(parent, name string) string {
		t.Helper()
		dir := filepath.Join(parent, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(dir, "init", "-b", "main")
		git(dir, "config", "user.email", "t@t")
		git(dir, "config", "user.name", "t")
		return dir
	}

	// Laptop repos: agentrepo (VM will commit), quietrepo (VM stays equal),
	// detrepo (VM goes detached), gonerepo (never mirrored to the VM).
	agentLaptop := mkRepo(laptop, "agentrepo")
	agentBase := commit(agentLaptop, "a.txt", "base", "base")
	quietLaptop := mkRepo(laptop, "quietrepo")
	quietSHA := commit(quietLaptop, "q.txt", "base", "base")
	detLaptop := mkRepo(laptop, "detrepo")
	commit(detLaptop, "d.txt", "base", "base")
	mkRepo(laptop, "gonerepo")
	commit(filepath.Join(laptop, "gonerepo"), "g.txt", "base", "base")

	// "VM" checkouts: clones of the laptop repos (as push would have left
	// them), under a flat code dir.
	if err := os.MkdirAll(vmCode, 0o755); err != nil {
		t.Fatal(err)
	}
	clone := func(name string) string {
		t.Helper()
		dir := filepath.Join(vmCode, name)
		git(vmCode, "clone", "-q", filepath.Join(laptop, name), dir)
		git(dir, "config", "user.email", "vm@vm")
		git(dir, "config", "user.name", "vm")
		return dir
	}
	agentVM := clone("agentrepo")
	clone("quietrepo")
	detVM := clone("detrepo")

	// Agent activity on the VM: two commits on a work branch in agentrepo,
	// one detached commit in detrepo.
	git(agentVM, "checkout", "-q", "-b", "agent-work")
	commit(agentVM, "w1.txt", "one", "agent c1")
	agentVMSHA := commit(agentVM, "w2.txt", "two", "agent c2")
	git(detVM, "checkout", "-q", "--detach", "HEAD")
	detVMSHA := commit(detVM, "d2.txt", "vm work", "detached vm commit")

	repos := []string{"agentrepo", "quietrepo", "detrepo", "gonerepo"}
	transport := &localPullTransport{t: t}

	// --- dry-run: no stage dir, no refs, no scripts beyond the probe ---
	if err := pullSatelliteRepos(transport, "sat1", laptop, vmCode, stage, repos, false, true); err != nil {
		t.Fatalf("dry-run pull: %v", err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create the stage dir: %v", err)
	}
	if len(transport.scripts) != 0 {
		t.Fatalf("dry-run must not run the bundle script:\n%s", transport.scripts)
	}
	if _, ok := localRefSHA(agentLaptop, "refs/satellite/sat1/agent-work"); ok {
		t.Fatal("dry-run must not write refs")
	}

	// --- real pull ---
	if err := pullSatelliteRepos(transport, "sat1", laptop, vmCode, stage, repos, false, false); err != nil {
		t.Fatalf("pull: %v", err)
	}
	// Branch-mapped ref carries the VM tip.
	if got, ok := localRefSHA(agentLaptop, "refs/satellite/sat1/agent-work"); !ok || got != agentVMSHA {
		t.Errorf("refs/satellite/sat1/agent-work = %q, want %q", got, agentVMSHA)
	}
	// Detached VM HEAD lands under .../detached.
	if got, ok := localRefSHA(detLaptop, "refs/satellite/sat1/detached"); !ok || got != detVMSHA {
		t.Errorf("refs/satellite/sat1/detached = %q, want %q", got, detVMSHA)
	}
	// Local branches, index, worktree untouched.
	if got := git(agentLaptop, "rev-parse", "main"); got != agentBase {
		t.Errorf("local main moved: %q, want %q", got, agentBase)
	}
	if got := git(agentLaptop, "rev-parse", "HEAD"); got != agentBase {
		t.Errorf("local HEAD moved: %q, want %q", got, agentBase)
	}
	if got := git(agentLaptop, "status", "--porcelain"); got != "" {
		t.Errorf("local worktree dirtied:\n%s", got)
	}
	// The up-to-date repo got no satellite ref.
	if _, ok := localRefSHA(quietLaptop, "refs/satellite/sat1/main"); ok {
		t.Error("quietrepo must not get a satellite ref (nothing new)")
	}
	_ = quietSHA
	// Stage cleaned up on success.
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage dir not cleaned after success: %v", err)
	}
	if len(transport.commands) == 0 || !strings.Contains(transport.commands[len(transport.commands)-1], "rm -rf "+stage) {
		t.Errorf("missing stage cleanup command: %v", transport.commands)
	}
	// The push interlock flips: agentrepo's VM head is now local, so the
	// mirror delta shows fetched-divergence (shippable), not held.
	mirrorDeltas := computeSatelliteMirrorDelta(
		[]string{"agentrepo"},
		map[string]repoTip{"agentrepo": {SHA: agentBase, Branch: "main"}},
		map[string]string{"agentrepo": agentVMSHA},
		func(_, sha string) bool { return localRepoIsAncestor(agentLaptop, sha) },
		func(_, sha string) bool { return localRepoHasObject(agentLaptop, sha) },
		false)
	if mirrorDeltas[0].Status != deltaStatusDiverged {
		t.Errorf("post-pull mirror status = %q, want %q", mirrorDeltas[0].Status, deltaStatusDiverged)
	}

	// --- idempotent re-pull: everything up-to-date, no bundle script runs ---
	before := len(transport.scripts)
	if err := pullSatelliteRepos(transport, "sat1", laptop, vmCode, stage, repos, false, false); err != nil {
		t.Fatalf("re-pull: %v", err)
	}
	if len(transport.scripts) != before {
		t.Fatalf("idempotent re-pull must not run the bundle script again")
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("re-pull created a stage dir: %v", err)
	}

	// --- incremental follow-up: more VM commits; the bundle request seeds
	// its bases from the previous satellite ref (and the local tip) ---
	agentVMSHA2 := commit(agentVM, "w3.txt", "three", "agent c3")
	if err := pullSatelliteRepos(transport, "sat1", laptop, vmCode, stage, repos, false, false); err != nil {
		t.Fatalf("incremental pull: %v", err)
	}
	if got, ok := localRefSHA(agentLaptop, "refs/satellite/sat1/agent-work"); !ok || got != agentVMSHA2 {
		t.Errorf("incremental ref = %q, want %q", got, agentVMSHA2)
	}
	lastScript := transport.scripts[len(transport.scripts)-1]
	if !strings.Contains(lastScript, "bundle_repo agentrepo "+agentVMSHA+" "+agentBase) {
		t.Errorf("incremental bundle request must lead with the previous satellite ref %s:\n%s", agentVMSHA, lastScript)
	}

	// --- per-repo failure isolation: one repo's transfer fails, the other
	// still lands; nonzero overall; stage kept ---
	commit(agentVM, "w4.txt", "four", "agent c4")
	git(detVM, "checkout", "-q", "--detach", "HEAD")
	detVMSHA2 := commit(detVM, "d3.txt", "more vm work", "detached vm commit 2")
	transport.failScpFor = map[string]bool{"agentrepo": true}
	err := pullSatelliteRepos(transport, "sat1", laptop, vmCode, stage, repos, false, false)
	if err == nil || !strings.Contains(err.Error(), "agentrepo") {
		t.Fatalf("failed transfer must surface the repo, got %v", err)
	}
	if got, ok := localRefSHA(detLaptop, "refs/satellite/sat1/detached"); !ok || got != detVMSHA2 {
		t.Errorf("detrepo must still pull despite agentrepo failing: %q, want %q", got, detVMSHA2)
	}
	if got, _ := localRefSHA(agentLaptop, "refs/satellite/sat1/agent-work"); got != agentVMSHA2 {
		t.Errorf("failed repo's ref must not move: %q, want %q", got, agentVMSHA2)
	}
	if _, serr := os.Stat(stage); serr != nil {
		t.Errorf("stage dir must be kept on failure: %v", serr)
	}
}

// TestSatelliteReposPullStrictness pins the strict/non-strict handling of
// resolved names that are not local git checkouts (config-derived sets skip
// with a notice; an explicit --repos list errors).
func TestSatelliteReposPullStrictness(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	transport := &localPullTransport{t: t}
	// Strict: unknown name is an error before any transport use.
	err := pullSatelliteRepos(transport, "sat1", root, root, filepath.Join(root, "stage"), []string{"nope"}, true, false)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("strict pull with a non-repo must error, got %v", err)
	}
	// Non-strict: skipped with a notice, nothing to do, no error.
	if err := pullSatelliteRepos(transport, "sat1", root, root, filepath.Join(root, "stage"), []string{"nope"}, false, false); err != nil {
		t.Fatalf("non-strict pull must skip non-repos, got %v", err)
	}
	if len(transport.scripts) != 0 {
		t.Fatal("no transport scripts expected for an empty pullable set")
	}
}
