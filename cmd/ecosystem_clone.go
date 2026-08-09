package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
)

// Card-declared ecosystem materialization: turning an [ecosystem] card plus a
// destination path into a checkout this machine's tooling can drive.
//
// The clone engine is deliberately separate from the verb that will call it
// (`grove ecosystem materialize`): the same steps are what `satellite up` does
// today in shell, and what a peer does on a fresh machine. What layout means:
//
//   - superrepo — the primary remote IS the superrepo; its submodules are the
//     member repos. This path is LIFTED from satellite-bootstrap.sh step 3
//     (source mode) rather than reinvented: clone if absent, `git submodule
//     update --init --jobs N`, then heal aborted checkouts. Its payoff is
//     tooling parity by construction: the clone root owns .gitmodules and a
//     grove manifest, so config.FindEcosystemConfig resolves it, which is what
//     workspace.SetupSubmodules (via findRootEcosystemPath) needs to create
//     plan worktrees on the materialized peer exactly as on the source.
//
//   - flat — for ecosystems that never were superrepos. There is no root repo
//     to clone, so the card's remotes ARE the member repos (name = the
//     directory, url = where to clone it from) and the ecosystem root is a
//     plain directory. That directory gets a wildcard grove manifest carrying
//     the card, which is what makes the result discoverable at all: an
//     ecosystem root is identified by a manifest with a non-empty `workspaces`
//     (config.FindEcosystemConfig), and without one a flat clone is just a
//     folder of repos.
//
// Both paths are idempotent and repair-shaped, never fail-shaped: an existing
// repo is kept and re-converged, a half-finished materialization is finished.
// That is the property `grove ecosystem materialize` is specified to have, and
// it belongs here rather than in the verb.

// ecosystemCloneJobsDefault matches satellite-bootstrap.sh's `--jobs 8`.
const ecosystemCloneJobsDefault = 8

// ecosystemCloneOptions tunes a materialization. The zero value is valid.
type ecosystemCloneOptions struct {
	// Jobs is the submodule clone parallelism (superrepo layout). <= 0 means
	// ecosystemCloneJobsDefault.
	Jobs int
	// Out receives progress; nil means os.Stdout. Git's own output is streamed
	// here too — a clone of a real ecosystem is minutes long and a silent
	// command looks hung.
	Out io.Writer
	// Subscription carries subscriber-local member selection. Its zero value
	// selects every member and preserves the pre-subscription behavior.
	Subscription config.MachineEcosystem
}

func (o ecosystemCloneOptions) jobs() int {
	if o.Jobs <= 0 {
		return ecosystemCloneJobsDefault
	}
	return o.Jobs
}

func (o ecosystemCloneOptions) out() io.Writer {
	if o.Out == nil {
		return os.Stdout
	}
	return o.Out
}

// cloneEcosystem materializes card into dest, dispatching on the card's
// declared layout. dest is created if absent; an existing dest is converged,
// not clobbered.
func cloneEcosystem(ctx context.Context, card config.EcosystemCard, dest string, opts ecosystemCloneOptions) error {
	if strings.TrimSpace(dest) == "" {
		return fmt.Errorf("materialize: destination path is empty")
	}
	if err := card.Validate(); err != nil {
		return fmt.Errorf("materialize: %w", err)
	}
	switch card.Layout {
	case config.LayoutSuperrepo:
		return cloneSuperrepoEcosystem(ctx, card, dest, opts)
	case config.LayoutFlat:
		return cloneFlatEcosystem(ctx, card, dest, opts)
	case "":
		// Deliberately not guessed. Layout decides whether the remotes are ONE
		// superrepo or N member repos, and guessing wrong either clones the
		// wrong thing or scatters repos that belong under a root.
		return fmt.Errorf("ecosystem card declares no layout; run `grove ecosystem adopt` on the source to record %q or %q", config.LayoutSuperrepo, config.LayoutFlat)
	default:
		return fmt.Errorf("ecosystem card declares unknown layout %q", card.Layout)
	}
}

// --- superrepo ---

// cloneSuperrepoEcosystem is satellite-bootstrap.sh step 3 in Go: clone the
// superrepo (only when there is no clone yet), initialize its submodules in
// parallel, and heal any submodule whose checkout was aborted.
func cloneSuperrepoEcosystem(ctx context.Context, card config.EcosystemCard, dest string, opts ecosystemCloneOptions) error {
	primary, err := primaryEcosystemRemote(card)
	if err != nil {
		return err
	}
	out := opts.out()

	switch existing, err := gitRepoRoot(dest); {
	case err != nil:
		return err
	case existing:
		fmt.Fprintf(out, "%s already holds a git checkout — converging it\n", dest)
	default:
		if err := ensureCloneTarget(dest); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		fmt.Fprintf(out, "Cloning %s (%s) into %s...\n", primary.URL, primary.Name, dest)
		// --origin names the remote after the card so a non-"origin" primary
		// keeps its identity on the peer.
		if err := runGit(ctx, "", out, "clone", "--origin", primary.Name, primary.URL, dest); err != nil {
			return fmt.Errorf("clone %s: %w", primary.URL, err)
		}
	}

	// The card's other remotes are parity, not transport: added if absent,
	// never re-pointed (a peer may legitimately have its own URL for a remote).
	for _, r := range card.Remotes {
		if r.Name == primary.Name {
			continue
		}
		if have, _ := gitOutput(dest, "remote", "get-url", r.Name); have != "" {
			continue
		}
		if err := runGit(ctx, dest, out, "remote", "add", r.Name, r.URL); err != nil {
			fmt.Fprintf(out, "warning: could not add remote %s (%s): %v\n", r.Name, r.URL, err)
		}
	}

	paths, err := gitmoduleSubmodulePaths(dest)
	if err != nil {
		return err
	}
	selected, err := selectedMembers(paths, opts.Subscription)
	if err != nil {
		return fmt.Errorf("select superrepo members: %w", err)
	}
	fmt.Fprintf(out, "Initializing %d/%d submodules (--jobs %d)...\n", len(selected), len(paths), opts.jobs())
	if len(selected) > 0 {
		args := []string{"submodule", "update", "--init", "--jobs", fmt.Sprint(opts.jobs()), "--"}
		args = append(args, selected...)
		if err := runGit(ctx, dest, out, args...); err != nil {
			return fmt.Errorf("submodule update in %s: %w", dest, err)
		}
	}
	if err := healAbortedSubmoduleCheckouts(ctx, dest, out); err != nil {
		return err
	}

	// The whole point of the superrepo layout: the result must be an ecosystem
	// root to grove, or none of the worktree/plan tooling resolves on the peer.
	if config.FindEcosystemConfig(dest) == "" {
		return fmt.Errorf("%s cloned, but no grove manifest with a non-empty `workspaces` is at its root — worktree/plan tooling will not resolve this as an ecosystem", dest)
	}
	return nil
}

// healAbortedSubmoduleCheckouts is the bootstrap's self-heal, verbatim in
// intent: an interrupted `submodule update` can leave a submodule with its
// HEAD recorded but the worktree never checked out (nothing beside .git). A
// plain re-run treats those as up to date, so each one is reset to its
// recorded HEAD. Idempotent — healthy submodules have files beside .git and
// are skipped.
func healAbortedSubmoduleCheckouts(ctx context.Context, root string, out io.Writer) error {
	paths, err := gitmoduleSubmodulePaths(root)
	if err != nil {
		return err
	}
	for _, sm := range paths {
		dir := filepath.Join(root, sm)
		// `[ -e "$sm/.git" ] || continue` — a submodule that was never
		// initialized at all is not an aborted checkout.
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		empty, err := onlyHoldsGitDir(dir)
		if err != nil {
			return err
		}
		if !empty {
			continue
		}
		fmt.Fprintf(out, "self-heal: restoring aborted checkout in %s\n", sm)
		if err := runGit(ctx, dir, out, "reset", "--hard", "HEAD"); err != nil {
			return fmt.Errorf("self-heal %s: %w", sm, err)
		}
	}
	return nil
}

// gitmoduleSubmodulePaths reads the submodule paths out of the root's
// .gitmodules, the same way the bootstrap does (`git config --file .gitmodules
// --get-regexp '^submodule\..*\.path$'` + take the value). Using git's own
// parser rather than a hand-rolled one keeps the two implementations honest
// about quoting and name/path divergence. A root without .gitmodules yields
// none, which is not an error: a superrepo may legitimately have no submodules
// yet.
func gitmoduleSubmodulePaths(root string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, ".gitmodules")); err != nil {
		return nil, nil //nolint:nilerr // no .gitmodules == no submodules
	}
	out, err := gitOutput(root, "config", "--file", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		// Exit 1 is "no matching keys" — an empty .gitmodules.
		return nil, nil //nolint:nilerr // absence, not failure
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		_, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "..") {
			continue
		}
		paths = append(paths, value)
	}
	return paths, nil
}

// onlyHoldsGitDir reports whether dir contains nothing but .git — the
// signature of an aborted submodule checkout. The bootstrap spells this
// `find "$sm" -mindepth 1 -maxdepth 1 ! -name .git -print -quit`.
func onlyHoldsGitDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			return false, nil
		}
	}
	return true, nil
}

// --- flat ---

// cloneFlatEcosystem clones each card remote as a member repo side by side and
// seeds the ecosystem root manifest. Sequential on purpose: the repos are
// independent, the output stays readable, and a flat ecosystem is a handful of
// repos rather than a superrepo's dozens.
func cloneFlatEcosystem(ctx context.Context, card config.EcosystemCard, dest string, opts ecosystemCloneOptions) error {
	if len(card.Remotes) == 0 {
		return fmt.Errorf("flat ecosystem card lists no remotes; there is nothing to clone (each [[ecosystem.remotes]] entry is one member repo: name = directory, url = clone source)")
	}
	out := opts.out()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	memberNames := make([]string, 0, len(card.Remotes))
	for _, remote := range card.Remotes {
		memberNames = append(memberNames, remote.Name)
	}
	selected, err := selectedMembers(memberNames, opts.Subscription)
	if err != nil {
		return fmt.Errorf("select flat members: %w", err)
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}

	var failed []string
	for _, r := range card.Remotes {
		if !selectedSet[r.Name] {
			fmt.Fprintf(out, "%s: omitted by subscription\n", r.Name)
			continue
		}
		// The name becomes a directory under dest and, later, a repo name in
		// generated shell on satellites — hold it to the same class the mirror
		// holds repo names to.
		if !repoNameRe.MatchString(r.Name) || r.Name == "." || r.Name == ".." {
			return fmt.Errorf("flat ecosystem card: %q is not usable as a repo directory name (allowed: A-Za-z0-9._-)", r.Name)
		}
		repoDir := filepath.Join(dest, r.Name)
		switch existing, err := gitRepoRoot(repoDir); {
		case err != nil:
			return err
		case existing:
			fmt.Fprintf(out, "%s: already cloned — left as is\n", r.Name)
			continue
		}
		if err := ensureCloneTarget(repoDir); err != nil {
			return err
		}
		fmt.Fprintf(out, "Cloning %s into %s...\n", r.URL, repoDir)
		if err := runGit(ctx, "", out, "clone", r.URL, repoDir); err != nil {
			// Per-repo isolation, same stance as the mirror verbs: one
			// unreachable remote must not strand the rest, and a re-run
			// finishes the job.
			fmt.Fprintf(out, "warning: %s: clone failed: %v\n", r.Name, err)
			failed = append(failed, r.Name)
		}
	}

	if err := seedFlatEcosystemRoot(dest, card, out); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("flat materialize incomplete: %s could not be cloned (re-run to finish)", strings.Join(failed, ", "))
	}
	return nil
}

// selectedMembers applies subscriber-local include/exclude intent to the
// card's complete member list. Unknown names are rejected: silently accepting
// a typo would produce a permanently incomplete checkout that looked healthy.
func selectedMembers(all []string, subscription config.MachineEcosystem) ([]string, error) {
	known := make(map[string]bool, len(all))
	for _, name := range all {
		known[name] = true
	}
	requested := subscription.Exclude
	if len(subscription.Repos) > 0 {
		requested = subscription.Repos
	}
	for _, name := range requested {
		if !known[name] {
			return nil, fmt.Errorf("repository %q is not in the ecosystem card (known: %s)", name, strings.Join(all, ", "))
		}
	}
	selected := make([]string, 0, len(all))
	for _, name := range all {
		if subscription.IncludesRepo(name) {
			selected = append(selected, name)
		}
	}
	return selected, nil
}

// seedFlatEcosystemRoot gives a flat ecosystem root the manifest that makes it
// one: a wildcard `workspaces` (the same shape the satellite repo mirror seeds,
// and what config.FindEcosystemConfig looks for) carrying the card, so the
// materialized peer holds the ecosystem's identity on disk exactly like a
// superrepo clone does — and a later `materialize` re-run reads it back.
//
// An existing manifest is never rewritten wholesale: WriteEcosystemCard edits
// only the [ecosystem] table and refuses to change a minted id.
func seedFlatEcosystemRoot(dest string, card config.EcosystemCard, out io.Writer) error {
	manifest := config.FindEcosystemManifest(dest)
	if manifest == "" {
		manifest = filepath.Join(dest, "grove.toml")
		if err := os.WriteFile(manifest, []byte("workspaces = [\"*\"]\n"), 0o600); err != nil {
			return fmt.Errorf("seed %s: %w", manifest, err)
		}
		fmt.Fprintf(out, "seeded %s (wildcard workspace manifest)\n", manifest)
	}
	if _, err := config.WriteEcosystemCard(manifest, card); err != nil {
		return fmt.Errorf("write the ecosystem card into %s: %w", manifest, err)
	}
	if config.FindEcosystemConfig(dest) == "" {
		// The manifest exists but declares no workspaces — an ecosystem root
		// only to the eye. Say so rather than leaving discovery to fail later.
		return fmt.Errorf("%s exists but declares no `workspaces`, so %s is not discoverable as an ecosystem root", manifest, dest)
	}
	return nil
}

// --- shared plumbing ---

// primaryEcosystemRemote picks the remote a superrepo is cloned from: the one
// named origin, else the first listed. Card order is meaningful — the writer
// sorts origin first.
func primaryEcosystemRemote(card config.EcosystemCard) (config.EcosystemRemote, error) {
	if len(card.Remotes) == 0 {
		return config.EcosystemRemote{}, fmt.Errorf("ecosystem card lists no remotes; a peer has nowhere to clone from (git remotes are the only durable transport)")
	}
	for _, r := range card.Remotes {
		if r.Name == "origin" {
			return r, nil
		}
	}
	return card.Remotes[0], nil
}

// gitRepoRoot reports whether dir is already a git checkout (.git as a dir for
// a primary clone, as a file for a worktree or submodule — both count, same
// test discoverLocalRepos uses).
func gitRepoRoot(dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", filepath.Join(dir, ".git"), err)
	}
	return false, nil
}

// ensureCloneTarget refuses to clone over a non-empty directory. `git clone`
// would refuse too, but with a message about git rather than about the
// materialization, and only after the network work.
func ensureCloneTarget(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty, but holds no git checkout — move it aside or pick another destination", dir)
	}
	return nil
}

// runGit runs git with its output streamed to out (clones are long). dir may
// be empty for commands that create their own directory.
func runGit(ctx context.Context, dir string, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: internal args
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
