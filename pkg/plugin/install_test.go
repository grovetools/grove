package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/exectrust"
	"github.com/pelletier/go-toml/v2"
)

// isolate points every grove-managed location at a temp root, so a test
// installs into its own config, data, bin and trust store and never touches
// the developer's.
func isolate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GROVE_HOME", root)
	t.Setenv("GROVE_BIN", "") // let BinDir fall through to GROVE_HOME/data/bin
	t.Setenv(exectrust.EnvStorePath, filepath.Join(root, "state", "grove", "exec-trust.json"))
	return root
}

// runGit runs one git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=grove test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=grove test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixtureManifest is a panel that needs no toolchain: `cp` is the build step
// and the program is a shell script already in the repo.
func fixtureManifest(name, icon string, keys ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `schema_version = 1

[plugin]
name        = %q
description = "a test panel"

[build]
command = ["cp", "panel.sh", "built-panel"]
binary  = "built-panel"

[panel]
icon     = %q
protocol = "embed/v1"
restart  = true
`, name, icon)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n[[panel.keys]]\nkey = %q\ndescription = \"a claimed chord\"\n", k)
	}
	return b.String()
}

// newFixtureRepo creates a git repository holding an installable plugin,
// tagged v1.0.0.
func newFixtureRepo(t *testing.T, manifest string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "grove-panel-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write(t, filepath.Join(dir, ManifestFile), manifest)
	write(t, filepath.Join(dir, "panel.sh"), "#!/bin/sh\necho hello from the test panel\n")
	runGit(t, dir, "init", "--quiet", "-b", "main")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "--quiet", "-m", "the panel")
	runGit(t, dir, "tag", "v1.0.0")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write %s: %v", path, err)
	}
}

// approving is an Installer that says yes and records what it was asked.
func approving(t *testing.T, seen *[]*ConsentRequest) *Installer {
	t.Helper()
	return &Installer{
		Out: io.Discard,
		Confirm: func(req *ConsentRequest) (bool, error) {
			*seen = append(*seen, req)
			return true, nil
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
	}
}

func TestInstallPinsTheResolvedCommit(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D", "ctrl+f"))
	tagged := runGit(t, repo, "rev-parse", "v1.0.0^{commit}")

	var seen []*ConsentRequest
	in := approving(t, &seen)
	res, err := in.Install(context.Background(), repo+"@v1.0.0", Options{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Action != "installed" || res.Name != "demo" {
		t.Fatalf("result = %+v", res)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one consent prompt, got %d", len(seen))
	}
	if seen[0].IsUpdate() {
		t.Error("a first install must not be presented as an update")
	}

	lock, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	pin := lock.Get("demo")
	if pin == nil {
		t.Fatal("no pin recorded")
	}
	if pin.Commit != tagged {
		t.Errorf("pinned commit = %s, want the commit v1.0.0 names (%s)", pin.Commit, tagged)
	}
	if pin.Ref != "v1.0.0" {
		t.Errorf("pinned ref = %q", pin.Ref)
	}

	// The fragment declares the panel, pointing at the installed binary.
	data, err := os.ReadFile(pin.Fragment)
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	var frag struct {
		TUI struct {
			Plugins map[string]struct {
				Command  string `toml:"command"`
				Icon     string `toml:"icon"`
				Protocol string `toml:"protocol"`
				Restart  bool   `toml:"restart"`
			} `toml:"plugins"`
		} `toml:"tui"`
	}
	if err := toml.Unmarshal(data, &frag); err != nil {
		t.Fatalf("the written fragment is not valid TOML: %v\n%s", err, data)
	}
	entry, ok := frag.TUI.Plugins["demo"]
	if !ok {
		t.Fatalf("fragment has no [tui.plugins.demo]:\n%s", data)
	}
	if entry.Command != pin.Binary {
		t.Errorf("fragment command = %q, want the installed binary %q", entry.Command, pin.Binary)
	}
	if entry.Protocol != ProtocolEmbedV1 || entry.Icon != "D" || !entry.Restart {
		t.Errorf("fragment lost manifest fields: %+v", entry)
	}

	// The binary is installed under the pin and linked into the grove bin dir.
	target, err := os.Readlink(pin.Binary)
	if err != nil {
		t.Fatalf("the bin dir entry is not a symlink: %v", err)
	}
	if !strings.Contains(target, tagged) {
		t.Errorf("bin dir entry -> %s, want a path under the pinned commit", target)
	}
	if _, err := os.Stat(pin.Binary); err != nil {
		t.Errorf("installed binary is not readable: %v", err)
	}

	// The approval lives in the exec-trust store, not in a second store.
	if !exectrust.Load().IsTrusted(pin.Fragment, pin.ConsentDigest) {
		t.Error("the install approval must be recorded in the exec-trust store")
	}
	if !IsApproved(pin.Fragment, pin.ConsentDigest) {
		t.Error("IsApproved must agree with the store")
	}
}

func TestInstallDeclinedWritesNothing(t *testing.T) {
	root := isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	in := &Installer{
		Out:     io.Discard,
		Confirm: func(*ConsentRequest) (bool, error) { return false, nil },
	}
	_, err := in.Install(context.Background(), repo+"@v1.0.0", Options{})
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("Install = %v, want ErrDeclined", err)
	}

	fragment, _ := FragmentPath("demo")
	if _, err := os.Stat(fragment); !os.IsNotExist(err) {
		t.Error("a declined install must not write a manifest fragment")
	}
	lockPath, _ := LockPath()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("a declined install must not write a lockfile")
	}
	binPath, _ := BinPath("built-panel")
	if _, err := os.Lstat(binPath); !os.IsNotExist(err) {
		t.Error("a declined install must not install a binary")
	}
	if entries := exectrust.Load().Entries(); len(entries) != 0 {
		t.Errorf("a declined install must not record trust, got %+v", entries)
	}
	srcRoot := filepath.Join(root, "data", "grove", "plugins", "src")
	if entries, err := os.ReadDir(srcRoot); err == nil && len(entries) != 0 {
		t.Errorf("a declined install must not leave a checkout behind, found %d", len(entries))
	}
}

// A declined install must never be reported as a build failure or a success,
// and it must not touch what is already installed.
func TestUpdateDeclinedKeepsThePreviousPin(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	if _, err := approving(t, &seen).Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	before, _ := LoadLock()
	firstCommit := before.Get("demo").Commit

	// A new version with a different build command — exactly what an approval
	// must not carry over.
	write(t, filepath.Join(repo, ManifestFile),
		strings.Replace(fixtureManifest("demo", "D"), `["cp", "panel.sh", "built-panel"]`, `["cp", "panel.sh", "built-panel", "--", "surprise"]`, 1))
	runGit(t, repo, "commit", "--quiet", "-am", "v2")
	runGit(t, repo, "tag", "v2.0.0")

	declining := &Installer{Out: io.Discard, Confirm: func(*ConsentRequest) (bool, error) { return false, nil }}
	if _, err := declining.Update(context.Background(), "demo", Options{Ref: "v2.0.0"}); !errors.Is(err, ErrDeclined) {
		t.Fatalf("Update = %v, want ErrDeclined", err)
	}

	after, _ := LoadLock()
	pin := after.Get("demo")
	if pin.Commit != firstCommit {
		t.Errorf("a declined update moved the pin: %s -> %s", firstCommit, pin.Commit)
	}
	if pin.Ref != "v1.0.0" {
		t.Errorf("a declined update changed the ref to %q", pin.Ref)
	}
	if !IsApproved(pin.Fragment, pin.ConsentDigest) {
		t.Error("a declined update must leave the previous approval intact")
	}
}

// The pin does not float: `update` on a plugin pinned to a tag re-resolves
// that tag, finds the same commit, and does nothing — even though the
// repository has moved on.
func TestUpdateOnAPinnedTagDoesNotFloat(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	pinned := mustPin(t, "demo").Commit

	write(t, filepath.Join(repo, "panel.sh"), "#!/bin/sh\necho a later version\n")
	runGit(t, repo, "commit", "--quiet", "-am", "move main forward")

	res, err := in.Update(context.Background(), "demo", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", res.Action)
	}
	if got := mustPin(t, "demo").Commit; got != pinned {
		t.Errorf("the pin moved without being asked to: %s -> %s", pinned, got)
	}
	if len(seen) != 1 {
		t.Errorf("a no-op update must not re-prompt (prompts: %d)", len(seen))
	}
}

// Moving the pin IS possible — it just has to be asked for, and it re-prompts
// with a diff of what changed.
func TestUpdateToANewRefRepromptsWithADiff(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	first := mustPin(t, "demo")

	write(t, filepath.Join(repo, ManifestFile), fixtureManifest("demo", "D", "ctrl+g"))
	runGit(t, repo, "commit", "--quiet", "-am", "claim a key")
	runGit(t, repo, "tag", "v1.1.0")

	res, err := in.Update(context.Background(), "demo", Options{Ref: "v1.1.0"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Action != "updated" {
		t.Errorf("action = %q, want updated", res.Action)
	}
	if len(seen) != 2 {
		t.Fatalf("expected a second consent prompt, got %d", len(seen))
	}
	req := seen[1]
	if !req.IsUpdate() {
		t.Error("an update must be presented as one, with the previous approval to diff against")
	}
	var fields []string
	for _, c := range req.Changes {
		fields = append(fields, c.Field)
	}
	if !contains(fields, "commit") || !contains(fields, "keys") {
		t.Errorf("diff fields = %v, want the commit and the new key claim", fields)
	}

	next := mustPin(t, "demo")
	if next.Commit == first.Commit {
		t.Error("the pin did not move")
	}
	if next.ConsentDigest == first.ConsentDigest {
		t.Error("the approval digest must change with the pin, so an old approval cannot cover new content")
	}
	if !IsApproved(next.Fragment, next.ConsentDigest) {
		t.Error("the new pin must be approved in the trust store")
	}
	if IsApproved(next.Fragment, first.ConsentDigest) {
		t.Error("the superseded approval must not still be honored")
	}
}

func TestUpdatePreservesUserSettingsAndAddsNewDefaults(t *testing.T) {
	isolate(t)
	v1 := settingsManifest("demo")
	repo := newFixtureRepo(t, v1)

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := in.Set("demo", []string{"work_minutes=40", "feed.limit=50"}, false); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// The release changes both defaults and adds a nested setting. Existing
	// values are user configuration now; only the new key should come from v2.
	v2 := strings.Replace(v1, "work_minutes = 25", "work_minutes = 15\nnew_default  = true", 1)
	v2 = strings.Replace(v2, "limit = 30", "limit = 10\nlayout = \"wide\"", 1)
	write(t, filepath.Join(repo, ManifestFile), v2)
	runGit(t, repo, "commit", "--quiet", "-am", "settings v2")
	runGit(t, repo, "tag", "v2.0.0")

	if _, err := in.Update(context.Background(), "demo", Options{Ref: "v2.0.0"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	settings := fragmentSettings(t, mustPin(t, "demo").Fragment, "demo")
	if got := settings["work_minutes"]; got != int64(40) {
		t.Errorf("work_minutes = %#v, want preserved user value 40", got)
	}
	if got := settings["new_default"]; got != true {
		t.Errorf("new_default = %#v, want new manifest default true", got)
	}
	feed := settings["feed"].(map[string]any)
	if got := feed["limit"]; got != int64(50) {
		t.Errorf("feed.limit = %#v, want preserved user value 50", got)
	}
	if got := feed["layout"]; got != "wide" {
		t.Errorf("feed.layout = %#v, want new nested default wide", got)
	}
	pin := mustPin(t, "demo")
	for _, want := range []string{"work_minutes = 40", "feed.limit = 50", "feed.layout = wide"} {
		if !contains(pin.Consent.Settings, want) {
			t.Errorf("consent settings %v do not include %q", pin.Consent.Settings, want)
		}
	}
}

func TestRemoveLeavesNothingBehind(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	pin := mustPin(t, "demo")
	versionsDir, _ := PluginVersionsDir("demo")

	removed, err := in.Remove("demo", false)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(removed) == 0 {
		t.Error("Remove reported nothing removed")
	}

	for _, p := range []string{pin.Fragment, pin.Binary, versionsDir, pin.SourceDir} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the uninstall", p)
		}
	}
	lock, _ := LoadLock()
	if lock.Get("demo") != nil {
		t.Error("the pin survived the uninstall")
	}
	if IsApproved(pin.Fragment, pin.ConsentDigest) {
		t.Error("the install approval survived the uninstall")
	}
	if entries := exectrust.Load().Entries(); len(entries) != 0 {
		t.Errorf("the trust store still holds %+v", entries)
	}

	if _, err := in.Remove("demo", false); err == nil {
		t.Error("removing what is not installed must be an error, not a silent success")
	}
}

// A plugin binary must not be able to replace a tool grove manages.
func TestInstallRefusesToStompAnUnrelatedBinary(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	binPath, err := BinPath("built-panel")
	if err != nil {
		t.Fatalf("BinPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write(t, binPath, "#!/bin/sh\necho an important tool\n")

	var seen []*ConsentRequest
	_, err = approving(t, &seen).Install(context.Background(), repo+"@v1.0.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Install = %v, want a refusal naming the existing entry", err)
	}
	data, _ := os.ReadFile(binPath)
	if !strings.Contains(string(data), "an important tool") {
		t.Error("the pre-existing binary was overwritten")
	}
}

// Installing the same pin twice is a no-op that does not re-ask.
func TestReinstallingThePinnedVersionIsANoOp(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	res, err := in.Install(context.Background(), repo+"@v1.0.0", Options{})
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if res.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", res.Action)
	}
	if len(seen) != 1 {
		t.Errorf("an unchanged install must not re-prompt (prompts: %d)", len(seen))
	}
}

func TestListReportsWhatIsInstalledAndWhetherItIsIntact(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, fixtureManifest("demo", "D"))

	var seen []*ConsentRequest
	if _, err := approving(t, &seen).Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d rows, want 1", len(list))
	}
	st := list[0]
	if !st.FragmentPresent || !st.BinaryPresent || !st.Approved {
		t.Errorf("a fresh install must read as intact: %+v", st)
	}

	// Hand-editing the fragment breaks the approval, and list says so rather
	// than repairing it.
	write(t, st.Pin.Fragment, "# emptied by hand\n")
	revoked := exectrust.Load()
	revoked.Revoke(st.Pin.Fragment)
	if err := revoked.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}
	list, err = List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list[0].Approved {
		t.Error("a fragment whose approval was withdrawn must not read as approved")
	}
}

func mustPin(t *testing.T, name string) *Pin {
	t.Helper()
	lock, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	pin := lock.Get(name)
	if pin == nil {
		t.Fatalf("%q is not in the lockfile", name)
	}
	return pin
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
