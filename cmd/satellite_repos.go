package cmd

// `grove satellite repos push` — the git-native laptop→VM repo mirror
// (slice 1; the slice-2 divergence interlock lives in
// computeSatelliteMirrorDelta, and the VM→laptop return path in
// satellite_repos_pull.go). Ships the laptop's PRIMARY checkout tips (committed state only,
// including unpushed commits) as ancestor-checked git bundles over the pinned
// SSH transport, then fetches + force-checkouts them under the VM's flat
// ~/code/grovetools/<repo> layout — the same bundle/probe/delta machinery
// `satellite upgrade` uses (satellite_mirror.go), minus the VM-side build.
// The mirror also leaves the ecosystem root discoverable: a wildcard
// grove.toml manifest and (when the laptop root has one) a minimal go.work
// are seeded if absent, never overwritten when they differ.
//
// `up --prebuilt` runs the same engine as a post-bootstrap step, replacing
// the bootstrap-side placeholder skeleton (grove 0bac7bd): real repos satisfy
// workspace discovery.
//
// Repo-set resolution precedence: --repos flag > [satellites.<name>.repos]
// workspaces > the resolved [satellites.<name>.sync] workspaces (which
// default to {"cloud","grovetools"}).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

// satelliteReposStageDir is the remote staging dir the mirror's bundles are
// scp'd into; the push script removes it on success, so re-runs stay clean.
// Deliberately separate from the upgrade stage dir.
const satelliteReposStageDir = "/tmp/grove-satellite-repos"

// Mirror-only divergence statuses (a VM head that is neither missing nor an
// ancestor of the local tip). The slice-2 interlock splits divergence on ONE
// precise condition — is the VM head OBJECT present locally? — because that,
// not ancestry, decides whether pushing loses history:
//
//   - object present locally (a prior `repos pull` fetched it into
//     refs/satellite/<name>/…, or it arrived via any other fetch): this is
//     ordinary divergence of already-fetched history. Local moved on after
//     the pull; force-checkouting the VM to the laptop tip discards nothing
//     the laptop doesn't hold. Ships as deltaStatusDiverged.
//   - object NOT present locally: the VM has commits the laptop has never
//     fetched — pushing would destroy them forever. Held
//     (deltaStatusHeldUnfetched) unless --force, which restores the old
//     discard behavior loudly (deltaStatusForcedDiverged).
const (
	deltaStatusDiverged       = "update (diverged — VM commits fetched locally)"
	deltaStatusHeldUnfetched  = "held (VM has unfetched commits — run 'grove satellite repos pull', or --force)"
	deltaStatusForcedDiverged = "update (FORCED — VM commits DISCARDED)"
)

// --- [satellites.<name>.repos] config ---

// satelliteReposOptions is the [satellites.<name>.repos] table — grove-CLI-only
// input to the repo mirror, riding alongside the registry entry the same way
// the .sync subtable does. The daemon ignores it.
type satelliteReposOptions struct {
	// Workspaces are the repo names (ecosystem-root subdirectories) mirrored
	// onto the satellite. Unset falls back to the resolved .sync workspaces.
	Workspaces []string `yaml:"workspaces"`
}

// loadSatelliteReposOptions reads [satellites.<name>.repos] from the layered
// grove config (mirrors loadSatelliteSyncOptions). Missing satellite or
// missing repos subtable yields the zero value; a malformed one is an error.
func loadSatelliteReposOptions(name string) (satelliteReposOptions, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return satelliteReposOptions{}, fmt.Errorf("load grove config: %w", err)
	}
	return satelliteReposOptionsFromConfig(cfg, name)
}

// satelliteReposOptionsFromConfig decodes only the repos subtables out of the
// [satellites.*] extension (separate decode from satelliteConfigEntry, same
// stance as satelliteSyncOptionsFromConfig).
func satelliteReposOptionsFromConfig(cfg *config.Config, name string) (satelliteReposOptions, error) {
	var raw map[string]struct {
		Repos satelliteReposOptions `yaml:"repos"`
	}
	if err := cfg.UnmarshalExtension("satellites", &raw); err != nil {
		return satelliteReposOptions{}, fmt.Errorf("parse [satellites.%s.repos]: %w", name, err)
	}
	return raw[name].Repos, nil
}

// resolveSatelliteMirrorRepos resolves the repo set to mirror with
// flag > [.repos] workspaces > resolved [.sync] workspaces precedence.
// syncWorkspaces is the caller's already-resolved sync set (which itself
// falls back to defaultSatelliteSyncWorkspaces) — `up` passes the one its
// --sync-workspaces flag resolved. A set-but-empty flag means "mirror
// nothing" (explicit disable), matching the sync flag's set-to-empty
// semantics.
func resolveSatelliteMirrorRepos(requested []string, flagSet bool, reposCfg satelliteReposOptions, syncWorkspaces []string) []string {
	if flagSet {
		return append([]string(nil), requested...)
	}
	if len(reposCfg.Workspaces) > 0 {
		return append([]string(nil), reposCfg.Workspaces...)
	}
	return append([]string(nil), syncWorkspaces...)
}

// --- delta (pure) ---

// computeSatelliteMirrorDelta diffs local tips against the VM's checkout HEADs
// for the mirror. Unlike the upgrade delta there is no compositor hold: a
// matching sha is up-to-date, a missing repo SHIPS (git init + full bundle),
// and a VM head that is neither missing nor an ancestor of the local tip is
// divergent. The divergence decision (the slice-2 interlock) hinges on object
// PRESENCE, not ancestry:
//
//   - hasObject(repo, sha) true → the VM head was already fetched to the
//     laptop (typically by `repos pull` into refs/satellite/…) — pushing is
//     safe, ships as deltaStatusDiverged (full bundle + force-checkout).
//   - hasObject false → the VM holds commits the laptop has never seen;
//     held (deltaStatusHeldUnfetched) unless force, which ships them into
//     oblivion as deltaStatusForcedDiverged.
//
// A fresh VM (every repo MISSING) never reaches the divergence arm, so `up
// --prebuilt`'s post-bootstrap mirror is unaffected by the interlock.
// isAncestor and hasObject probe the LOCAL repo (injected for testability).
func computeSatelliteMirrorDelta(repos []string, local map[string]repoTip, remote map[string]string, isAncestor, hasObject func(repo, sha string) bool, force bool) []repoDelta {
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
			switch {
			case isAncestor(r, sha):
				d.Status = deltaStatusUpdate
			case hasObject(r, sha):
				// Divergence of FETCHED history: the VM head object exists
				// locally, so its commits are recoverable on the laptop
				// (refs/satellite/… after a pull). NOT blocked — this is the
				// ordinary state right after `repos pull` + local work.
				d.Status = deltaStatusDiverged
			case force:
				d.Status = deltaStatusForcedDiverged
			default:
				// The VM head object is absent locally: unfetched VM commits.
				// Refuse — pull them back (or --force) first.
				d.Status = deltaStatusHeldUnfetched
			}
		}
		deltas = append(deltas, d)
	}
	return deltas
}

// mirrorDeltasToShip selects the repos the mirror actually ships: updates,
// missing-on-VM repos (init + full bundle), and diverged repos (full bundle +
// force-checkout — fetched-divergence or --force, warned about by the
// caller), in input order. Held repos (unfetched VM commits) never ship.
func mirrorDeltasToShip(deltas []repoDelta) []repoDelta {
	var out []repoDelta
	for _, d := range deltas {
		switch d.Status {
		case deltaStatusUpdate, deltaStatusMissingRemote, deltaStatusDiverged, deltaStatusForcedDiverged:
			out = append(out, d)
		}
	}
	return out
}

// mirrorBundleBaseSHA picks the bundle base for a shipped delta: incremental
// from the VM's head for a plain update (the ancestor check already passed),
// full bundle ("" base) for missing and diverged/forced repos — a diverged
// VM head is useless as a base, and re-probing it locally would just re-fail.
func mirrorBundleBaseSHA(d repoDelta) string {
	if d.Status == deltaStatusUpdate {
		return d.RemoteSHA
	}
	return ""
}

// --- ecosystem root files ---

// goWorkGoDirectiveRe is the only shape of `go` directive we re-embed in the
// generated remote script (defense in depth: the value comes from the local
// go.work, but it lands inside generated shell).
var goWorkGoDirectiveRe = regexp.MustCompile(`^go [0-9]+(\.[0-9]+)*$`)

// localGoWorkGoDirective reads the `go <version>` directive from the laptop
// ecosystem root's go.work, so the VM-side seeded go.work matches the local
// toolchain expectation. Empty (no local go.work, or an unexpected directive
// shape) means the mirror skips go.work seeding entirely — no guess is safer
// than a wrong pin.
func localGoWorkGoDirective(sourceAbs string) string {
	data, err := os.ReadFile(filepath.Join(sourceAbs, "go.work"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") && goWorkGoDirectiveRe.MatchString(line) {
			return line
		}
	}
	return ""
}

// --- remote script generation (pure; exercised by unit tests) ---

// buildSatelliteReposPushScript generates the single remote mirror script:
// ensure $CODE exists, then per shipped repo: git init if missing, fetch from
// the staged bundle, force-checkout the shipped branch/sha with the upgrade
// script's self-heal idiom. After the repos land it seeds the ecosystem root
// files (wildcard grove.toml manifest; minimal go.work over the mirrored
// repos carrying a go.mod, when goWorkGo is non-empty — the guard needs the
// mirrored go.mod files on disk, hence the ordering) — never overwriting an
// existing root file that differs — and finally removes the stage dir on
// success.
//
// Every repo is failure-isolated (no global set -e): a failed mirror records
// the repo and CONTINUES, the script ends with a per-repo summary and exits
// nonzero (keeping the stage dir) when anything failed. Idempotent: a re-run
// re-fetches no-ops and re-checkouts the same sha; with nothing shipped it
// only converges the root files.
func buildSatelliteReposPushScript(remoteCodeDir, stageDir string, updates []repoDelta, allRepos []string, goWorkGo string) string {
	var b strings.Builder
	b.WriteString("# generated by `grove satellite repos push` — idempotent, per-repo failure-isolated\n")
	b.WriteString("set -uo pipefail\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "STAGE=%q\n", stageDir)
	b.WriteString(`mkdir -p "$CODE" || exit 1

FAILED_REPOS=""
SUMMARY=""
mark_failed() { FAILED_REPOS="$FAILED_REPOS $1"; }
record() { SUMMARY="$SUMMARY  $1: $2"$'\n'; }

update_repo() { # <repo> <ref> <sha> — init if missing, fetch from bundle, force-checkout, self-heal
  local repo="$1" ref="$2" sha="$3"
  local dir="$CODE/$repo"
  echo "==> $repo: mirror ${sha:0:12}"
  if [ ! -e "$dir/.git" ]; then
    mkdir -p "$dir" || return 1
    git -C "$dir" init -q || return 1
  fi
  if [ -f "$STAGE/$repo.bundle" ]; then
    git -C "$dir" fetch "$STAGE/$repo.bundle" "$ref" || return 1
  fi
  if [ "$ref" = HEAD ]; then
    git -C "$dir" checkout -f --detach "$sha" || true
  else
    git -C "$dir" checkout -f -B "$ref" "$sha" || true
  fi
  # Self-heal aborted checkouts (same idiom as satellite upgrade): an
  # interrupted checkout can leave HEAD moved but the worktree empty apart
  # from .git — and the '|| true' above routes a failed checkout here too.
  if [ "$(git -C "$dir" rev-parse HEAD 2>/dev/null)" != "$sha" ] \
     || [ -z "$(find "$dir" -mindepth 1 -maxdepth 1 ! -name .git -print -quit)" ]; then
    echo "self-heal: hard-resetting $repo to ${sha:0:12}" >&2
    git -C "$dir" reset --hard "$sha" || return 1
  fi
  [ "$(git -C "$dir" rev-parse HEAD)" = "$sha" ]
}

mirror_repo() { # <repo> <ref> <sha> — failure-isolated: a bad repo only excludes itself
  if update_repo "$1" "$2" "$3"; then
    record "$1" "mirrored (${3:0:12})"
  else
    echo "==> $1: mirror FAILED — continuing with the remaining repos" >&2
    mark_failed "$1"
    record "$1" "mirror FAILED"
  fi
}
`)
	b.WriteString("\n")
	for _, d := range updates {
		ref := d.Branch
		if ref == "" {
			ref = "HEAD"
		}
		fmt.Fprintf(&b, "mirror_repo %s %s %s\n", d.Repo, ref, d.LocalSHA)
	}

	// Ecosystem root files, AFTER the repos land (the go.work use-line guard
	// needs the mirrored go.mod files on disk): seed if absent, never
	// overwrite an existing root file that differs.
	b.WriteString(`
# --- ecosystem root files: seed if absent, never overwrite a differing file ---
if [ ! -f "$CODE/grove.toml" ]; then
  printf 'workspaces = ["*"]\n' > "$CODE/grove.toml"
  echo "seeded $CODE/grove.toml (wildcard workspace manifest)"
elif [ "$(cat "$CODE/grove.toml")" != 'workspaces = ["*"]' ]; then
  echo "notice: $CODE/grove.toml exists and differs from the wildcard manifest — left untouched"
fi
`)
	if goWorkGo != "" {
		b.WriteString("if [ ! -f \"$CODE/go.work\" ]; then\n")
		b.WriteString("  {\n")
		fmt.Fprintf(&b, "    printf '%s\\n\\nuse (\\n'\n", goWorkGo)
		for _, r := range allRepos {
			fmt.Fprintf(&b, "    [ -f \"$CODE/%s/go.mod\" ] && printf '\\t./%s\\n'\n", r, r)
		}
		b.WriteString("    printf ')\\n'\n")
		b.WriteString("  } > \"$CODE/go.work\"\n")
		b.WriteString("  echo \"seeded $CODE/go.work (mirrored repos with a go.mod)\"\n")
		b.WriteString("else\n")
		b.WriteString("  echo \"notice: $CODE/go.work already exists — left untouched\"\n")
		b.WriteString("fi\n")
	}
	b.WriteString(`
echo
echo "=== repos push summary ==="
printf '%s' "$SUMMARY"
if [ -n "$FAILED_REPOS" ]; then
  echo "repos push FAILED for:$FAILED_REPOS (stage kept at $STAGE)" >&2
  exit 1
fi
rm -rf "$STAGE"
echo "repos push complete"
`)
	return b.String()
}

// --- the push engine (shared by the verb and `up --prebuilt`) ---

// pushSatelliteReposOverSSH wires pushSatelliteRepos to the pinned transport
// from the registry entry (same C2 never-TOFU stance as every other remote
// satellite step).
func pushSatelliteReposOverSSH(name string, entry satelliteConfigEntry, sourceAbs, remoteCodeDir string, repos []string, strict, dryRun, assumeYes, force bool) error {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-repos-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return fmt.Errorf("satellite %q: %w", name, err)
	}
	return pushSatelliteRepos(ssh, name, sourceAbs, remoteCodeDir, repos, strict, dryRun, assumeYes, force)
}

// pushSatelliteRepos mirrors the laptop's committed repo tips onto the VM:
// probe remote heads, compute the delta, bundle + scp the repos needing an
// update, and run the mirror script (root-file seeding + per-repo fetch/
// force-checkout). COMMITTED-HEAD-ONLY: dirty local trees are warned about
// loudly and ship their HEAD, never working-tree content. strict=false skips
// (with a notice) resolved repo names that are not local git checkouts —
// config-derived workspace names may not all be code repos; an explicit
// --repos list is always strict. force restores the pre-interlock behavior
// for repos whose VM head is unknown locally: ship anyway, DISCARDING the
// unfetched VM commits (loudly).
func pushSatelliteRepos(ssh *satelliteSSH, name, sourceAbs, remoteCodeDir string, repos []string, strict, dryRun, assumeYes, force bool) error {
	if len(repos) == 0 {
		fmt.Println("No repos to mirror (empty repo set) — nothing to do.")
		return nil
	}
	for _, r := range repos {
		if !repoNameRe.MatchString(r) {
			return fmt.Errorf("invalid repo name %q (allowed: A-Za-z0-9._-)", r)
		}
	}

	// Validate against the local ecosystem root; non-strict (config-derived)
	// sets skip non-repo names with a notice.
	var mirror []string
	for _, r := range repos {
		if _, err := os.Stat(filepath.Join(sourceAbs, r, ".git")); err != nil {
			if strict {
				return fmt.Errorf("--repos %q: no git repo %s/%s", r, sourceAbs, r)
			}
			fmt.Printf("(%s: not a git repo under %s — skipped)\n", r, sourceAbs)
			continue
		}
		mirror = append(mirror, r)
	}
	if len(mirror) == 0 {
		fmt.Printf("No mirrorable repos under %s — nothing to do.\n", sourceAbs)
		return nil
	}

	// Local tips + dirtiness. Committed state only: a dirty tree ships its
	// HEAD, loudly (mirroring the prebuilt <sha>-dirty convention's noise).
	local := map[string]repoTip{}
	var dirtyRepos []string
	for _, r := range mirror {
		dir := filepath.Join(sourceAbs, r)
		tip, err := localRepoTip(dir)
		if err != nil {
			return fmt.Errorf("read local HEAD of %s: %w", r, err)
		}
		local[r] = tip
		d, err := localRepoDirty(dir)
		if err != nil {
			return fmt.Errorf("read local dirtiness of %s: %w", r, err)
		}
		if d {
			dirtyRepos = append(dirtyRepos, r)
		}
	}
	if len(dirtyRepos) > 0 {
		fmt.Printf("\nWARNING: DIRTY working tree(s): %s\n", strings.Join(dirtyRepos, ", "))
		fmt.Println("The mirror ships COMMITTED state only (HEAD) — uncommitted changes do NOT reach the VM.")
		fmt.Println("Commit them first if the satellite needs them (cf. the prebuilt <sha>-dirty convention).")
	}

	// Remote heads → delta.
	fmt.Printf("Reading repo HEADs on %s (%s)...\n", name, ssh.dest())
	out, err := ssh.outputScript(buildRemoteHeadsScript(remoteCodeDir, mirror))
	if err != nil {
		return fmt.Errorf("read remote HEADs: %w", err)
	}
	remote := parseRemoteHeads(out)
	deltas := computeSatelliteMirrorDelta(mirror, local, remote,
		func(repo, sha string) bool { return localRepoIsAncestor(filepath.Join(sourceAbs, repo), sha) },
		func(repo, sha string) bool { return localRepoHasObject(filepath.Join(sourceAbs, repo), sha) },
		force)
	printSatelliteDelta(deltas)

	if held := deltasWithStatus(deltas, deltaStatusHeldUnfetched); len(held) > 0 {
		fmt.Printf("\nHELD (unfetched VM commits): %s\n", strings.Join(deltaRepoNames(held), ", "))
		fmt.Println("The VM holds commits the laptop has never fetched — pushing would destroy them.")
		fmt.Printf("Fetch them back first (safe; writes refs/satellite/%s/… only):\n", name)
		fmt.Printf("  grove satellite repos pull %s\n", name)
		fmt.Println("or re-run push with --force to DISCARD the VM-side commits.")
	}
	if diverged := deltasWithStatus(deltas, deltaStatusDiverged); len(diverged) > 0 {
		fmt.Printf("\nnote: diverged but already fetched: %s\n", strings.Join(deltaRepoNames(diverged), ", "))
		fmt.Println("Their VM heads are not ancestors of the local tips, but those commits are")
		fmt.Printf("present locally (refs/satellite/%s/… after a pull) — the mirror force-checkouts\n", name)
		fmt.Println("the laptop tip; nothing is lost.")
	}
	if forced := deltasWithStatus(deltas, deltaStatusForcedDiverged); len(forced) > 0 {
		fmt.Printf("\nWARNING: --force: VM-side commits will be DISCARDED for: %s\n", strings.Join(deltaRepoNames(forced), ", "))
		fmt.Println("Their VM heads hold commits the laptop has NEVER fetched; the mirror")
		fmt.Printf("force-checkouts the laptop tip and the commits are gone. `grove satellite repos pull %s`\n", name)
		fmt.Println("first would have saved them.")
	}
	if errored := deltasWithStatus(deltas, deltaStatusRemoteError); len(errored) > 0 {
		fmt.Printf("\nnote: unreadable VM HEAD for %s — skipped (inspect the VM checkout manually).\n", strings.Join(deltaRepoNames(errored), ", "))
	}

	updates := mirrorDeltasToShip(deltas)
	if dryRun {
		if len(updates) == 0 {
			fmt.Printf("\n(dry-run) satellite %q mirror is up to date with %s — nothing to ship.\n", name, sourceAbs)
		} else {
			fmt.Printf("\n(dry-run) would mirror %d repo(s): %s\n", len(updates), strings.Join(deltaRepoNames(updates), ", "))
		}
		return nil
	}

	if len(updates) == 0 {
		fmt.Printf("\nSatellite %q mirror is up to date with %s — converging root files only.\n", name, sourceAbs)
	} else if !assumeYes {
		prompt := fmt.Sprintf("Mirror %d repo(s) onto %q? (force-checkout discards uncommitted VM changes)", len(updates), name)
		if forced := deltasWithStatus(deltas, deltaStatusForcedDiverged); len(forced) > 0 {
			prompt = fmt.Sprintf("Mirror %d repo(s) onto %q — DISCARDING unfetched VM commits in %s?", len(updates), name, strings.Join(deltaRepoNames(forced), ", "))
		}
		if !confirmYesNo(prompt) {
			return fmt.Errorf("aborted")
		}
	}

	// Bundles → scp. Idempotent re-runs short-circuit here: an up-to-date
	// delta ships no bundles and the script only converges root files.
	if len(updates) > 0 {
		bundleDir, err := os.MkdirTemp("", "grove-satellite-repo-bundles-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(bundleDir) }()
		var bundlePaths []string
		for _, d := range updates {
			bundlePath := filepath.Join(bundleDir, d.Repo+".bundle")
			if err := createRepoBundle(filepath.Join(sourceAbs, d.Repo), bundlePath, d.Branch, mirrorBundleBaseSHA(d)); err != nil {
				return fmt.Errorf("bundle %s: %w", d.Repo, err)
			}
			bundlePaths = append(bundlePaths, bundlePath)
		}
		fmt.Printf("\nShipping %d bundle(s) to %s:%s ...\n", len(bundlePaths), name, satelliteReposStageDir)
		if err := ssh.runCommand("mkdir -p " + satelliteReposStageDir); err != nil {
			return fmt.Errorf("create remote stage dir: %w", err)
		}
		if err := ssh.scp(bundlePaths, satelliteReposStageDir+"/"); err != nil {
			return fmt.Errorf("scp bundles: %w", err)
		}
	}

	fmt.Println("\nRunning remote mirror script (root files, fetch, force-checkout)...")
	script := buildSatelliteReposPushScript(remoteCodeDir, satelliteReposStageDir, updates, mirror, localGoWorkGoDirective(sourceAbs))
	if err := ssh.runScript(script); err != nil {
		return fmt.Errorf("remote mirror script failed (per-repo summary above; stage kept at %s): %w", satelliteReposStageDir, err)
	}
	if len(updates) > 0 {
		fmt.Printf("\nSatellite %q mirrored (%s).\n", name, strings.Join(deltaRepoNames(updates), ", "))
	}
	// Held repos are per-repo isolated (everything else shipped above), but
	// the run as a whole did NOT converge — exit nonzero so callers notice.
	if held := deltasWithStatus(deltas, deltaStatusHeldUnfetched); len(held) > 0 {
		return fmt.Errorf("%d repo(s) held (unfetched VM commits): %s — run `grove satellite repos pull %s` first, or push with --force", len(held), strings.Join(deltaRepoNames(held), ", "), name)
	}
	return nil
}

// --- the verb ---

func newSatelliteReposCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("repos", "Mirror laptop repo checkouts onto a satellite VM")
	cmd.AddCommand(newSatelliteReposPushCmd())
	cmd.AddCommand(newSatelliteReposPullCmd())
	return cmd
}

func newSatelliteReposPushCmd() *cobra.Command {
	var (
		reposFlag     string
		sourceDir     string
		remoteCodeDir string
		dryRun        bool
		assumeYes     bool
		force         bool
	)
	cmd := cli.NewStandardCommand("push <name>", "Ship local git repo tips to the VM as bundles and check them out")
	cmd.Long = `Mirror the laptop's repo checkouts onto a satellite VM, git-natively.

For each repo the local PRIMARY checkout's HEAD (committed state only —
including unpushed commits) is compared against the VM's checkout under the
remote code dir; changed/missing repos ship as git bundles over the pinned SSH
transport and are fetched + force-checked-out on the VM (flat
~/code/grovetools/<repo> layout). The mirror also seeds the ecosystem root
files (wildcard grove.toml manifest; a minimal go.work when the laptop root
has one) if absent — existing root files that differ are left untouched.

Repo-set precedence: --repos flag > [satellites.<name>.repos] workspaces >
the resolved [satellites.<name>.sync] workspaces (default cloud,grovetools):

  [satellites.mysat.repos]
  workspaces = ["grove", "daemon", "core"]

Notes:
  - COMMITTED state only: dirty working trees ship their HEAD, never
    uncommitted content (a loud warning tells you so).
  - a repo whose VM head holds commits the laptop has NEVER fetched is HELD
    (not shipped; the other repos still ship): run
    'grove satellite repos pull <name>' to fetch them into
    refs/satellite/<name>/…, or pass --force to discard them.
  - a VM head that merely diverged from the local tip but is already present
    locally (i.e. previously pulled) is force-checked-out to the laptop tip —
    nothing is lost; the commits stay reachable via refs/satellite/<name>/….
  - idempotent and cheap to re-run: matching shas ship no bundles.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&reposFlag, "repos", "", "Comma-separated repos to mirror (overrides [satellites.<name>.repos]/.sync workspaces)")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "Local ecosystem worktree root (default: the go.work root above cwd)")
	cmd.Flags().StringVar(&remoteCodeDir, "remote-code-dir", defaultRemoteCodeDir, "Ecosystem root on the VM")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Stop after printing the local-vs-VM delta table")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the mirror confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "Ship repos whose VM heads hold unfetched commits, DISCARDING those commits")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		entry, ok := loadMergedSatellites()[name]
		if !ok {
			return fmt.Errorf("satellite %q not found in the registry (config or state) — run `grove satellite up %s` first", name, name)
		}

		requested, err := parseReposFlag(reposFlag)
		if err != nil {
			return err
		}
		reposCfg, err := loadSatelliteReposOptions(name)
		if err != nil {
			return err
		}
		syncCfg, err := loadSatelliteSyncOptions(name)
		if err != nil {
			return err
		}
		flagSet := cmd.Flags().Changed("repos")
		repos := resolveSatelliteMirrorRepos(requested, flagSet, reposCfg, resolveSatelliteSyncWorkspaces(syncCfg, "", false))

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

		// An explicit --repos list is strict (unknown names are errors, like
		// upgrade's --repos); config-derived sets skip non-repos with a notice.
		return pushSatelliteReposOverSSH(name, entry, sourceAbs, remoteCodeDir, repos, flagSet, dryRun, assumeYes, force)
	}
	return cmd
}
