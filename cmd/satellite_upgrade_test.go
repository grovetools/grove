package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

// assertBashParses pipes a generated script through `bash -n` so emitted
// remote scripts are at least syntactically valid shell.
func assertBashParses(t *testing.T, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated script does not parse (%v): %s\n%s", err, out, script)
	}
}

func TestComputeSatelliteDelta(t *testing.T) {
	repos := []string{"compositor", "core", "daemon", "grove", "sync"}
	local := map[string]repoTip{
		"compositor": {SHA: "c1", Branch: "grove-satellite-poc"},
		"core":       {SHA: "c2", Branch: "grove-satellite-poc"},
		"daemon":     {SHA: "c3", Branch: "grove-satellite-poc"},
		"grove":      {SHA: "c4", Branch: ""}, // detached
		"sync":       {SHA: "c5", Branch: "grove-satellite-poc"},
	}
	remote := map[string]string{
		"compositor": "old1",
		"core":       "c2",      // up to date
		"daemon":     "MISSING", // not cloned on the VM
		"grove":      "ERROR",   // unreadable HEAD
		"sync":       "old5",
	}

	byRepo := func(deltas []repoDelta) map[string]repoDelta {
		m := map[string]repoDelta{}
		for _, d := range deltas {
			m[d.Repo] = d
		}
		return m
	}

	// Default (non-explicit) run: compositor is held, not silently built.
	got := byRepo(computeSatelliteDelta(repos, local, remote, false))
	want := map[string]string{
		"compositor": deltaStatusHeld,
		"core":       deltaStatusUpToDate,
		"daemon":     deltaStatusMissingRemote,
		"grove":      deltaStatusRemoteError,
		"sync":       deltaStatusUpdate,
	}
	for repo, status := range want {
		if got[repo].Status != status {
			t.Errorf("delta[%s].Status = %q, want %q", repo, got[repo].Status, status)
		}
	}
	if got["sync"].RemoteSHA != "old5" || got["sync"].LocalSHA != "c5" {
		t.Errorf("sync delta shas = %+v", got["sync"])
	}
	if got["daemon"].RemoteSHA != "" {
		t.Errorf("missing repo should have empty RemoteSHA, got %q", got["daemon"].RemoteSHA)
	}

	// Explicit --repos/--all run: compositor becomes a normal update and the
	// up-to-date core is FORCED (rebuild+reinstall despite matching shas).
	got = byRepo(computeSatelliteDelta(repos, local, remote, true))
	if got["compositor"].Status != deltaStatusUpdate {
		t.Errorf("explicit compositor delta = %q, want %q", got["compositor"].Status, deltaStatusUpdate)
	}
	if got["core"].Status != deltaStatusForced {
		t.Errorf("explicit up-to-date core delta = %q, want %q", got["core"].Status, deltaStatusForced)
	}

	// Ordering follows the input repo order.
	deltas := computeSatelliteDelta(repos, local, remote, false)
	for i, r := range repos {
		if deltas[i].Repo != r {
			t.Fatalf("delta order[%d] = %s, want %s", i, deltas[i].Repo, r)
		}
	}

	// Selection helper: by default only real updates ship.
	if names := strings.Join(deltaRepoNames(deltasToShip(deltas)), ","); names != "sync" {
		t.Errorf("default ship set = %q, want sync only", names)
	}
	// Forced run: updates AND forced ship; missing/error repos never do.
	forcedShip := deltasToShip(computeSatelliteDelta(repos, local, remote, true))
	if names := strings.Join(deltaRepoNames(forcedShip), ","); names != "compositor,core,sync" {
		t.Errorf("forced ship set = %q, want compositor,core,sync", names)
	}
}

func TestResolveUpgradeRepoSet(t *testing.T) {
	localRepos := []string{"core", "daemon", "grove", "sync"}

	// No flags: every repo probed, changed-only (not forced).
	repos, forced, err := resolveUpgradeRepoSet(nil, false, localRepos, "/src")
	if err != nil || forced || strings.Join(repos, ",") != "core,daemon,grove,sync" {
		t.Fatalf("default = (%v, %v, %v)", repos, forced, err)
	}

	// --repos: exactly the listed repos, forced regardless of delta status.
	repos, forced, err = resolveUpgradeRepoSet([]string{"grove", "sync"}, false, localRepos, "/src")
	if err != nil || !forced || strings.Join(repos, ",") != "grove,sync" {
		t.Fatalf("--repos = (%v, %v, %v)", repos, forced, err)
	}

	// --repos with an unknown repo errors.
	if _, _, err = resolveUpgradeRepoSet([]string{"nope"}, false, localRepos, "/src"); err == nil {
		t.Fatal("expected error for unknown --repos entry")
	}

	// --all: every repo, forced.
	repos, forced, err = resolveUpgradeRepoSet(nil, true, localRepos, "/src")
	if err != nil || !forced || strings.Join(repos, ",") != "core,daemon,grove,sync" {
		t.Fatalf("--all = (%v, %v, %v)", repos, forced, err)
	}

	// --all + --repos: mutually exclusive.
	if _, _, err = resolveUpgradeRepoSet([]string{"grove"}, true, localRepos, "/src"); err == nil {
		t.Fatal("expected error for --all with --repos")
	}
}

func TestBuildRemoteHeadsScript(t *testing.T) {
	script := buildRemoteHeadsScript("~/code/grovetools", []string{"grove", "daemon"})
	for _, want := range []string{
		`CODE="$HOME/code/grovetools"`,
		`"$CODE/grove/.git"`,
		`git -C "$CODE/daemon" rev-parse HEAD`,
		`echo "grove MISSING"`,
		`echo ERROR`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("heads script missing %q:\n%s", want, script)
		}
	}
	// The probe must be read-only: no writes, no sudo, no systemctl.
	for _, forbid := range []string{"sudo", "systemctl", "checkout", "reset", "mkdir"} {
		if strings.Contains(script, forbid) {
			t.Errorf("heads script contains non-read-only token %q:\n%s", forbid, script)
		}
	}

	// Absolute remote dir is used verbatim.
	if s := buildRemoteHeadsScript("/srv/code", []string{"grove"}); !strings.Contains(s, `CODE="/srv/code"`) {
		t.Errorf("absolute remote dir not preserved:\n%s", s)
	}

	assertBashParses(t, script)
}

func TestParseRemoteHeads(t *testing.T) {
	out := "grove abc123\n\ndaemon MISSING\nsync ERROR\ngarbage line with too many fields\n"
	heads := parseRemoteHeads(out)
	if heads["grove"] != "abc123" || heads["daemon"] != "MISSING" || heads["sync"] != "ERROR" {
		t.Fatalf("parseRemoteHeads = %v", heads)
	}
	if len(heads) != 3 {
		t.Fatalf("expected 3 entries, got %v", heads)
	}
}

func TestBuildSatelliteDeployScript(t *testing.T) {
	updates := []repoDelta{
		{Repo: "grove", Branch: "grove-satellite-poc", LocalSHA: "aaa111", Status: deltaStatusUpdate},
		{Repo: "sync", Branch: "grove-satellite-poc", LocalSHA: "bbb222", Status: deltaStatusUpdate},
		{Repo: "compositor", Branch: "grove-satellite-poc", LocalSHA: "ccc333", Status: deltaStatusUpdate},
		{Repo: "flow", Branch: "", LocalSHA: "ddd444", Status: deltaStatusUpdate}, // detached
	}
	script := buildSatelliteDeployScript("~/code/grovetools", satelliteStageDir, updates)

	for _, want := range []string{
		"set -uo pipefail", // NOT -e: a repo failure must not abort the deploy
		`export PATH="/usr/local/go/bin:$HOME/.local/share/grove/bin:$PATH"`,
		`CODE="$HOME/code/grovetools"`,
		`STAGE="` + satelliteStageDir + `"`,
		"checkout_repo grove grove-satellite-poc aaa111",
		"checkout_repo flow HEAD ddd444", // detached local HEAD ships by sha
		"build_install_repo compositor",
		"make zig",
		"build_install_repo sync",
		// forced repos ship no bundle — fetch only when the bundle exists
		`if [ -f "$STAGE/$repo.bundle" ]; then`,
		// failure isolation: record + continue, tail the failed build log,
		// end with a per-repo summary and a nonzero exit
		`mark_failed "$repo"`,
		`record "$repo" "build FAILED"`,
		"tail -n 40",
		`record "$repo" "built+installed"`,
		`record "$repo" "skipped (no build recipe)"`,
		"=== deploy summary ===",
		"exit 1",
		// grove-syncd sudo install is gated on sync actually building
		"if ! repo_failed sync; then",
		// self-heal for aborted checkouts (bootstrap step 3's failure mode)
		"reset --hard",
		`! -name .git -print -quit`,
		// atomic install: temp + mv, never in-place copy (ETXTBSY)
		`mv -f "$BIN_DIR/.$b.tmp" "$BIN_DIR/$b"`,
		// grove-syncd system install via sudo temp+mv
		"sudo mv -f /usr/local/bin/.grove-syncd.tmp /usr/local/bin/grove-syncd",
		`rm -rf "$STAGE"`,
		// a source build supersedes any prebuilt-heads overlay entry
		`HEADS="$HOME/.local/share/grove/prebuilt-heads"`,
		`clear_prebuilt_head "$repo"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("deploy script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "set -euo pipefail") {
		t.Errorf("deploy script must not be globally fail-fast (set -e aborts on the first repo build failure):\n%s", script)
	}

	// compositor builds first (grove/treemux/tuimux link its zig lib).
	if idx1, idx2 := strings.Index(script, "build_install_repo compositor"), strings.Index(script, "build_install_repo grove"); idx1 > idx2 {
		t.Error("compositor must build before grove")
	}

	// The script must never restart services — that step is separately gated.
	if strings.Contains(script, "systemctl") {
		t.Errorf("deploy script must not restart services:\n%s", script)
	}

	// Without sync in the delta there is no sudo install of grove-syncd.
	noSync := buildSatelliteDeployScript("~/code/grovetools", satelliteStageDir, updates[:1])
	if strings.Contains(noSync, "grove-syncd") && strings.Contains(noSync, "sudo cp") {
		t.Errorf("grove-syncd sudo install emitted without sync in the delta:\n%s", noSync)
	}

	assertBashParses(t, script)
	assertBashParses(t, noSync)
}

// TestSatelliteDeployScriptFailureIsolation executes a generated deploy script
// against throwaway git repos (the forced, bundle-less path) and proves K1: a
// repo whose build fails is recorded and does NOT prevent later repos from
// building and installing; the script exits nonzero after a per-repo summary,
// surfaces the failed build's output, and keeps the build logs.
func TestSatelliteDeployScriptFailureIsolation(t *testing.T) {
	for _, tool := range []string{"bash", "git", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	home := t.TempDir()
	code := filepath.Join(home, "code")
	stage := filepath.Join(home, "stage")

	mkRepo := func(name string, files map[string]string) repoDelta {
		t.Helper()
		dir := filepath.Join(code, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run := func(args ...string) string {
			t.Helper()
			out, err := gitOutput(dir, args...)
			if err != nil {
				t.Fatalf("git %v in %s: %v", args, name, err)
			}
			return out
		}
		run("init", "-b", "main")
		run("config", "user.email", "t@t")
		run("config", "user.name", "t")
		for f, content := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", ".")
		run("commit", "-m", "c1")
		// Forced: sha already on the "VM" — no bundle is shipped for it.
		return repoDelta{Repo: name, Branch: "main", LocalSHA: run("rev-parse", "HEAD"), Status: deltaStatusForced}
	}

	bad := mkRepo("badrepo", map[string]string{
		"Makefile": "build:\n\t@echo boom-badrepo-build\n\t@exit 1\n",
	})
	good := mkRepo("goodrepo", map[string]string{
		"Makefile": "build:\n\tmkdir -p bin\n\tprintf '#!/bin/sh\\necho ok\\n' > bin/goodtool\n\tchmod +x bin/goodtool\n",
	})
	content := mkRepo("contentrepo", map[string]string{"README.md": "content only\n"})

	// badrepo FIRST: its failure must not stop goodrepo's build+install.
	script := buildSatelliteDeployScript(code, stage, []repoDelta{bad, good, content})
	assertBashParses(t, script)

	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err == nil {
		t.Fatalf("script must exit nonzero when a repo fails:\n%s", output)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got %v:\n%s", err, output)
	}

	// The repo after the failure still built AND installed.
	if _, statErr := os.Stat(filepath.Join(home, ".local/share/grove/bin/goodtool")); statErr != nil {
		t.Errorf("goodrepo binary not installed despite badrepo failing first: %v\n%s", statErr, output)
	}

	for _, want := range []string{
		"badrepo: build FAILED",
		"goodrepo: built+installed",
		"contentrepo: skipped (no build recipe)",
		"boom-badrepo-build", // failed build output surfaced (tail)
		"=== deploy summary ===",
		"deploy FAILED for: badrepo",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("deploy output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "deploy complete") {
		t.Errorf("failed deploy must not report success:\n%s", output)
	}

	// The failed build's log is kept for postmortem (stage not removed).
	if _, statErr := os.Stat(filepath.Join(stage, "build-logs", "badrepo.log")); statErr != nil {
		t.Errorf("badrepo build log not kept: %v\n%s", statErr, output)
	}
}

func TestBuildSatelliteRestartScript(t *testing.T) {
	script := buildSatelliteRestartScript()
	for _, want := range []string{
		"set -euo pipefail",
		`export XDG_RUNTIME_DIR="/run/user/$(id -u)"`,
		"sudo systemctl restart grove-syncd",
		"systemctl --user restart groved",
		"is-active grove-syncd",
		"is-active groved",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("restart script missing %q:\n%s", want, script)
		}
	}
	assertBashParses(t, script)
}

func TestKnownHostsLine(t *testing.T) {
	key := "ssh-ed25519 AAAATESTKEY"
	if got := knownHostsLine("203.0.113.7", "22", key); got != "203.0.113.7 ssh-ed25519 AAAATESTKEY" {
		t.Errorf("port-22 line = %q", got)
	}
	if got := knownHostsLine("203.0.113.7", "2222", key); got != "[203.0.113.7]:2222 ssh-ed25519 AAAATESTKEY" {
		t.Errorf("custom-port line = %q", got)
	}
}

func TestNewSatelliteSSHPinsHostKey(t *testing.T) {
	dir := t.TempDir()

	// No host_key → hard refusal (pinning is never TOFU, C2).
	if _, err := newSatelliteSSH(satelliteConfigEntry{SSHAddr: "1.2.3.4:22", User: "u"}, dir); err == nil {
		t.Fatal("expected error for entry without host_key")
	}

	entry := satelliteConfigEntry{
		SSHAddr:      "203.0.113.7:22",
		User:         "solair",
		HostKey:      "ecdsa-sha2-nistp256 AAAAE2VjZHNh",
		IdentityFile: "/home/u/.ssh/id_ed25519",
	}
	s, err := newSatelliteSSH(entry, dir)
	if err != nil {
		t.Fatalf("newSatelliteSSH: %v", err)
	}
	if s.hostKeyAlgo != "ecdsa-sha2-nistp256" {
		t.Errorf("hostKeyAlgo = %q", s.hostKeyAlgo)
	}
	if s.dest() != "solair@203.0.113.7" {
		t.Errorf("dest = %q", s.dest())
	}
	data, err := os.ReadFile(s.knownHosts)
	if err != nil {
		t.Fatalf("known_hosts not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != "203.0.113.7 ecdsa-sha2-nistp256 AAAAE2VjZHNh" {
		t.Errorf("known_hosts content = %q", data)
	}

	opts := strings.Join(s.baseOptions(), " ")
	for _, want := range []string{
		"BatchMode=yes",
		"StrictHostKeyChecking=yes",
		"UserKnownHostsFile=" + s.knownHosts,
		"GlobalKnownHostsFile=/dev/null",
		"HostKeyAlgorithms=ecdsa-sha2-nistp256",
		"IdentitiesOnly=yes",
		"-i /home/u/.ssh/id_ed25519",
	} {
		if !strings.Contains(opts, want) {
			t.Errorf("ssh options missing %q: %s", want, opts)
		}
	}
	if strings.Contains(opts, "accept-new") {
		t.Errorf("ssh options must not allow TOFU: %s", opts)
	}

	// Without an identity file, agent-only: no -i / IdentitiesOnly.
	entry.IdentityFile = ""
	s2, _ := newSatelliteSSH(entry, dir)
	if opts2 := strings.Join(s2.baseOptions(), " "); strings.Contains(opts2, "-i ") || strings.Contains(opts2, "IdentitiesOnly") {
		t.Errorf("agent-only options should not pass an identity: %s", opts2)
	}
}

func TestRemoteCodeDirExpr(t *testing.T) {
	cases := map[string]string{
		"~/code/grovetools": `"$HOME/code/grovetools"`,
		"~":                 `"$HOME"`,
		"/srv/code":         `"/srv/code"`,
	}
	for in, want := range cases {
		if got := remoteCodeDirExpr(in); got != want {
			t.Errorf("remoteCodeDirExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCreateRepoBundle exercises the bundle helper against a real throwaway
// git repo: ranged bundle when the VM sha is an ancestor, full bundle on
// divergence.
func TestCreateRepoBundle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := gitOutput(repo, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "grove-satellite-poc")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c1")
	c1 := run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "c2")
	c2 := run("rev-parse", "HEAD")

	// Ranged bundle: VM at c1, local at c2.
	bundle := filepath.Join(t.TempDir(), "repo.bundle")
	if err := createRepoBundle(repo, bundle, "grove-satellite-poc", c1); err != nil {
		t.Fatalf("createRepoBundle (ranged): %v", err)
	}
	heads, err := gitOutput(repo, "bundle", "list-heads", bundle)
	if err != nil {
		t.Fatalf("bundle list-heads: %v", err)
	}
	if !strings.Contains(heads, c2) || !strings.Contains(heads, "refs/heads/grove-satellite-poc") {
		t.Fatalf("ranged bundle heads = %q, want %s on refs/heads/grove-satellite-poc", heads, c2)
	}

	// Unknown/non-ancestor VM sha → full bundle fallback still succeeds.
	full := filepath.Join(t.TempDir(), "full.bundle")
	if err := createRepoBundle(repo, full, "grove-satellite-poc", strings.Repeat("0", 40)); err != nil {
		t.Fatalf("createRepoBundle (full fallback): %v", err)
	}
	if heads, err = gitOutput(repo, "bundle", "list-heads", full); err != nil || !strings.Contains(heads, c2) {
		t.Fatalf("full bundle heads = %q err=%v", heads, err)
	}

	// No remote sha (repo missing remotely is never bundled, but sha may be
	// empty on first deploys) → full bundle of the branch.
	empty := filepath.Join(t.TempDir(), "empty.bundle")
	if err := createRepoBundle(repo, empty, "grove-satellite-poc", ""); err != nil {
		t.Fatalf("createRepoBundle (no remote sha): %v", err)
	}
}

// TestSatelliteRegistryOptionalFields covers the `up` completeness fix, now
// through the STATE file: the entry round-trips identity_file and socket_path
// through satellites.json, omits them from the JSON text when empty
// (omitempty), and grove.toml is never touched.
func TestSatelliteRegistryOptionalFields(t *testing.T) {
	configDir := setupGroveHome(t)
	tomlPath := filepath.Join(configDir, "grove.toml")
	if err := os.WriteFile(tomlPath, []byte("# keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	full := satelliteConfigEntry{
		SSHAddr:      "34.138.65.32:22",
		User:         "solair",
		HostKey:      "ecdsa-sha2-nistp256 AAAAE2VjZHNh",
		IdentityFile: "/Users/solair/.ssh/id_ed25519",
		SocketPath:   "/run/user/1001/grove/groved.sock",
	}
	if err := upsertSatelliteState("sat-full", full); err != nil {
		t.Fatalf("upsertSatelliteState: %v", err)
	}
	minimal := satelliteConfigEntry{
		SSHAddr: "10.0.0.9:22",
		User:    "u",
		HostKey: "ssh-ed25519 AAAA",
	}
	if err := upsertSatelliteState("sat-min", minimal); err != nil {
		t.Fatalf("upsertSatelliteState (minimal): %v", err)
	}

	state := mustLoadSatelliteState(t)
	if got := state["sat-full"]; got != full {
		t.Fatalf("loaded sat-full = %+v, want %+v", got, full)
	}
	if got := state["sat-min"]; got != minimal {
		t.Fatalf("loaded sat-min = %+v, want %+v", got, minimal)
	}

	statePath, err := satelliteStatePath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"socket_path": "/run/user/1001/grove/groved.sock"`) {
		t.Errorf("socket_path not written:\n%s", text)
	}
	if !strings.Contains(text, `"identity_file": "/Users/solair/.ssh/id_ed25519"`) {
		t.Errorf("identity_file not written:\n%s", text)
	}
	// The minimal entry stays minimal: exactly one of each optional key in the
	// whole file (from sat-full only).
	if n := strings.Count(text, "identity_file"); n != 1 {
		t.Errorf("identity_file appears %d times, want 1 (empty fields must be omitted):\n%s", n, text)
	}
	if n := strings.Count(text, "socket_path"); n != 1 {
		t.Errorf("socket_path appears %d times, want 1 (empty fields must be omitted):\n%s", n, text)
	}

	// grove.toml untouched.
	cfgData, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgData) != "# keep me\n" {
		t.Errorf("grove.toml modified by state writes:\n%s", cfgData)
	}
}

// --- --prebuilt tests ---

func TestComputeSatellitePrebuiltDelta(t *testing.T) {
	repos := []string{"compositor", "core", "grove", "sync", "nav", "flow"}
	local := map[string]repoTip{
		"compositor": {SHA: "c1", Branch: "main"},
		"core":       {SHA: "c2", Branch: "main"},
		"grove":      {SHA: "c3", Branch: "main"},
		"sync":       {SHA: "c4", Branch: "main"},
		"nav":        {SHA: "c5", Branch: "main"},
		"flow":       {SHA: "c6", Branch: "main"},
	}
	remote := map[string]string{
		"compositor": "old1",
		"core":       "c2",       // up to date, clean
		"grove":      "c3",       // up to date but DIRTY locally
		"sync":       "c4-dirty", // overlay says a dirty build is installed; local now clean at c4
		"nav":        "MISSING",  // dirtiness must not override MISSING semantics
		"flow":       "old6",
	}
	dirty := map[string]bool{"grove": true, "nav": true}

	byRepo := func(deltas []repoDelta) map[string]repoDelta {
		m := map[string]repoDelta{}
		for _, d := range deltas {
			m[d.Repo] = d
		}
		return m
	}

	got := byRepo(computeSatellitePrebuiltDelta(repos, local, remote, false, dirty))
	want := map[string]string{
		"compositor": deltaStatusHeldPrebuilt, // never shippable prebuilt
		"core":       deltaStatusUpToDate,
		"grove":      deltaStatusDirty,  // dirty ships even at a matching HEAD
		"sync":       deltaStatusUpdate, // -dirty overlay sha != clean local sha → re-ship
		"nav":        deltaStatusMissingRemote,
		"flow":       deltaStatusUpdate,
	}
	for repo, status := range want {
		if got[repo].Status != status {
			t.Errorf("prebuilt delta[%s].Status = %q, want %q", repo, got[repo].Status, status)
		}
	}

	// Even --repos/--all (forced) cannot ship compositor prebuilt.
	got = byRepo(computeSatellitePrebuiltDelta(repos, local, remote, true, dirty))
	if got["compositor"].Status != deltaStatusHeldPrebuilt {
		t.Errorf("forced prebuilt compositor = %q, want %q", got["compositor"].Status, deltaStatusHeldPrebuilt)
	}
	if got["core"].Status != deltaStatusForced {
		t.Errorf("forced up-to-date core = %q, want %q", got["core"].Status, deltaStatusForced)
	}

	// The ship set includes dirty repos but never compositor or MISSING.
	ship := deltaRepoNames(deltasToShip(computeSatellitePrebuiltDelta(repos, local, remote, false, dirty)))
	if strings.Join(ship, ",") != "grove,sync,flow" {
		t.Errorf("prebuilt ship set = %v, want [grove sync flow]", ship)
	}
}

func TestBuildRemoteHeadsScriptPrebuilt(t *testing.T) {
	script := buildRemoteHeadsScriptPrebuilt("~/code/grovetools", []string{"grove", "daemon"})
	for _, want := range []string{
		`CODE="$HOME/code/grovetools"`,
		`HEADS="$HOME/.local/share/grove/prebuilt-heads"`,
		"head_of()", // overlay entry wins over git HEAD
		`s="$(head_of grove)"`,
		`git -C "$CODE/daemon" rev-parse HEAD`,
		`echo "grove MISSING"`,
		`echo ERROR`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("prebuilt heads script missing %q:\n%s", want, script)
		}
	}
	// Same read-only contract as the source probe.
	for _, forbid := range []string{"sudo", "systemctl", "checkout", "reset", "mkdir"} {
		if strings.Contains(script, forbid) {
			t.Errorf("prebuilt heads script contains non-read-only token %q:\n%s", forbid, script)
		}
	}
	assertBashParses(t, script)
}

func TestBuildPrebuiltManifest(t *testing.T) {
	repos := []prebuiltRepo{
		{Repo: "grove", SHA: "abc123", Binaries: []prebuiltBinary{
			{Name: "grove", SHA256: "aaaa"},
			{Name: "grove-helper", SHA256: "bbbb"},
		}},
		{Repo: "sync", SHA: "def456-dirty", Binaries: []prebuiltBinary{
			{Name: "grove-syncd", SHA256: "cccc"},
		}},
	}
	got := buildPrebuiltManifest(repos)
	want := "grove abc123 grove aaaa\n" +
		"grove abc123 grove-helper bbbb\n" +
		"sync def456-dirty grove-syncd cccc\n"
	if got != want {
		t.Errorf("manifest = %q, want %q", got, want)
	}
}

func TestBuildSatellitePrebuiltInstallScript(t *testing.T) {
	repos := []prebuiltRepo{
		{Repo: "grove", SHA: "abc123", Binaries: []prebuiltBinary{{Name: "grove", SHA256: "aaaa"}}},
		{Repo: "sync", SHA: "def456-dirty", Binaries: []prebuiltBinary{
			{Name: "grove-sync", SHA256: "bbbb"},
			{Name: "grove-syncd", SHA256: "cccc"},
		}},
	}
	script := buildSatellitePrebuiltInstallScript(satelliteStageDir, repos)

	for _, want := range []string{
		"set -uo pipefail", // NOT -e: per-repo failure isolation
		`STAGE="` + satelliteStageDir + `"`,
		`BIN_DIR="$HOME/.local/share/grove/bin"`,
		`HEADS="$HOME/.local/share/grove/prebuilt-heads"`,
		`tar -xzf "$ARCHIVE" -C "$STAGE"`,
		// verify-then-install: one mismatch excludes the whole repo
		`if [ "$(sha256sum "$STAGE/$repo/$name" 2>/dev/null | awk '{print $1}')" != "$sum" ]; then`,
		`record "$repo" "sha256 mismatch ($name)"`,
		// atomic user install: temp + mv (ETXTBSY-safe)
		`mv -f "$BIN_DIR/.$b.tmp" "$BIN_DIR/$b"`,
		// grove-syncd system install via sudo temp+mv
		"sudo mv -f /usr/local/bin/.grove-syncd.tmp /usr/local/bin/grove-syncd",
		// atomic prebuilt-heads rewrite
		`mv "$HEADS.tmp" "$HEADS"`,
		// per-repo invocations carry the (possibly -dirty) sha and hashes
		"deploy_repo grove abc123 grove:aaaa",
		"deploy_repo sync def456-dirty grove-sync:bbbb grove-syncd:cccc",
		"=== prebuilt install summary ===",
		"exit 1",
		`rm -rf "$STAGE"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("prebuilt install script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "set -euo pipefail") {
		t.Errorf("install script must not be globally fail-fast:\n%s", script)
	}
	// No git, no VM build, no bundles, no service restarts.
	for _, forbid := range []string{"git ", "make ", ".bundle", "systemctl", "go build"} {
		if strings.Contains(script, forbid) {
			t.Errorf("prebuilt install script must not contain %q:\n%s", forbid, script)
		}
	}
	assertBashParses(t, script)
}

// TestCollectAndArchivePrebuiltBinaries covers the collect → manifest →
// archive pipeline against a real temp layout: only regular executable files
// are collected, hashes match the file contents, and the tar.gz round-trips
// paths as <repo>/<binary> with exec mode preserved.
func TestCollectAndArchivePrebuiltBinaries(t *testing.T) {
	target := orch.Target{GOOS: "linux", GOARCH: "amd64"}
	src := t.TempDir()
	repoDir := filepath.Join(src, "grove")
	binDir := prebuiltBinDir(repoDir, target)
	if err := os.MkdirAll(filepath.Join(binDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "grove"), []byte("BINARY-A"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "notes.txt"), []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	bins, err := collectPrebuiltBinaries(repoDir, target)
	if err != nil {
		t.Fatalf("collectPrebuiltBinaries: %v", err)
	}
	if len(bins) != 1 || bins[0].Name != "grove" {
		t.Fatalf("collected = %+v, want just the grove executable", bins)
	}
	// sha256("BINARY-A")
	if bins[0].SHA256 != "3d17ff33ca2e42d1241ff08060354fc406fb05c07d9b6bd70f8ebe988287f55d" {
		t.Errorf("sha256 = %s", bins[0].SHA256)
	}

	// A repo with no target bin dir collects nothing (→ the hard
	// GROVE_BUILD_OUT warning path), without error.
	if bins, err := collectPrebuiltBinaries(filepath.Join(src, "nope"), target); err != nil || len(bins) != 0 {
		t.Fatalf("missing dir collect = (%v, %v), want empty", bins, err)
	}

	repos := []prebuiltRepo{{Repo: "grove", SHA: "abc", Binaries: bins}}
	archive := filepath.Join(t.TempDir(), prebuiltArchiveName)
	if err := writePrebuiltArchive(archive, src, target, repos); err != nil {
		t.Fatalf("writePrebuiltArchive: %v", err)
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not on PATH")
	}
	dest := t.TempDir()
	if out, err := exec.Command("tar", "-xzf", archive, "-C", dest).CombinedOutput(); err != nil {
		t.Fatalf("untar: %v: %s", err, out)
	}
	extracted := filepath.Join(dest, "grove", "grove")
	info, err := os.Stat(extracted)
	if err != nil {
		t.Fatalf("archived binary missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("exec mode not preserved: %v", info.Mode())
	}
	data, err := os.ReadFile(extracted)
	if err != nil || string(data) != "BINARY-A" {
		t.Errorf("archived content = %q err=%v", data, err)
	}
}

// TestPrebuiltInstallScriptExecution executes a generated install script
// against a real staged archive: a repo with a tampered hash is excluded
// entirely (none of its binaries install), the good repo installs and its
// (-dirty) sha lands in prebuilt-heads atomically, the script exits nonzero
// with a per-repo summary, and the stage dir is kept.
func TestPrebuiltInstallScriptExecution(t *testing.T) {
	for _, tool := range []string{"bash", "tar", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	target := orch.Target{GOOS: "linux", GOARCH: "amd64"}
	home := t.TempDir()
	src := t.TempDir()
	stage := filepath.Join(home, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}

	mkBin := func(repo, name, content string) prebuiltRepo {
		t.Helper()
		dir := prebuiltBinDir(filepath.Join(src, repo), target)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		bins, err := collectPrebuiltBinaries(filepath.Join(src, repo), target)
		if err != nil {
			t.Fatal(err)
		}
		return prebuiltRepo{Repo: repo, Binaries: bins}
	}

	good := mkBin("goodrepo", "goodtool", "#!/bin/sh\necho good\n")
	good.SHA = "aaa111-dirty" // dirty builds are recorded with the suffix
	bad := mkBin("badrepo", "badtool", "#!/bin/sh\necho bad\n")
	bad.SHA = "bbb222"
	bad.Binaries[0].SHA256 = strings.Repeat("0", 64) // tampered → mismatch

	// A pre-existing overlay entry for goodrepo must be REPLACED, not
	// duplicated; badrepo's stale entry must survive its failed install.
	headsPath := filepath.Join(home, ".local/share/grove/prebuilt-heads")
	if err := os.MkdirAll(filepath.Dir(headsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(headsPath, []byte("goodrepo old000\nbadrepo old111\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos := []prebuiltRepo{bad, good} // bad FIRST: must not block good
	if err := writePrebuiltArchive(filepath.Join(stage, prebuiltArchiveName), src, target, repos); err != nil {
		t.Fatal(err)
	}

	script := buildSatellitePrebuiltInstallScript(stage, repos)
	assertBashParses(t, script)

	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err == nil {
		t.Fatalf("script must exit nonzero when a repo fails:\n%s", output)
	}

	// goodrepo installed despite badrepo failing first.
	installed := filepath.Join(home, ".local/share/grove/bin/goodtool")
	if info, statErr := os.Stat(installed); statErr != nil {
		t.Errorf("goodtool not installed: %v\n%s", statErr, output)
	} else if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("goodtool not executable: %v", info.Mode())
	}
	// badrepo's binary must NOT be installed (sha mismatch excludes the repo).
	if _, statErr := os.Stat(filepath.Join(home, ".local/share/grove/bin/badtool")); statErr == nil {
		t.Errorf("badtool installed despite sha mismatch:\n%s", output)
	}

	heads, readErr := os.ReadFile(headsPath)
	if readErr != nil {
		t.Fatalf("prebuilt-heads missing: %v\n%s", readErr, output)
	}
	text := string(heads)
	if !strings.Contains(text, "goodrepo aaa111-dirty") {
		t.Errorf("goodrepo head not recorded with -dirty suffix:\n%s", text)
	}
	if strings.Contains(text, "old000") {
		t.Errorf("goodrepo's stale overlay entry not replaced:\n%s", text)
	}
	if !strings.Contains(text, "badrepo old111") {
		t.Errorf("badrepo's overlay entry must survive its failed install:\n%s", text)
	}

	for _, want := range []string{
		"badrepo: sha256 MISMATCH",
		"installed goodtool",
		"=== prebuilt install summary ===",
		"install FAILED for: badrepo",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("install output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "prebuilt install complete") {
		t.Errorf("failed install must not report success:\n%s", output)
	}
	// Stage kept for postmortem.
	if _, statErr := os.Stat(filepath.Join(stage, prebuiltArchiveName)); statErr != nil {
		t.Errorf("stage dir not kept on failure: %v", statErr)
	}
}

// TestSatelliteUpgradeRefusesExecKind pins R5's guard. `upgrade`'s restart
// step runs `systemctl restart grove-syncd` + `--user restart groved` under
// `set -euo pipefail` — services an exec-only satellite does not have — so
// without this the verb mutates a tart/docker guest and only THEN hard-fails.
// The refusal must land before any remote work, i.e. before the ssh transport
// is built (asserted here by an empty PATH: reaching ssh/git would surface a
// different error).
func TestSatelliteUpgradeRefusesExecKind(t *testing.T) {
	setupGroveHome(t)
	writeSatelliteStateEntry(t, "tartdemo", satelliteConfigEntry{
		SSHAddr:     "192.168.64.2:22",
		User:        "admin",
		HostKey:     "ssh-ed25519 AAAA",
		Kind:        satelliteKindExec,
		ProviderRef: "tart:grove-sat-tartdemo",
	})
	t.Setenv("PATH", t.TempDir())

	err := runSatelliteCmd(t, newSatelliteUpgradeCmd(), "tartdemo", "--yes")
	if err == nil {
		t.Fatal("upgrade of an exec-only satellite was not refused")
	}
	for _, want := range []string{"exec-only satellite", "no groved or grove-syncd", "grove satellite up tartdemo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("upgrade error %q missing %q", err, want)
		}
	}
}

// TestSatelliteUpgradeAllowsFullKind pins the other side of the guard: a
// full-kind satellite still proceeds past it (and then fails on the missing
// registry-adjacent work, not on the exec refusal).
func TestSatelliteUpgradeFullTartRequiresArm64Prebuilt(t *testing.T) {
	setupGroveHome(t)
	writeSatelliteStateEntry(t, "tartfull", satelliteConfigEntry{
		SSHAddr: "192.168.64.2:22", User: "admin", HostKey: "ssh-ed25519 AAAA",
		ProviderRef: "tart:grove-sat-tartfull",
	})
	t.Setenv("PATH", t.TempDir())

	err := runSatelliteCmd(t, newSatelliteUpgradeCmd(), "tartfull", "--yes")
	if err == nil || !strings.Contains(err.Error(), "requires --prebuilt upgrades (linux/arm64)") {
		t.Fatalf("source upgrade was not refused: %v", err)
	}
	err = runSatelliteCmd(t, newSatelliteUpgradeCmd(), "tartfull", "--yes", "--prebuilt", "--target", "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "requires --target linux/arm64") {
		t.Fatalf("mismatched prebuilt target was not refused: %v", err)
	}
}

func TestSatelliteUpgradeAllowsFullKind(t *testing.T) {
	setupGroveHome(t)
	writeSatelliteStateEntry(t, "gcpsat", satelliteConfigEntry{
		SSHAddr: "203.0.113.7:22",
		User:    "grovedev",
		HostKey: "ssh-ed25519 AAAA",
	})
	t.Setenv("PATH", t.TempDir())

	err := runSatelliteCmd(t, newSatelliteUpgradeCmd(), "gcpsat", "--yes", "--dry-run")
	if err != nil && strings.Contains(err.Error(), "exec-only satellite") {
		t.Errorf("full-kind satellite must pass the exec guard: %v", err)
	}
}
