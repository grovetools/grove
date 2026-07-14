package cmd

// Shared laptop→VM git-mirroring primitives, extracted from
// satellite_upgrade.go so both `grove satellite upgrade` and
// `grove satellite repos push` (satellite_repos.go) build on the same
// machinery: local repo probes (tips, dirtiness, discovery), ancestor-checked
// incremental git bundles, the remote HEADs probe + parser, and the pure
// local-vs-VM delta computation with its status taxonomy and table printer.
// Pure code motion + small parameterization — upgrade behavior is unchanged.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grovetools/core/tui/components/table"
)

// defaultRemoteCodeDir is where satellite-bootstrap.sh clones the superrepo.
const defaultRemoteCodeDir = "~/code/grovetools"

// satelliteStageBase is the base directory for the VM-side staging dirs the
// repos/worktree verbs ship bundles through. Default /tmp (matching the
// provisioned VMs). GROVE_SATELLITE_STAGE_BASE overrides it — the satellite
// E2E simulator runs in sandboxed environments where /tmp is not writable,
// and its "VM" shares the local filesystem, so the laptop-side override
// propagates consistently into the generated scripts and scp destinations.
func satelliteStageBase() string {
	if base := os.Getenv("GROVE_SATELLITE_STAGE_BASE"); base != "" {
		return base
	}
	return "/tmp"
}

// repoNameRe keeps repo names safe to embed as bare words in the generated
// remote scripts (they come from local directory listings, --repos flags, or
// config workspace lists).
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

// localRepoDirty reports whether the repo's working tree has uncommitted
// changes (git status --porcelain non-empty).
func localRepoDirty(repoDir string) (bool, error) {
	out, err := gitOutput(repoDir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// localRepoIsAncestor reports whether sha is an ancestor of the repo's local
// HEAD — the incremental-vs-full bundle decision, and (for the mirror) the
// divergence detector.
func localRepoIsAncestor(repoDir, sha string) bool {
	return exec.Command("git", "-C", repoDir, "merge-base", "--is-ancestor", sha, "HEAD").Run() == nil //nolint:gosec // G204: internal args
}

// localRepoHasObject reports whether the repo's object store holds sha as a
// commit (any ref or no ref at all — refs/satellite/… included). This is the
// push interlock's exact question: a VM head present locally is recoverable,
// one absent locally would be destroyed by a force-checkout.
func localRepoHasObject(repoDir, sha string) bool {
	return exec.Command("git", "-C", repoDir, "cat-file", "-e", sha+"^{commit}").Run() == nil //nolint:gosec // G204: internal args
}

// localRefSHA resolves a fully-qualified local ref to its sha ("" and false
// when the ref does not exist) — used to seed pull bundle-base candidates
// from the previously fetched refs/satellite/… tip.
func localRefSHA(repoDir, ref string) (string, bool) {
	out, err := gitOutput(repoDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || out == "" {
		return "", false
	}
	return out, true
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
		if localRepoIsAncestor(repoDir, remoteSHA) {
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
	deltaStatusForced        = "forced"
	deltaStatusHeld          = "held (zig; opt in via --repos)"
	deltaStatusMissingRemote = "missing on VM"
	deltaStatusRemoteError   = "remote error"
	// prebuilt-only statuses: a DIRTY local tree always ships (loudly), and
	// compositor — a library with no bin/ — is never shippable prebuilt,
	// not even via --repos/--all.
	deltaStatusDirty        = "DIRTY (always shipped)"
	deltaStatusHeldPrebuilt = "held (library; no prebuilt bins)"
)

type repoDelta struct {
	Repo      string
	Branch    string // local branch ("" = detached)
	LocalSHA  string
	RemoteSHA string // "" when missing/unreadable
	Status    string
}

// computeSatelliteDelta diffs local tips against the VM's HEADs for repos (in
// order). forced (--repos/--all) means explicitly named repos deploy even when
// their shas already match: up-to-date becomes forced (rebuild+reinstall).
// compositor is special-cased: its zig build is slow and rarely needed, so a
// changed compositor is HELD unless the user passed --repos/--all explicitly.
func computeSatelliteDelta(repos []string, local map[string]repoTip, remote map[string]string, forced bool) []repoDelta {
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
			if forced {
				d.Status = deltaStatusForced
			}
		default:
			d.RemoteSHA = sha
			d.Status = deltaStatusUpdate
			if r == "compositor" && !forced {
				d.Status = deltaStatusHeld
			}
		}
		deltas = append(deltas, d)
	}
	return deltas
}

// deltasToShip selects the repos the deploy actually ships: real updates plus
// explicitly forced up-to-date repos (and, prebuilt-only, dirty trees), in
// input order.
func deltasToShip(deltas []repoDelta) []repoDelta {
	var out []repoDelta
	for _, d := range deltas {
		if d.Status == deltaStatusUpdate || d.Status == deltaStatusForced || d.Status == deltaStatusDirty {
			out = append(out, d)
		}
	}
	return out
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
