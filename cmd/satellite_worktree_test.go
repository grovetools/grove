package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildSatelliteWorktreeHeadsScriptAndParse checks the worktree probe's
// content (base-vs-worktree existence branches with sentinels) and the
// parser's decode, including the "-"/"ERROR" base-sha → "" mapping.
func TestBuildSatelliteWorktreeHeadsScriptAndParse(t *testing.T) {
	script := buildSatelliteWorktreeHeadsScript("~/code/grovetools", "plan-x", []string{"grove", "nb"})
	for _, want := range []string{
		`CODE="$HOME/code/grovetools"`,
		`WT="$CODE/.grove-worktrees/plan-x"`,
		`echo "grove NOBASE -"`,
		`echo "nb NOBASE -"`,
		`grove MISSING $(git -C "$CODE/grove" rev-parse HEAD`,
		`git -C "$WT/grove" rev-parse HEAD`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("worktree probe script missing %q:\n%s", want, script)
		}
	}
	assertBashParses(t, script)

	out := `grove aaa111 bbb222
nb MISSING ccc333
core NOBASE -
odd ERROR ERROR
noise
`
	heads := parseSatelliteWorktreeHeads(out)
	cases := map[string]satelliteWorktreeHead{
		"grove": {WTSHA: "aaa111", BaseSHA: "bbb222"},
		"nb":    {WTSHA: "MISSING", BaseSHA: "ccc333"},
		"core":  {WTSHA: "NOBASE", BaseSHA: ""},
		"odd":   {WTSHA: "ERROR", BaseSHA: ""},
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

// TestComputeSatelliteWorktreeDelta pins the worktree push's status
// selection: NOBASE held, missing worktree ships, matching sha up-to-date,
// and the shared slice-2 divergence arms (ancestor update / fetched-diverged
// / held-unfetched / forced) — plus the bundle-base choice per status.
func TestComputeSatelliteWorktreeDelta(t *testing.T) {
	repos := []string{"nobase", "missing", "same", "ahead", "pulled", "unfetched", "broken"}
	local := map[string]repoTip{
		"nobase":    {SHA: "l1", Branch: "plan-x"},
		"missing":   {SHA: "l2", Branch: "plan-x"},
		"same":      {SHA: "l3", Branch: "plan-x"},
		"ahead":     {SHA: "l4", Branch: "plan-x"},
		"pulled":    {SHA: "l5", Branch: "plan-x"},
		"unfetched": {SHA: "l6", Branch: "plan-x"},
		"broken":    {SHA: "l7", Branch: "plan-x"},
	}
	remote := map[string]satelliteWorktreeHead{
		"nobase":    {WTSHA: "NOBASE"},
		"missing":   {WTSHA: "MISSING", BaseSHA: "base2"},
		"same":      {WTSHA: "l3", BaseSHA: "base3"},
		"ahead":     {WTSHA: "old4", BaseSHA: "base4"}, // ancestor of local tip
		"pulled":    {WTSHA: "vm5", BaseSHA: "base5"},  // diverged, but fetched locally
		"unfetched": {WTSHA: "vm6", BaseSHA: "base6"},  // diverged, never fetched
		"broken":    {WTSHA: "ERROR", BaseSHA: ""},
	}
	isAncestor := func(repo, sha string) bool { return repo == "ahead" }
	hasObject := func(repo, sha string) bool { return repo == "pulled" }

	deltas := computeSatelliteWorktreeDelta(repos, local, remote, isAncestor, hasObject, false)
	byRepo := map[string]repoDelta{}
	for _, d := range deltas {
		byRepo[d.Repo] = d
	}
	want := map[string]string{
		"nobase":    deltaStatusNoBase,
		"missing":   deltaStatusMissingRemote,
		"same":      deltaStatusUpToDate,
		"ahead":     deltaStatusUpdate,
		"pulled":    deltaStatusDiverged,
		"unfetched": deltaStatusHeldUnfetched,
		"broken":    deltaStatusRemoteError,
	}
	for repo, status := range want {
		if byRepo[repo].Status != status {
			t.Errorf("delta[%s].Status = %q, want %q", repo, byRepo[repo].Status, status)
		}
	}
	// --force flips only the unfetched repo to forced-diverged.
	forced := computeSatelliteWorktreeDelta(repos, local, remote, isAncestor, hasObject, true)
	for _, d := range forced {
		switch d.Repo {
		case "unfetched":
			if d.Status != deltaStatusForcedDiverged {
				t.Errorf("forced delta[unfetched].Status = %q, want %q", d.Status, deltaStatusForcedDiverged)
			}
		case "nobase":
			if d.Status != deltaStatusNoBase {
				t.Errorf("--force must not unlock a missing base repo: %q", d.Status)
			}
		}
	}
	// Ship set: missing + update + diverged (never NOBASE or held-unfetched).
	if names := strings.Join(deltaRepoNames(mirrorDeltasToShip(deltas)), ","); names != "missing,ahead,pulled" {
		t.Fatalf("ship set = %q, want missing,ahead,pulled", names)
	}
	// Bundle bases: update rides the VM worktree head; a missing worktree
	// rides the VM BASE repo head (hex-guarded); diverged ships full.
	if got := worktreeBundleBaseSHA(byRepo["ahead"], remote["ahead"].BaseSHA); got != "old4" {
		t.Errorf("update bundle base = %q, want old4", got)
	}
	if got := worktreeBundleBaseSHA(byRepo["missing"], "beef1234"); got != "beef1234" {
		t.Errorf("missing-worktree bundle base = %q, want beef1234", got)
	}
	if got := worktreeBundleBaseSHA(byRepo["missing"], "$(boom)"); got != "" {
		t.Errorf("non-hex base must be dropped, got %q", got)
	}
	if got := worktreeBundleBaseSHA(byRepo["pulled"], "beef1234"); got != "" {
		t.Errorf("diverged must ship a full bundle, got base %q", got)
	}
}

// TestBuildSatelliteWorktreePushScript checks the remote worktree script's
// content contract: base-repo-missing refusal (pointing at repos push),
// worktree add for a missing worktree vs branch force-checkout for updates,
// container grove.toml + marker seeding (never overwritten), per-repo failure
// isolation, and nonzero exit with the stage kept.
func TestBuildSatelliteWorktreePushScript(t *testing.T) {
	updates := []repoDelta{
		{Repo: "grove", Branch: "plan-x", LocalSHA: "aaa111", Status: deltaStatusMissingRemote},
		{Repo: "det", Branch: "", LocalSHA: "bbb222", Status: deltaStatusUpdate},
	}
	script := buildSatelliteWorktreePushScript("~/code/grovetools", "plan-x", "plan-x", "/tmp/wt-stage", updates, []string{"grove", "det", "nb"})
	for _, want := range []string{
		"set -uo pipefail", // NOT -e: per-repo isolation
		`CODE="$HOME/code/grovetools"`,
		`WT="$CODE/.grove-worktrees/plan-x"`,
		`STAGE="/tmp/wt-stage"`,
		`run 'grove satellite repos push' first`, // base-missing refusal
		`git -C "$base" fetch "$STAGE/$repo.bundle" "$ref"`,
		`git -C "$base" worktree add --detach "$dir" "$sha"`,
		`git -C "$dir" checkout -f -B "$ref" "$sha"`, // branch update = fetch + reset
		`git -C "$dir" checkout -f --detach "$sha"`,  // detached laptop checkout
		"worktree_repo grove plan-x aaa111",
		"worktree_repo det HEAD bbb222",
		`if [ ! -f "$WT/grove.toml" ]`, // seed-if-absent, never overwrite
		`printf 'workspaces = ["*"]\n' > "$WT/grove.toml"`,
		`if [ ! -f "$WT/.grove/workspace" ]`,
		`printf 'branch: plan-x\nplan: plan-x\ncreated_at: %s\n'`,
		`printf 'owner: %s\necosystem: true\nrepos:\n' "$CODE"`,
		`printf '  - nb\n'`,
		`mark_failed "$1"`,
		"=== worktree push summary ===",
		"exit 1",
		`rm -rf "$STAGE"`,
		"worktree push complete",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("worktree push script missing %q:\n%s", want, script)
		}
	}
	// The base repos' checkouts are never touched: every checkout/reset runs
	// in the worktree dir, never in $base.
	for _, forbid := range []string{`git -C "$base" checkout`, `git -C "$base" reset`} {
		if strings.Contains(script, forbid) {
			t.Errorf("worktree push script must not contain %q:\n%s", forbid, script)
		}
	}
	assertBashParses(t, script)
}

// TestSatelliteWorktreeFFDecision pins the --ff safety table: fast-forward
// only a clean checkout, on the VM's branch, strictly behind the fetched tip.
func TestSatelliteWorktreeFFDecision(t *testing.T) {
	cases := []struct {
		name               string
		localBranch        string
		vmBranch           string
		dirty              bool
		localSHA, vmSHA    string
		behind             bool
		wantOK             bool
		wantReasonContains string
	}{
		{"clean and behind → ff", "plan-x", "plan-x", false, "a", "b", true, true, ""},
		{"already at tip", "plan-x", "plan-x", false, "a", "a", false, false, "already"},
		{"detached VM", "plan-x", "", false, "a", "b", true, false, "VM checkout is detached"},
		{"detached local", "", "plan-x", false, "a", "b", true, false, "local checkout is detached"},
		{"branch mismatch", "main", "plan-x", false, "a", "b", true, false, "!="},
		{"dirty tree", "plan-x", "plan-x", true, "a", "b", true, false, "dirty"},
		{"diverged", "plan-x", "plan-x", false, "a", "b", false, false, "diverged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := satelliteWorktreeFFDecision(tc.localBranch, tc.vmBranch, tc.dirty, tc.localSHA, tc.vmSHA, tc.behind)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v (reason %q), want %v", ok, reason, tc.wantOK)
			}
			if !ok && !strings.Contains(reason, tc.wantReasonContains) {
				t.Errorf("reason %q missing %q", reason, tc.wantReasonContains)
			}
		})
	}
}

// TestSatelliteWorktreeEngineExecution drives the full plan-worktree loop
// against real throwaway git repos with the local fake transport:
//
//  1. push creates real VM worktrees (branch checked out at the shipped sha)
//     attached to the mirrored base repos, plus the container grove.toml and
//     marker; a repo without a VM base repo is held with a repos-push
//     pointer while the others still ship.
//  2. a re-push is an idempotent no-op (no bundles shipped).
//  3. VM-side "agent" commits make the next push HOLD (unfetched), worktree
//     pull fetches them into refs/satellite/… without touching local
//     branches, and the interlock then lets push force-checkout again.
//  4. pull --ff fast-forwards a clean, strictly-behind laptop checkout to
//     the VM tip, and refuses a dirty one.
func TestSatelliteWorktreeEngineExecution(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	root := t.TempDir()
	laptopWT := filepath.Join(root, "laptop-wt") // the laptop plan worktree container
	vmCode := filepath.Join(root, "vm-code")     // the VM ecosystem root (mirrored bases)
	pushStage := filepath.Join(root, "wt-stage")
	pullStage := filepath.Join(root, "wt-pull-stage")

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
	mkRepo := func(parent, name, branch string) string {
		t.Helper()
		dir := filepath.Join(parent, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(dir, "init", "-b", branch)
		git(dir, "config", "user.email", "t@t")
		git(dir, "config", "user.name", "t")
		return dir
	}

	// Laptop plan worktree: two repos on the plan branch (the container in
	// production holds linked worktrees; plain repos have the same shape for
	// the engine — a .git and a checked-out branch). A third repo has no VM
	// base and must be held.
	agentLaptop := mkRepo(laptopWT, "agentrepo", "main")
	commit(agentLaptop, "a.txt", "base", "base")
	git(agentLaptop, "checkout", "-q", "-b", "plan-x")
	agentPlanSHA := commit(agentLaptop, "p.txt", "plan work", "plan c1")
	quietLaptop := mkRepo(laptopWT, "quietrepo", "main")
	commit(quietLaptop, "q.txt", "base", "base")
	git(quietLaptop, "checkout", "-q", "-b", "plan-x")
	quietPlanSHA := commit(quietLaptop, "q2.txt", "plan work", "plan c1")
	nobaseLaptop := mkRepo(laptopWT, "nobaserepo", "plan-x")
	commit(nobaseLaptop, "n.txt", "base", "base")

	// VM base repos: mirrors of the PRIMARY branches (as `repos push` leaves
	// them) — they do NOT have the plan branch.
	if err := os.MkdirAll(vmCode, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agentrepo", "quietrepo"} {
		src := filepath.Join(laptopWT, name)
		dst := filepath.Join(vmCode, name)
		git(vmCode, "clone", "-q", "--branch", "main", src, dst)
		git(dst, "config", "user.email", "vm@vm")
		git(dst, "config", "user.name", "vm")
	}

	repos := []string{"agentrepo", "quietrepo", "nobaserepo"}
	transport := &localPullTransport{t: t}
	vmWT := filepath.Join(vmCode, ".grove-worktrees", "plan-x")

	// --- dry-run: nothing lands on the "VM" ---
	if err := pushSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", "plan-x", pushStage, repos, true, true, false); err != nil {
		t.Fatalf("dry-run push: %v", err)
	}
	if _, err := os.Stat(vmWT); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create the VM worktree: %v", err)
	}

	// --- push 1: creates the worktrees; nobaserepo is held with a pointer ---
	err := pushSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", "plan-x", pushStage, repos, false, true, false)
	if err == nil || !strings.Contains(err.Error(), "nobaserepo") || !strings.Contains(err.Error(), "repos push") {
		t.Fatalf("push with a baseless repo must fail pointing at repos push, got %v", err)
	}
	for repo, wantSHA := range map[string]string{"agentrepo": agentPlanSHA, "quietrepo": quietPlanSHA} {
		dir := filepath.Join(vmWT, repo)
		if got := git(dir, "rev-parse", "HEAD"); got != wantSHA {
			t.Errorf("VM worktree %s HEAD = %q, want %q", repo, got, wantSHA)
		}
		if got := git(dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "plan-x" {
			t.Errorf("VM worktree %s branch = %q, want plan-x", repo, got)
		}
	}
	// Base repo checkouts stay on main, untouched.
	if got := git(filepath.Join(vmCode, "agentrepo"), "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("VM base repo branch moved: %q", got)
	}
	// Container ecosystem files seeded.
	if data, err := os.ReadFile(filepath.Join(vmWT, "grove.toml")); err != nil || strings.TrimSpace(string(data)) != `workspaces = ["*"]` {
		t.Errorf("container grove.toml = %q, %v", data, err)
	}
	marker, err2 := os.ReadFile(filepath.Join(vmWT, ".grove", "workspace"))
	if err2 != nil {
		t.Fatalf("container marker: %v", err2)
	}
	for _, want := range []string{"branch: plan-x", "plan: plan-x", "ecosystem: true", "- agentrepo", "owner: " + vmCode} {
		if !strings.Contains(string(marker), want) {
			t.Errorf("container marker missing %q:\n%s", want, marker)
		}
	}

	// --- push 2 (repos that have bases): idempotent no-op, no bundles ---
	scpBefore := len(transport.scpTo)
	if err := pushSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", "plan-x", pushStage, []string{"agentrepo", "quietrepo"}, false, true, false); err != nil {
		t.Fatalf("idempotent re-push: %v", err)
	}
	if len(transport.scpTo) != scpBefore {
		t.Fatalf("idempotent re-push must ship no bundles: %v", transport.scpTo)
	}

	// --- VM agent commits → push holds; pull fetches; push then proceeds ---
	agentVMSHA := commit(filepath.Join(vmWT, "agentrepo"), "w1.txt", "agent", "agent c1")
	err = pushSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", "plan-x", pushStage, []string{"agentrepo"}, false, true, false)
	if err == nil || !strings.Contains(err.Error(), "unfetched") {
		t.Fatalf("push over unfetched VM commits must hold, got %v", err)
	}
	if got := git(filepath.Join(vmWT, "agentrepo"), "rev-parse", "HEAD"); got != agentVMSHA {
		t.Fatalf("held push must not move the VM worktree: %q", got)
	}

	if err := pullSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", pullStage, []string{"agentrepo", "quietrepo"}, false, false); err != nil {
		t.Fatalf("worktree pull: %v", err)
	}
	if got, ok := localRefSHA(agentLaptop, "refs/satellite/sat1/plan-x"); !ok || got != agentVMSHA {
		t.Errorf("refs/satellite/sat1/plan-x = %q, want %q", got, agentVMSHA)
	}
	// Pull without --ff never moves the local branch.
	if got := git(agentLaptop, "rev-parse", "HEAD"); got != agentPlanSHA {
		t.Errorf("pull moved the local checkout: %q, want %q", got, agentPlanSHA)
	}

	// Interlock flipped: push now force-checkouts the VM back to the laptop
	// tip (the VM commits stay reachable via refs/satellite/… locally).
	if err := pushSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", "plan-x", pushStage, []string{"agentrepo"}, false, true, false); err != nil {
		t.Fatalf("post-pull push: %v", err)
	}
	if got := git(filepath.Join(vmWT, "agentrepo"), "rev-parse", "HEAD"); got != agentPlanSHA {
		t.Errorf("post-pull push must reset the VM worktree to the laptop tip: %q, want %q", got, agentPlanSHA)
	}

	// --- pull --ff: clean + strictly-behind fast-forwards; dirty refuses ---
	agentVMSHA2 := commit(filepath.Join(vmWT, "agentrepo"), "w2.txt", "agent", "agent c2")
	quietVMSHA2 := commit(filepath.Join(vmWT, "quietrepo"), "w3.txt", "agent", "agent c3")
	// Dirty the quiet laptop checkout so only agentrepo may fast-forward.
	if err := os.WriteFile(filepath.Join(quietLaptop, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pullSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", pullStage, []string{"agentrepo", "quietrepo"}, false, true); err != nil {
		t.Fatalf("pull --ff: %v", err)
	}
	if got := git(agentLaptop, "rev-parse", "HEAD"); got != agentVMSHA2 {
		t.Errorf("--ff must fast-forward the clean checkout: %q, want %q", got, agentVMSHA2)
	}
	if got := git(agentLaptop, "rev-parse", "--abbrev-ref", "HEAD"); got != "plan-x" {
		t.Errorf("--ff must stay on the branch: %q", got)
	}
	if got := git(quietLaptop, "rev-parse", "HEAD"); got != quietPlanSHA {
		t.Errorf("--ff must refuse a dirty checkout: %q, want %q", got, quietPlanSHA)
	}
	if got, ok := localRefSHA(quietLaptop, "refs/satellite/sat1/plan-x"); !ok || got != quietVMSHA2 {
		t.Errorf("the refused repo still gets its satellite ref: %q, want %q", got, quietVMSHA2)
	}

	// --- --ff idempotence: re-pull with nothing new fetches nothing and
	// leaves the fast-forwarded checkout alone ---
	if err := pullSatelliteWorktree(transport, "sat1", laptopWT, vmCode, "plan-x", pullStage, []string{"agentrepo"}, false, true); err != nil {
		t.Fatalf("idempotent pull --ff: %v", err)
	}
	if got := git(agentLaptop, "rev-parse", "HEAD"); got != agentVMSHA2 {
		t.Errorf("idempotent pull --ff moved HEAD: %q", got)
	}
}
