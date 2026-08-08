package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// initTestRepo makes dir a git repo with no remotes. Failures are fatal: a
// silently non-repo fixture would make the remote assertions vacuous.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.test")
	run("config", "user.name", "Test")
}

func TestDetectEcosystemLayout(t *testing.T) {
	dir := t.TempDir()
	if got := detectEcosystemLayout(dir); got != config.LayoutFlat {
		t.Errorf("layout without .gitmodules = %q, want %q", got, config.LayoutFlat)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte("[submodule \"a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectEcosystemLayout(dir); got != config.LayoutSuperrepo {
		t.Errorf("layout with .gitmodules = %q, want %q", got, config.LayoutSuperrepo)
	}
}

func TestDiscoverEcosystemRemotes(t *testing.T) {
	dir := t.TempDir()

	// A non-repo yields no remotes rather than an error.
	if got := discoverEcosystemRemotes(dir); len(got) != 0 {
		t.Errorf("remotes in a non-repo = %+v, want none", got)
	}

	initTestRepo(t, dir)
	if got := discoverEcosystemRemotes(dir); len(got) != 0 {
		t.Errorf("remotes in a fresh repo = %+v, want none", got)
	}

	for _, r := range [][2]string{
		{"upstream", "https://example.test/upstream.git"},
		{"origin", "https://example.test/origin.git"},
		{"alpha", "https://example.test/alpha.git"},
	} {
		cmd := exec.Command("git", "remote", "add", r[0], r[1])
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git remote add %s: %v\n%s", r[0], err, out)
		}
	}

	got := discoverEcosystemRemotes(dir)
	want := []config.EcosystemRemote{
		{Name: "origin", URL: "https://example.test/origin.git"},
		{Name: "alpha", URL: "https://example.test/alpha.git"},
		{Name: "upstream", URL: "https://example.test/upstream.git"},
	}
	if len(got) != len(want) {
		t.Fatalf("remotes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("remote[%d] = %+v, want %+v (origin must sort first, then alphabetically)", i, got[i], want[i])
		}
	}
}

// TestDiscoverEcosystemRemotesIgnoresInsteadOf is the portability guard: the
// card must carry the URL as configured, not as this host rewrites it, because
// the whole point of a card is to be read on a machine that does not share
// this host's git config.
func TestDiscoverEcosystemRemotesIgnoresInsteadOf(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	for _, args := range [][]string{
		{"remote", "add", "origin", "https://example.test/eco.git"},
		{"config", "url.ssh://git@example.test/.insteadOf", "https://example.test/"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	got := discoverEcosystemRemotes(dir)
	if len(got) != 1 {
		t.Fatalf("remotes = %+v, want exactly one", got)
	}
	if got[0].URL != "https://example.test/eco.git" {
		t.Errorf("url = %q, want the configured https URL — an insteadOf rewrite leaked into the card", got[0].URL)
	}
}

// TestDeriveEcosystemCardMintsOnceAndKeepsDeclarations: the id and the
// notebook bindings survive re-derivation; layout and remotes are re-observed.
func TestDeriveEcosystemCardMintsOnceAndKeepsDeclarations(t *testing.T) {
	dir := t.TempDir()

	first := deriveEcosystemCard(dir, nil)
	if first.ID == "" {
		t.Fatal("expected a minted id")
	}
	if first.Layout != config.LayoutFlat {
		t.Errorf("layout = %q, want %q", first.Layout, config.LayoutFlat)
	}

	second := deriveEcosystemCard(dir, &first)
	if second.ID != first.ID {
		t.Errorf("id was re-minted: %q → %q", first.ID, second.ID)
	}

	existing := config.EcosystemCard{
		ID:        first.ID,
		Layout:    config.LayoutFlat,
		Notebooks: map[string]config.EcosystemNotebook{"work": {Default: true, Audience: "org"}},
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte("[submodule \"a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := deriveEcosystemCard(dir, &existing)
	if third.Layout != config.LayoutSuperrepo {
		t.Errorf("layout = %q, want the re-observed %q", third.Layout, config.LayoutSuperrepo)
	}
	if !third.Notebooks["work"].Default || third.Notebooks["work"].Audience != "org" {
		t.Errorf("notebook declaration was lost: %+v", third.Notebooks)
	}
	// The carried map must be a copy — mutating the derived card must not
	// reach back into the caller's existing card.
	third.Notebooks["other"] = config.EcosystemNotebook{}
	if _, leaked := existing.Notebooks["other"]; leaked {
		t.Error("deriveEcosystemCard aliased the caller's notebook map")
	}
}

func TestSetEcosystemDefaultNotebook(t *testing.T) {
	card := config.EcosystemCard{
		Notebooks: map[string]config.EcosystemNotebook{
			"old": {Default: true, Audience: "personal"},
		},
	}
	setEcosystemDefaultNotebook(&card, "new")
	if card.Notebooks["old"].Default {
		t.Error("the previous default should have been demoted")
	}
	if card.Notebooks["old"].Audience != "personal" {
		t.Error("demoting the previous default must not drop its other fields")
	}
	if !card.Notebooks["new"].Default {
		t.Error("the new notebook should be the default")
	}
	if err := card.Validate(); err != nil {
		t.Errorf("card is invalid after re-pointing the default: %v", err)
	}

	// An empty name is a no-op, not a way to clear the binding.
	setEcosystemDefaultNotebook(&card, "")
	if !card.Notebooks["new"].Default {
		t.Error("an empty name should have changed nothing")
	}
}

// TestRunEcosystemAdoptIsIdempotent drives the command itself: the first run
// writes a card, the second leaves the file byte-identical.
func TestRunEcosystemAdoptIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	manifest := filepath.Join(dir, "grove.toml")
	original := "# keep me\nname = \"eco\"\nworkspaces = [\"*\"]\n"
	if err := os.WriteFile(manifest, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	initTestRepo(t, dir)

	withAdoptFlags(t, true, "", "notes")
	if err := runEcosystemAdopt(nil, []string{dir}); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	first, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(first), original) {
		t.Errorf("the original manifest was not preserved:\n%s", first)
	}

	card, err := config.LoadEcosystemCard(manifest)
	if err != nil {
		t.Fatalf("LoadEcosystemCard: %v", err)
	}
	if card == nil || card.ID == "" {
		t.Fatalf("no card was written: %+v", card)
	}
	if card.DefaultNotebookName() != "notes" {
		t.Errorf("default notebook = %q, want %q", card.DefaultNotebookName(), "notes")
	}

	if err := runEcosystemAdopt(nil, []string{dir}); err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	second, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("re-running adopt mutated the manifest:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	after, err := config.LoadEcosystemCard(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != card.ID {
		t.Errorf("the id was re-minted on the second run: %q → %q", card.ID, after.ID)
	}
}

func TestRunEcosystemAdoptRejectsANonEcosystem(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withAdoptFlags(t, true, "", "")
	err := runEcosystemAdopt(nil, []string{t.TempDir()})
	if err == nil {
		t.Fatal("expected an error for a directory with no grove manifest")
	}
	if !strings.Contains(err.Error(), "grove ecosystem init") {
		t.Errorf("error = %v, want it to point at `grove ecosystem init`", err)
	}
}

func TestRunEcosystemAdoptRejectsABadLayout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grove.toml"), []byte("name = \"eco\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withAdoptFlags(t, true, "monorepo", "")
	err := runEcosystemAdopt(nil, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "--layout") {
		t.Fatalf("err = %v, want a --layout validation error", err)
	}
}

// TestRunEcosystemAdoptAdoptsYAMLManifests: every ecosystem scaffolded before
// `grove ecosystem init` switched to TOML carries a grove.yml, so YAML
// ecosystems are the ones most in need of a backfill.
func TestRunEcosystemAdoptAdoptsYAMLManifests(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	manifest := filepath.Join(dir, "grove.yml")
	if err := os.WriteFile(manifest, []byte("name: eco\nworkspaces:\n  - \"*\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	initTestRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte("[submodule \"a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	withAdoptFlags(t, true, "", "")
	if err := runEcosystemAdopt(nil, []string{dir}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	card, err := config.LoadEcosystemCard(manifest)
	if err != nil {
		t.Fatalf("LoadEcosystemCard: %v", err)
	}
	if card == nil || card.Layout != config.LayoutSuperrepo {
		t.Fatalf("card = %+v, want a superrepo card", card)
	}
}

// withAdoptFlags sets the command's package-level flag vars for the duration
// of a test and restores them afterwards (cobra flag vars are process state).
func withAdoptFlags(t *testing.T, yes bool, layout, notebook string) {
	t.Helper()
	oldYes, oldLayout, oldNotebook := ecosystemAdoptYes, ecosystemAdoptLayout, ecosystemAdoptNotebook
	t.Cleanup(func() {
		ecosystemAdoptYes, ecosystemAdoptLayout, ecosystemAdoptNotebook = oldYes, oldLayout, oldNotebook
	})
	ecosystemAdoptYes, ecosystemAdoptLayout, ecosystemAdoptNotebook = yes, layout, notebook
}
