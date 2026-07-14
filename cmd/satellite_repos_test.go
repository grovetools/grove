package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// TestResolveSatelliteMirrorReposPrecedence pins flag > [.repos] workspaces >
// resolved sync workspaces, including the set-to-empty flag disable.
func TestResolveSatelliteMirrorReposPrecedence(t *testing.T) {
	reposCfg := satelliteReposOptions{Workspaces: []string{"grove", "daemon"}}
	syncSet := []string{"cloud", "grovetools"}

	// Flag set → flag wins over both config layers.
	if got := resolveSatelliteMirrorRepos([]string{"x", "y"}, true, reposCfg, syncSet); strings.Join(got, ",") != "x,y" {
		t.Fatalf("flag-set resolve = %v", got)
	}
	// Flag set but empty → explicit disable (mirror nothing).
	if got := resolveSatelliteMirrorRepos(nil, true, reposCfg, syncSet); len(got) != 0 {
		t.Fatalf("set-to-empty flag should disable the mirror, got %v", got)
	}
	// Flag unset → [.repos] workspaces win over the sync fallback.
	if got := resolveSatelliteMirrorRepos([]string{"ignored"}, false, reposCfg, syncSet); strings.Join(got, ",") != "grove,daemon" {
		t.Fatalf(".repos resolve = %v", got)
	}
	// No [.repos] block → the resolved sync set.
	if got := resolveSatelliteMirrorRepos(nil, false, satelliteReposOptions{}, syncSet); strings.Join(got, ",") != "cloud,grovetools" {
		t.Fatalf("sync-fallback resolve = %v", got)
	}
	// And the sync set itself defaults to the compat pair when nothing is
	// configured anywhere (the full three-level fallback the verb wires up).
	defaultSync := resolveSatelliteSyncWorkspaces(satelliteSyncOptions{}, "", false)
	if got := resolveSatelliteMirrorRepos(nil, false, satelliteReposOptions{}, defaultSync); strings.Join(got, ",") != strings.Join(defaultSatelliteSyncWorkspaces, ",") {
		t.Fatalf("default resolve = %v", got)
	}
}

// TestSatelliteReposOptionsFromConfig decodes the [satellites.<name>.repos]
// subtable out of the layered config (same decode stance as the .sync test),
// and yields the zero value when the subtable is absent.
func TestSatelliteReposOptionsFromConfig(t *testing.T) {
	configDir := setupGroveHome(t)
	tomlPath := filepath.Join(configDir, "grove.toml")
	content := `# hand-written config
[satellites.sat1.sync]
workspaces = ["cloud"]

[satellites.sat1.repos]
workspaces = ["grove", "daemon", "core"]
`
	if err := os.WriteFile(tomlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts, err := satelliteReposOptionsFromConfig(cfg, "sat1")
	if err != nil {
		t.Fatalf("satelliteReposOptionsFromConfig: %v", err)
	}
	if strings.Join(opts.Workspaces, ",") != "grove,daemon,core" {
		t.Fatalf("repos workspaces = %v", opts.Workspaces)
	}
	// The sibling .sync decode is untouched by the new subtable.
	syncOpts, err := satelliteSyncOptionsFromConfig(cfg, "sat1")
	if err != nil || strings.Join(syncOpts.Workspaces, ",") != "cloud" {
		t.Fatalf("sync workspaces = %v (err %v)", syncOpts.Workspaces, err)
	}
	// Unknown satellite / absent subtable → zero value, no error.
	if opts, err := satelliteReposOptionsFromConfig(cfg, "nope"); err != nil || len(opts.Workspaces) != 0 {
		t.Fatalf("absent subtable = %+v (err %v)", opts, err)
	}
}

// TestComputeSatelliteMirrorDelta pins the mirror's status selection: matching
// shas are up-to-date, missing repos SHIP, ancestor VM heads are plain
// updates, non-ancestor heads are DIVERGED, ERROR skips — and the bundle base
// is the VM sha only for the incremental (ancestor) case.
func TestComputeSatelliteMirrorDelta(t *testing.T) {
	repos := []string{"same", "behind", "diverged", "absent", "broken"}
	local := map[string]repoTip{
		"same":     {SHA: "c1", Branch: "main"},
		"behind":   {SHA: "c2", Branch: "main"},
		"diverged": {SHA: "c3", Branch: ""}, // detached
		"absent":   {SHA: "c4", Branch: "main"},
		"broken":   {SHA: "c5", Branch: "main"},
	}
	remote := map[string]string{
		"same":     "c1",
		"behind":   "old2", // ancestor of local tip
		"diverged": "old3", // NOT an ancestor
		"absent":   "MISSING",
		"broken":   "ERROR",
	}
	isAncestor := func(repo, sha string) bool { return repo == "behind" }

	deltas := computeSatelliteMirrorDelta(repos, local, remote, isAncestor)
	byRepo := map[string]repoDelta{}
	for _, d := range deltas {
		byRepo[d.Repo] = d
	}
	want := map[string]string{
		"same":     deltaStatusUpToDate,
		"behind":   deltaStatusUpdate,
		"diverged": deltaStatusDiverged,
		"absent":   deltaStatusMissingRemote,
		"broken":   deltaStatusRemoteError,
	}
	for repo, status := range want {
		if byRepo[repo].Status != status {
			t.Errorf("mirror delta[%s].Status = %q, want %q", repo, byRepo[repo].Status, status)
		}
	}

	// Ship set: updates + missing + diverged, in input order; never
	// up-to-date or remote-error.
	ship := mirrorDeltasToShip(deltas)
	if names := strings.Join(deltaRepoNames(ship), ","); names != "behind,diverged,absent" {
		t.Fatalf("mirror ship set = %q, want behind,diverged,absent", names)
	}

	// Bundle base: incremental for the ancestor update, full ("") for
	// missing and diverged.
	if got := mirrorBundleBaseSHA(byRepo["behind"]); got != "old2" {
		t.Errorf("behind bundle base = %q, want old2 (incremental)", got)
	}
	if got := mirrorBundleBaseSHA(byRepo["diverged"]); got != "" {
		t.Errorf("diverged bundle base = %q, want \"\" (full bundle)", got)
	}
	if got := mirrorBundleBaseSHA(byRepo["absent"]); got != "" {
		t.Errorf("absent bundle base = %q, want \"\" (full bundle)", got)
	}
}

// TestLocalGoWorkGoDirective covers the go.work go-directive probe: the
// directive is returned verbatim, odd shapes and missing files yield "" (the
// mirror then skips go.work seeding).
func TestLocalGoWorkGoDirective(t *testing.T) {
	dir := t.TempDir()
	if got := localGoWorkGoDirective(dir); got != "" {
		t.Fatalf("missing go.work directive = %q, want empty", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.25\n\nuse (\n\t./grove\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := localGoWorkGoDirective(dir); got != "go 1.25" {
		t.Fatalf("go directive = %q, want 'go 1.25'", got)
	}
	// Unexpected directive shapes are refused (they land in generated shell).
	if err := os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.25rc1 // odd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := localGoWorkGoDirective(dir); got != "" {
		t.Fatalf("odd directive should be refused, got %q", got)
	}
}

// TestBuildSatelliteReposPushScript checks the generated mirror script's
// content contract: root-file seeding with never-overwrite guards, the
// git-init + fetch-if-bundle + force-checkout + self-heal repo idiom,
// failure isolation, and the on-success stage cleanup.
func TestBuildSatelliteReposPushScript(t *testing.T) {
	updates := []repoDelta{
		{Repo: "grove", Branch: "grove-satellite-poc", LocalSHA: "aaa111", Status: deltaStatusUpdate},
		{Repo: "cloud", Branch: "main", LocalSHA: "bbb222", Status: deltaStatusMissingRemote},
		{Repo: "nb", Branch: "", LocalSHA: "ddd444", Status: deltaStatusDiverged}, // detached
	}
	all := []string{"grove", "cloud", "nb"}
	script := buildSatelliteReposPushScript("~/code/grovetools", satelliteReposStageDir, updates, all, "go 1.25")

	for _, want := range []string{
		"set -uo pipefail", // NOT -e: a repo failure must not abort the mirror
		`CODE="$HOME/code/grovetools"`,
		`STAGE="` + satelliteReposStageDir + `"`,
		// root grove.toml: seed if absent, notice (never overwrite) if it differs
		`printf 'workspaces = ["*"]\n' > "$CODE/grove.toml"`,
		`if [ ! -f "$CODE/grove.toml" ]; then`,
		"exists and differs from the wildcard manifest — left untouched",
		// go.work: seed if absent (go.mod-guarded use lines), notice if present
		`if [ ! -f "$CODE/go.work" ]; then`,
		`printf 'go 1.25\n\nuse (\n'`,
		`[ -f "$CODE/grove/go.mod" ] && printf '\t./grove\n'`,
		`[ -f "$CODE/cloud/go.mod" ] && printf '\t./cloud\n'`,
		"go.work already exists — left untouched",
		// per-repo idiom: init if missing, fetch only when a bundle shipped,
		// force-checkout by branch or detached sha, self-heal reset --hard
		`git -C "$dir" init -q`,
		`if [ -f "$STAGE/$repo.bundle" ]; then`,
		`git -C "$dir" checkout -f -B "$ref" "$sha"`,
		`git -C "$dir" checkout -f --detach "$sha"`,
		"reset --hard",
		`! -name .git -print -quit`,
		// per-repo invocations (detached local HEAD ships by sha)
		"mirror_repo grove grove-satellite-poc aaa111",
		"mirror_repo cloud main bbb222",
		"mirror_repo nb HEAD ddd444",
		// failure isolation + summary + cheap idempotence
		`mark_failed "$1"`,
		"=== repos push summary ===",
		"exit 1",
		`rm -rf "$STAGE"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("mirror script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "set -euo pipefail") {
		t.Errorf("mirror script must not be globally fail-fast:\n%s", script)
	}
	// The mirror never builds, installs, or restarts anything.
	for _, forbid := range []string{"make ", "go build", "systemctl", "sudo"} {
		if strings.Contains(script, forbid) {
			t.Errorf("mirror script must not contain %q:\n%s", forbid, script)
		}
	}
	assertBashParses(t, script)

	// Without a local go.work directive the go.work seeding is skipped
	// entirely; grove.toml seeding stays.
	noGoWork := buildSatelliteReposPushScript("~/code/grovetools", satelliteReposStageDir, updates, all, "")
	if strings.Contains(noGoWork, "go.work") {
		t.Errorf("script must not touch go.work without a local directive:\n%s", noGoWork)
	}
	if !strings.Contains(noGoWork, `> "$CODE/grove.toml"`) {
		t.Errorf("grove.toml seeding lost in the no-go.work variant:\n%s", noGoWork)
	}
	assertBashParses(t, noGoWork)

	// Zero updates (idempotent re-run): no mirror_repo calls, root-file
	// convergence only — and the script still parses.
	empty := buildSatelliteReposPushScript("~/code/grovetools", satelliteReposStageDir, nil, all, "go 1.25")
	if strings.Contains(empty, "mirror_repo ") && strings.Contains(empty, "\nmirror_repo") {
		t.Errorf("empty update set must emit no mirror_repo calls:\n%s", empty)
	}
	assertBashParses(t, empty)
}

// TestSatelliteReposPushScriptExecution executes generated mirror scripts
// against real throwaway git repos, covering the full slice-1 loop locally:
// initial mirror of missing repos (git init + full bundle + checkout), root
// grove.toml/go.work seeding, incremental update from a ranged bundle,
// no-op convergence that never overwrites differing root files, and failure
// isolation (a broken repo doesn't block the rest; nonzero exit; stage kept).
func TestSatelliteReposPushScriptExecution(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	home := t.TempDir()
	laptop := t.TempDir()
	code := filepath.Join(home, "code", "grovetools") // absolute → used verbatim
	stage := filepath.Join(home, "stage")

	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitOutput(dir, args...)
		if err != nil {
			t.Fatalf("git %v in %s: %v", args, dir, err)
		}
		return out
	}
	mkLaptopRepo := func(name string, files map[string]string) string {
		t.Helper()
		dir := filepath.Join(laptop, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(dir, "init", "-b", "main")
		git(dir, "config", "user.email", "t@t")
		git(dir, "config", "user.name", "t")
		for f, content := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git(dir, "add", ".")
		git(dir, "commit", "-m", "c1")
		return git(dir, "rev-parse", "HEAD")
	}
	runScript := func(script string) (string, error) {
		t.Helper()
		assertBashParses(t, script)
		cmd := exec.Command("bash", "-s")
		cmd.Stdin = strings.NewReader(script)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	bundle := func(repo, base string) {
		t.Helper()
		if err := os.MkdirAll(stage, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := createRepoBundle(filepath.Join(laptop, repo), filepath.Join(stage, repo+".bundle"), "main", base); err != nil {
			t.Fatalf("bundle %s: %v", repo, err)
		}
	}

	gomodSHA := mkLaptopRepo("gomodrepo", map[string]string{"go.mod": "module example.com/gomodrepo\n\ngo 1.25\n"})
	plainSHA := mkLaptopRepo("plainrepo", map[string]string{"README.md": "notes only\n"})
	all := []string{"gomodrepo", "plainrepo"}

	// --- round 1: both repos missing on the "VM" → init + full bundles ---
	bundle("gomodrepo", "")
	bundle("plainrepo", "")
	updates := []repoDelta{
		{Repo: "gomodrepo", Branch: "main", LocalSHA: gomodSHA, Status: deltaStatusMissingRemote},
		{Repo: "plainrepo", Branch: "main", LocalSHA: plainSHA, Status: deltaStatusMissingRemote},
	}
	out, err := runScript(buildSatelliteReposPushScript(code, stage, updates, all, "go 1.25"))
	if err != nil {
		t.Fatalf("initial mirror failed: %v\n%s", err, out)
	}
	for repo, sha := range map[string]string{"gomodrepo": gomodSHA, "plainrepo": plainSHA} {
		dir := filepath.Join(code, repo)
		if got := git(dir, "rev-parse", "HEAD"); got != sha {
			t.Errorf("%s VM HEAD = %s, want %s\n%s", repo, got, sha, out)
		}
		if got := git(dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
			t.Errorf("%s VM branch = %s, want main", repo, got)
		}
	}
	// Root files seeded.
	if data, rerr := os.ReadFile(filepath.Join(code, "grove.toml")); rerr != nil || string(data) != "workspaces = [\"*\"]\n" {
		t.Errorf("grove.toml = %q err=%v", data, rerr)
	}
	gowork, rerr := os.ReadFile(filepath.Join(code, "go.work"))
	if rerr != nil {
		t.Fatalf("go.work not seeded: %v\n%s", rerr, out)
	}
	if !strings.Contains(string(gowork), "go 1.25") || !strings.Contains(string(gowork), "./gomodrepo") {
		t.Errorf("go.work missing directive or gomodrepo use line:\n%s", gowork)
	}
	if strings.Contains(string(gowork), "plainrepo") {
		t.Errorf("go.work must not use a repo without go.mod:\n%s", gowork)
	}
	// Stage removed on success.
	if _, serr := os.Stat(stage); !os.IsNotExist(serr) {
		t.Errorf("stage dir not cleaned on success: %v", serr)
	}
	if !strings.Contains(out, "repos push complete") {
		t.Errorf("missing success line:\n%s", out)
	}

	// --- round 2: incremental update from a ranged bundle ---
	if err := os.WriteFile(filepath.Join(laptop, "gomodrepo", "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(filepath.Join(laptop, "gomodrepo"), "add", ".")
	git(filepath.Join(laptop, "gomodrepo"), "commit", "-m", "c2")
	newSHA := git(filepath.Join(laptop, "gomodrepo"), "rev-parse", "HEAD")
	bundle("gomodrepo", gomodSHA) // ranged: VM sha is an ancestor
	updates = []repoDelta{{Repo: "gomodrepo", Branch: "main", LocalSHA: newSHA, RemoteSHA: gomodSHA, Status: deltaStatusUpdate}}
	if out, err = runScript(buildSatelliteReposPushScript(code, stage, updates, all, "go 1.25")); err != nil {
		t.Fatalf("incremental mirror failed: %v\n%s", err, out)
	}
	if got := git(filepath.Join(code, "gomodrepo"), "rev-parse", "HEAD"); got != newSHA {
		t.Errorf("incremental VM HEAD = %s, want %s\n%s", got, newSHA, out)
	}

	// --- round 3: no-op convergence; differing root files stay untouched ---
	custom := "# customized by the user\nworkspaces = [\"grove\"]\n"
	if err := os.WriteFile(filepath.Join(code, "grove.toml"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err = runScript(buildSatelliteReposPushScript(code, stage, nil, all, "go 1.25")); err != nil {
		t.Fatalf("no-op mirror failed: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(filepath.Join(code, "grove.toml")); string(data) != custom {
		t.Errorf("differing grove.toml was overwritten:\n%s", data)
	}
	for _, want := range []string{
		"grove.toml exists and differs from the wildcard manifest — left untouched",
		"go.work already exists — left untouched",
		"repos push complete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no-op output missing %q:\n%s", want, out)
		}
	}

	// --- failure isolation: a bad repo (bogus sha, no bundle) doesn't block
	// the good one; nonzero exit; per-repo summary ---
	if err := os.RemoveAll(filepath.Join(code, "gomodrepo")); err != nil {
		t.Fatal(err)
	}
	bundle("gomodrepo", "")
	updates = []repoDelta{
		{Repo: "brokenrepo", Branch: "main", LocalSHA: strings.Repeat("0", 40), Status: deltaStatusMissingRemote},
		{Repo: "gomodrepo", Branch: "main", LocalSHA: newSHA, Status: deltaStatusMissingRemote},
	}
	out, err = runScript(buildSatelliteReposPushScript(code, stage, updates, all, "go 1.25"))
	if err == nil {
		t.Fatalf("script must exit nonzero when a repo fails:\n%s", out)
	}
	if got := git(filepath.Join(code, "gomodrepo"), "rev-parse", "HEAD"); got != newSHA {
		t.Errorf("gomodrepo not mirrored despite brokenrepo failing first: %s\n%s", got, out)
	}
	for _, want := range []string{
		"brokenrepo: mirror FAILED",
		"gomodrepo: mirrored",
		"=== repos push summary ===",
		"repos push FAILED for: brokenrepo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "repos push complete") {
		t.Errorf("failed mirror must not report success:\n%s", out)
	}
	// Stage kept for postmortem.
	if _, serr := os.Stat(filepath.Join(stage, "gomodrepo.bundle")); serr != nil {
		t.Errorf("stage dir not kept on failure: %v", serr)
	}
}
