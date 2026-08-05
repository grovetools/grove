package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The properties a development install has to hold, and the reason each one is
// here rather than left to the reader of install.go:
//
//   - it builds the user's directory, never a copy, because that is the only
//     thing that puts the build inside their workspace;
//   - it pins nothing, and says so everywhere a pin would otherwise be shown;
//   - it never deletes the source, because the source is someone's working
//     copy rather than a checkout grove created;
//   - its approval is not interchangeable with a pinned install's.
//
// The last one is the security-relevant one: the exec-trust store keys on the
// consent digest, so if a dev install hashed like a pinned install of the same
// panel, an approval granted for a fixed commit would silently authorize a
// mutable directory.

func TestDevInstallBuildsInPlaceAndPinsNothing(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))
	head := runGit(t, repo, "rev-parse", "HEAD")

	var seen []*ConsentRequest
	in := approving(t, &seen)
	res, err := in.Install(context.Background(), repo, Options{Dev: true})
	if err != nil {
		t.Fatalf("Install --dev: %v", err)
	}

	pin := res.Pin
	if !pin.Dev {
		t.Error("pin is not marked as a development install")
	}
	// The whole point: the build ran in the user's tree, so anything their
	// workspace resolves — a go.work, an unpublished sibling — resolves here.
	// Compared against the symlink-resolved path because DevSource evaluates
	// it deliberately: on macOS the temp dir is /var -> /private/var, and the
	// recorded path has to be the one a go.work lookup will walk up from.
	wantDir, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if pin.SourceDir != wantDir {
		t.Errorf("SourceDir = %s, want the user's own directory %s", pin.SourceDir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(repo, "built-panel")); err != nil {
		t.Errorf("the build did not run in the user's directory: %v", err)
	}
	// HEAD is recorded, but as a fact about the tree rather than as a pin.
	if pin.Commit != head {
		t.Errorf("recorded HEAD = %q, want %q", pin.Commit, head)
	}
	if pin.Ref != "" {
		t.Errorf("a dev install must carry no ref, got %q", pin.Ref)
	}

	if len(seen) != 1 {
		t.Fatalf("expected one consent prompt, got %d", len(seen))
	}
	if !seen[0].Facts.Dev {
		t.Error("the consent screen was not told this is a development install")
	}
	if !strings.Contains(seen[0].Facts.Source, "working tree") {
		t.Errorf("consent source = %q, want it to say the source is a working tree", seen[0].Facts.Source)
	}
}

func TestDevInstallRebuildsRatherThanReportingUnchanged(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo, Options{Dev: true}); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Edit the panel WITHOUT touching the manifest, which is exactly the shape
	// of every iteration: the consent digest is unchanged, so a pinned install
	// would short-circuit to "unchanged" and the user would keep running the
	// old binary while believing they had reinstalled.
	write(t, filepath.Join(repo, "panel.sh"), "#!/bin/sh\necho second version\n")

	res, err := in.Install(context.Background(), repo, Options{Dev: true})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Action == "unchanged" {
		t.Fatal("a dev install reported \"unchanged\" — it can never conclude that, the source is mutable")
	}
	built, err := os.ReadFile(filepath.Join(repo, "built-panel")) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("read the rebuilt binary: %v", err)
	}
	if !strings.Contains(string(built), "second version") {
		t.Error("the rebuild did not pick up the edited source")
	}
}

func TestRemoveNeverDeletesADevSource(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo, Options{Dev: true}); err != nil {
		t.Fatalf("Install --dev: %v", err)
	}
	if _, err := in.Remove("demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// keepSource=false is the default, and on a normal install it is what
	// removes the checkout. Here it must not touch anything.
	if _, err := os.Stat(filepath.Join(repo, ManifestFile)); err != nil {
		t.Fatalf("remove deleted the user's working tree: %v", err)
	}
}

func TestDevAndPinnedApprovalsAreNotInterchangeable(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	pinned, err := in.Install(context.Background(), repo+"@v1.0.0", Options{})
	if err != nil {
		t.Fatalf("pinned install: %v", err)
	}
	dev, err := in.Install(context.Background(), repo, Options{Dev: true})
	if err != nil {
		t.Fatalf("dev install: %v", err)
	}
	if pinned.Pin.ConsentDigest == dev.Pin.ConsentDigest {
		t.Fatal("a dev install hashes identically to a pinned one — an approval for a fixed commit would authorize a mutable directory")
	}
	if len(seen) != 2 {
		t.Errorf("expected the mode change to re-open the prompt, saw %d prompts", len(seen))
	}
}

func TestDevRejectsWhatItCannotBuildInPlace(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)

	// A remote source has nothing local to build, and silently cloning it would
	// defeat the only reason the mode exists.
	if _, err := in.Install(context.Background(), "github.com/user/grove-panel-foo", Options{Dev: true}); err == nil {
		t.Error("--dev accepted a remote source")
	}
	// A ref names a commit; --dev builds the tree as it is. Accepting both
	// would mean silently ignoring one of them.
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{Dev: true}); err == nil {
		t.Error("--dev accepted a ref")
	}
}

func TestDevFragmentDoesNotClaimAPin(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	res, err := in.Install(context.Background(), repo, Options{Dev: true})
	if err != nil {
		t.Fatalf("Install --dev: %v", err)
	}
	body, err := os.ReadFile(res.Pin.Fragment) //nolint:gosec // grove's own config dir
	if err != nil {
		t.Fatalf("read the fragment: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "# pinned:") {
		t.Error("the fragment header claims a pin that a dev install does not have")
	}
	if !strings.Contains(text, "DEVELOPMENT") {
		t.Error("the fragment header does not say the panel is a development install")
	}
}

// A dev entry has to stay one across `grove plugin update`, which is the
// rebuild loop. Re-pinning it to a commit behind the user's back would silently
// swap out the mode they chose.
func TestUpdateKeepsADevInstallInDevMode(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo, Options{Dev: true}); err != nil {
		t.Fatalf("Install --dev: %v", err)
	}
	write(t, filepath.Join(repo, "panel.sh"), "#!/bin/sh\necho updated\n")

	res, err := in.Update(context.Background(), "demo", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !res.Pin.Dev {
		t.Fatal("update re-pinned a development install to a commit")
	}
	built, err := os.ReadFile(filepath.Join(repo, "built-panel")) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("read the rebuilt binary: %v", err)
	}
	if !strings.Contains(string(built), "updated") {
		t.Error("update did not rebuild from the working tree")
	}
	if _, err := in.Update(context.Background(), "demo", Options{Ref: "v1.0.0"}); err == nil {
		t.Error("--ref was accepted on a development install")
	}
}
