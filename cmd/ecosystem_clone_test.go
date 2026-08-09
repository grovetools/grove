package cmd

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// sandboxGitEnv makes a test's git hermetic: its own HOME (so the developer's
// ~/.gitconfig and, more importantly, their real state dirs are never read or
// written) and file-protocol submodules allowed, which recent git refuses by
// default and every fixture superrepo here needs.
func sandboxGitEnv(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = t\n\temail = t@t\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Submodules pointing at local paths (the only kind a hermetic test can
	// have) are blocked by protocol.file.allow=user since git 2.38.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// seedSourceRepo creates a git repo with one committed file.
func seedSourceRepo(t *testing.T, dir, file, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

// seedSourceSuperrepo builds the fixture a peer materializes: an ecosystem root
// repo carrying a wildcard grove.toml with an [ecosystem] card, plus one
// submodule.
func seedSourceSuperrepo(t *testing.T, root string, card config.EcosystemCard) {
	t.Helper()
	member := seedSourceRepo(t, filepath.Join(filepath.Dir(root), "member-src"), "member.txt", "member")

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "init", "-q", "-b", "main")
	manifest := filepath.Join(root, "grove.toml")
	if err := os.WriteFile(manifest, []byte("workspaces = [\"*\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteEcosystemCard(manifest, card); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "submodule", "add", "-q", member, "alpha")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "ecosystem root")
}

// TestCloneSuperrepoEcosystemResolvesAsEcosystemRoot is the acceptance case for
// the superrepo path: materializing into a fresh directory yields a checkout
// whose root the worktree/plan machinery resolves as the ecosystem
// (config.FindEcosystemConfig is exactly what findRootEcosystemPath —
// core/pkg/workspace/lookup.go, used by SetupSubmodules — walks), with the
// submodule checked out and the card readable from disk.
func TestCloneSuperrepoEcosystemResolvesAsEcosystemRoot(t *testing.T) {
	sandboxGitEnv(t)
	root := t.TempDir()
	source := filepath.Join(root, "source", "grovetools")
	card := config.EcosystemCard{ID: "01J8SOURCEID", Layout: config.LayoutSuperrepo}
	seedSourceSuperrepo(t, source, card)

	card.Remotes = []config.EcosystemRemote{{Name: "origin", URL: source}}
	dest := filepath.Join(root, "peer", "grovetools")

	var log strings.Builder
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: &log, Jobs: 2}); err != nil {
		t.Fatalf("cloneEcosystem: %v\n%s", err, log.String())
	}

	// The property the whole superrepo layout exists for.
	if got := config.FindEcosystemConfig(dest); got != filepath.Join(dest, "grove.toml") {
		t.Fatalf("FindEcosystemConfig(%s) = %q, want the clone's own grove.toml", dest, got)
	}
	if _, err := os.Stat(filepath.Join(dest, ".gitmodules")); err != nil {
		t.Fatalf("clone root must own .gitmodules: %v", err)
	}
	// Submodule initialized AND checked out (init alone leaves it empty).
	if _, err := os.Stat(filepath.Join(dest, "alpha", "member.txt")); err != nil {
		t.Fatalf("submodule not checked out: %v\n%s", err, log.String())
	}
	// The card travelled with the clone.
	got, err := config.LoadEcosystemCard(filepath.Join(dest, "grove.toml"))
	if err != nil || got == nil {
		t.Fatalf("LoadEcosystemCard on the clone: %v (card %v)", err, got)
	}
	if got.ID != card.ID || got.Layout != config.LayoutSuperrepo {
		t.Errorf("cloned card = %+v, want id %q layout %q", got, card.ID, config.LayoutSuperrepo)
	}

	// Idempotent: a re-run converges the existing checkout instead of failing.
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: io.Discard}); err != nil {
		t.Fatalf("re-materialize must be a no-op repair, got %v", err)
	}
}

// TestCloneSuperrepoEcosystemHealsAbortedSubmoduleCheckout covers the self-heal
// lifted from satellite-bootstrap.sh: a submodule whose HEAD is recorded but
// whose worktree was never written (nothing beside .git) is invisible to a
// plain `submodule update` re-run, so materialize resets it to its recorded
// HEAD.
func TestCloneSuperrepoEcosystemHealsAbortedSubmoduleCheckout(t *testing.T) {
	sandboxGitEnv(t)
	root := t.TempDir()
	source := filepath.Join(root, "source", "grovetools")
	card := config.EcosystemCard{ID: "01J8HEAL", Layout: config.LayoutSuperrepo}
	seedSourceSuperrepo(t, source, card)
	card.Remotes = []config.EcosystemRemote{{Name: "origin", URL: source}}

	dest := filepath.Join(root, "peer", "grovetools")
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: io.Discard}); err != nil {
		t.Fatalf("initial materialize: %v", err)
	}

	// Simulate the aborted checkout: everything but .git removed.
	sub := filepath.Join(dest, "alpha")
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(sub, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(sub, "member.txt")); !os.IsNotExist(err) {
		t.Fatalf("fixture setup failed to empty the submodule: %v", err)
	}

	var log strings.Builder
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: &log}); err != nil {
		t.Fatalf("repair materialize: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(filepath.Join(sub, "member.txt")); err != nil {
		t.Fatalf("self-heal did not restore the aborted checkout: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "self-heal") {
		t.Errorf("self-heal must announce itself; log:\n%s", log.String())
	}
}

// TestCloneFlatEcosystemYieldsDiscoverableRepos is the acceptance case for the
// flat path: each card remote is a member repo cloned side by side, and the
// root becomes discoverable — a wildcard manifest carrying the card, since a
// flat root has no repo of its own to bring one.
func TestCloneFlatEcosystemYieldsDiscoverableRepos(t *testing.T) {
	sandboxGitEnv(t)
	root := t.TempDir()
	alphaSrc := seedSourceRepo(t, filepath.Join(root, "source", "alpha"), "a.txt", "alpha")
	betaSrc := seedSourceRepo(t, filepath.Join(root, "source", "beta"), "b.txt", "beta")

	card := config.EcosystemCard{
		ID:     "01J8FLAT",
		Layout: config.LayoutFlat,
		Remotes: []config.EcosystemRemote{
			{Name: "alpha", URL: alphaSrc},
			{Name: "beta", URL: betaSrc},
		},
		Notebooks: map[string]config.EcosystemNotebook{"personal": {Default: true}},
	}
	dest := filepath.Join(root, "peer", "flatco")

	var log strings.Builder
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: &log}); err != nil {
		t.Fatalf("cloneEcosystem: %v\n%s", err, log.String())
	}

	for name, file := range map[string]string{"alpha": "a.txt", "beta": "b.txt"} {
		if _, err := os.Stat(filepath.Join(dest, name, ".git")); err != nil {
			t.Errorf("%s not cloned: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dest, name, file)); err != nil {
			t.Errorf("%s not checked out: %v", name, err)
		}
	}
	// Discoverable as an ecosystem root, and the identity is on disk.
	manifest := filepath.Join(dest, "grove.toml")
	if got := config.FindEcosystemConfig(dest); got != manifest {
		t.Fatalf("FindEcosystemConfig(%s) = %q, want %q", dest, got, manifest)
	}
	got, err := config.LoadEcosystemCard(manifest)
	if err != nil || got == nil {
		t.Fatalf("LoadEcosystemCard: %v (card %v)", err, got)
	}
	if got.ID != card.ID || got.Layout != config.LayoutFlat || len(got.Remotes) != 2 {
		t.Errorf("seeded card = %+v, want the source card", got)
	}
	if !got.Notebooks["personal"].Default {
		t.Errorf("notebook binding lost: %+v", got.Notebooks)
	}
	// And the repos are discoverable to the mirror's own repo lister.
	repos, err := discoverLocalRepos(dest)
	if err != nil {
		t.Fatalf("discoverLocalRepos: %v", err)
	}
	if len(repos) != 2 || repos[0] != "alpha" || repos[1] != "beta" {
		t.Errorf("discoverLocalRepos = %v, want [alpha beta]", repos)
	}

	// Idempotent, and it does not re-mint or rewrite the card.
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: io.Discard}); err != nil {
		t.Fatalf("re-materialize: %v", err)
	}
}

func TestCloneFlatEcosystemMaterializesSelectedSubset(t *testing.T) {
	sandboxGitEnv(t)
	root := t.TempDir()
	alphaSrc := seedSourceRepo(t, filepath.Join(root, "source", "alpha"), "a.txt", "alpha")
	betaSrc := seedSourceRepo(t, filepath.Join(root, "source", "beta"), "b.txt", "beta")
	card := config.EcosystemCard{ID: "01J8PARTIALFLAT", Layout: config.LayoutFlat, Remotes: []config.EcosystemRemote{
		{Name: "alpha", URL: alphaSrc}, {Name: "beta", URL: betaSrc},
	}}
	dest := filepath.Join(root, "peer", "flatco")
	var log strings.Builder
	err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{
		Out: &log, Subscription: config.MachineEcosystem{Repos: []string{"alpha"}},
	})
	if err != nil {
		t.Fatalf("partial flat clone: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(filepath.Join(dest, "alpha", "a.txt")); err != nil {
		t.Fatalf("selected member missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "beta")); !os.IsNotExist(err) {
		t.Fatalf("omitted member was materialized: %v", err)
	}
	if cardOnDisk, err := config.LoadEcosystemCard(filepath.Join(dest, "grove.toml")); err != nil || cardOnDisk == nil || len(cardOnDisk.Remotes) != 2 {
		t.Fatalf("partial root must retain the complete card for later widening: card=%+v err=%v", cardOnDisk, err)
	}
}

func TestCloneSuperrepoEcosystemInitializesSelectedSubmodules(t *testing.T) {
	sandboxGitEnv(t)
	root := t.TempDir()
	source := filepath.Join(root, "source", "grovetools")
	card := config.EcosystemCard{ID: "01J8PARTIALSUPER", Layout: config.LayoutSuperrepo}
	seedSourceSuperrepo(t, source, card)
	beta := seedSourceRepo(t, filepath.Join(root, "beta-src"), "beta.txt", "beta")
	mustGit(t, source, "submodule", "add", "-q", beta, "beta")
	mustGit(t, source, "commit", "-q", "-am", "add beta")
	card.Remotes = []config.EcosystemRemote{{Name: "origin", URL: source}}

	dest := filepath.Join(root, "peer", "grovetools")
	if err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{
		Out: io.Discard, Subscription: config.MachineEcosystem{Exclude: []string{"beta"}},
	}); err != nil {
		t.Fatalf("partial superrepo clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "alpha", "member.txt")); err != nil {
		t.Fatalf("selected submodule missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "beta", "beta.txt")); !os.IsNotExist(err) {
		t.Fatalf("excluded submodule was checked out: %v", err)
	}
}

func TestCloneEcosystemRejectsUnknownSelectedMember(t *testing.T) {
	card := config.EcosystemCard{Layout: config.LayoutFlat, Remotes: []config.EcosystemRemote{{Name: "alpha", URL: "unused"}}}
	err := cloneEcosystem(context.Background(), card, t.TempDir(), ecosystemCloneOptions{
		Out: io.Discard, Subscription: config.MachineEcosystem{Repos: []string{"typo"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not in the ecosystem card") {
		t.Fatalf("error = %v, want unknown-member error", err)
	}
}

func TestCloneEcosystemRejectsUnusableCards(t *testing.T) {
	sandboxGitEnv(t)
	root := t.TempDir()
	remotes := []config.EcosystemRemote{{Name: "origin", URL: "https://example.invalid/x.git"}}

	cases := []struct {
		name string
		card config.EcosystemCard
		want string
	}{
		{"no layout", config.EcosystemCard{Remotes: remotes}, "declares no layout"},
		{"unknown layout", config.EcosystemCard{Layout: "monorepo", Remotes: remotes}, "layout"},
		{"superrepo without remotes", config.EcosystemCard{Layout: config.LayoutSuperrepo}, "no remotes"},
		{"flat without remotes", config.EcosystemCard{Layout: config.LayoutFlat}, "no remotes"},
		{"flat with an unusable repo name", config.EcosystemCard{
			Layout:  config.LayoutFlat,
			Remotes: []config.EcosystemRemote{{Name: "../escape", URL: "https://example.invalid/x.git"}},
		}, "repo directory name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cloneEcosystem(context.Background(), tc.card, filepath.Join(root, tc.name), ecosystemCloneOptions{Out: io.Discard})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCloneEcosystemRefusesNonEmptyDestination: materialize repairs its own
// half-finished work, but never clones over unrelated files.
func TestCloneEcosystemRefusesNonEmptyDestination(t *testing.T) {
	sandboxGitEnv(t)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "someones-work.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	card := config.EcosystemCard{
		Layout:  config.LayoutSuperrepo,
		Remotes: []config.EcosystemRemote{{Name: "origin", URL: "https://example.invalid/x.git"}},
	}
	err := cloneEcosystem(context.Background(), card, dest, ecosystemCloneOptions{Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v, want a refusal naming the non-empty destination", err)
	}
}

func TestPrimaryEcosystemRemote(t *testing.T) {
	origin := config.EcosystemRemote{Name: "origin", URL: "o"}
	fork := config.EcosystemRemote{Name: "fork", URL: "f"}

	got, err := primaryEcosystemRemote(config.EcosystemCard{Remotes: []config.EcosystemRemote{fork, origin}})
	if err != nil || got.Name != "origin" {
		t.Errorf("origin must win regardless of order: %+v (%v)", got, err)
	}
	got, err = primaryEcosystemRemote(config.EcosystemCard{Remotes: []config.EcosystemRemote{fork}})
	if err != nil || got.Name != "fork" {
		t.Errorf("first remote is the fallback primary: %+v (%v)", got, err)
	}
	if _, err := primaryEcosystemRemote(config.EcosystemCard{}); err == nil {
		t.Error("a card with no remotes must not resolve a primary")
	}
}
