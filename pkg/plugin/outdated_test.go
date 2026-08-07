package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRemote turns a fixture repo into a bare one and returns its path. A bare
// repo on disk is a real remote as far as git is concerned — ls-remote talks to
// it exactly as it would to a URL — so the check under test runs unmodified and
// the test needs no network.
func newRemote(t *testing.T, work string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "grove-panel-test.git")
	runGit(t, filepath.Dir(bare), "clone", "--quiet", "--bare", work, bare)
	return bare
}

// installFrom installs the fixture at spec and returns its pin.
func installFrom(t *testing.T, spec string, opts Options) *Pin {
	t.Helper()
	var seen []*ConsentRequest
	res, err := approving(t, &seen).Install(context.Background(), spec, opts)
	if err != nil {
		t.Fatalf("Install %s: %v", spec, err)
	}
	return mustPin(t, res.Name)
}

func check(t *testing.T, names ...string) OutdatedReport {
	t.Helper()
	reports, err := Outdated(context.Background(), names)
	if err != nil {
		t.Fatalf("Outdated: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("Outdated returned %d reports, want 1", len(reports))
	}
	return reports[0]
}

// A branch pin follows the branch, so the check has to notice when the branch
// moves — and has to say "current" until it does.
func TestOutdatedFollowsABranchPin(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	remote := newRemote(t, work)

	pin := installFrom(t, remote, Options{Ref: "main"})
	if r := check(t); r.State != StateCurrent {
		t.Fatalf("a freshly installed pin reads %q (%+v), want current", r.State, r)
	}
	if r := check(t); r.Latest != pin.Commit {
		t.Errorf("latest = %s, want the pinned commit %s", r.Latest, pin.Commit)
	}

	write(t, filepath.Join(work, "panel.sh"), "#!/bin/sh\necho a later version\n")
	runGit(t, work, "commit", "--quiet", "-am", "move main forward")
	runGit(t, work, "push", "--quiet", remote, "main")
	moved := runGit(t, work, "rev-parse", "HEAD")

	r := check(t)
	if r.State != StateOutdated {
		t.Fatalf("state = %q, want outdated", r.State)
	}
	if r.Latest != moved {
		t.Errorf("latest = %s, want the commit main names now (%s)", r.Latest, moved)
	}

	// The whole point of a read-only check: it reported a moved ref without
	// moving anything.
	if after := mustPin(t, "demo"); after.Commit != pin.Commit {
		t.Errorf("the check moved the pin: %s -> %s", pin.Commit, after.Commit)
	}
	if head := strings.TrimSpace(runGit(t, pin.SourceDir, "rev-parse", "HEAD")); head != pin.Commit {
		t.Errorf("the check moved the managed checkout to %s", head)
	}
}

// A tag pin does not float, so the check must keep saying current while the
// branch runs ahead of it.
func TestOutdatedDoesNotFloatATagPin(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	remote := newRemote(t, work)
	installFrom(t, remote+"@v1.0.0", Options{})

	write(t, filepath.Join(work, "panel.sh"), "#!/bin/sh\necho a later version\n")
	runGit(t, work, "commit", "--quiet", "-am", "move main forward")
	runGit(t, work, "push", "--quiet", remote, "main")

	if r := check(t); r.State != StateCurrent {
		t.Errorf("state = %q (%+v), want current — a tag that has not moved is not outdated", r.State, r)
	}

	// Moving the tag IS what makes a tag pin outdated.
	runGit(t, work, "tag", "--force", "v1.0.0")
	runGit(t, work, "push", "--quiet", "--force", "--tags", remote)
	if r := check(t); r.State != StateOutdated {
		t.Errorf("state = %q, want outdated once the tag itself moved", r.State)
	}
}

// An annotated tag's own object is not the commit it names, and ls-remote will
// happily report the tag object. Comparing that against a pin would report
// every annotated tag as outdated forever.
func TestOutdatedPeelsAnAnnotatedTag(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	runGit(t, work, "tag", "-a", "v2.0.0", "-m", "an annotated release")
	remote := newRemote(t, work)

	pin := installFrom(t, remote+"@v2.0.0", Options{})
	r := check(t)
	if r.State != StateCurrent {
		t.Errorf("state = %q, want current (pinned %s, latest %s)", r.State, pin.Commit, r.Latest)
	}
	if r.Latest != pin.Commit {
		t.Errorf("latest = %s, want the commit the tag points at (%s)", r.Latest, pin.Commit)
	}
}

// A commit pin can never go out of date, and a commit is not a ref — so the
// remote having nothing by that name is the expected answer rather than a
// failure to reach it.
func TestOutdatedTreatsACommitPinAsCurrent(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	commit := runGit(t, work, "rev-parse", "HEAD")
	remote := newRemote(t, work)

	installFrom(t, remote+"@"+commit, Options{})
	if r := check(t); r.State != StateCurrent {
		t.Errorf("state = %q (%+v), want current", r.State, r)
	}
}

// A remote that cannot be asked is reported per plugin, and the check still
// succeeds — one unreachable remote must not fail the answer for the rest.
func TestOutdatedDegradesToUnreachable(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	remote := newRemote(t, work)
	installFrom(t, remote, Options{Ref: "main"})

	if err := os.RemoveAll(remote); err != nil {
		t.Fatalf("remove the remote: %v", err)
	}
	r := check(t)
	if r.State != StateUnreachable {
		t.Fatalf("state = %q, want unreachable", r.State)
	}
	if r.Reason == "" {
		t.Error("an unreachable remote must say why — offline and mistyped are not the same problem")
	}
	if strings.Contains(r.Reason, "\n") {
		t.Errorf("the reason must stay on one line for the table: %q", r.Reason)
	}
}

// A ref that the remote no longer has is reachable-but-unanswerable, which is
// not the same as current.
func TestOutdatedReportsAVanishedRef(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	remote := newRemote(t, work)
	installFrom(t, remote+"@v1.0.0", Options{})

	runGit(t, work, "push", "--quiet", "--delete", remote, "v1.0.0")
	r := check(t)
	if r.State != StateUnreachable {
		t.Fatalf("state = %q, want unreachable", r.State)
	}
	if !strings.Contains(r.Reason, "v1.0.0") {
		t.Errorf("reason = %q, want it to name the ref that vanished", r.Reason)
	}
}

// A development install is built from a working tree. There is no ref to
// re-resolve and no pin for a remote to be ahead of, so the honest answer is
// that the question does not apply.
func TestOutdatedReportsDevAsNotApplicable(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	installFrom(t, work, Options{Dev: true})

	r := check(t)
	if r.State != StateDev {
		t.Errorf("state = %q, want dev", r.State)
	}
	if r.Latest != "" {
		t.Errorf("latest = %q, want nothing — a dev entry has no remote to compare", r.Latest)
	}
}

func TestOutdatedChecksEveryPluginAndRejectsUnknownNames(t *testing.T) {
	isolate(t)
	work := newFixtureRepo(t, fixtureManifest("demo", "D"))
	installFrom(t, newRemote(t, work), Options{Ref: "main"})

	reports, err := Outdated(context.Background(), nil)
	if err != nil {
		t.Fatalf("Outdated: %v", err)
	}
	if len(reports) != 1 || reports[0].Name != "demo" {
		t.Errorf("checking everything gave %+v", reports)
	}

	// Naming something that is not installed is a usage error, and that is the
	// only kind of failure this command has.
	if _, err := Outdated(context.Background(), []string{"nope"}); err == nil {
		t.Error("naming an uninstalled plugin must be an error")
	}

	// Nothing installed at all is an empty answer, not a failure.
	if _, err := setter(t).Remove("demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if reports, err := Outdated(context.Background(), nil); err != nil || len(reports) != 0 {
		t.Errorf("Outdated on an empty lockfile = %+v, %v", reports, err)
	}
}
