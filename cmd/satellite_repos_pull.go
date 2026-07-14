package cmd

// `grove satellite repos pull` — the VM→laptop return path of the repo
// mirror (slice 2), symmetric with `repos push` (satellite_repos.go).
// Satellites run agents that COMMIT on the VM; pull brings those commits
// back, laptop-initiated, over the same pinned SSH transport:
//
//   probe VM HEADs+branches → compute the pull delta locally (a VM sha
//   already in the local object store means nothing to fetch) → VM-side
//   script bundles each repo's new commits into a stage dir (incremental
//   `<base>..HEAD` when a laptop-known sha is an ancestor of the VM HEAD,
//   full tip bundle otherwise) → scp the bundles BACK → fetch each into the
//   laptop repo as refs/satellite/<name>/<branch>.
//
// Pull NEVER touches local branch heads, the index, or the working tree —
// only refs/satellite/… move. Integrating the fetched commits (log/diff/
// merge) is deliberately left to the user; the per-repo summary prints
// copy-pasteable hints. Once fetched, the VM head object exists locally,
// which is exactly what flips push's divergence interlock from "held" to
// "safe to overwrite".

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"
)

// satelliteReposPullStageDir is the VM-side staging dir the pull's bundles
// are created in. The bundle script recreates it fresh on every run (a stale
// bundle from an earlier failed run must never be mistaken for this run's),
// and the laptop removes it after a fully successful pull — kept on failure
// for postmortem, like push's stage.
const satelliteReposPullStageDir = "/tmp/grove-satellite-repos-pull"

// deltaStatusFetch is the pull-only status for a VM head whose commit object
// is absent from the laptop's object store: there is something to bring back.
const deltaStatusFetch = "fetch (new VM commits)"

// satelliteRemoteHead is one probed VM checkout: its HEAD sha and checked-out
// branch ("" when detached or unreadable).
type satelliteRemoteHead struct {
	SHA    string
	Branch string
}

// hexSHARe guards shas we embed into generated remote shell (they come from
// our own git plumbing, but defense in depth is cheap).
var hexSHARe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// --- probe (script + parser) ---

// buildRemoteHeadsBranchesScript is buildRemoteHeadsScript extended with the
// checked-out branch: one "<repo> <sha> <branch>" line per repo, with
// MISSING/ERROR sentinels instead of failures so one bad repo does not mask
// the rest ("-" placeholds the branch of a missing repo; a detached HEAD
// reports the literal "HEAD", mapped to "" by the parser).
func buildRemoteHeadsBranchesScript(remoteCodeDir string, repos []string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	for _, r := range repos {
		fmt.Fprintf(&b, "if [ -e \"$CODE/%s/.git\" ]; then echo \"%s $(git -C \"$CODE/%s\" rev-parse HEAD 2>/dev/null || echo ERROR) $(git -C \"$CODE/%s\" rev-parse --abbrev-ref HEAD 2>/dev/null || echo ERROR)\"; else echo \"%s MISSING -\"; fi\n", r, r, r, r, r)
	}
	return b.String()
}

// parseRemoteHeadsBranches decodes buildRemoteHeadsBranchesScript output.
// Branch "HEAD" (detached), "-" (missing placeholder), and "ERROR" all map
// to "" — pull then files the fetch under the "detached" ref segment.
func parseRemoteHeadsBranches(out string) map[string]satelliteRemoteHead {
	heads := map[string]satelliteRemoteHead{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 3 {
			continue
		}
		branch := fields[2]
		if branch == "HEAD" || branch == "-" || branch == "ERROR" {
			branch = ""
		}
		heads[fields[0]] = satelliteRemoteHead{SHA: fields[1], Branch: branch}
	}
	return heads
}

// --- pull delta (pure) ---

// computeSatellitePullDelta diffs the VM's heads against the laptop's OBJECT
// STORE (not its tips): a VM sha already present locally is up-to-date —
// whether it equals the local tip, is an ancestor, or was fetched into
// refs/satellite/… earlier — because pull's only job is object transport.
// Missing/unreadable VM repos are reported and skipped. hasObject probes the
// LOCAL repo (injected for testability). In the returned deltas, Branch is
// the VM's checked-out branch (unlike push, where it is the local branch);
// the printed table's Branch column reads accordingly.
func computeSatellitePullDelta(repos []string, local map[string]repoTip, remote map[string]satelliteRemoteHead, hasObject func(repo, sha string) bool) []repoDelta {
	deltas := make([]repoDelta, 0, len(repos))
	for _, r := range repos {
		d := repoDelta{Repo: r, LocalSHA: local[r].SHA}
		switch head, ok := remote[r]; {
		case !ok || head.SHA == "MISSING":
			// No VM branch to show; fall back to the local branch so the
			// table's Branch column doesn't misreport "(detached)".
			d.Branch = local[r].Branch
			d.Status = deltaStatusMissingRemote
		case head.SHA == "ERROR":
			d.Branch = local[r].Branch
			d.Status = deltaStatusRemoteError
		default:
			d.RemoteSHA = head.SHA
			d.Branch = head.Branch
			if hasObject(r, head.SHA) {
				d.Status = deltaStatusUpToDate
			} else {
				d.Status = deltaStatusFetch
			}
		}
		deltas = append(deltas, d)
	}
	return deltas
}

// --- ref mapping ---

// satellitePullRefSegRe allows the same characters as repo names inside each
// slash-separated branch segment (branch names may nest: feature/x).
var satellitePullRefSegRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// satellitePullRef maps a VM branch to the local ref the fetch writes:
// refs/satellite/<sat>/<branch>, with "" (detached VM HEAD) filed under
// "detached". The branch comes off the wire, so it is validated before being
// used as a ref name (and later as a git argument): slash-nested segments of
// safe characters, no "..", no dot-leading segments, no ".lock" suffixes.
func satellitePullRef(satName, branch string) (string, error) {
	if branch == "" {
		branch = "detached"
	}
	if err := validateBranchRefSegments(branch); err != nil {
		return "", err
	}
	return "refs/satellite/" + satName + "/" + branch, nil
}

// validateBranchRefSegments checks that a branch name is safe both as a local
// ref suffix and as an argument embedded in generated remote shell:
// slash-nested segments of safe characters, no "..", no dot-leading segments,
// no ".lock" suffixes. Shared by the pull ref mapping (VM branches off the
// wire) and the worktree push script generation (laptop worktree branches).
func validateBranchRefSegments(branch string) error {
	if strings.Contains(branch, "..") {
		return fmt.Errorf("branch %q is not usable as a ref segment", branch)
	}
	for _, seg := range strings.Split(branch, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") || strings.HasSuffix(seg, ".lock") || !satellitePullRefSegRe.MatchString(seg) {
			return fmt.Errorf("branch %q is not usable as a ref segment", branch)
		}
	}
	return nil
}

// --- VM-side bundle script (pure; exercised by unit tests) ---

// satellitePullBundleReq is one repo's bundle order for the VM script: the
// candidate incremental bases (laptop-known shas), tried in order.
type satellitePullBundleReq struct {
	Repo  string
	Bases []string
}

// buildSatelliteReposPullBundleScript generates the VM-side bundle script:
// recreate the stage dir FRESH (stale bundles from a failed earlier run must
// never be scp'd as this run's), then per repo pick the first candidate base
// that `git merge-base --is-ancestor` confirms as an ancestor of the VM HEAD
// and bundle `<base>..HEAD` — falling back to a full tip bundle (`HEAD`) when
// no candidate qualifies (diverged or unrelated history). READ-ONLY apart
// from the stage dir: it never moves the VM's checkouts.
//
// Same isolation contract as push's script (no global set -e): a repo whose
// bundle fails records and CONTINUES, the script ends with a per-repo summary
// and exits nonzero when anything failed — the laptop still fetches the
// bundles that were created. Candidate bases that fail hexSHARe are dropped
// at generation time (they only ever come from local git plumbing).
func buildSatelliteReposPullBundleScript(remoteCodeDir, stageDir string, reqs []satellitePullBundleReq) string {
	var b strings.Builder
	b.WriteString("# generated by `grove satellite repos pull` — read-only apart from the stage dir\n")
	b.WriteString("set -uo pipefail\n")
	fmt.Fprintf(&b, "CODE=%s\n", remoteCodeDirExpr(remoteCodeDir))
	fmt.Fprintf(&b, "STAGE=%q\n", stageDir)
	b.WriteString(`rm -rf "$STAGE" && mkdir -p "$STAGE" || exit 1

FAILED_REPOS=""
SUMMARY=""
mark_failed() { FAILED_REPOS="$FAILED_REPOS $1"; }
record() { SUMMARY="$SUMMARY  $1: $2"$'\n'; }

bundle_repo() { # <repo> [<base>...] — incremental past the first ancestor base, else full tip bundle
  local repo="$1"
  shift
  local dir="$CODE/$repo" spec=HEAD base
  for base in "$@"; do
    if git -C "$dir" merge-base --is-ancestor "$base" HEAD 2>/dev/null; then
      spec="$base..HEAD"
      break
    fi
  done
  echo "==> $repo: bundle $spec"
  if git -C "$dir" bundle create "$STAGE/$repo.bundle" "$spec"; then
    record "$repo" "bundled ($spec)"
  else
    echo "==> $repo: bundle FAILED — continuing with the remaining repos" >&2
    mark_failed "$repo"
    record "$repo" "bundle FAILED"
  fi
}
`)
	b.WriteString("\n")
	for _, req := range reqs {
		fmt.Fprintf(&b, "bundle_repo %s", req.Repo)
		for _, base := range req.Bases {
			if hexSHARe.MatchString(base) {
				fmt.Fprintf(&b, " %s", base)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(`
echo
echo "=== repos pull bundle summary ==="
printf '%s' "$SUMMARY"
if [ -n "$FAILED_REPOS" ]; then
  echo "bundle FAILED for:$FAILED_REPOS (stage kept at $STAGE)" >&2
  exit 1
fi
echo "repos pull bundles ready"
`)
	return b.String()
}

// --- laptop-side fetch ---

// fetchSatelliteBundle fetches the bundle's HEAD into ref (forced: the
// satellite ref tracks whatever the VM checked out, non-fast-forwards
// included). Object transport + one refs/satellite/… write — never local
// branches, the index, or the working tree.
func fetchSatelliteBundle(repoDir, bundlePath, ref string) error {
	_, err := gitOutput(repoDir, "fetch", "--quiet", bundlePath, "+HEAD:"+ref)
	return err
}

// --- the pull engine ---

// satelliteReposTransport is the slice of the pinned SSH transport the
// pull/worktree engines need (satisfied by *satelliteSSH). An interface so
// tests — and the plan-scoped worktree callers — can run the engines against
// a local fake.
type satelliteReposTransport interface {
	dest() string
	outputScript(script string) (string, error)
	runScript(script string) error
	runCommand(command string) error
	scp(localPaths []string, remoteDir string) error
	scpFrom(remotePaths []string, localDir string) error
}

// satellitePullOutcome is one repo's result of a pull: the VM head that is
// now guaranteed present in the local object store, the VM's checked-out
// branch ("" = detached), and the refs/satellite/… ref this pull wrote (""
// when the head was already local and nothing was fetched). The worktree
// pull's --ff pass consumes these.
type satellitePullOutcome struct {
	Repo   string
	Branch string
	SHA    string
	Ref    string
}

// pullSatelliteReposOverSSH wires pullSatelliteRepos to the pinned transport
// from the registry entry (same C2 never-TOFU stance as push).
func pullSatelliteReposOverSSH(name string, entry satelliteConfigEntry, sourceAbs, remoteCodeDir string, repos []string, strict, dryRun bool) error {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-repos-pull-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return fmt.Errorf("satellite %q: %w", name, err)
	}
	_, err = pullSatelliteRepos(ssh, name, sourceAbs, remoteCodeDir, satelliteReposPullStageDir, repos, strict, dryRun)
	return err
}

// pullSatelliteRepos fetches VM-side commits back into the laptop repos:
// probe VM heads+branches, compute the pull delta against the local object
// stores, have the VM bundle the repos with new commits (incremental past a
// laptop-known base when possible), scp the bundles back, and fetch each as
// refs/satellite/<name>/<branch>. Per-repo failure isolation throughout; the
// VM stage dir (stageDir; satelliteReposPullStageDir in production) is
// removed only after a fully successful pull. Idempotent: a re-pull with
// nothing new probes, prints, and fetches no bundles. dryRun stops after the
// delta table, creating and transferring nothing.
//
// The returned outcomes cover every repo whose VM head is KNOWN-LOCAL after
// the call — already-up-to-date repos (Ref "") plus successfully fetched ones
// (Ref set) — so plan-scoped callers (`satellite worktree pull --ff`) can act
// on them; they are valid even when an error is also returned (per-repo
// isolation: the successes stand). Empty on dry runs.
func pullSatelliteRepos(transport satelliteReposTransport, name, sourceAbs, remoteCodeDir, stageDir string, repos []string, strict, dryRun bool) ([]satellitePullOutcome, error) {
	if !repoNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid satellite name %q for ref mapping (allowed: A-Za-z0-9._-)", name)
	}
	if len(repos) == 0 {
		fmt.Println("No repos to pull (empty repo set) — nothing to do.")
		return nil, nil
	}
	for _, r := range repos {
		if !repoNameRe.MatchString(r) {
			return nil, fmt.Errorf("invalid repo name %q (allowed: A-Za-z0-9._-)", r)
		}
	}

	// A pull target must be a local git checkout (the fetch lands there);
	// non-strict (config-derived) sets skip non-repo names with a notice,
	// same stance as push.
	var mirror []string
	for _, r := range repos {
		if _, err := os.Stat(filepath.Join(sourceAbs, r, ".git")); err != nil {
			if strict {
				return nil, fmt.Errorf("--repos %q: no git repo %s/%s to pull into", r, sourceAbs, r)
			}
			fmt.Printf("(%s: not a git repo under %s — skipped)\n", r, sourceAbs)
			continue
		}
		mirror = append(mirror, r)
	}
	if len(mirror) == 0 {
		fmt.Printf("No pullable repos under %s — nothing to do.\n", sourceAbs)
		return nil, nil
	}

	local := map[string]repoTip{}
	for _, r := range mirror {
		tip, err := localRepoTip(filepath.Join(sourceAbs, r))
		if err != nil {
			return nil, fmt.Errorf("read local HEAD of %s: %w", r, err)
		}
		local[r] = tip
	}

	// Probe VM heads + branches → pull delta (against local OBJECT presence).
	fmt.Printf("Reading repo HEADs on %s (%s)...\n", name, transport.dest())
	out, err := transport.outputScript(buildRemoteHeadsBranchesScript(remoteCodeDir, mirror))
	if err != nil {
		return nil, fmt.Errorf("read remote HEADs: %w", err)
	}
	remote := parseRemoteHeadsBranches(out)
	deltas := computeSatellitePullDelta(mirror, local, remote, func(repo, sha string) bool {
		return localRepoHasObject(filepath.Join(sourceAbs, repo), sha)
	})
	printSatelliteDelta(deltas)

	if missing := deltasWithStatus(deltas, deltaStatusMissingRemote); len(missing) > 0 {
		fmt.Printf("\nnote: not on the VM (nothing to pull): %s — `grove satellite repos push %s` creates them.\n", strings.Join(deltaRepoNames(missing), ", "), name)
	}
	if errored := deltasWithStatus(deltas, deltaStatusRemoteError); len(errored) > 0 {
		fmt.Printf("\nnote: unreadable VM HEAD for %s — skipped (inspect the VM checkout manually).\n", strings.Join(deltaRepoNames(errored), ", "))
	}

	// Up-to-date repos are already valid outcomes: their VM head is local.
	var outcomes []satellitePullOutcome
	for _, d := range deltasWithStatus(deltas, deltaStatusUpToDate) {
		outcomes = append(outcomes, satellitePullOutcome{Repo: d.Repo, Branch: d.Branch, SHA: d.RemoteSHA})
	}

	fetches := deltasWithStatus(deltas, deltaStatusFetch)
	if dryRun {
		if len(fetches) == 0 {
			fmt.Printf("\n(dry-run) nothing new on %q — no bundles would transfer.\n", name)
		} else {
			fmt.Printf("\n(dry-run) would pull %d repo(s): %s\n", len(fetches), strings.Join(deltaRepoNames(fetches), ", "))
		}
		return nil, nil
	}
	if len(fetches) == 0 {
		fmt.Printf("\nNothing new on %q — every VM head is already in the local object store.\n", name)
		return outcomes, nil
	}

	// Ref mapping + bundle-base candidates. Bases (in preference order): the
	// previously fetched refs/satellite/… tip (the freshest VM state the
	// laptop knows), then the local branch tip (push mirrored it there
	// earlier, so it is usually an ancestor of the VM HEAD). The VM script
	// verifies each with merge-base --is-ancestor before trusting it.
	type pullPlan struct {
		delta repoDelta
		ref   string
	}
	var plans []pullPlan
	var reqs []satellitePullBundleReq
	var failed []string
	for _, d := range fetches {
		ref, err := satellitePullRef(name, d.Branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v — skipped\n", d.Repo, err)
			failed = append(failed, d.Repo)
			continue
		}
		repoDir := filepath.Join(sourceAbs, d.Repo)
		var bases []string
		if prev, ok := localRefSHA(repoDir, ref); ok {
			bases = append(bases, prev)
		}
		if tip := local[d.Repo].SHA; tip != "" && !containsString(bases, tip) {
			bases = append(bases, tip)
		}
		plans = append(plans, pullPlan{delta: d, ref: ref})
		reqs = append(reqs, satellitePullBundleReq{Repo: d.Repo, Bases: bases})
	}

	var pulled []string
	if len(plans) > 0 {
		fmt.Printf("\nCreating %d bundle(s) on %q (stage %s)...\n", len(plans), name, stageDir)
		if err := transport.runScript(buildSatelliteReposPullBundleScript(remoteCodeDir, stageDir, reqs)); err != nil {
			// Per-repo isolation: fetch whatever bundles WERE created; the
			// repos without one fail individually below.
			fmt.Fprintln(os.Stderr, "warning: VM bundle script failed for some repos (summary above) — fetching the bundles that were created.")
		}

		bundleDir, err := os.MkdirTemp("", "grove-satellite-pull-bundles-")
		if err != nil {
			return outcomes, err
		}
		defer func() { _ = os.RemoveAll(bundleDir) }()

		fmt.Println()
		for _, p := range plans {
			repoDir := filepath.Join(sourceAbs, p.delta.Repo)
			if err := transport.scpFrom([]string{stageDir + "/" + p.delta.Repo + ".bundle"}, bundleDir); err != nil {
				fmt.Fprintf(os.Stderr, "%s: bundle transfer failed: %v — skipped\n", p.delta.Repo, err)
				failed = append(failed, p.delta.Repo)
				continue
			}
			if err := fetchSatelliteBundle(repoDir, filepath.Join(bundleDir, p.delta.Repo+".bundle"), p.ref); err != nil {
				fmt.Fprintf(os.Stderr, "%s: fetch into %s failed: %v — skipped\n", p.delta.Repo, p.ref, err)
				failed = append(failed, p.delta.Repo)
				continue
			}
			got, ok := localRefSHA(repoDir, p.ref)
			if !ok || got != p.delta.RemoteSHA {
				fmt.Fprintf(os.Stderr, "%s: %s is %s after the fetch, expected %s — skipped\n", p.delta.Repo, p.ref, shortSHA(got), shortSHA(p.delta.RemoteSHA))
				failed = append(failed, p.delta.Repo)
				continue
			}
			pulled = append(pulled, p.delta.Repo)
			outcomes = append(outcomes, satellitePullOutcome{Repo: p.delta.Repo, Branch: p.delta.Branch, SHA: p.delta.RemoteSHA, Ref: p.ref})

			// Per-repo summary: ref written, sha range, integration hints.
			// Local branch heads were NOT moved — merging is the user's call.
			upstream := local[p.delta.Repo].Branch
			if upstream == "" {
				upstream = "HEAD" // detached local checkout
			}
			fmt.Printf("%s: %s -> %s (was %s)\n", p.delta.Repo, p.ref, shortSHA(p.delta.RemoteSHA), shortSHA(local[p.delta.Repo].SHA))
			fmt.Printf("  inspect: git -C %s log %s..%s\n", repoDir, upstream, p.ref)
			fmt.Printf("  merge:   git -C %s merge %s\n", repoDir, p.ref)
		}
	}

	if len(failed) > 0 {
		return outcomes, fmt.Errorf("repos pull failed for: %s (VM stage kept at %s)", strings.Join(failed, ", "), stageDir)
	}
	if err := transport.runCommand("rm -rf " + stageDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove the VM stage dir %s: %v (the next pull recreates it fresh)\n", stageDir, err)
	}
	fmt.Printf("\nSatellite %q pulled (%s) — local branches untouched.\n", name, strings.Join(pulled, ", "))
	return outcomes, nil
}

// --- the verb ---

func newSatelliteReposPullCmd() *cobra.Command {
	var (
		reposFlag     string
		sourceDir     string
		remoteCodeDir string
		dryRun        bool
	)
	cmd := cli.NewStandardCommand("pull <name>", "Fetch VM-side commits back into refs/satellite/<name>/… locally")
	cmd.Long = `Fetch commits made on a satellite VM back into the laptop's repos.

For each repo the VM checkout's HEAD (and checked-out branch) is probed; VM
heads already present in the local object store are up-to-date. Repos with new
VM commits are bundled ON the VM — incrementally past a laptop-known base when
possible — scp'd back over the pinned SSH transport, and fetched into the
local repo as:

  refs/satellite/<name>/<branch>     (branch = the VM's checked-out branch,
                                      or "detached" for a detached VM HEAD)

Local branch heads, the index, and the working tree are NEVER touched —
integrate at your leisure:

  git -C <repo> log  main..refs/satellite/<name>/<branch>
  git -C <repo> merge refs/satellite/<name>/<branch>

Once pulled, 'grove satellite repos push' no longer holds the repo: the VM's
commits are safe locally, so overwriting the VM checkout loses nothing.

Repo-set precedence matches push: --repos flag > [satellites.<name>.repos]
workspaces > the resolved [satellites.<name>.sync] workspaces.

Notes:
  - idempotent and cheap to re-run: nothing new means no bundles transfer.
  - repos missing or unreadable on the VM are reported and skipped.
  - per-repo failure isolation: one bad repo never blocks the rest.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&reposFlag, "repos", "", "Comma-separated repos to pull (overrides [satellites.<name>.repos]/.sync workspaces)")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "Local ecosystem worktree root (default: the go.work root above cwd)")
	cmd.Flags().StringVar(&remoteCodeDir, "remote-code-dir", defaultRemoteCodeDir, "Ecosystem root on the VM")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Stop after printing the VM-vs-local delta table (transfers nothing)")
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

		// An explicit --repos list is strict (unknown names are errors);
		// config-derived sets skip non-repos with a notice — same as push.
		return pullSatelliteReposOverSSH(name, entry, sourceAbs, remoteCodeDir, repos, flagSet, dryRun)
	}
	return cmd
}
