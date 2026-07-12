package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	// Explicit --repos run: compositor becomes a normal update.
	got = byRepo(computeSatelliteDelta(repos, local, remote, true))
	if got["compositor"].Status != deltaStatusUpdate {
		t.Errorf("explicit compositor delta = %q, want %q", got["compositor"].Status, deltaStatusUpdate)
	}

	// Ordering follows the input repo order.
	deltas := computeSatelliteDelta(repos, local, remote, false)
	for i, r := range repos {
		if deltas[i].Repo != r {
			t.Fatalf("delta order[%d] = %s, want %s", i, deltas[i].Repo, r)
		}
	}

	// Selection helper: only real updates ship.
	updates := deltasWithStatus(deltas, deltaStatusUpdate)
	if names := strings.Join(deltaRepoNames(updates), ","); names != "sync" {
		t.Errorf("updates = %q, want sync only", names)
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
		"set -euo pipefail",
		`export PATH="/usr/local/go/bin:$HOME/.local/share/grove/bin:$PATH"`,
		`CODE="$HOME/code/grovetools"`,
		`STAGE="` + satelliteStageDir + `"`,
		"update_repo grove grove-satellite-poc aaa111",
		"update_repo flow HEAD ddd444", // detached local HEAD ships by sha
		"build_repo compositor",
		"make zig",
		"install_bins sync",
		// self-heal for aborted checkouts (bootstrap step 3's failure mode)
		"reset --hard",
		`! -name .git -print -quit`,
		// atomic install: temp + mv, never in-place copy (ETXTBSY)
		`mv -f "$BIN_DIR/.$b.tmp" "$BIN_DIR/$b"`,
		// grove-syncd system install via sudo temp+mv
		"sudo mv -f /usr/local/bin/.grove-syncd.tmp /usr/local/bin/grove-syncd",
		`rm -rf "$STAGE"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("deploy script missing %q:\n%s", want, script)
		}
	}

	// compositor builds first (grove/treemux/tuimux link its zig lib).
	if idx1, idx2 := strings.Index(script, "build_repo compositor"), strings.Index(script, "build_repo grove"); idx1 > idx2 {
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

// TestSatelliteRegistryOptionalFields covers the `up` completeness fix: the
// registry entry round-trips identity_file and socket_path through the real
// config loader, and omits them from the TOML text when empty.
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
	if err := writeSatelliteRegistry("sat-full", full); err != nil {
		t.Fatalf("writeSatelliteRegistry: %v", err)
	}
	minimal := satelliteConfigEntry{
		SSHAddr: "10.0.0.9:22",
		User:    "u",
		HostKey: "ssh-ed25519 AAAA",
	}
	if err := writeSatelliteRegistry("sat-min", minimal); err != nil {
		t.Fatalf("writeSatelliteRegistry (minimal): %v", err)
	}

	sats := loadSatellitesViaConfig(t)
	if got := sats["sat-full"]; got != full {
		t.Fatalf("loaded sat-full = %+v, want %+v", got, full)
	}
	if got := sats["sat-min"]; got != minimal {
		t.Fatalf("loaded sat-min = %+v, want %+v", got, minimal)
	}

	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `socket_path = "/run/user/1001/grove/groved.sock"`) {
		t.Errorf("socket_path not written:\n%s", text)
	}
	if !strings.Contains(text, `identity_file = "/Users/solair/.ssh/id_ed25519"`) {
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
	if !strings.Contains(text, "# keep me") {
		t.Errorf("comment destroyed:\n%s", text)
	}
}
