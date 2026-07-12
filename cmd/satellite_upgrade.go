package cmd

// grove satellite upgrade — redeploy the grove stack on a running satellite VM.
//
// The manual PoC redeploy runbook, made a verb: read the VM's per-repo HEADs,
// diff against the local ecosystem worktree, ship git bundles for the changed
// repos, then (in ONE generated remote script, like bootstrap) fetch+checkout,
// build, and atomically install on the VM, and finally restart grove-syncd +
// groved. Every SSH/scp invocation pins the registry host_key — never TOFU
// (C2). Building happens ON the VM (sync needs CGO+fts5; never cross-compile).

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/components/table"
	"github.com/spf13/cobra"
)

// defaultRemoteCodeDir is where satellite-bootstrap.sh clones the superrepo.
const defaultRemoteCodeDir = "~/code/grovetools"

// satelliteStageDir is the remote staging dir bundles are scp'd into; the
// deploy script removes it on success, so re-runs stay clean.
const satelliteStageDir = "/tmp/grove-satellite-upgrade"

// satelliteUserBinDir mirrors bootstrap step 4's BIN_DIR (as a remote shell
// expression).
const satelliteUserBinDir = "$HOME/.local/share/grove/bin"

// satelliteRemotePATH matches bootstrap's /etc/profile.d PATH for non-login
// shells (go for builds, the grove bin dir for tooling).
const satelliteRemotePATH = "/usr/local/go/bin:$HOME/.local/share/grove/bin:$PATH"

func newSatelliteUpgradeCmd() *cobra.Command {
	var (
		reposFlag     string
		sourceDir     string
		remoteCodeDir string
		dryRun        bool
		assumeYes     bool
	)
	cmd := cli.NewStandardCommand("upgrade <name>", "Redeploy the grove stack on a satellite VM from local branch tips")
	cmd.Long = `Redeploy the grove stack on a running satellite VM.

Compares each local repo's HEAD against the VM's checkout under the remote
code dir, ships git bundles for the changed repos, then fetches, force-checks-
out, builds (on the VM — sync needs CGO/fts5), and atomically installs the
binaries, and restarts grove-syncd + groved.

Notes:
  - compositor (zig) is slow to build and rarely needed; when it changed it is
    HELD unless explicitly listed in --repos.
  - core is a library: its changes only reach binaries when dependent repos are
    rebuilt — include those dependents in --repos if only core moved.
  - the VM checkout is forced to the shipped tip; uncommitted VM changes to
    tracked files are discarded.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&reposFlag, "repos", "", "Comma-separated repos to upgrade (default: every changed repo)")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "Local ecosystem worktree root (default: the go.work root above cwd)")
	cmd.Flags().StringVar(&remoteCodeDir, "remote-code-dir", defaultRemoteCodeDir, "Superrepo checkout on the VM")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Stop after printing the local-vs-VM delta table")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the deploy and restart confirmation prompts")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		entry, ok := loadConfiguredSatellites()[name]
		if !ok {
			return fmt.Errorf("no [satellites.%s] entry in the grove config — run `grove satellite up %s` first", name, name)
		}

		// Resolve the local ecosystem root.
		if sourceDir == "" {
			root, err := defaultUpgradeSourceDir()
			if err != nil {
				return err
			}
			sourceDir = root
		}
		sourceAbs, err := filepath.Abs(sourceDir)
		if err != nil {
			return fmt.Errorf("resolve --source-dir: %w", err)
		}

		// Requested repos (empty = all changed).
		requested, err := parseReposFlag(reposFlag)
		if err != nil {
			return err
		}

		localRepos, err := discoverLocalRepos(sourceAbs)
		if err != nil {
			return err
		}
		if len(localRepos) == 0 {
			return fmt.Errorf("no git repos found under %s — is this an ecosystem worktree? (pass --source-dir)", sourceAbs)
		}
		repoSet := localRepos
		if len(requested) > 0 {
			for _, r := range requested {
				if !containsString(localRepos, r) {
					return fmt.Errorf("--repos %q: no git repo %s/%s", r, sourceAbs, r)
				}
			}
			repoSet = requested
		}

		// Local tips.
		local := map[string]repoTip{}
		for _, r := range repoSet {
			tip, err := localRepoTip(filepath.Join(sourceAbs, r))
			if err != nil {
				return fmt.Errorf("read local HEAD of %s: %w", r, err)
			}
			local[r] = tip
		}

		// Pinned SSH transport from the registry entry.
		sshDir, err := os.MkdirTemp("", "grove-satellite-upgrade-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(sshDir) }()
		ssh, err := newSatelliteSSH(entry, sshDir)
		if err != nil {
			return fmt.Errorf("satellite %q: %w", name, err)
		}

		// (a) Remote HEADs → delta table.
		fmt.Printf("Reading repo HEADs on %s (%s)...\n", name, ssh.dest())
		out, err := ssh.outputScript(buildRemoteHeadsScript(remoteCodeDir, repoSet))
		if err != nil {
			return fmt.Errorf("read remote HEADs: %w", err)
		}
		remote := parseRemoteHeads(out)
		deltas := computeSatelliteDelta(repoSet, local, remote, len(requested) > 0)
		printSatelliteDelta(deltas)

		updates := deltasWithStatus(deltas, deltaStatusUpdate)
		if held := deltasWithStatus(deltas, deltaStatusHeld); len(held) > 0 {
			fmt.Printf("\nwarning: compositor changed but is HELD — its zig build is slow and rarely needed.\n")
			fmt.Printf("Include it explicitly to deploy it: --repos %s\n", strings.Join(append(deltaRepoNames(updates), "compositor"), ","))
		}
		for _, d := range updates {
			if d.Repo == "core" {
				fmt.Printf("\nnote: core is a library — its changes only reach binaries via dependent repo rebuilds;\ninclude the dependents (e.g. grove,daemon,sync,flow,nb) in --repos if they show up-to-date.\n")
			}
		}
		if len(updates) == 0 {
			fmt.Printf("\nSatellite %q is up to date with %s — nothing to do.\n", name, sourceAbs)
			return nil
		}
		if dryRun {
			fmt.Printf("\n(dry-run) would upgrade %d repo(s): %s\n", len(updates), strings.Join(deltaRepoNames(updates), ", "))
			return nil
		}

		if !assumeYes {
			if !confirmYesNo(fmt.Sprintf("Ship, build, and install %d repo(s) on %q? (force-checkout discards uncommitted VM changes)", len(updates), name)) {
				return fmt.Errorf("aborted")
			}
		}

		// (b) Bundles → scp.
		bundleDir, err := os.MkdirTemp("", "grove-satellite-bundles-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(bundleDir) }()
		var bundlePaths []string
		for _, d := range updates {
			bundlePath := filepath.Join(bundleDir, d.Repo+".bundle")
			if err := createRepoBundle(filepath.Join(sourceAbs, d.Repo), bundlePath, d.Branch, d.RemoteSHA); err != nil {
				return fmt.Errorf("bundle %s: %w", d.Repo, err)
			}
			bundlePaths = append(bundlePaths, bundlePath)
		}
		fmt.Printf("\nShipping %d bundle(s) to %s:%s ...\n", len(bundlePaths), name, satelliteStageDir)
		if err := ssh.runCommand("mkdir -p " + satelliteStageDir); err != nil {
			return fmt.Errorf("create remote stage dir: %w", err)
		}
		if err := ssh.scp(bundlePaths, satelliteStageDir+"/"); err != nil {
			return fmt.Errorf("scp bundles: %w", err)
		}

		// (c)–(e) One generated remote script: fetch+checkout (self-healing),
		// build, install (temp+mv, ETXTBSY-safe).
		fmt.Println("\nRunning remote deploy script (fetch, checkout, build, install)...")
		if err := ssh.runScript(buildSatelliteDeployScript(remoteCodeDir, satelliteStageDir, updates)); err != nil {
			return fmt.Errorf("remote deploy failed: %w", err)
		}

		// (f) Restart, gated.
		if !assumeYes {
			if !confirmYesNo(fmt.Sprintf("Restart grove-syncd and groved on %q now?", name)) {
				fmt.Println("\nBinaries are installed but services still run the old ones. Restart manually with:")
				fmt.Printf("  ssh %s 'sudo systemctl restart grove-syncd'\n", ssh.dest())
				fmt.Printf("  ssh %s 'XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart groved'\n", ssh.dest())
				return nil
			}
		}
		fmt.Println("\nRestarting grove-syncd + groved...")
		if err := ssh.runScript(buildSatelliteRestartScript()); err != nil {
			return fmt.Errorf("remote restart/verify failed: %w", err)
		}

		// (g) Verified by the restart script (is-active); laptop half.
		fmt.Printf("\nSatellite %q upgraded (%s).\n", name, strings.Join(deltaRepoNames(updates), ", "))
		printSatelliteNextSteps(entry.User, ssh.host, false)
		return nil
	}
	return cmd
}

// defaultUpgradeSourceDir resolves the ecosystem worktree root the command runs
// from: the nearest ancestor holding a go.work (grove worktrees tie the repos
// together with one), falling back to cwd.
func defaultUpgradeSourceDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if cfg, err := workspace.FindRootGoWorkspace(cwd); err == nil && cfg != nil {
		return cfg.WorkspaceRoot, nil
	}
	return cwd, nil
}

// repoNameRe keeps repo names safe to embed as bare words in the generated
// remote scripts (they come from local directory listings or --repos).
var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func parseReposFlag(flag string) ([]string, error) {
	if strings.TrimSpace(flag) == "" {
		return nil, nil
	}
	var repos []string
	for _, r := range strings.Split(flag, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !repoNameRe.MatchString(r) {
			return nil, fmt.Errorf("--repos: invalid repo name %q", r)
		}
		repos = append(repos, r)
	}
	return repos, nil
}

// discoverLocalRepos lists the git-repo subdirectories of the ecosystem root
// (worktree checkouts have .git as a file, primary checkouts as a dir — both
// count), sorted. Names that would be unsafe in a generated script are skipped.
func discoverLocalRepos(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read --source-dir: %w", err)
	}
	var repos []string
	for _, e := range entries {
		if !e.IsDir() || !repoNameRe.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), ".git")); err == nil {
			repos = append(repos, e.Name())
		}
	}
	return repos, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// repoTip is a local repo's HEAD sha and branch ("" when detached).
type repoTip struct {
	SHA    string
	Branch string
}

func localRepoTip(repoDir string) (repoTip, error) {
	sha, err := gitOutput(repoDir, "rev-parse", "HEAD")
	if err != nil {
		return repoTip{}, err
	}
	branch, err := gitOutput(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return repoTip{}, err
	}
	if branch == "HEAD" { // detached
		branch = ""
	}
	return repoTip{SHA: sha, Branch: branch}, nil
}

func gitOutput(repoDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...) //nolint:gosec // G204: internal args
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// createRepoBundle writes a git bundle for repoDir carrying the commits the VM
// is missing. When the VM's sha is a local ancestor the bundle is ranged
// (small); on divergence/force-push it falls back to a full bundle of the tip.
func createRepoBundle(repoDir, bundlePath, branch, remoteSHA string) error {
	ref := "HEAD"
	if branch != "" {
		ref = branch
	}
	spec := ref
	if remoteSHA != "" {
		if err := exec.Command("git", "-C", repoDir, "merge-base", "--is-ancestor", remoteSHA, "HEAD").Run(); err == nil { //nolint:gosec // G204
			spec = remoteSHA + ".." + ref
		} else {
			fmt.Printf("(%s: VM sha %.12s is not an ancestor of local HEAD — shipping a full bundle)\n", filepath.Base(repoDir), remoteSHA)
		}
	}
	if _, err := gitOutput(repoDir, "bundle", "create", bundlePath, spec); err != nil {
		return err
	}
	return nil
}

// --- delta computation (pure) ---

const (
	deltaStatusUpToDate      = "up-to-date"
	deltaStatusUpdate        = "update"
	deltaStatusHeld          = "held (zig; opt in via --repos)"
	deltaStatusMissingRemote = "missing on VM"
	deltaStatusRemoteError   = "remote error"
)

type repoDelta struct {
	Repo      string
	Branch    string // local branch ("" = detached)
	LocalSHA  string
	RemoteSHA string // "" when missing/unreadable
	Status    string
}

// computeSatelliteDelta diffs local tips against the VM's HEADs for repos (in
// order). compositor is special-cased: its zig build is slow and rarely needed,
// so a changed compositor is HELD unless the user passed --repos explicitly.
func computeSatelliteDelta(repos []string, local map[string]repoTip, remote map[string]string, explicit bool) []repoDelta {
	deltas := make([]repoDelta, 0, len(repos))
	for _, r := range repos {
		tip := local[r]
		d := repoDelta{Repo: r, Branch: tip.Branch, LocalSHA: tip.SHA}
		switch sha, ok := remote[r]; {
		case !ok || sha == "MISSING":
			d.Status = deltaStatusMissingRemote
		case sha == "ERROR":
			d.Status = deltaStatusRemoteError
		case sha == tip.SHA:
			d.RemoteSHA = sha
			d.Status = deltaStatusUpToDate
		default:
			d.RemoteSHA = sha
			d.Status = deltaStatusUpdate
			if r == "compositor" && !explicit {
				d.Status = deltaStatusHeld
			}
		}
		deltas = append(deltas, d)
	}
	return deltas
}

func deltasWithStatus(deltas []repoDelta, status string) []repoDelta {
	var out []repoDelta
	for _, d := range deltas {
		if d.Status == status {
			out = append(out, d)
		}
	}
	return out
}

func deltaRepoNames(deltas []repoDelta) []string {
	names := make([]string, 0, len(deltas))
	for _, d := range deltas {
		names = append(names, d.Repo)
	}
	return names
}

func printSatelliteDelta(deltas []repoDelta) {
	rows := make([][]string, 0, len(deltas))
	for _, d := range deltas {
		branch := d.Branch
		if branch == "" {
			branch = "(detached)"
		}
		rows = append(rows, []string{d.Repo, d.Status, shortSHA(d.LocalSHA), shortSHA(d.RemoteSHA), branch})
	}
	tbl := table.NewStyledTable().
		Headers("Repo", "Status", "Local", "VM", "Branch").
		Rows(rows...)
	fmt.Println(tbl.Render())
}

func shortSHA(sha string) string {
	if sha == "" {
		return "-"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// parseRemoteHeads decodes buildRemoteHeadsScript output: one "<repo> <sha>"
// line per repo (sha may be the sentinel MISSING or ERROR).
func parseRemoteHeads(out string) map[string]string {
	heads := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 {
			heads[fields[0]] = fields[1]
		}
	}
	return heads
}

// --- remote script generation (pure; exercised by unit tests) ---

// remoteCodeDirExpr renders the --remote-code-dir value as a double-quoted
// remote shell expression, translating a leading ~ into $HOME (ssh commands are
// not login shells, and quoted ~ would not expand anyway).
func remoteCodeDirExpr(dir string) string {
	switch {
	case dir == "~":
		return `"$HOME"`
	case strings.HasPrefix(dir, "~/"):
		return `"$HOME/` + strings.TrimPrefix(dir, "~/") + `"`
	default:
		return `"` + dir + `"`
	}
}

// buildRemoteHeadsScript emits the read-only probe: one "<repo> <sha>" line per
// repo, with MISSING/ERROR sentinels instead of failures so one bad repo does
// not mask the rest.
func buildRemoteHeadsScript(remoteCodeDir string, repos []string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	for _, r := range repos {
		fmt.Fprintf(&b, "if [ -e \"$CODE/%s/.git\" ]; then echo \"%s $(git -C \"$CODE/%s\" rev-parse HEAD 2>/dev/null || echo ERROR)\"; else echo \"%s MISSING\"; fi\n", r, r, r, r)
	}
	return b.String()
}

// buildSatelliteDeployScript generates the single remote script covering steps
// (c)-(e): fetch from the shipped bundles, force-checkout the tips (with the
// bootstrap script's empty-worktree self-heal), build on the VM, and install
// binaries atomically (copy-to-temp + mv dodges ETXTBSY on running binaries;
// grove-syncd goes to /usr/local/bin via sudo the same way). Idempotent: a
// re-run re-fetches no-ops, re-checkouts the same sha, rebuilds, reinstalls.
func buildSatelliteDeployScript(remoteCodeDir, stageDir string, updates []repoDelta) string {
	// compositor first: grove/treemux/tuimux link its zig lib.
	ordered := make([]repoDelta, 0, len(updates))
	for _, d := range updates {
		if d.Repo == "compositor" {
			ordered = append(ordered, d)
		}
	}
	for _, d := range updates {
		if d.Repo != "compositor" {
			ordered = append(ordered, d)
		}
	}

	var b strings.Builder
	b.WriteString("# generated by `grove satellite upgrade` — idempotent\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "export PATH=\"%s\"\n", satelliteRemotePATH)
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "STAGE=%q\n", stageDir)
	fmt.Fprintf(&b, "BIN_DIR=\"%s\"\n", satelliteUserBinDir)
	b.WriteString(`mkdir -p "$BIN_DIR"

update_repo() { # <repo> <ref> <sha> — fetch from bundle, force-checkout, self-heal
  local repo="$1" ref="$2" sha="$3"
  local dir="$CODE/$repo"
  echo "==> $repo: fetch + checkout ${sha:0:12}"
  git -C "$dir" fetch "$STAGE/$repo.bundle" "$ref"
  if [ "$ref" = HEAD ]; then
    git -C "$dir" checkout -f --detach "$sha" || true
  else
    git -C "$dir" checkout -f -B "$ref" "$sha" || true
  fi
  # Self-heal aborted checkouts (cf. satellite-bootstrap.sh step 3): an
  # interrupted checkout can leave HEAD moved but the worktree empty apart
  # from .git — and the '|| true' above routes a failed checkout here too.
  if [ "$(git -C "$dir" rev-parse HEAD)" != "$sha" ] \
     || [ -z "$(find "$dir" -mindepth 1 -maxdepth 1 ! -name .git -print -quit)" ]; then
    echo "self-heal: hard-resetting $repo to ${sha:0:12}" >&2
    git -C "$dir" reset --hard "$sha"
  fi
}

build_repo() { # <repo> — mirror bootstrap step 4 (build ON the VM; sync needs CGO+fts5)
  local repo="$1"
  echo "==> $repo: build"
  cd "$CODE/$repo"
  if [ "$repo" = compositor ]; then
    make zig
  elif [ -f Makefile ] && grep -q '^build:' Makefile; then
    make build
  elif [ -f go.mod ]; then
    mkdir -p bin && go build -o bin/ ./...
  else
    echo "==> $repo: no build recipe (content-only repo; skipping build)"
  fi
}

install_bins() { # <repo> — user binaries via copy-to-temp + mv (atomic; no ETXTBSY)
  local repo="$1"
  local dir="$CODE/$repo/bin"
  [ -d "$dir" ] || return 0
  local f b
  for f in "$dir"/*; do
    [ -f "$f" ] && [ -x "$f" ] || continue
    b="$(basename "$f")"
    [ "$b" = grove-syncd ] && continue # system binary, installed below via sudo
    cp "$f" "$BIN_DIR/.$b.tmp"
    mv -f "$BIN_DIR/.$b.tmp" "$BIN_DIR/$b"
    echo "installed $b -> $BIN_DIR"
  done
}
`)
	b.WriteString("\n")
	syncIncluded := false
	for _, d := range ordered {
		if d.Repo == "sync" {
			syncIncluded = true
		}
		ref := d.Branch
		if ref == "" {
			ref = "HEAD"
		}
		fmt.Fprintf(&b, "update_repo %s %s %s\n", d.Repo, ref, d.LocalSHA)
	}
	b.WriteString("\n")
	for _, d := range ordered {
		fmt.Fprintf(&b, "build_repo %s\n", d.Repo)
	}
	b.WriteString("\n")
	for _, d := range ordered {
		fmt.Fprintf(&b, "install_bins %s\n", d.Repo)
	}
	if syncIncluded {
		b.WriteString(`
# grove-syncd is a system binary under systemd; sudo temp+mv (a running
# grove-syncd makes in-place copy fail with ETXTBSY).
sudo cp "$CODE/sync/bin/grove-syncd" /usr/local/bin/.grove-syncd.tmp
sudo chmod 0755 /usr/local/bin/.grove-syncd.tmp
sudo mv -f /usr/local/bin/.grove-syncd.tmp /usr/local/bin/grove-syncd
echo "installed grove-syncd -> /usr/local/bin"
`)
	}
	b.WriteString("\nrm -rf \"$STAGE\"\necho \"deploy complete\"\n")
	return b.String()
}

// buildSatelliteRestartScript restarts both units and verifies is-active
// (set -e turns an inactive unit into a hard failure). groved runs under
// systemd --user, which needs XDG_RUNTIME_DIR on a non-login SSH shell.
func buildSatelliteRestartScript() string {
	return `set -euo pipefail
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
sudo systemctl restart grove-syncd
systemctl --user restart groved
sleep 2
printf 'grove-syncd: %s\n' "$(sudo systemctl is-active grove-syncd)"
printf 'groved: %s\n' "$(systemctl --user is-active groved)"
`
}

// --- pinned SSH transport ---

// satelliteSSH shells out to ssh/scp with the registry host_key pinned via a
// generated known_hosts file (StrictHostKeyChecking=yes + HostKeyAlgorithms
// locked to the pinned key's type, mirroring the daemon transport) — never
// TOFU (C2). BatchMode keeps every invocation non-interactive.
type satelliteSSH struct {
	host         string
	port         string
	user         string
	identityFile string
	knownHosts   string
	hostKeyAlgo  string
}

func newSatelliteSSH(entry satelliteConfigEntry, tmpDir string) (*satelliteSSH, error) {
	if entry.HostKey == "" {
		return nil, fmt.Errorf("registry entry has no host_key — refusing to ssh without a pin (C2)")
	}
	host, port, err := net.SplitHostPort(entry.SSHAddr)
	if err != nil {
		host, port = entry.SSHAddr, "22"
	}
	if host == "" {
		return nil, fmt.Errorf("registry entry has no ssh_addr")
	}
	fields := strings.Fields(entry.HostKey)
	if len(fields) < 2 {
		return nil, fmt.Errorf("registry host_key %q is not in '<type> <base64>' form", entry.HostKey)
	}
	khPath := filepath.Join(tmpDir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(knownHostsLine(host, port, entry.HostKey)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return &satelliteSSH{
		host:         host,
		port:         port,
		user:         entry.User,
		identityFile: entry.IdentityFile,
		knownHosts:   khPath,
		hostKeyAlgo:  fields[0],
	}, nil
}

// knownHostsLine renders the pinned registry host_key as a known_hosts entry
// (non-default ports use the [host]:port form).
func knownHostsLine(host, port, hostKey string) string {
	if port == "" || port == "22" {
		return host + " " + hostKey
	}
	return "[" + host + "]:" + port + " " + hostKey
}

func (s *satelliteSSH) dest() string {
	if s.user == "" {
		return s.host
	}
	return s.user + "@" + s.host
}

func (s *satelliteSSH) baseOptions() []string {
	opts := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + s.knownHosts,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "HostKeyAlgorithms=" + s.hostKeyAlgo,
	}
	if s.identityFile != "" {
		opts = append(opts, "-o", "IdentitiesOnly=yes", "-i", s.identityFile)
	}
	return opts
}

// runScript streams script to `bash -s` on the VM with the caller's stdout/
// stderr attached (one round trip for many remote steps, like bootstrap).
func (s *satelliteSSH) runScript(script string) error {
	args := append(s.baseOptions(), "-p", s.port, s.dest(), "bash -s")
	cmd := exec.Command("ssh", args...) //nolint:gosec // G204: registry/flag-derived
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// outputScript runs script via `bash -s` and captures stdout (stderr surfaces
// in the error).
func (s *satelliteSSH) outputScript(script string) (string, error) {
	args := append(s.baseOptions(), "-p", s.port, s.dest(), "bash -s")
	cmd := exec.Command("ssh", args...) //nolint:gosec // G204: registry/flag-derived
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// runCommand runs a single remote command, capturing output into the error.
func (s *satelliteSSH) runCommand(command string) error {
	args := append(s.baseOptions(), "-p", s.port, s.dest(), command)
	cmd := exec.Command("ssh", args...) //nolint:gosec // G204: registry/flag-derived
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// scp ships local files to remoteDir with the same pin.
func (s *satelliteSSH) scp(localPaths []string, remoteDir string) error {
	args := append(s.baseOptions(), "-q", "-P", s.port)
	args = append(args, localPaths...)
	args = append(args, s.dest()+":"+remoteDir)
	cmd := exec.Command("scp", args...) //nolint:gosec // G204: registry/flag-derived
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- `up` completeness helpers ---

// probeSatelliteSocketPath derives the remote groved socket path from the VM's
// uid (systemd --user ⇒ /run/user/<uid>/grove/groved.sock) over a pinned SSH
// probe, and reports whether the socket exists yet.
func probeSatelliteSocketPath(entry satelliteConfigEntry) (path string, exists bool, err error) {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-probe-")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return "", false, err
	}
	out, err := ssh.outputScript("id -u\n")
	if err != nil {
		return "", false, fmt.Errorf("probe remote uid: %w", err)
	}
	uid := strings.TrimSpace(out)
	if !regexp.MustCompile(`^\d+$`).MatchString(uid) {
		return "", false, fmt.Errorf("unexpected `id -u` output %q", uid)
	}
	path = fmt.Sprintf("/run/user/%s/grove/groved.sock", uid)
	exists = ssh.runCommand("test -S "+path) == nil
	return path, exists, nil
}

// expandUserPath resolves a leading ~/ so the registry stores a path the
// daemon can use verbatim.
func expandUserPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// printSatelliteNextSteps is the copy-paste laptop-half block `up` and
// `upgrade` end with. Full laptop-side sync.toml automation is out of scope —
// this points at the template instead.
func printSatelliteNextSteps(user, ip string, freshProvision bool) {
	dest := ip
	if user != "" {
		dest = user + "@" + ip
	}
	fmt.Println()
	fmt.Println("Next steps (laptop side):")
	if freshProvision {
		fmt.Println("  1. Reload the laptop daemon's satellite registry (loads only at boot):")
		fmt.Println("       groved upgrade --global   # or restart your groved service")
		fmt.Println("  2. Tunnel note-sync to the VM's syncd (keep open while syncing):")
		fmt.Printf("       ssh -fN -L 8788:127.0.0.1:8788 %s\n", dest)
		fmt.Println("  3. Point the laptop's sync at the tunnel (PUSH-only) — template:")
		fmt.Println("       cloud/poc/grove-satellite/templates/sync.toml.laptop -> ~/.config/grove/sync.toml")
		fmt.Println("     (bootstrap already fetched the token to ~/.config/grove/sync.token),")
		fmt.Println("     then restart the laptop groved.")
		fmt.Println("  4. Verify the connection:")
		fmt.Println("       grove satellite status")
	} else {
		fmt.Println("  - Nothing to restart here: the laptop daemon reconnects automatically")
		fmt.Println("    (ConnManager backoff); the registry is unchanged.")
		fmt.Println("  - Verify the connection:")
		fmt.Println("       grove satellite status")
		fmt.Println("  - If your note-sync tunnel dropped during the restart, re-open it:")
		fmt.Printf("       ssh -fN -L 8788:127.0.0.1:8788 %s\n", dest)
		fmt.Println("    (laptop sync config stays ~/.config/grove/sync.toml, push-only)")
	}
}
