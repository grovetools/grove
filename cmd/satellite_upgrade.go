package cmd

// grove satellite upgrade — redeploy the grove stack on a running satellite VM.
//
// The manual PoC redeploy runbook, made a verb: read the VM's per-repo HEADs,
// diff against the local ecosystem worktree, ship git bundles for the changed
// repos, then (in ONE generated remote script, like bootstrap) fetch+checkout,
// build, and atomically install on the VM, and finally restart grove-syncd +
// groved. Every SSH/scp invocation pins the registry host_key — never TOFU
// (C2). In the default source mode building happens ON the VM (sync needs
// CGO+fts5); with --prebuilt the binaries are cross-compiled locally instead
// (`grove build --target`, zig cc as the sanctioned cgo cross path) and only
// verified binaries ship — no VM-side git or build. Prebuilt installs record
// their shas in the VM's prebuilt-heads overlay so later deltas compare
// against what is installed, not just the checkout's git HEAD.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
)

// satelliteStageDir is the remote staging dir bundles are scp'd into; the
// deploy script removes it on success, so re-runs stay clean.
var satelliteStageDir = satelliteStageBase() + "/grove-satellite-upgrade"

// satelliteUserBinDir mirrors bootstrap step 4's BIN_DIR (as a remote shell
// expression).
const satelliteUserBinDir = "$HOME/.local/share/grove/bin"

// satelliteRemotePATH matches bootstrap's /etc/profile.d PATH for non-login
// shells (go for builds, the grove bin dir for tooling).
const satelliteRemotePATH = "/usr/local/go/bin:$HOME/.local/share/grove/bin:$PATH"

// satellitePrebuiltHeads is the VM-side overlay recording which sha's
// prebuilt binaries are installed per repo ("<repo> <sha>" lines; the sha
// carries a -dirty suffix for builds from dirty trees). Installed binaries
// can be newer than the checkout's git HEAD, so the prebuilt heads probe
// prefers this file; a source build deletes the repo's line again so the
// overlay never goes stale-authoritative.
const satellitePrebuiltHeads = "$HOME/.local/share/grove/prebuilt-heads"

// prebuiltArchiveName / prebuiltManifestName are the payload files staged on
// the VM by --prebuilt.
const (
	prebuiltArchiveName  = "grove-prebuilt.tar.gz"
	prebuiltManifestName = "grove-prebuilt.manifest"
)

func newSatelliteUpgradeCmd() *cobra.Command {
	var (
		reposFlag     string
		allRepos      bool
		sourceDir     string
		remoteCodeDir string
		dryRun        bool
		assumeYes     bool
		prebuilt      bool
		targetFlag    string
	)
	cmd := cli.NewStandardCommand("upgrade <name>", "Redeploy the grove stack on a satellite VM from local branch tips")
	cmd.Long = `Redeploy the grove stack on a running satellite VM.

Compares each local repo's HEAD against the VM's checkout under the remote
code dir, ships git bundles for the changed repos, then fetches, force-checks-
out, builds (on the VM — sync needs CGO/fts5), and atomically installs the
binaries, and restarts grove-syncd + groved.

Notes:
  - --repos is a force list: every repo named there is rebuilt and reinstalled
    even when already at the local tip (shown as "forced" in the delta table).
    --all forces every registered repo the same way. Without either flag only
    changed repos deploy.
  - a failed repo build no longer aborts the deploy: the remaining repos still
    build and install, the restart still runs, and the command exits nonzero
    with a per-repo summary — rerun with --repos <failed,...> after fixing.
  - compositor (zig) is slow to build and rarely needed; when it changed it is
    HELD unless explicitly listed in --repos (or --all).
  - core is a library: its changes only reach binaries when dependent repos are
    rebuilt — include those dependents in --repos if only core moved.
  - the VM checkout is forced to the shipped tip; uncommitted VM changes to
    tracked files are discarded.
  - --prebuilt cross-compiles the ship set locally (grove build --target,
    default linux/amd64) and ships only the verified binaries — no VM-side
    git or build. Installed shas are tracked in the VM's prebuilt-heads
    overlay; dirty local trees still ship (recorded as <sha>-dirty) but are
    flagged loudly. compositor is a library with no binaries and is never
    shipped prebuilt.
  - repos that link compositor (flow, tuimux, treemux, cx, skills, nav, nb,
    hooks) cross-compile once compositor's per-target zig static libs exist
    (zig/zig-out-<goos>-<goarch> + lib/ghostty-<goos>-<goarch> in the
    compositor repo). grove build --target builds those automatically:
    compositor's make build runs its zig-cross target in an earlier wave than
    its dependents. compositor itself remains a library with no binaries and
    is never shipped prebuilt.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&reposFlag, "repos", "", "Comma-separated repos to deploy; listed repos are forced even when already up-to-date (default: every changed repo)")
	cmd.Flags().BoolVar(&allRepos, "all", false, "Force-deploy every repo, including ones already at the local tip")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "Local ecosystem worktree root (default: the go.work root above cwd)")
	cmd.Flags().StringVar(&remoteCodeDir, "remote-code-dir", defaultRemoteCodeDir, "Superrepo checkout on the VM")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Stop after printing the local-vs-VM delta table")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the deploy and restart confirmation prompts")
	cmd.Flags().BoolVar(&prebuilt, "prebuilt", false, "Cross-compile locally and ship verified binaries — no VM-side git or build")
	cmd.Flags().StringVar(&targetFlag, "target", "linux/amd64", "Prebuilt cross-compile target as <goos>/<goarch> (the VM arch)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		entry, ok := loadMergedSatellites()[name]
		if !ok {
			return fmt.Errorf("satellite %q not found in the registry (config or state) — run `grove satellite up %s` first", name, name)
		}
		// Exec-kind satellites (tart, docker) run no groved and no
		// grove-syncd, so the restart step below hard-fails under `set -euo
		// pipefail` AFTER the guest has already been mutated. Refuse before
		// touching it — re-running `up` reinstalls their prebuilt stack, which
		// is the whole of an exec satellite.
		if entry.isExec() {
			return fmt.Errorf("satellite %q is an exec-only satellite (kind %q): it runs no groved or grove-syncd for `upgrade` to restart — reinstall its prebuilt stack with `grove satellite up %s` instead", name, satelliteKindExec, name)
		}

		// Full Tart is prebuilt-only and always linux/arm64. Do not let the
		// upgrade verb's historical GCP linux/amd64 default silently ship an
		// incompatible binary set; GCP behavior remains unchanged.
		fullTart := satelliteProviderRefTarget(entry.ProviderRef) == tartSatelliteTarget
		if fullTart {
			if !prebuilt {
				return fmt.Errorf("full Tart satellite %q requires --prebuilt upgrades (linux/arm64); source upgrades are not supported for this target", name)
			}
			if cmd.Flags().Changed("target") && targetFlag != "linux/arm64" {
				return fmt.Errorf("full Tart satellite %q requires --target linux/arm64, got %q", name, targetFlag)
			}
			targetFlag = "linux/arm64"
		}

		// --prebuilt target (validated up front; --target is meaningless
		// without --prebuilt).
		var target orch.Target
		if prebuilt {
			t, err := orch.ParseTarget(targetFlag)
			if err != nil {
				return fmt.Errorf("--prebuilt: %w", err)
			}
			target = t
		} else if cmd.Flags().Changed("target") {
			return fmt.Errorf("--target only applies to --prebuilt (source mode builds on the VM)")
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

		// Requested repos (empty = all changed; --repos/--all force).
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
		repoSet, forced, err := resolveUpgradeRepoSet(requested, allRepos, localRepos, sourceAbs)
		if err != nil {
			return err
		}

		// Local tips (+ per-repo dirtiness in prebuilt mode: dirty trees are
		// a supported dev loop but must be LOUD, and always ship).
		local := map[string]repoTip{}
		dirty := map[string]bool{}
		for _, r := range repoSet {
			tip, err := localRepoTip(filepath.Join(sourceAbs, r))
			if err != nil {
				return fmt.Errorf("read local HEAD of %s: %w", r, err)
			}
			local[r] = tip
			if prebuilt {
				d, err := localRepoDirty(filepath.Join(sourceAbs, r))
				if err != nil {
					return fmt.Errorf("read local dirtiness of %s: %w", r, err)
				}
				dirty[r] = d
			}
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

		// (a) Remote HEADs → delta table. In prebuilt mode the probe prefers
		// the prebuilt-heads overlay (installed binaries can be newer than
		// the checkout's git HEAD) and dirty local trees always ship.
		fmt.Printf("Reading repo HEADs on %s (%s)...\n", name, ssh.dest())
		headsScript := buildRemoteHeadsScript(remoteCodeDir, repoSet)
		if prebuilt {
			headsScript = buildRemoteHeadsScriptPrebuilt(remoteCodeDir, repoSet)
		}
		out, err := ssh.outputScript(headsScript)
		if err != nil {
			return fmt.Errorf("read remote HEADs: %w", err)
		}
		remote := parseRemoteHeads(out)
		var deltas []repoDelta
		if prebuilt {
			deltas = computeSatellitePrebuiltDelta(repoSet, local, remote, forced, dirty)
		} else {
			deltas = computeSatelliteDelta(repoSet, local, remote, forced)
		}
		printSatelliteDelta(deltas)

		updates := deltasToShip(deltas)
		if held := deltasWithStatus(deltas, deltaStatusHeld); len(held) > 0 {
			fmt.Printf("\nwarning: compositor changed but is HELD — its zig build is slow and rarely needed.\n")
			fmt.Printf("Include it explicitly to deploy it: --repos %s\n", strings.Join(append(deltaRepoNames(updates), "compositor"), ","))
		}
		if held := deltasWithStatus(deltas, deltaStatusHeldPrebuilt); len(held) > 0 {
			fmt.Printf("\nnote: compositor changed but is a library with no binaries — it never ships in --prebuilt mode\n(use a source-mode upgrade to rebuild it and its consumers on the VM).\n")
		}
		for _, d := range updates {
			if d.Repo == "core" {
				fmt.Printf("\nnote: core is a library — its changes only reach binaries via dependent repo rebuilds;\ninclude the dependents (e.g. grove,daemon,sync,flow,nb) in --repos if they show up-to-date.\n")
			}
		}
		dirtyShipped := deltaRepoNames(deltasWithStatus(deltas, deltaStatusDirty))
		if len(dirtyShipped) > 0 {
			fmt.Printf("\nWARNING: DIRTY working tree(s) ship as-is and are recorded as <sha>-dirty on the VM: %s\n", strings.Join(dirtyShipped, ", "))
		}
		if len(updates) == 0 {
			fmt.Printf("\nSatellite %q is up to date with %s — nothing to do.\n", name, sourceAbs)
			return nil
		}
		if dryRun {
			if prebuilt {
				fmt.Printf("\n(dry-run) would cross-build for %s and install %d repo(s): %s\n", target, len(updates), strings.Join(deltaRepoNames(updates), ", "))
			} else {
				fmt.Printf("\n(dry-run) would upgrade %d repo(s): %s\n", len(updates), strings.Join(deltaRepoNames(updates), ", "))
			}
			return nil
		}

		if !assumeYes {
			prompt := fmt.Sprintf("Ship, build, and install %d repo(s) on %q? (force-checkout discards uncommitted VM changes)", len(updates), name)
			if prebuilt {
				prompt = fmt.Sprintf("Cross-build for %s and install %d prebuilt repo(s) on %q?", target, len(updates), name)
				if len(dirtyShipped) > 0 {
					prompt = fmt.Sprintf("Cross-build for %s and install %d prebuilt repo(s) on %q — INCLUDING DIRTY TREES (%s)?", target, len(updates), name, strings.Join(dirtyShipped, ", "))
				}
			}
			if err := confirmOrAbort(prompt); err != nil {
				return err
			}
		}

		var deployErr error
		var shippedRepos []string
		if prebuilt {
			// (b')–(e') Build locally with the grove build orchestrator,
			// verify + package the target binaries, scp, and run the
			// prebuilt install script (sha256-verify, install, record
			// prebuilt-heads). Local build failures drop the repo from the
			// ship set; if everything drops the VM is never touched.
			shippedRepos, deployErr, err = deploySatellitePrebuilt(ssh, name, sourceAbs, target, updates, dirty)
			if err != nil {
				return err
			}
		} else {
			shippedRepos = deltaRepoNames(updates)

			// (b) Bundles → scp.
			bundleDir, err := os.MkdirTemp("", "grove-satellite-bundles-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(bundleDir) }()
			var bundlePaths []string
			for _, d := range updates {
				if d.Status == deltaStatusForced {
					continue // the VM already has the sha — the deploy script re-checkouts and rebuilds without a bundle
				}
				bundlePath := filepath.Join(bundleDir, d.Repo+".bundle")
				if err := createRepoBundle(filepath.Join(sourceAbs, d.Repo), bundlePath, d.Branch, d.RemoteSHA); err != nil {
					return fmt.Errorf("bundle %s: %w", d.Repo, err)
				}
				bundlePaths = append(bundlePaths, bundlePath)
			}
			if len(bundlePaths) > 0 {
				fmt.Printf("\nShipping %d bundle(s) to %s:%s ...\n", len(bundlePaths), name, satelliteStageDir)
				if err := ssh.runCommand("mkdir -p " + satelliteStageDir); err != nil {
					return fmt.Errorf("create remote stage dir: %w", err)
				}
				if err := ssh.scp(bundlePaths, satelliteStageDir+"/"); err != nil {
					return fmt.Errorf("scp bundles: %w", err)
				}
			}

			// (c)–(e) One generated remote script: fetch+checkout (self-healing),
			// build, install (temp+mv, ETXTBSY-safe). Each repo is failure-isolated
			// inside the script: a failed build records the repo and moves on, so
			// every repo that DID build still gets installed — and we still restart
			// below to put those binaries live. The script exits nonzero after its
			// per-repo summary when anything failed.
			fmt.Println("\nRunning remote deploy script (fetch, checkout, build, install)...")
			deployErr = ssh.runScript(buildSatelliteDeployScript(remoteCodeDir, satelliteStageDir, updates))
			if deployErr != nil {
				fmt.Fprintln(os.Stderr, "\nwarning: deploy failed for some repos (per-repo summary above) — proceeding to restart so the repos that did build go live.")
			}
		}

		// (f) Restart, gated.
		if !assumeYes {
			if !confirmYesNo(fmt.Sprintf("Restart grove-syncd and groved on %q now?", name)) {
				fmt.Println("\nBinaries are installed but services still run the old ones. Restart manually with:")
				if !satelliteStdinIsTTY() {
					fmt.Println("  (or re-run with --yes — the prompt cannot be answered with stdin off a terminal)")
				}
				fmt.Printf("  ssh %s 'sudo systemctl restart grove-syncd'\n", ssh.dest())
				fmt.Printf("  ssh %s 'XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart groved'\n", ssh.dest())
				if deployErr != nil {
					return fmt.Errorf("deploy failed for some repos — rerun with --repos <failed,...> after fixing: %w", deployErr)
				}
				return nil
			}
		}
		fmt.Println("\nRestarting grove-syncd + groved...")
		if err := ssh.runScript(buildSatelliteRestartScript()); err != nil {
			if deployErr != nil {
				return fmt.Errorf("remote restart/verify failed (%v) after a deploy with failed repos: %w", err, deployErr)
			}
			return fmt.Errorf("remote restart/verify failed: %w", err)
		}
		if deployErr != nil {
			return fmt.Errorf("services restarted, but deploy failed for some repos (per-repo summary above) — rerun with --repos <failed,...> after fixing: %w", deployErr)
		}

		// (g) Verified by the restart script (is-active); laptop half.
		fmt.Printf("\nSatellite %q upgraded (%s).\n", name, strings.Join(shippedRepos, ", "))
		printSatelliteNextSteps(false, false, entry.SyncLocalPort)
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

// resolveUpgradeRepoSet turns the --repos/--all flags into the repo set to
// probe plus the force bit: naming repos explicitly (either way) means "deploy
// these regardless of delta status" — up-to-date repos come back as forced.
// No flags = every repo, changed-only.
func resolveUpgradeRepoSet(requested []string, all bool, localRepos []string, sourceAbs string) (repos []string, forced bool, err error) {
	if all && len(requested) > 0 {
		return nil, false, fmt.Errorf("--all and --repos are mutually exclusive")
	}
	if all {
		return localRepos, true, nil
	}
	if len(requested) == 0 {
		return localRepos, false, nil
	}
	for _, r := range requested {
		if !containsString(localRepos, r) {
			return nil, false, fmt.Errorf("--repos %q: no git repo %s/%s", r, sourceAbs, r)
		}
	}
	return requested, true, nil
}

// computeSatellitePrebuiltDelta is the --prebuilt variant of
// computeSatelliteDelta: the remote sha comes from the prebuilt-heads overlay
// when present (so it may carry a -dirty suffix and never match a clean local
// sha — correctly re-shipping the clean build), a dirty local tree always
// ships even at a matching HEAD, and compositor is never shippable (library,
// no bin/) regardless of --repos/--all. MISSING/ERROR semantics are unchanged.
func computeSatellitePrebuiltDelta(repos []string, local map[string]repoTip, remote map[string]string, forced bool, dirty map[string]bool) []repoDelta {
	deltas := computeSatelliteDelta(repos, local, remote, forced)
	for i := range deltas {
		d := &deltas[i]
		wouldShip := d.Status == deltaStatusUpdate || d.Status == deltaStatusForced || d.Status == deltaStatusHeld
		if d.Repo == "compositor" {
			if wouldShip {
				d.Status = deltaStatusHeldPrebuilt
			}
			continue
		}
		if dirty[d.Repo] && (wouldShip || d.Status == deltaStatusUpToDate) {
			d.Status = deltaStatusDirty
		}
	}
	return deltas
}

// --- remote script generation (pure; exercised by unit tests) ---

// buildRemoteHeadsScriptPrebuilt is the --prebuilt heads probe: per-repo sha
// resolution prefers the prebuilt-heads overlay entry (installed binaries can
// be newer than the checkout), falling back to the checkout's git HEAD with
// the same MISSING/ERROR sentinels as the source probe. Still read-only.
func buildRemoteHeadsScriptPrebuilt(remoteCodeDir string, repos []string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "HEADS=\"%s\"\n", satellitePrebuiltHeads)
	// Last entry wins, mirroring the append-then-rewrite install flow.
	b.WriteString("head_of() { [ -f \"$HEADS\" ] && awk -v r=\"$1\" '$1==r{s=$2} END{if(s)print s}' \"$HEADS\"; return 0; }\n")
	for _, r := range repos {
		fmt.Fprintf(&b, "s=\"$(head_of %s)\"\n", r)
		fmt.Fprintf(&b, "if [ -n \"$s\" ]; then echo \"%s $s\"; elif [ -e \"$CODE/%s/.git\" ]; then echo \"%s $(git -C \"$CODE/%s\" rev-parse HEAD 2>/dev/null || echo ERROR)\"; else echo \"%s MISSING\"; fi\n", r, r, r, r, r)
	}
	return b.String()
}

// buildSatelliteDeployScript generates the single remote script covering steps
// (c)-(e): fetch from the shipped bundles, force-checkout the tips (with the
// bootstrap script's empty-worktree self-heal), build on the VM, and install
// binaries atomically (copy-to-temp + mv dodges ETXTBSY on running binaries;
// grove-syncd goes to /usr/local/bin via sudo the same way).
//
// Every repo is failure-isolated (no global set -e): a failed checkout or
// build records the repo and CONTINUES, every repo that built still installs,
// and the script ends with a per-repo outcome summary (built+installed /
// build FAILED / skipped / ...), exiting nonzero if anything failed — the
// caller still restarts services so the successful binaries go live. Build
// output goes to per-repo logs under $STAGE/build-logs; a failure surfaces
// its tail inline and the stage dir (with logs) is kept for inspection.
// Forced repos ship no bundle (the sha is already on the VM), so update_repo
// only fetches when its bundle exists. Idempotent: a re-run re-fetches
// no-ops, re-checkouts the same sha, rebuilds, reinstalls.
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
	b.WriteString("# generated by `grove satellite upgrade` — idempotent, per-repo failure-isolated\n")
	b.WriteString("set -uo pipefail\n")
	fmt.Fprintf(&b, "export PATH=\"%s\"\n", satelliteRemotePATH)
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "STAGE=%q\n", stageDir)
	fmt.Fprintf(&b, "BIN_DIR=\"%s\"\n", satelliteUserBinDir)
	fmt.Fprintf(&b, "HEADS=\"%s\"\n", satellitePrebuiltHeads)
	b.WriteString(`LOG_DIR="$STAGE/build-logs"
mkdir -p "$BIN_DIR" "$LOG_DIR" || exit 1

FAILED_REPOS=""
SUMMARY=""
mark_failed() { FAILED_REPOS="$FAILED_REPOS $1"; }
repo_failed() { case " $FAILED_REPOS " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
record() { SUMMARY="$SUMMARY  $1: $2"$'\n'; }

clear_prebuilt_head() { # <repo> — a source build supersedes any prebuilt overlay entry
  [ -f "$HEADS" ] || return 0
  { grep -v "^$1 " "$HEADS" || true; } > "$HEADS.tmp" && mv "$HEADS.tmp" "$HEADS"
}

update_repo() { # <repo> <ref> <sha> — fetch from bundle (if shipped), force-checkout, self-heal
  local repo="$1" ref="$2" sha="$3"
  local dir="$CODE/$repo"
  echo "==> $repo: fetch + checkout ${sha:0:12}"
  # forced repos ship no bundle: the sha is already on the VM.
  if [ -f "$STAGE/$repo.bundle" ]; then
    git -C "$dir" fetch "$STAGE/$repo.bundle" "$ref" || return 1
  fi
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
    git -C "$dir" reset --hard "$sha" || return 1
  fi
  [ "$(git -C "$dir" rev-parse HEAD)" = "$sha" ]
}

checkout_repo() { # <repo> <ref> <sha> — failure-isolated: a bad checkout only excludes this repo
  if ! update_repo "$1" "$2" "$3"; then
    echo "==> $1: checkout FAILED — excluded from build, continuing" >&2
    mark_failed "$1"
    record "$1" "checkout FAILED"
  fi
}

build_repo() { # <repo> — mirror bootstrap step 4 (build ON the VM; sync needs CGO+fts5)
  local repo="$1"
  BUILD_RESULT=built
  cd "$CODE/$repo" || return 1
  if [ "$repo" = compositor ]; then
    make zig
  elif [ -f Makefile ] && grep -q '^build:' Makefile; then
    make build
  elif [ -f go.mod ]; then
    mkdir -p bin && go build -o bin/ ./...
  else
    echo "==> $repo: no build recipe (content-only repo; skipping build)"
    BUILD_RESULT=skipped
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
    cp "$f" "$BIN_DIR/.$b.tmp" || return 1
    mv -f "$BIN_DIR/.$b.tmp" "$BIN_DIR/$b" || return 1
    echo "installed $b -> $BIN_DIR"
  done
  return 0
}

build_install_repo() { # <repo> — failure-isolated: a failed build records and CONTINUES
  local repo="$1"
  local log="$LOG_DIR/$repo.log"
  if repo_failed "$repo"; then
    return 0 # checkout already failed; recorded above
  fi
  echo "==> $repo: build"
  if ! build_repo "$repo" >"$log" 2>&1; then
    echo "==> $repo: build FAILED — continuing with the remaining repos" >&2
    echo "--- tail of $log ---" >&2
    tail -n 40 "$log" >&2
    mark_failed "$repo"
    record "$repo" "build FAILED"
    return 0
  fi
  cat "$log"
  if [ "$BUILD_RESULT" = skipped ]; then
    record "$repo" "skipped (no build recipe)"
    return 0
  fi
  if install_bins "$repo"; then
    record "$repo" "built+installed"
    clear_prebuilt_head "$repo"
  else
    echo "==> $repo: install FAILED — continuing with the remaining repos" >&2
    mark_failed "$repo"
    record "$repo" "install FAILED"
  fi
  return 0
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
		fmt.Fprintf(&b, "checkout_repo %s %s %s\n", d.Repo, ref, d.LocalSHA)
	}
	b.WriteString("\n")
	for _, d := range ordered {
		fmt.Fprintf(&b, "build_install_repo %s\n", d.Repo)
	}
	if syncIncluded {
		b.WriteString(`
# grove-syncd is a system binary under systemd; sudo temp+mv (a running
# grove-syncd makes in-place copy fail with ETXTBSY).
if ! repo_failed sync; then
  if sudo cp "$CODE/sync/bin/grove-syncd" /usr/local/bin/.grove-syncd.tmp \
     && sudo chmod 0755 /usr/local/bin/.grove-syncd.tmp \
     && sudo mv -f /usr/local/bin/.grove-syncd.tmp /usr/local/bin/grove-syncd; then
    echo "installed grove-syncd -> /usr/local/bin"
  else
    echo "==> sync: grove-syncd system install FAILED" >&2
    mark_failed sync
    record sync "grove-syncd system install FAILED"
  fi
fi
`)
	}
	b.WriteString(`
echo
echo "=== deploy summary ==="
printf '%s' "$SUMMARY"
if [ -n "$FAILED_REPOS" ]; then
  echo "deploy FAILED for:$FAILED_REPOS (build logs kept in $LOG_DIR)" >&2
  exit 1
fi
rm -rf "$STAGE"
echo "deploy complete"
`)
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

// --- prebuilt deploys (--prebuilt): local cross-build, package, install ---

// prebuiltBinary is one packaged executable and its content hash.
type prebuiltBinary struct {
	Name   string
	SHA256 string
}

// prebuiltRepo is one repo's packaged payload. SHA is the local HEAD, with
// "-dirty" appended when the working tree was dirty at build time.
type prebuiltRepo struct {
	Repo     string
	SHA      string
	Binaries []prebuiltBinary
}

// binNameRe keeps binary names safe to embed as bare words in the generated
// install script and manifest lines (no spaces, no colons — the script packs
// "<name>:<sha256>" args).
var binNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// prebuiltBinDir is where a `grove build --target` run left the target's
// binaries: bin/<goos>_<goarch> for a cross target, plain bin/ when the
// target IS the host (native builds get no GROVE_BUILD_OUT injection).
func prebuiltBinDir(repoDir string, target orch.Target) string {
	if target.IsNative() {
		return filepath.Join(repoDir, "bin")
	}
	return filepath.Join(repoDir, filepath.FromSlash(target.OutDir()))
}

// collectPrebuiltBinaries gathers the regular executable files a build left
// in the repo's target bin dir, hashing each with sha256. A missing dir is
// not an error — it returns empty, which the caller turns into the hard
// "Makefile ignores GROVE_BUILD_OUT" warning.
func collectPrebuiltBinaries(repoDir string, target orch.Target) ([]prebuiltBinary, error) {
	dir := prebuiltBinDir(repoDir, target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var bins []prebuiltBinary
	for _, e := range entries {
		if !e.Type().IsRegular() || !binNameRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		sum, err := fileSHA256(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		bins = append(bins, prebuiltBinary{Name: e.Name(), SHA256: sum})
	}
	return bins, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildPrebuiltManifest renders the manifest shipped alongside the archive:
// one "<repo> <sha> <binary> <sha256>" line per binary (sha carries the
// -dirty suffix for dirty trees).
func buildPrebuiltManifest(repos []prebuiltRepo) string {
	var b strings.Builder
	for _, r := range repos {
		for _, bin := range r.Binaries {
			fmt.Fprintf(&b, "%s %s %s %s\n", r.Repo, r.SHA, bin.Name, bin.SHA256)
		}
	}
	return b.String()
}

// writePrebuiltArchive tars the collected binaries as <repo>/<binary>
// (preserving exec mode) and gzips the result to outPath.
func writePrebuiltArchive(outPath, sourceAbs string, target orch.Target, repos []prebuiltRepo) (err error) {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, r := range repos {
		dir := prebuiltBinDir(filepath.Join(sourceAbs, r.Repo), target)
		for _, bin := range r.Binaries {
			p := filepath.Join(dir, bin.Name)
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = r.Repo + "/" + bin.Name
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, src)
			_ = src.Close()
			if err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// buildSatellitePrebuiltInstallScript generates the remote install script for
// --prebuilt, reusing the deploy script's idioms (set -uo pipefail, per-repo
// failure isolation with record/repo summary, exit 1 keeps the stage dir):
// untar the archive, then per repo verify EVERY binary's sha256 (one mismatch
// excludes all of that repo's binaries), install user binaries via
// copy-to-temp + mv (ETXTBSY-safe), grove-syncd via the sudo temp+mv variant
// to /usr/local/bin, and on success rewrite the repo's prebuilt-heads line
// atomically (tmp + mv). No git, no VM build, no bundles. Idempotent: a
// re-run re-verifies and re-installs the same binaries.
func buildSatellitePrebuiltInstallScript(stageDir string, repos []prebuiltRepo) string {
	var b strings.Builder
	b.WriteString("# generated by `grove satellite upgrade --prebuilt` — idempotent, per-repo failure-isolated\n")
	b.WriteString("set -uo pipefail\n")
	fmt.Fprintf(&b, "STAGE=%q\n", stageDir)
	fmt.Fprintf(&b, "BIN_DIR=\"%s\"\n", satelliteUserBinDir)
	fmt.Fprintf(&b, "HEADS=\"%s\"\n", satellitePrebuiltHeads)
	fmt.Fprintf(&b, "ARCHIVE=\"$STAGE/%s\"\n", prebuiltArchiveName)
	b.WriteString(`mkdir -p "$BIN_DIR" || exit 1
tar -xzf "$ARCHIVE" -C "$STAGE" || exit 1

FAILED_REPOS=""
SUMMARY=""
mark_failed() { FAILED_REPOS="$FAILED_REPOS $1"; }
repo_failed() { case " $FAILED_REPOS " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
record() { SUMMARY="$SUMMARY  $1: $2"$'\n'; }

record_head() { # <repo> <sha> — atomic overlay rewrite: tmp + mv
  touch "$HEADS" || return 1
  { grep -v "^$1 " "$HEADS" || true; echo "$1 $2"; } > "$HEADS.tmp" || return 1
  mv "$HEADS.tmp" "$HEADS"
}

install_bin() { # <repo> <name> — copy-to-temp + mv (atomic; no ETXTBSY)
  local repo="$1" b="$2" f
  f="$STAGE/$repo/$b"
  if [ "$b" = grove-syncd ]; then
    # system binary under systemd; sudo temp+mv to /usr/local/bin
    sudo cp "$f" /usr/local/bin/.grove-syncd.tmp \
      && sudo chmod 0755 /usr/local/bin/.grove-syncd.tmp \
      && sudo mv -f /usr/local/bin/.grove-syncd.tmp /usr/local/bin/grove-syncd \
      && echo "installed grove-syncd -> /usr/local/bin"
  else
    cp "$f" "$BIN_DIR/.$b.tmp" && chmod 0755 "$BIN_DIR/.$b.tmp" \
      && mv -f "$BIN_DIR/.$b.tmp" "$BIN_DIR/$b" \
      && echo "installed $b -> $BIN_DIR"
  fi
}

deploy_repo() { # <repo> <sha> <binary>:<sha256>... — verify ALL, then install, then record head
  local repo="$1" sha="$2"
  shift 2
  local spec name sum
  echo "==> $repo: verify + install ${sha:0:12}"
  for spec in "$@"; do
    name="${spec%%:*}"
    sum="${spec##*:}"
    if [ "$(sha256sum "$STAGE/$repo/$name" 2>/dev/null | awk '{print $1}')" != "$sum" ]; then
      echo "==> $repo: sha256 MISMATCH for $name — no $repo binaries installed" >&2
      mark_failed "$repo"
      record "$repo" "sha256 mismatch ($name)"
      return 0
    fi
  done
  for spec in "$@"; do
    name="${spec%%:*}"
    if ! install_bin "$repo" "$name"; then
      echo "==> $repo: install FAILED ($name) — continuing with the remaining repos" >&2
      mark_failed "$repo"
      record "$repo" "install FAILED ($name)"
      return 0
    fi
  done
  if record_head "$repo" "$sha"; then
    record "$repo" "installed (${sha:0:12})"
  else
    echo "==> $repo: prebuilt-heads update FAILED" >&2
    mark_failed "$repo"
    record "$repo" "heads update FAILED"
  fi
  return 0
}
`)
	b.WriteString("\n")
	for _, r := range repos {
		fmt.Fprintf(&b, "deploy_repo %s %s", r.Repo, r.SHA)
		for _, bin := range r.Binaries {
			fmt.Fprintf(&b, " %s:%s", bin.Name, bin.SHA256)
		}
		b.WriteString("\n")
	}
	b.WriteString(`
echo
echo "=== prebuilt install summary ==="
printf '%s' "$SUMMARY"
if [ -n "$FAILED_REPOS" ]; then
  echo "install FAILED for:$FAILED_REPOS (stage kept at $STAGE)" >&2
  exit 1
fi
rm -rf "$STAGE"
echo "prebuilt install complete"
`)
	return b.String()
}

// tailLines returns the last n non-empty-trimmed lines of s, for surfacing a
// failed local build's output without dumping everything.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// deploySatellitePrebuilt runs the --prebuilt deploy: cross-build the ship
// set locally through the grove build orchestrator (wave-ordered, parallel —
// satellite upgrade finally IS grove build), collect + hash the target
// binaries, package manifest + tar.gz, scp them over the pinned connection,
// and run the remote install script.
//
// Per-repo isolation mirrors source mode: a repo that fails to build locally
// (or whose Makefile ignores GROVE_BUILD_OUT) is reported and dropped, not
// fatal — but if NOTHING survives, it returns a fatal error before touching
// the VM. The returned deployErr aggregates dropped repos and remote install
// failures; fatal aborts the upgrade.
func deploySatellitePrebuilt(ssh *satelliteSSH, satName, sourceAbs string, target orch.Target, updates []repoDelta, dirty map[string]bool) (shipped []string, deployErr error, fatal error) {
	fmt.Printf("\nCross-building %d repo(s) for %s (grove build --target %s, wave-ordered)...\n", len(updates), target, target)
	results, err := BuildReposForTargetLocal(context.Background(), sourceAbs, deltaRepoNames(updates), target, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("local cross-build: %w", err)
	}
	byRepo := map[string]orch.TaskResult{}
	for _, r := range results {
		byRepo[r.Job.Name] = r
	}

	var pkgRepos []prebuiltRepo
	var dropped []string
	for _, d := range updates {
		res, ok := byRepo[d.Repo]
		switch {
		case !ok:
			dropped = append(dropped, d.Repo)
			fmt.Fprintf(os.Stderr, "warning: %s: no local build result — dropped from the ship set\n", d.Repo)
			continue
		case res.Err != nil:
			dropped = append(dropped, d.Repo)
			fmt.Fprintf(os.Stderr, "warning: %s: local build FAILED — dropped from the ship set\n%s\n", d.Repo, tailLines(string(res.Output), 40))
			continue
		case res.Skipped:
			fmt.Printf("%s: no build recipe — nothing to ship\n", d.Repo)
			continue
		case res.Cached:
			fmt.Printf("%s: build cached (%s already current)\n", d.Repo, target.Pair())
		}
		bins, err := collectPrebuiltBinaries(filepath.Join(sourceAbs, d.Repo), target)
		if err != nil {
			return nil, nil, fmt.Errorf("collect %s binaries: %w", d.Repo, err)
		}
		if len(bins) == 0 {
			dropped = append(dropped, d.Repo)
			fmt.Fprintf(os.Stderr, "WARNING: %s built for %s but produced no executable in %s — its Makefile ignores GROVE_BUILD_OUT; excluded from this prebuilt deploy\n", d.Repo, target, target.OutDir())
			continue
		}
		sha := d.LocalSHA
		if dirty[d.Repo] {
			sha += "-dirty"
		}
		pkgRepos = append(pkgRepos, prebuiltRepo{Repo: d.Repo, SHA: sha, Binaries: bins})
	}
	if len(pkgRepos) == 0 {
		return nil, nil, fmt.Errorf("no repos left to ship (all failed to build or produced no %s binaries) — VM untouched", target.OutDir())
	}

	// Package: manifest + one tar.gz in a local temp dir, then scp both.
	pkgDir, err := os.MkdirTemp("", "grove-satellite-prebuilt-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(pkgDir) }()
	manifestPath := filepath.Join(pkgDir, prebuiltManifestName)
	if err := os.WriteFile(manifestPath, []byte(buildPrebuiltManifest(pkgRepos)), 0o644); err != nil {
		return nil, nil, err
	}
	archivePath := filepath.Join(pkgDir, prebuiltArchiveName)
	if err := writePrebuiltArchive(archivePath, sourceAbs, target, pkgRepos); err != nil {
		return nil, nil, fmt.Errorf("package prebuilt archive: %w", err)
	}

	binCount := 0
	for _, r := range pkgRepos {
		binCount += len(r.Binaries)
	}
	fmt.Printf("\nShipping %d binaries for %d repo(s) to %s:%s ...\n", binCount, len(pkgRepos), satName, satelliteStageDir)
	if err := ssh.runCommand("mkdir -p " + satelliteStageDir); err != nil {
		return nil, nil, fmt.Errorf("create remote stage dir: %w", err)
	}
	if err := ssh.scp([]string{manifestPath, archivePath}, satelliteStageDir+"/"); err != nil {
		return nil, nil, fmt.Errorf("scp prebuilt payload: %w", err)
	}

	fmt.Println("\nRunning remote install script (verify sha256, install, record prebuilt-heads)...")
	deployErr = ssh.runScript(buildSatellitePrebuiltInstallScript(satelliteStageDir, pkgRepos))
	if deployErr != nil {
		fmt.Fprintln(os.Stderr, "\nwarning: install failed for some repos (per-repo summary above) — proceeding to restart so the repos that did install go live.")
	}
	if len(dropped) > 0 {
		dropErr := fmt.Errorf("%d repo(s) never shipped (local build/collect failures): %s", len(dropped), strings.Join(dropped, ", "))
		if deployErr != nil {
			deployErr = fmt.Errorf("%v; %w", dropErr, deployErr)
		} else {
			deployErr = dropErr
		}
	}
	shipped = make([]string, 0, len(pkgRepos))
	for _, r := range pkgRepos {
		shipped = append(shipped, r.Repo)
	}
	return shipped, deployErr, nil
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

// outputCommand runs a single remote command with the given stdin payload
// attached, capturing stdout (stderr surfaces in the error). This is how
// secrets reach a remote command: on stdin, never argv (same policy as the
// bootstrap framing).
func (s *satelliteSSH) outputCommand(command, stdin string) (string, error) {
	args := append(s.baseOptions(), "-p", s.port, s.dest(), command)
	cmd := exec.Command("ssh", args...) //nolint:gosec // G204: registry/flag-derived
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// execCommand runs a remote command (empty = an interactive login shell) with
// the CALLER's stdio attached, so the guest's output streams live and stdin can
// be piped in. tty requests a remote pty (-tt, forced because our own stdin may
// not be one). The returned *exec.ExitError carries the remote command's exit
// status, which ssh mirrors.
func (s *satelliteSSH) execCommand(command string, tty bool) error {
	args := s.baseOptions()
	if tty {
		args = append(args, "-tt")
	}
	args = append(args, "-p", s.port, s.dest())
	if command != "" {
		args = append(args, command)
	}
	cmd := exec.Command("ssh", args...) //nolint:gosec // G204: registry/flag-derived
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

// scpFrom is scp's return direction: fetch remote files into localDir with
// the same pinned options (`repos pull` brings bundles back this way).
func (s *satelliteSSH) scpFrom(remotePaths []string, localDir string) error {
	args := append(s.baseOptions(), "-q", "-P", s.port)
	for _, p := range remotePaths {
		args = append(args, s.dest()+":"+p)
	}
	args = append(args, localDir)
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

// printSatelliteNextSteps is the laptop-half block `up` and `upgrade` end
// with. The laptop sync half (token, push-only sync.toml, registry forward
// fields) is automated by `up`, the daemon owns the syncd forward, and `up`
// now hot-reloads the daemon's registry as its final step — registryReloaded
// says whether that POST landed. Only when it didn't (daemon not running, or
// an old groved without /api/satellites/reload) does a manual daemon reload
// remain as a step; there is no tunnel line either way.
func printSatelliteNextSteps(freshProvision, registryReloaded bool, syncPort int) {
	fmt.Println()
	fmt.Println("Next steps (laptop side):")
	if freshProvision && registryReloaded {
		fmt.Println("  - Nothing to reload: the daemon picked up the registry entry and is")
		fmt.Println("    connecting now (ConnManager backoff covers a still-booting VM).")
		if syncPort != 0 {
			fmt.Printf("    (it binds 127.0.0.1:%d and forwards note-sync to the VM's\n", syncPort)
			fmt.Println("     syncd over its pinned SSH connection — no manual tunnel)")
			fmt.Println("    Caveat: the laptop sync ENGINE config (sync.toml) still loads only")
			fmt.Println("    at daemon boot — if this `up` created it for the first time, run")
			fmt.Println("    `groved upgrade --global` once so notes start pushing.")
		}
		fmt.Println("  - Verify the connection:")
		fmt.Println("       grove satellite status")
	} else if freshProvision {
		fmt.Println("  1. Reload the laptop daemon (it was not running, or predates the")
		fmt.Println("     registry hot-reload endpoint):")
		fmt.Println("       groved upgrade --global   # or restart your groved service")
		if syncPort != 0 {
			fmt.Printf("     (it then binds 127.0.0.1:%d and forwards note-sync to the VM's\n", syncPort)
			fmt.Println("      syncd over its pinned SSH connection — no manual tunnel)")
		}
		fmt.Println("  2. Verify the connection:")
		fmt.Println("       grove satellite status")
	} else {
		fmt.Println("  - Nothing to restart here: the laptop daemon reconnects automatically")
		fmt.Println("    (ConnManager backoff) and re-establishes its sync forward; the")
		fmt.Println("    registry is unchanged.")
		fmt.Println("  - Verify the connection:")
		fmt.Println("       grove satellite status")
	}
}
