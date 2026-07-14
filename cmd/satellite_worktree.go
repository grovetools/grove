package cmd

// `grove satellite worktree push|pull` — the plan-scoped remote workflow
// (slice 3), built on the repo-mirror primitives (satellite_mirror.go,
// satellite_repos.go, satellite_repos_pull.go). Where `repos push/pull` moves
// the PRIMARY checkouts, these verbs move ONE PLAN's worktree:
//
//   push: for each repo in the plan worktree's repo set, bundle the plan
//         BRANCH tip from the laptop's plan worktree (committed state only)
//         and, VM-side, fetch it into the mirrored BASE repo (which must
//         already exist — `repos push` creates it) and create-or-update a
//         real git worktree for it under the VM's XDG worktree layout:
//
//           <VM WorktreesDir()>/<DirIdentifier(remote-code-dir)>/<worktree>/<repo>
//
//         plus the container's synthetic grove.toml and .grove/workspace
//         marker, so the VM-side flow executor resolves it exactly like a
//         locally created worktree.
//
//   pull: bundle the plan branch back from the VM worktree (worktrees share
//         the base repo's object store, so this IS the repos-pull engine
//         pointed at the container), fetch into refs/satellite/<name>/… in
//         the laptop's plan-worktree repos, and optionally --ff the laptop
//         checkout when that is provably safe.
//
// WHO COMPUTES THE VM-SIDE PATH: the XDG container path embeds
// workspace.DirIdentifier(gitRoot) = pathutil.WorktreeID — a sanitized
// basename plus sha256(NormalizeForLookup(abs gitRoot))[:8], normalized
// (symlinks, case) against the VM's OWN filesystem — which the laptop cannot
// reproduce faithfully in generated shell without duplicating that logic (and
// drifting from it). So both verbs first ask the VM's installed grove binary
// via the hidden plumbing verb `grove internal worktree-path` (existing
// worktree resolves legacy-then-XDG exactly like core FindWorktreePath; a new
// one lands in the XDG layout), and use the single returned path everywhere.
// This deliberately couples the verbs to the VM's binary vintage: `worktree
// push` already requires a provisioned satellite with mirrored repos, and a
// missing/too-old binary fails the whole run with a pointer at
// `grove satellite upgrade --prebuilt` (resolveRemoteWorktreeContainer). The
// returned path is validated laptop-side (absolute, single line, conservative
// charset) before it is embedded into any generated script.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/worktreeregistry"
	"github.com/spf13/cobra"
)

// Stage dirs, deliberately separate from the repos push/pull stages so a
// concurrent repos operation can never eat a worktree bundle (and vice versa).
var (
	satelliteWorktreeStageDir     = satelliteStageBase() + "/grove-satellite-worktree"
	satelliteWorktreePullStageDir = satelliteStageBase() + "/grove-satellite-worktree-pull"
)

// deltaStatusNoBase is the worktree-push-only status for a repo whose BASE
// mirror is absent on the VM: the plan worktree would have nothing to attach
// to. Never ships; `repos push` creates the base first.
const deltaStatusNoBase = "held (no base repo on VM — run 'grove satellite repos push' first)"

// deltaStatusCreateNoBundle is the worktree-push-only status for a repo whose
// plan branch tip IS the VM base repo's tip: the common fresh-plan case where
// a plan spans several repos but this one has no plan commits yet. Every
// object is already VM-side (the worktree attaches to the base's object
// store), so no bundle ships — bundling would produce an empty range and
// `git bundle create` refuses those — but the repo still SHIPS: the remote
// script must create/checkout the VM worktree at that sha.
const deltaStatusCreateNoBundle = "create (no bundle — VM base has the objects)"

// satelliteWorktreeHead is one probed VM repo for the worktree push: the plan
// worktree's HEAD (or the NOBASE/MISSING/ERROR sentinels) and the base
// repo's HEAD ("" when unknown) — the latter only seeds bundle bases.
type satelliteWorktreeHead struct {
	WTSHA   string
	BaseSHA string
}

// --- VM-side container resolution (the plumbing handshake) ---

// satelliteWorktreePathVerb is the hidden plumbing verb (under `grove
// internal`) that computes the VM-side worktree path with the VM's own core
// functions. The literal doubles as the fake-transport interception marker in
// tests.
const satelliteWorktreePathVerb = "internal worktree-path"

// buildSatelliteWorktreePathScript emits the container-resolution script: ask
// the VM's installed grove binary where the plan worktree lives (existing,
// either layout) or would be created (XDG layout). Non-login ssh shells lack
// the grove bin dir, so the script exports satelliteRemotePATH first (same
// idiom as the upgrade deploy script). Sentinels instead of failures so the
// laptop can produce a precise vintage error:
//
//	NOGROVE: no grove binary on the VM's PATH
//	NOVERB : grove exists but does not know the plumbing verb (too old)
func buildSatelliteWorktreePathScript(remoteCodeDir, worktreeName string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "export PATH=\"%s\"\n", satelliteRemotePATH)
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	b.WriteString("if ! command -v grove >/dev/null 2>&1; then echo NOGROVE; exit 0; fi\n")
	fmt.Fprintf(&b, "if OUT=\"$(grove %s --git-root \"$CODE\" --name %q 2>/dev/null)\"; then printf '%%s\\n' \"$OUT\"; else echo NOVERB; fi\n",
		satelliteWorktreePathVerb, worktreeName)
	return b.String()
}

// remoteWorktreePathRe is the conservative charset a plumbing-returned VM
// worktree path must match before it is embedded in a generated script:
// absolute, and nothing a shell could interpret (no whitespace, quotes,
// backslashes, $, backticks, …). The XDG layout only produces [A-Za-z0-9._-]
// components under $HOME-rooted bases, so this loses nothing real.
var remoteWorktreePathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// validateRemoteWorktreePath vets a plumbing-returned path as DATA before it
// becomes part of a script: non-empty, single line, absolute, conservative
// charset, no ".." components.
func validateRemoteWorktreePath(p string) error {
	if p == "" {
		return fmt.Errorf("empty worktree path")
	}
	if strings.ContainsAny(p, "\n\r") {
		return fmt.Errorf("multi-line worktree path %q", p)
	}
	if !remoteWorktreePathRe.MatchString(p) {
		return fmt.Errorf("worktree path %q is not an absolute path in the safe charset [A-Za-z0-9._/-]", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("worktree path %q contains a '..' component", p)
		}
	}
	return nil
}

// resolveRemoteWorktreeContainer asks the VM where the plan worktree
// container lives (or should be created) via the plumbing verb, and validates
// the answer. A missing binary or unknown verb fails the WHOLE run — the
// container path is per-plan, not per-repo, so a stale VM binary affects
// every repo identically and a clear "upgrade first" error beats partial
// state — with a pointer at `grove satellite upgrade --prebuilt`.
func resolveRemoteWorktreeContainer(transport satelliteReposTransport, name, remoteCodeDir, worktreeName string) (string, error) {
	out, err := transport.outputScript(buildSatelliteWorktreePathScript(remoteCodeDir, worktreeName))
	if err != nil {
		return "", fmt.Errorf("resolve VM worktree path: %w", err)
	}
	p := strings.TrimSpace(out)
	switch p {
	case "NOGROVE":
		return "", fmt.Errorf("satellite %q has no grove binary on the VM's PATH — the worktree verbs need it to compute the VM-side worktree layout; run `grove satellite upgrade %s --prebuilt` first", name, name)
	case "NOVERB":
		return "", fmt.Errorf("satellite %q runs a grove binary too old for `grove %s` — run `grove satellite upgrade %s --prebuilt` first", name, satelliteWorktreePathVerb, name)
	}
	if err := validateRemoteWorktreePath(p); err != nil {
		return "", fmt.Errorf("satellite %q returned an unusable VM worktree path: %w (run `grove satellite upgrade %s --prebuilt` if the VM's grove is stale)", name, err, name)
	}
	return p, nil
}

// --- probe (script + parser) ---

// buildSatelliteWorktreeHeadsScript emits the read-only worktree probe: one
// "<repo> <worktree-sha> <base-sha>" line per repo, with sentinels instead of
// failures so one bad repo does not mask the rest:
//
//	NOBASE  - : the base repo is not mirrored on the VM
//	MISSING <base-sha>: base present, plan worktree absent (push creates it)
//	ERROR: an unreadable HEAD
//
// remoteWT is the plumbing-resolved (and validated) VM container path — the
// SAME path the push script uses, so probe and create/update can never
// disagree about where the worktree lives.
func buildSatelliteWorktreeHeadsScript(remoteCodeDir, remoteWT string, repos []string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "WT=%q\n", remoteWT)
	for _, r := range repos {
		fmt.Fprintf(&b,
			"if [ ! -e \"$CODE/%s/.git\" ]; then echo \"%s NOBASE -\"; "+
				"elif [ ! -e \"$WT/%s/.git\" ]; then echo \"%s MISSING $(git -C \"$CODE/%s\" rev-parse HEAD 2>/dev/null || echo ERROR)\"; "+
				"else echo \"%s $(git -C \"$WT/%s\" rev-parse HEAD 2>/dev/null || echo ERROR) $(git -C \"$CODE/%s\" rev-parse HEAD 2>/dev/null || echo ERROR)\"; fi\n",
			r, r, r, r, r, r, r, r)
	}
	return b.String()
}

// parseSatelliteWorktreeHeads decodes buildSatelliteWorktreeHeadsScript
// output. A base-sha of "-" or "ERROR" maps to "" (no usable bundle base).
func parseSatelliteWorktreeHeads(out string) map[string]satelliteWorktreeHead {
	heads := map[string]satelliteWorktreeHead{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 3 {
			continue
		}
		base := fields[2]
		if base == "-" || base == "ERROR" {
			base = ""
		}
		heads[fields[0]] = satelliteWorktreeHead{WTSHA: fields[1], BaseSHA: base}
	}
	return heads
}

// --- delta (pure) ---

// computeSatelliteWorktreeDelta diffs the laptop plan worktree's tips against
// the VM plan worktree's HEADs. Same interlock as the repo mirror
// (mirrorDivergenceStatus — object presence decides held vs shippable), with
// two worktree-specific arms: a missing BASE repo is held with a pointer to
// `repos push`, and a missing plan worktree ships (the script creates it) —
// bundle-less (deltaStatusCreateNoBundle) when the plan tip IS the base tip,
// since the VM then already holds every object the worktree needs.
// isAncestor and hasObject probe the LAPTOP worktree repo (injected for
// testability; linked worktrees share the primary's object store, so a
// `repos pull` also flips this interlock).
func computeSatelliteWorktreeDelta(repos []string, local map[string]repoTip, remote map[string]satelliteWorktreeHead, isAncestor, hasObject func(repo, sha string) bool, force bool) []repoDelta {
	deltas := make([]repoDelta, 0, len(repos))
	for _, r := range repos {
		tip := local[r]
		d := repoDelta{Repo: r, Branch: tip.Branch, LocalSHA: tip.SHA}
		switch head, ok := remote[r]; {
		case !ok || head.WTSHA == "NOBASE":
			d.Status = deltaStatusNoBase
		case head.WTSHA == "MISSING":
			d.Status = deltaStatusMissingRemote
			if head.BaseSHA != "" && head.BaseSHA == tip.SHA {
				d.Status = deltaStatusCreateNoBundle
			}
		case head.WTSHA == "ERROR":
			d.Status = deltaStatusRemoteError
		case head.WTSHA == tip.SHA:
			d.RemoteSHA = head.WTSHA
			d.Status = deltaStatusUpToDate
		default:
			d.RemoteSHA = head.WTSHA
			d.Status = mirrorDivergenceStatus(r, head.WTSHA, isAncestor, hasObject, force)
		}
		deltas = append(deltas, d)
	}
	return deltas
}

// worktreeDeltasToShip selects the repos the worktree push actually ships, in
// input order: the mirror's ship set (mirrorDeltaShips) plus the
// worktree-only bundle-less create arm — those repos transfer no bundle, but
// the remote script still creates/checkouts their VM worktree.
func worktreeDeltasToShip(deltas []repoDelta) []repoDelta {
	var out []repoDelta
	for _, d := range deltas {
		if mirrorDeltaShips(d) || d.Status == deltaStatusCreateNoBundle {
			out = append(out, d)
		}
	}
	return out
}

// worktreeBundleBaseSHA picks the bundle base for a shipped worktree delta:
// incremental from the VM worktree's head for a plain update; for a worktree
// the VM does not have yet, the VM BASE repo's head is the natural candidate
// (the worktree attaches to the base's object store, so everything the base
// holds need not travel) — createRepoBundle ancestor-checks it and falls back
// to a full bundle when it does not qualify. Diverged/forced ship full.
func worktreeBundleBaseSHA(d repoDelta, baseSHA string) string {
	switch d.Status {
	case deltaStatusUpdate:
		return d.RemoteSHA
	case deltaStatusMissingRemote:
		if hexSHARe.MatchString(baseSHA) {
			return baseSHA
		}
	}
	return ""
}

// --- VM-side push script (pure; exercised by unit tests) ---

// buildSatelliteWorktreePushScript generates the remote worktree script:
// ensure the container exists and carries the synthetic ecosystem files
// (grove.toml wildcard manifest + .grove/workspace marker — the same shape
// core workspace.Prepare writes, so VM-side discovery classifies the
// container as an ecosystem worktree), then per shipped repo: refuse when the
// BASE repo is missing (pointing at `repos push`), fetch the staged bundle
// into the base repo's object store, `git worktree add` the plan worktree if
// absent, and force-checkout the plan branch at the shipped sha (create and
// update are the same idiom: checkout -f -B = fetch + reset). Same self-heal
// and per-repo failure isolation as the repos push script; the stage dir is
// removed only on full success. Idempotent: re-running with the same shas
// re-fetches no-ops and re-checkouts the same tips.
//
// remoteWT is the plumbing-resolved (and validated) VM container path — the
// XDG container for a fresh plan worktree; whatever FindWorktreePath returned
// for an existing one.
func buildSatelliteWorktreePushScript(remoteCodeDir, remoteWT, worktreeName, planName, stageDir string, updates []repoDelta, allRepos []string) string {
	var b strings.Builder
	b.WriteString("# generated by `grove satellite worktree push` — idempotent, per-repo failure-isolated\n")
	b.WriteString("set -uo pipefail\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "WT=%q\n", remoteWT)
	fmt.Fprintf(&b, "STAGE=%q\n", stageDir)
	b.WriteString(`mkdir -p "$WT" || exit 1

FAILED_REPOS=""
SUMMARY=""
mark_failed() { FAILED_REPOS="$FAILED_REPOS $1"; }
record() { SUMMARY="$SUMMARY  $1: $2"$'\n'; }

update_worktree() { # <repo> <ref> <sha> — fetch into the base repo, worktree-add if absent, force-checkout the branch
  local repo="$1" ref="$2" sha="$3"
  local base="$CODE/$repo" dir="$WT/$repo"
  echo "==> $repo: worktree ${sha:0:12}"
  if [ ! -e "$base/.git" ]; then
    echo "$repo: no base repo at $base — run 'grove satellite repos push' first" >&2
    return 1
  fi
  if [ -f "$STAGE/$repo.bundle" ]; then
    git -C "$base" fetch "$STAGE/$repo.bundle" "$ref" || return 1
  fi
  if [ ! -e "$dir/.git" ]; then
    git -C "$base" worktree prune 2>/dev/null
    git -C "$base" worktree add --detach "$dir" "$sha" || return 1
  fi
  if [ "$ref" = HEAD ]; then
    git -C "$dir" checkout -f --detach "$sha" || return 1
  else
    git -C "$dir" checkout -f -B "$ref" "$sha" || return 1
  fi
  # Self-heal aborted checkouts (same idiom as the repos push script): an
  # interrupted checkout can leave HEAD moved but the worktree empty apart
  # from .git.
  if [ "$(git -C "$dir" rev-parse HEAD 2>/dev/null)" != "$sha" ] \
     || [ -z "$(find "$dir" -mindepth 1 -maxdepth 1 ! -name .git -print -quit)" ]; then
    echo "self-heal: hard-resetting $repo to ${sha:0:12}" >&2
    git -C "$dir" reset --hard "$sha" || return 1
  fi
  [ "$(git -C "$dir" rev-parse HEAD)" = "$sha" ]
}

worktree_repo() { # <repo> <ref> <sha> — failure-isolated: a bad repo only excludes itself
  if update_worktree "$1" "$2" "$3"; then
    record "$1" "worktree at ${3:0:12}"
  else
    echo "==> $1: worktree update FAILED — continuing with the remaining repos" >&2
    mark_failed "$1"
    record "$1" "worktree update FAILED"
  fi
}
`)
	b.WriteString("\n")
	for _, d := range updates {
		ref := d.Branch
		if ref == "" {
			ref = "HEAD"
		}
		fmt.Fprintf(&b, "worktree_repo %s %s %s\n", d.Repo, ref, d.LocalSHA)
	}

	// Container ecosystem files: seed if absent, never overwrite. Written
	// AFTER the repos so a first push that fails wholesale leaves no
	// half-plausible container behind for the executor to resolve.
	b.WriteString(`
# --- container ecosystem files: seed if absent, never overwrite ---
if [ ! -f "$WT/grove.toml" ]; then
  printf 'workspaces = ["*"]\n' > "$WT/grove.toml"
  echo "seeded $WT/grove.toml (wildcard workspace manifest)"
fi
if [ ! -f "$WT/.grove/workspace" ]; then
  mkdir -p "$WT/.grove"
  {
`)
	fmt.Fprintf(&b, "    printf 'branch: %s\\nplan: %s\\ncreated_at: %%s\\n' \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\"\n", worktreeName, planName)
	b.WriteString("    printf 'owner: %s\\necosystem: true\\nrepos:\\n' \"$CODE\"\n")
	for _, r := range allRepos {
		fmt.Fprintf(&b, "    printf '  - %s\\n'\n", r)
	}
	b.WriteString(`  } > "$WT/.grove/workspace"
  echo "seeded $WT/.grove/workspace (worktree marker)"
fi

echo
echo "=== worktree push summary ==="
printf '%s' "$SUMMARY"
if [ -n "$FAILED_REPOS" ]; then
  echo "worktree push FAILED for:$FAILED_REPOS (stage kept at $STAGE)" >&2
  exit 1
fi
rm -rf "$STAGE"
echo "worktree push complete"
`)
	return b.String()
}

// --- the push engine ---

// pushSatelliteWorktreeOverSSH wires pushSatelliteWorktree to the pinned
// transport from the registry entry (same C2 never-TOFU stance as repos).
func pushSatelliteWorktreeOverSSH(name string, entry satelliteConfigEntry, containerAbs, remoteCodeDir, worktreeName, planName string, repos []string, dryRun, assumeYes, force bool) error {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-worktree-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return fmt.Errorf("satellite %q: %w", name, err)
	}
	return pushSatelliteWorktree(ssh, name, containerAbs, remoteCodeDir, worktreeName, planName, satelliteWorktreeStageDir, repos, dryRun, assumeYes, force)
}

// pushSatelliteWorktree ships the laptop plan worktree's branch tips onto the
// VM as a real worktree: probe the VM plan worktree (and base) heads, compute
// the interlocked delta, bundle + scp the repos needing an update, and run
// the worktree script (base-repo fetch, worktree add/force-checkout,
// container seeding). COMMITTED-STATE-ONLY, same as the repo mirror: dirty
// laptop worktrees warn loudly and ship their HEAD. Repos whose VM worktree
// head is unknown locally are HELD (worktree pull fetches them; --force
// discards); repos whose BASE mirror is absent on the VM are held with a
// pointer to `repos push`. Idempotent: matching shas ship nothing.
func pushSatelliteWorktree(transport satelliteReposTransport, name, containerAbs, remoteCodeDir, worktreeName, planName, stageDir string, repos []string, dryRun, assumeYes, force bool) error {
	for label, v := range map[string]string{"satellite": name, "worktree": worktreeName, "plan": planName} {
		if !repoNameRe.MatchString(v) {
			return fmt.Errorf("invalid %s name %q (allowed: A-Za-z0-9._-)", label, v)
		}
	}
	if len(repos) == 0 {
		fmt.Println("No repos in the plan worktree set — nothing to do.")
		return nil
	}
	for _, r := range repos {
		if !repoNameRe.MatchString(r) {
			return fmt.Errorf("invalid repo name %q (allowed: A-Za-z0-9._-)", r)
		}
	}

	// Every repo must exist as a checkout inside the laptop plan worktree
	// container (a linked worktree's .git is a file; Stat accepts both).
	var mirror []string
	for _, r := range repos {
		if _, err := os.Stat(filepath.Join(containerAbs, r, ".git")); err != nil {
			fmt.Printf("(%s: not a git checkout under %s — skipped)\n", r, containerAbs)
			continue
		}
		mirror = append(mirror, r)
	}
	if len(mirror) == 0 {
		return fmt.Errorf("no git checkouts under %s — is this a plan worktree container?", containerAbs)
	}

	// Local tips + dirtiness (committed state only, loudly).
	local := map[string]repoTip{}
	var dirtyRepos []string
	for _, r := range mirror {
		dir := filepath.Join(containerAbs, r)
		tip, err := localRepoTip(dir)
		if err != nil {
			return fmt.Errorf("read local HEAD of %s: %w", r, err)
		}
		if tip.Branch != "" {
			if err := validateBranchRefSegments(tip.Branch); err != nil {
				return fmt.Errorf("%s: %w", r, err)
			}
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
		fmt.Printf("\nWARNING: DIRTY working tree(s) in the plan worktree: %s\n", strings.Join(dirtyRepos, ", "))
		fmt.Println("The push ships COMMITTED state only (HEAD) — uncommitted changes do NOT reach the VM.")
	}

	// Resolve the VM-side container path with the VM's own grove binary (the
	// XDG layout's identifier hash can only be computed there); a missing or
	// too-old binary fails the run with an upgrade pointer.
	remoteWT, err := resolveRemoteWorktreeContainer(transport, name, remoteCodeDir, worktreeName)
	if err != nil {
		return err
	}
	fmt.Printf("VM plan worktree container: %s\n", remoteWT)

	// Probe VM worktree (and base) heads → interlocked delta.
	fmt.Printf("Reading plan-worktree HEADs on %s (%s)...\n", name, transport.dest())
	out, err := transport.outputScript(buildSatelliteWorktreeHeadsScript(remoteCodeDir, remoteWT, mirror))
	if err != nil {
		return fmt.Errorf("read remote worktree HEADs: %w", err)
	}
	remote := parseSatelliteWorktreeHeads(out)
	deltas := computeSatelliteWorktreeDelta(mirror, local, remote,
		func(repo, sha string) bool { return localRepoIsAncestor(filepath.Join(containerAbs, repo), sha) },
		func(repo, sha string) bool { return localRepoHasObject(filepath.Join(containerAbs, repo), sha) },
		force)
	printSatelliteDelta(deltas)

	if nobase := deltasWithStatus(deltas, deltaStatusNoBase); len(nobase) > 0 {
		fmt.Printf("\nHELD (no base repo on the VM): %s\n", strings.Join(deltaRepoNames(nobase), ", "))
		fmt.Println("The plan worktree attaches to the mirrored base repos — create them first:")
		fmt.Printf("  grove satellite repos push %s --repos %s\n", name, strings.Join(deltaRepoNames(nobase), ","))
	}
	if held := deltasWithStatus(deltas, deltaStatusHeldUnfetched); len(held) > 0 {
		fmt.Printf("\nHELD (unfetched VM commits): %s\n", strings.Join(deltaRepoNames(held), ", "))
		fmt.Println("The VM plan worktree holds commits the laptop has never fetched — pushing would destroy them.")
		fmt.Printf("Fetch them back first (safe; writes refs/satellite/%s/… only):\n", name)
		fmt.Printf("  grove satellite worktree pull %s --plan %s\n", name, planName)
		fmt.Println("or re-run push with --force to DISCARD the VM-side commits.")
	}
	if diverged := deltasWithStatus(deltas, deltaStatusDiverged); len(diverged) > 0 {
		fmt.Printf("\nnote: diverged but already fetched: %s — the push force-checkouts the laptop tip; nothing is lost (refs/satellite/%s/… keeps the VM commits).\n",
			strings.Join(deltaRepoNames(diverged), ", "), name)
	}
	if forced := deltasWithStatus(deltas, deltaStatusForcedDiverged); len(forced) > 0 {
		fmt.Printf("\nWARNING: --force: VM-side commits will be DISCARDED for: %s\n", strings.Join(deltaRepoNames(forced), ", "))
		fmt.Printf("`grove satellite worktree pull %s --plan %s` first would have saved them.\n", name, planName)
	}
	if errored := deltasWithStatus(deltas, deltaStatusRemoteError); len(errored) > 0 {
		fmt.Printf("\nnote: unreadable VM HEAD for %s — skipped (inspect the VM worktree manually).\n", strings.Join(deltaRepoNames(errored), ", "))
	}

	updates := worktreeDeltasToShip(deltas)
	if dryRun {
		if len(updates) == 0 {
			fmt.Printf("\n(dry-run) VM plan worktree %q is up to date — nothing to ship.\n", worktreeName)
		} else {
			fmt.Printf("\n(dry-run) would push %d repo(s): %s\n", len(updates), strings.Join(deltaRepoNames(updates), ", "))
		}
		return nil
	}

	if len(updates) == 0 {
		fmt.Printf("\nVM plan worktree %q is up to date — converging container files only.\n", worktreeName)
	} else if !assumeYes {
		prompt := fmt.Sprintf("Push %d repo(s) of plan worktree %q onto %q? (force-checkout discards uncommitted VM worktree changes)", len(updates), worktreeName, name)
		if forced := deltasWithStatus(deltas, deltaStatusForcedDiverged); len(forced) > 0 {
			prompt = fmt.Sprintf("Push plan worktree %q onto %q — DISCARDING unfetched VM commits in %s?", worktreeName, name, strings.Join(deltaRepoNames(forced), ", "))
		}
		if !confirmYesNo(prompt) {
			return fmt.Errorf("aborted")
		}
	}

	if len(updates) > 0 {
		bundleDir, err := os.MkdirTemp("", "grove-satellite-worktree-bundles-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(bundleDir) }()
		var bundlePaths []string
		for _, d := range updates {
			if d.Status == deltaStatusCreateNoBundle {
				// The VM base repo's tip IS this sha: every object is already
				// there, and an incremental bundle would be empty (git
				// refuses those). The script still creates the worktree.
				continue
			}
			bundlePath := filepath.Join(bundleDir, d.Repo+".bundle")
			if err := createRepoBundle(filepath.Join(containerAbs, d.Repo), bundlePath, d.Branch, worktreeBundleBaseSHA(d, remote[d.Repo].BaseSHA)); err != nil {
				return fmt.Errorf("bundle %s: %w", d.Repo, err)
			}
			bundlePaths = append(bundlePaths, bundlePath)
		}
		if len(bundlePaths) > 0 {
			fmt.Printf("\nShipping %d bundle(s) to %s:%s ...\n", len(bundlePaths), name, stageDir)
			if err := transport.runCommand("mkdir -p " + stageDir); err != nil {
				return fmt.Errorf("create remote stage dir: %w", err)
			}
			if err := transport.scp(bundlePaths, stageDir+"/"); err != nil {
				return fmt.Errorf("scp bundles: %w", err)
			}
		}
	}

	fmt.Println("\nRunning remote worktree script (base fetch, worktree add/checkout, container files)...")
	script := buildSatelliteWorktreePushScript(remoteCodeDir, remoteWT, worktreeName, planName, stageDir, updates, mirror)
	if err := transport.runScript(script); err != nil {
		return fmt.Errorf("remote worktree script failed (per-repo summary above; stage kept at %s): %w", stageDir, err)
	}
	if len(updates) > 0 {
		fmt.Printf("\nPlan worktree %q pushed to %q (%s).\n", worktreeName, name, strings.Join(deltaRepoNames(updates), ", "))
		fmt.Printf("Dispatch with: flow plan run  (auto-routes when the plan designates satellite %q)\n", name)
	}
	var heldMsgs []string
	if held := deltasWithStatus(deltas, deltaStatusHeldUnfetched); len(held) > 0 {
		heldMsgs = append(heldMsgs, fmt.Sprintf("%d repo(s) held (unfetched VM commits): %s — run `grove satellite worktree pull %s --plan %s` first, or push with --force",
			len(held), strings.Join(deltaRepoNames(held), ", "), name, planName))
	}
	if nobase := deltasWithStatus(deltas, deltaStatusNoBase); len(nobase) > 0 {
		heldMsgs = append(heldMsgs, fmt.Sprintf("%d repo(s) held (no base repo on the VM): %s — run `grove satellite repos push %s` first",
			len(nobase), strings.Join(deltaRepoNames(nobase), ", "), name))
	}
	if len(heldMsgs) > 0 {
		return fmt.Errorf("%s", strings.Join(heldMsgs, "; "))
	}
	return nil
}

// --- the pull engine ---

// pullSatelliteWorktreeOverSSH wires pullSatelliteWorktree to the pinned
// transport from the registry entry.
func pullSatelliteWorktreeOverSSH(name string, entry satelliteConfigEntry, containerAbs, remoteCodeDir, worktreeName string, repos []string, dryRun, ff bool) error {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-worktree-pull-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return fmt.Errorf("satellite %q: %w", name, err)
	}
	return pullSatelliteWorktree(ssh, name, containerAbs, remoteCodeDir, worktreeName, satelliteWorktreePullStageDir, repos, dryRun, ff)
}

// pullSatelliteWorktree fetches VM plan-worktree commits back into the laptop
// plan worktree's repos. Because a git worktree shares its base repo's object
// store, this IS the repos-pull engine pointed at the VM container dir: probe
// the worktree heads+branches, bundle the new commits VM-side, scp them back,
// fetch into refs/satellite/<name>/<branch>. Local branches are never touched
// by the pull itself; with ff=true a follow-up pass fast-forwards each laptop
// worktree checkout that is provably safe to move (same branch as the VM,
// clean tree, strictly behind) — and only ever fast-forwards (--ff-only), so
// no local branch is force-moved. Repos that cannot be fast-forwarded keep
// the printed merge hints.
func pullSatelliteWorktree(transport satelliteReposTransport, name, containerAbs, remoteCodeDir, worktreeName, stageDir string, repos []string, dryRun, ff bool) error {
	if !repoNameRe.MatchString(worktreeName) {
		return fmt.Errorf("invalid worktree name %q (allowed: A-Za-z0-9._-)", worktreeName)
	}
	// Same plumbing resolution as push, so pull probes exactly the container
	// push created (legacy-then-XDG for an existing worktree).
	remoteWT, err := resolveRemoteWorktreeContainer(transport, name, remoteCodeDir, worktreeName)
	if err != nil {
		return err
	}
	outcomes, pullErr := pullSatelliteRepos(transport, name, containerAbs, remoteWT, stageDir, repos, false, dryRun)
	if dryRun || !ff {
		if !dryRun && pullErr == nil && len(outcomes) > 0 {
			fmt.Printf("(tip: --ff fast-forwards clean, strictly-behind laptop checkouts to the fetched tips)\n")
		}
		return pullErr
	}

	// --ff pass over every repo whose VM head is now known-local (fetched by
	// this pull, or already present from an earlier one).
	var ffFailed []string
	for _, o := range outcomes {
		dir := filepath.Join(containerAbs, o.Repo)
		tip, err := localRepoTip(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: --ff: read local HEAD: %v\n", o.Repo, err)
			ffFailed = append(ffFailed, o.Repo)
			continue
		}
		dirty, err := localRepoDirty(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: --ff: read local dirtiness: %v\n", o.Repo, err)
			ffFailed = append(ffFailed, o.Repo)
			continue
		}
		behind := tip.SHA != o.SHA && localRepoHEADIsAncestorOf(dir, o.SHA)
		ok, reason := satelliteWorktreeFFDecision(tip.Branch, o.Branch, dirty, tip.SHA, o.SHA, behind)
		if !ok {
			if reason != ffReasonUpToDate {
				fmt.Printf("%s: not fast-forwarded — %s\n", o.Repo, reason)
			}
			continue
		}
		target := o.Ref
		if target == "" {
			target = o.SHA
		}
		if _, err := gitOutput(dir, "merge", "--ff-only", target); err != nil {
			fmt.Fprintf(os.Stderr, "%s: --ff failed: %v\n", o.Repo, err)
			ffFailed = append(ffFailed, o.Repo)
			continue
		}
		fmt.Printf("%s: fast-forwarded %s -> %s\n", o.Repo, shortSHA(tip.SHA), shortSHA(o.SHA))
	}
	if pullErr != nil {
		return pullErr
	}
	if len(ffFailed) > 0 {
		return fmt.Errorf("--ff failed for: %s", strings.Join(ffFailed, ", "))
	}
	return nil
}

// ffReasonUpToDate is the quiet skip: nothing to move.
const ffReasonUpToDate = "already at the VM tip"

// satelliteWorktreeFFDecision decides whether --ff may move a laptop plan
// worktree checkout to the fetched VM tip. Fast-forward ONLY when it is
// provably safe: the checkout is on the same branch the VM had, the tree is
// clean, and the local head is strictly behind the VM head. Everything else
// is a reasoned refusal — pull's merge hints stand. Pure for testability.
func satelliteWorktreeFFDecision(localBranch, vmBranch string, dirty bool, localSHA, vmSHA string, localBehindVM bool) (bool, string) {
	switch {
	case localSHA == vmSHA:
		return false, ffReasonUpToDate
	case vmBranch == "":
		return false, "the VM checkout is detached — integrate manually"
	case localBranch == "":
		return false, "the local checkout is detached — integrate manually"
	case localBranch != vmBranch:
		return false, fmt.Sprintf("local branch %q != VM branch %q — integrate manually", localBranch, vmBranch)
	case dirty:
		return false, "the local working tree is dirty — commit or stash first"
	case !localBehindVM:
		return false, "the local branch has diverged from the VM tip — merge refs/satellite/… manually"
	default:
		return true, ""
	}
}

// localRepoHEADIsAncestorOf reports whether the repo's HEAD is an ancestor of
// sha — the "strictly behind" probe for --ff (the reverse direction of
// localRepoIsAncestor).
func localRepoHEADIsAncestorOf(repoDir, sha string) bool {
	_, err := gitOutput(repoDir, "merge-base", "--is-ancestor", "HEAD", sha)
	return err == nil
}

// --- plan → worktree resolution (laptop-side) ---

// resolvePlanWorktreeEntry finds the registry entry of the worktree created
// for planName: registry-first (Entry.Plan is written at creation by core
// workspace.Prepare via flow plan init), live entries only. Falls back to an
// entry whose container BASENAME equals the plan name (worktree name == plan
// name is the flow default). Ambiguity is an error, not a guess.
func resolvePlanWorktreeEntry(planName string) (*worktreeregistry.Entry, error) {
	entries, err := worktreeregistry.ListAll()
	if err != nil {
		return nil, fmt.Errorf("read worktree registry: %w", err)
	}
	var byPlan, byName []*worktreeregistry.Entry
	for _, e := range entries {
		if e == nil || e.IsArchived() || e.AbsPath == "" {
			continue
		}
		if _, statErr := os.Stat(e.AbsPath); statErr != nil {
			continue
		}
		if e.Plan == planName {
			byPlan = append(byPlan, e)
		}
		if filepath.Base(e.AbsPath) == planName {
			byName = append(byName, e)
		}
	}
	pick := byPlan
	if len(pick) == 0 {
		pick = byName
	}
	switch len(pick) {
	case 0:
		return nil, fmt.Errorf("no live worktree found for plan %q in the registry — create it with `flow plan init %s --worktree`", planName, planName)
	case 1:
		return pick[0], nil
	default:
		var candidates []string
		for _, e := range pick {
			candidates = append(candidates, e.AbsPath)
		}
		return nil, fmt.Errorf("plan %q matches %d worktrees (%s) — disambiguate the registry", planName, len(pick), strings.Join(candidates, ", "))
	}
}

// planNameFromCwd walks up from the working directory looking for a
// .grove/workspace worktree marker and returns its plan: key — the
// working-dir-aware default for --plan when invoked from inside a plan
// worktree.
func planNameFromCwd() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, ".grove", "workspace")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "plan:"); ok {
					if p := strings.TrimSpace(v); p != "" {
						return p, true
					}
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// resolveWorktreePushTarget resolves the laptop-side inputs shared by the
// push and pull verbs: the plan name (flag or cwd marker), the worktree
// container, its name, and the repo set (explicit --repos wins; then the
// registry entry's repo list intersected with what is actually checked out;
// then on-disk discovery).
func resolveWorktreePushTarget(planFlag, reposFlag string) (planName, containerAbs, worktreeName string, repos []string, err error) {
	planName = planFlag
	if planName == "" {
		var ok bool
		if planName, ok = planNameFromCwd(); !ok {
			return "", "", "", nil, fmt.Errorf("--plan is required (or run from inside a plan worktree)")
		}
		fmt.Printf("(plan %q resolved from the enclosing worktree marker)\n", planName)
	}
	entry, err := resolvePlanWorktreeEntry(planName)
	if err != nil {
		return "", "", "", nil, err
	}
	containerAbs, err = filepath.Abs(entry.AbsPath)
	if err != nil {
		return "", "", "", nil, err
	}
	worktreeName = filepath.Base(containerAbs)

	requested, err := parseReposFlag(reposFlag)
	if err != nil {
		return "", "", "", nil, err
	}
	switch {
	case len(requested) > 0:
		repos = requested
	case len(entry.Repos) > 0:
		repos = append([]string(nil), entry.Repos...)
	default:
		if repos, err = discoverLocalRepos(containerAbs); err != nil {
			return "", "", "", nil, err
		}
	}
	return planName, containerAbs, worktreeName, repos, nil
}

// --- the verbs ---

func newSatelliteWorktreeCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("worktree", "Ship a plan's worktree to a satellite VM and fetch agent commits back")
	cmd.AddCommand(newSatelliteWorktreePushCmd())
	cmd.AddCommand(newSatelliteWorktreePullCmd())
	return cmd
}

func newSatelliteWorktreePushCmd() *cobra.Command {
	var (
		planFlag      string
		reposFlag     string
		remoteCodeDir string
		dryRun        bool
		assumeYes     bool
		force         bool
	)
	cmd := cli.NewStandardCommand("push <name>", "Ship the plan worktree's branch tips to the VM as a real worktree")
	cmd.Long = `Ship a plan's worktree to a satellite VM, git-natively.

For each repo in the plan worktree's repo set, the laptop worktree checkout's
HEAD (committed state only) ships as a git bundle over the pinned SSH
transport; VM-side it is fetched into the mirrored BASE repo and checked out
as a real git worktree under the VM's XDG worktree layout:

  <VM worktrees dir>/<repo identifier>/<worktree>/<repo>

The container path is computed ON the VM by its installed grove binary
('grove internal worktree-path' plumbing) — the same core resolution the
VM-side flow executor uses, so dispatched jobs run inside it. The container's
synthetic grove.toml and .grove/workspace marker are seeded alongside. A VM
grove binary that is missing or predates the plumbing verb fails the push —
run 'grove satellite upgrade --prebuilt' first.

The base repos must already be mirrored ('grove satellite repos push'); a repo
without a base is held with a pointer. A VM worktree holding commits the
laptop has never fetched is held too — 'grove satellite worktree pull' fetches
them into refs/satellite/<name>/…, or --force discards them.

Plan-scoped remote workflow:
  flow plan init <plan> --worktree --satellite <name>   # designate
  grove satellite worktree push <name> --plan <plan>    # ship the worktree
  flow plan run                                         # auto-routes to <name>
  grove satellite worktree pull <name> --plan <plan> --ff  # fetch agent commits

Notes:
  - COMMITTED state only: dirty laptop worktrees ship their HEAD, never
    uncommitted content (a loud warning tells you so).
  - idempotent and cheap to re-run: matching shas ship no bundles.
  - --plan defaults to the enclosing worktree's plan when run from inside one.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&planFlag, "plan", "", "Plan whose worktree to push (default: the enclosing worktree's plan)")
	cmd.Flags().StringVar(&reposFlag, "repos", "", "Comma-separated repos to push (default: the worktree's registered repo set)")
	cmd.Flags().StringVar(&remoteCodeDir, "remote-code-dir", defaultRemoteCodeDir, "Ecosystem root on the VM")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Stop after printing the local-vs-VM delta table")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the push confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "Push repos whose VM worktree holds unfetched commits, DISCARDING those commits")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		entry, ok := loadMergedSatellites()[name]
		if !ok {
			return fmt.Errorf("satellite %q not found in the registry (config or state) — run `grove satellite up %s` first", name, name)
		}
		planName, containerAbs, worktreeName, repos, err := resolveWorktreePushTarget(planFlag, reposFlag)
		if err != nil {
			return err
		}
		return pushSatelliteWorktreeOverSSH(name, entry, containerAbs, remoteCodeDir, worktreeName, planName, repos, dryRun, assumeYes, force)
	}
	return cmd
}

func newSatelliteWorktreePullCmd() *cobra.Command {
	var (
		planFlag      string
		reposFlag     string
		remoteCodeDir string
		dryRun        bool
		ff            bool
	)
	cmd := cli.NewStandardCommand("pull <name>", "Fetch the VM plan worktree's commits back (optionally fast-forwarding)")
	cmd.Long = `Fetch commits made in a satellite VM's plan worktree back to the laptop.

Agents dispatched with 'flow plan run' commit inside the VM's plan worktree;
this probes those checkouts, bundles the new commits VM-side, and fetches them
into the laptop plan worktree's repos as:

  refs/satellite/<name>/<branch>

The pull itself NEVER touches local branch heads, the index, or the working
tree. With --ff, each laptop checkout that is provably safe to move — same
branch as the VM, clean tree, strictly behind the fetched tip — is then
fast-forwarded (git merge --ff-only; a local branch is never force-moved).
Everything else keeps the printed inspect/merge hints.

Once pulled, 'grove satellite worktree push' no longer holds the repo: the
VM's commits are safe locally.

Plan-scoped remote workflow:
  flow plan init <plan> --worktree --satellite <name>   # designate
  grove satellite worktree push <name> --plan <plan>    # ship the worktree
  flow plan run                                         # auto-routes to <name>
  grove satellite worktree pull <name> --plan <plan> --ff  # fetch agent commits

Notes:
  - idempotent and cheap to re-run: nothing new means no bundles transfer.
  - --plan defaults to the enclosing worktree's plan when run from inside one.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&planFlag, "plan", "", "Plan whose worktree to pull (default: the enclosing worktree's plan)")
	cmd.Flags().StringVar(&reposFlag, "repos", "", "Comma-separated repos to pull (default: the worktree's registered repo set)")
	cmd.Flags().StringVar(&remoteCodeDir, "remote-code-dir", defaultRemoteCodeDir, "Ecosystem root on the VM")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Stop after printing the VM-vs-local delta table (transfers nothing)")
	cmd.Flags().BoolVar(&ff, "ff", false, "Fast-forward clean, strictly-behind laptop checkouts to the fetched tips (never force-moves)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		entry, ok := loadMergedSatellites()[name]
		if !ok {
			return fmt.Errorf("satellite %q not found in the registry (config or state) — run `grove satellite up %s` first", name, name)
		}
		_, containerAbs, worktreeName, repos, err := resolveWorktreePushTarget(planFlag, reposFlag)
		if err != nil {
			return err
		}
		return pullSatelliteWorktreeOverSSH(name, entry, containerAbs, remoteCodeDir, worktreeName, repos, dryRun, ff)
	}
	return cmd
}
