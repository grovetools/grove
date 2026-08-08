package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/exectrust"
	"github.com/pelletier/go-toml/v2"
)

// The [tool] kind through the same pipeline the panel tests drive: real git
// against local temp repos, a `cp` build, no mocks. What these tests hold is
// the three ways a tool differs from a panel — the pin records the kind, the
// fragment is a comment-only trust anchor rather than a [tui.plugins] entry,
// and a verb has exactly one owner — and that nothing else differs at all.

// fixtureToolManifest is a tool that needs no toolchain: `cp` is the build
// step and the program is a shell script already in the repo.
func fixtureToolManifest(name string, provides ...string) string {
	quoted := make([]string, 0, len(provides))
	for _, p := range provides {
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	return fmt.Sprintf(`schema_version = 1

[plugin]
name        = %q
description = "a test tool"

[build]
command = ["cp", "tool.sh", "built-tool"]
binary  = "built-tool"

[tool]
provides = [%s]
`, name, strings.Join(quoted, ", "))
}

// newFixtureToolRepo creates a git repository holding an installable tool
// plugin, tagged v1.0.0.
func newFixtureToolRepo(t *testing.T, manifest string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "grove-tool-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write(t, filepath.Join(dir, ManifestFile), manifest)
	write(t, filepath.Join(dir, "tool.sh"), "#!/bin/sh\necho hello from the test tool\n")
	runGit(t, dir, "init", "--quiet", "-b", "main")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "--quiet", "-m", "the tool")
	runGit(t, dir, "tag", "v1.0.0")
	return dir
}

func TestInstallToolPinsKindAndWritesAnEmptyFragment(t *testing.T) {
	isolate(t)
	repo := newFixtureToolRepo(t, fixtureToolManifest("forgy", "forgy up", "forgy status"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	res, err := in.Install(context.Background(), repo+"@v1.0.0", Options{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Action != "installed" || res.Name != "forgy" {
		t.Fatalf("result = %+v", res)
	}

	pin := mustPin(t, "forgy")
	if pin.Kind != "tool" {
		t.Errorf("pin.Kind = %q, want \"tool\"", pin.Kind)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one consent prompt, got %d", len(seen))
	}
	facts := seen[0].Facts
	if facts.Kind != "tool" {
		t.Errorf("consent kind = %q, want \"tool\"", facts.Kind)
	}
	if len(facts.Provides) != 2 || facts.Provides[0] != "grove forgy up" || facts.Provides[1] != "grove forgy status" {
		t.Errorf("consent provides = %v, want the phrases spelled as grove commands", facts.Provides)
	}
	// None of the panel facts: a tool manifest cannot declare them, and an
	// empty claim bound into the approval would still be a claim.
	if facts.Protocol != "" || facts.Icon != "" || facts.Label != "" ||
		len(facts.Keys) != 0 || len(facts.Views) != 0 || len(facts.Settings) != 0 ||
		len(facts.SettingOptions) != 0 || facts.NotebookSubtree != "" || facts.DigestDescription != "" {
		t.Errorf("a tool's consent facts carry panel facts: %+v", facts)
	}
	if len(facts.Run) != 1 || facts.Run[0] != pin.Binary {
		t.Errorf("consent run = %v, want exactly the installed binary %q", facts.Run, pin.Binary)
	}

	// The fragment file EXISTS — it is the trust anchor and the uninstall
	// unit — but it parses as an empty TOML document, which is what keeps
	// treemux from ever seeing a pane for it.
	data, err := os.ReadFile(pin.Fragment)
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the tool fragment is not valid TOML: %v\n%s", err, data)
	}
	if len(doc) != 0 {
		t.Errorf("the tool fragment declares config, want an empty document: %v\n%s", doc, data)
	}
	text := string(data)
	for _, want := range []string{"trust anchor", "grove forgy up", "grove forgy status", pin.Binary, pin.Commit} {
		if !strings.Contains(text, want) {
			t.Errorf("the fragment record does not mention %q:\n%s", want, text)
		}
	}

	// The binary is installed and linked exactly like a panel's.
	if _, err := os.Stat(pin.Binary); err != nil {
		t.Errorf("installed binary is not readable: %v", err)
	}
	target, err := os.Readlink(pin.Binary)
	if err != nil {
		t.Fatalf("the bin dir entry is not a symlink: %v", err)
	}
	if !strings.Contains(target, pin.Commit) {
		t.Errorf("bin dir entry -> %s, want a path under the pinned commit", target)
	}

	// The approval lives in the exec-trust store, keyed on the fragment.
	if !exectrust.Load().IsTrusted(pin.Fragment, pin.ConsentDigest) {
		t.Error("the install approval must be recorded in the exec-trust store")
	}
}

func TestToolVerbCollisionIsRefusedBeforeConsent(t *testing.T) {
	isolate(t)
	first := newFixtureToolRepo(t, fixtureToolManifest("alpha", "forgy up"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), first+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("first install: %v", err)
	}

	second := newFixtureToolRepo(t, fixtureToolManifest("beta", "forgy down"))
	_, err := in.Install(context.Background(), second+"@v1.0.0", Options{})
	if err == nil {
		t.Fatal("a second owner for `grove forgy` was installed")
	}
	// The refusal names both parties: the plugin being refused and the one
	// holding the verb.
	for _, want := range []string{"beta", "alpha", "forgy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	// Fail fast means BEFORE the consent prompt: the user was never asked
	// about an install that could not proceed.
	if len(seen) != 1 {
		t.Errorf("the refused install reached the consent prompt (%d prompts)", len(seen))
	}
}

func TestToolVerbCollidingWithAnotherPluginsBinaryIsRefused(t *testing.T) {
	isolate(t)
	// A PANEL whose installed binary is named "built-tool": a tool verb equal
	// to any installed binary name is the same collision.
	panelRepo := newFixtureRepo(t, strings.Replace(fixtureManifest("paneled", "P"),
		`command = ["cp", "panel.sh", "built-panel"]
binary  = "built-panel"`,
		`command = ["cp", "panel.sh", "built-tool"]
binary  = "built-tool"`, 1))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), panelRepo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("panel install: %v", err)
	}

	toolRepo := newFixtureToolRepo(t, fixtureToolManifest("clasher", "built-tool run"))
	_, err := in.Install(context.Background(), toolRepo+"@v1.0.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "paneled") || !strings.Contains(err.Error(), "clasher") {
		t.Fatalf("Install = %v, want a refusal naming both plugins", err)
	}
}

func TestReservedVerbIsRefused(t *testing.T) {
	isolate(t)
	repo := newFixtureToolRepo(t, fixtureToolManifest("shadower", "status now"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	in.ReservedVerbs = []string{"status"}
	_, err := in.Install(context.Background(), repo+"@v1.0.0", Options{})
	if err == nil {
		t.Fatal("a tool claiming a reserved verb was installed")
	}
	if !strings.Contains(err.Error(), "shadower") || !strings.Contains(err.Error(), "status") {
		t.Errorf("refusal %q does not name the plugin and the verb", err)
	}
	if len(seen) != 0 {
		t.Errorf("the refused install reached the consent prompt (%d prompts)", len(seen))
	}
}

// An update re-claims the plugin's own verbs without colliding with itself,
// and a changed provides list re-opens the consent prompt with a row saying
// exactly what the tool now answers.
func TestToolUpdateDoesNotSelfCollideAndDiffsProvides(t *testing.T) {
	isolate(t)
	repo := newFixtureToolRepo(t, fixtureToolManifest("forgy", "forgy up"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	write(t, filepath.Join(repo, ManifestFile), fixtureToolManifest("forgy", "forgy up", "forgy down"))
	runGit(t, repo, "commit", "--quiet", "-am", "answer more")
	runGit(t, repo, "tag", "v1.1.0")

	res, err := in.Update(context.Background(), "forgy", Options{Ref: "v1.1.0"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Action != "updated" {
		t.Errorf("action = %q, want updated", res.Action)
	}
	if len(seen) != 2 {
		t.Fatalf("a grown provides list must re-open the prompt, got %d prompts", len(seen))
	}
	var providesRow bool
	for _, c := range seen[1].Changes {
		if c.Field == "provides" {
			providesRow = true
			if !strings.Contains(c.New, "grove forgy down") {
				t.Errorf("provides diff row = %+v, want the new phrase in it", c)
			}
		}
	}
	if !providesRow {
		t.Errorf("diff has no provides row: %+v", seen[1].Changes)
	}
}

func TestDevToolInstallRebuildsAndRecordsTheModeInTheFragment(t *testing.T) {
	isolate(t)
	repo := newFixtureToolRepo(t, fixtureToolManifest("forgy", "forgy up"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	res, err := in.Install(context.Background(), repo, Options{Dev: true})
	if err != nil {
		t.Fatalf("Install --dev: %v", err)
	}
	if !res.Pin.Dev || res.Pin.Kind != "tool" {
		t.Errorf("pin = dev:%v kind:%q, want a dev tool pin", res.Pin.Dev, res.Pin.Kind)
	}

	body, err := os.ReadFile(res.Pin.Fragment)
	if err != nil {
		t.Fatalf("read the fragment: %v", err)
	}
	if !strings.Contains(string(body), "DEVELOPMENT") {
		t.Error("the fragment does not say the tool is a development install")
	}
	if strings.Contains(string(body), "# pinned:") {
		t.Error("the fragment claims a pin that a dev install does not have")
	}
	var doc map[string]any
	if err := toml.Unmarshal(body, &doc); err != nil || len(doc) != 0 {
		t.Errorf("the dev tool fragment must still be an empty document: %v %v", err, doc)
	}

	// The iteration loop: edit, reinstall, and the build picks up the edit.
	write(t, filepath.Join(repo, "tool.sh"), "#!/bin/sh\necho second version\n")
	res, err = in.Install(context.Background(), repo, Options{Dev: true})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Action == "unchanged" {
		t.Fatal("a dev install reported \"unchanged\" — it can never conclude that")
	}
	built, err := os.ReadFile(filepath.Join(repo, "built-tool")) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("read the rebuilt binary: %v", err)
	}
	if !strings.Contains(string(built), "second version") {
		t.Error("the rebuild did not pick up the edited source")
	}
}

func TestToolRemoveLeavesNothingBehind(t *testing.T) {
	isolate(t)
	repo := newFixtureToolRepo(t, fixtureToolManifest("forgy", "forgy up"))

	var seen []*ConsentRequest
	in := approving(t, &seen)
	if _, err := in.Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	pin := mustPin(t, "forgy")
	versionsDir, _ := PluginVersionsDir("forgy")

	removed, err := in.Remove("forgy", false)
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
	if lock.Get("forgy") != nil {
		t.Error("the pin survived the uninstall")
	}
	if IsApproved(pin.Fragment, pin.ConsentDigest) {
		t.Error("the install approval survived the uninstall")
	}
	if entries := exectrust.Load().Entries(); len(entries) != 0 {
		t.Errorf("the trust store still holds %+v", entries)
	}
}

func TestToolVerbsAndPinVerbs(t *testing.T) {
	got := ToolVerbs([]string{"forge up", "forge status", "dns zones list"})
	if len(got) != 2 || got[0] != "forge" || got[1] != "dns" {
		t.Errorf("ToolVerbs = %v, want [forge dns]", got)
	}
	pin := &Pin{Consent: ConsentFacts{Provides: []string{"grove forge up", "grove forge status"}}}
	if v := PinVerbs(pin); len(v) != 1 || v[0] != "forge" {
		t.Errorf("PinVerbs = %v, want [forge]", v)
	}
	if v := PinVerbs(&Pin{}); v != nil {
		t.Errorf("PinVerbs of a panel pin = %v, want nil", v)
	}
}

func TestResolveToolVerb(t *testing.T) {
	lock := &Lock{Plugins: map[string]*Pin{
		"forge": {
			Kind:    "tool",
			Binary:  "/opt/bin/forge",
			Consent: ConsentFacts{Kind: "tool", Provides: []string{"grove forge up"}},
		},
		// A tool whose verb differs from its binary's name: the verb is the
		// declared phrase, the basename is a second door to the same pin.
		"oddball": {
			Kind:    "tool",
			Binary:  "/opt/bin/ob",
			Consent: ConsentFacts{Kind: "tool", Provides: []string{"grove odd run"}},
		},
		// A panel is never dispatched to, even by its binary's name.
		"paneled": {Binary: "/opt/bin/panelbin"},
	}}

	if name, pin, ok := ResolveToolVerb(lock, "forge"); !ok || name != "forge" || pin.Binary != "/opt/bin/forge" {
		t.Errorf("forge -> %q %v %v", name, pin, ok)
	}
	// The binary does not have to exist for the verb to be OWNED — the caller
	// renders "its binary is missing", which is a different answer from
	// "unknown tool".
	if name, _, ok := ResolveToolVerb(lock, "odd"); !ok || name != "oddball" {
		t.Errorf("odd -> %q %v, want the oddball pin by its verb", name, ok)
	}
	if name, _, ok := ResolveToolVerb(lock, "ob"); !ok || name != "oddball" {
		t.Errorf("ob -> %q %v, want the oddball pin by its binary name", name, ok)
	}
	if _, _, ok := ResolveToolVerb(lock, "panelbin"); ok {
		t.Error("a panel pin resolved as a tool verb")
	}
	if _, _, ok := ResolveToolVerb(lock, "nonesuch"); ok {
		t.Error("an unowned verb resolved")
	}
}
