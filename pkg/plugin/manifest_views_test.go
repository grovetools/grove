package plugin

import (
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/exectrust"
	"github.com/pelletier/go-toml/v2"
)

// `[panel.views.<name>]` is the AUTHOR's half of named views: the user names a
// view in their own config, and this is where the panel says which views exist
// and which of them belong in a drawer. It grants nothing and forbids nothing —
// the host reads one bool off it and never a name.

// viewManifest is the shape the ticket specifies, declared in the order that
// carries the preference: `full` first because it is the panel's main layout,
// `compact` marked as the one a drawer should get.
const viewManifest = `
schema_version = 1

[plugin]
name        = "breaktimer"
description = "A break timer"

[build]
command = ["go", "build", "-o", "bin/breaktimer", "."]
binary  = "bin/breaktimer"

[panel]
protocol = "embed/v1"

[panel.views.full]
description = "clock, history and help"
drawer      = false

[panel.views.compact]
description = "one line: state and time remaining"
drawer      = true
`

func TestManifestViewsParseInDeclarationOrder(t *testing.T) {
	m, err := ParseManifest([]byte(viewManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if got := m.Panel.ViewNames(); len(got) != 2 || got[0] != "full" || got[1] != "compact" {
		t.Fatalf("view names = %v, want [full compact] — declaration order is the preference order", got)
	}
	if got := m.Panel.Views["compact"]; !got.Drawer || got.Description != "one line: state and time remaining" {
		t.Errorf("compact = %+v", got)
	}
	// A panel declining to offer its full layout to a drawer is information, not
	// a gap: there is no drawer width at which a title, a clock, a history list
	// and a footer are right.
	if m.Panel.Views["full"].Drawer {
		t.Error("full read as drawer-suitable")
	}
	if got := m.Panel.PreferredDrawerView(); got != "compact" {
		t.Errorf("preferred drawer view = %q, want compact", got)
	}
	if len(m.Unknown) != 0 {
		t.Errorf("unexpected unknown keys: %v", m.Unknown)
	}
}

// Order is recovered from the document, so every spelling TOML allows for the
// same table has to yield the same order. The author picks the spelling; the
// host must not reward one over another.
func TestViewOrderSurvivesEveryTomlSpelling(t *testing.T) {
	cases := map[string]string{
		"table headers": `
[panel.views.full]
description = "big"
[panel.views.compact]
description = "small"
drawer      = true
`,
		"dotted keys under a shared header": `
[panel.views]
full.description    = "big"
compact.description = "small"
compact.drawer      = true
`,
		"fully dotted under panel": `
[panel]
views.full.description    = "big"
views.compact.description = "small"
views.compact.drawer      = true
`,
		"inline table": `
[panel]
views = { full = { description = "big" }, compact = { description = "small", drawer = true } }
`,
	}
	const head = `
schema_version = 1

[plugin]
name        = "breaktimer"
description = "A break timer"

[build]
binary = "bin/breaktimer"
`
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := ParseManifest([]byte(head + body))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			if got := m.Panel.ViewNames(); len(got) != 2 || got[0] != "full" || got[1] != "compact" {
				t.Errorf("view names = %v, want [full compact]", got)
			}
			if got := m.Panel.PreferredDrawerView(); got != "compact" {
				t.Errorf("preferred drawer view = %q, want compact", got)
			}
		})
	}
}

// Declaring no views, and declaring none for the drawer, are both VALID. The
// first is every panel written before this table existed; the second is a panel
// saying none of its layouts belongs in a narrow column.
func TestManifestViewsMayBeAbsentOrAllUnsuitable(t *testing.T) {
	none, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("a manifest with no views must parse: %v", err)
	}
	if got := none.Panel.ViewNames(); got != nil {
		t.Errorf("view names = %v, want nil", got)
	}
	if got := none.Panel.PreferredDrawerView(); got != "" {
		t.Errorf("preferred drawer view = %q, want empty", got)
	}

	unsuitable, err := ParseManifest([]byte(strings.Replace(viewManifest, "drawer      = true", "drawer      = false", 1)))
	if err != nil {
		t.Fatalf("a manifest offering no view to the drawer must parse: %v", err)
	}
	if got := unsuitable.Panel.PreferredDrawerView(); got != "" {
		t.Errorf("preferred drawer view = %q, want empty — nothing was offered", got)
	}
	// Still two declared views, still in order: nothing is dropped for being
	// unsuitable, because the user may still ask for one by name.
	if got := unsuitable.Panel.ViewNames(); len(got) != 2 {
		t.Errorf("view names = %v, want both", got)
	}
}

func TestManifestViewsRejectWhatCannotBeRead(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		wantIn   string
	}{
		{
			"view without a description",
			strings.Replace(viewManifest, `description = "one line: state and time remaining"`, "", 1),
			"panel.views.compact.description is required",
		},
		{
			"name padded with whitespace",
			strings.Replace(viewManifest, "[panel.views.compact]", `[panel.views." compact "]`, 1),
			"must not begin or end with whitespace",
		},
		{
			"blank name",
			strings.Replace(viewManifest, "[panel.views.compact]", `[panel.views.""]`, 1),
			"blank name",
		},
		{
			"control character in a name",
			strings.Replace(viewManifest, "[panel.views.compact]", `[panel.views."comp\u001b[2Jact"]`, 1),
			"control character",
		},
		{
			"control character in a description",
			strings.Replace(viewManifest, `description = "one line: state and time remaining"`, `description = "one \u001b[2Jline"`, 1),
			"control character",
		},
		{
			// A wrong TYPE is a decode failure rather than a strict-mode warning,
			// so unlike an unknown key it stops the install: the manifest says
			// something about the drawer that cannot be read as a yes or a no.
			"drawer that is not a bool",
			strings.Replace(viewManifest, "drawer      = true", `drawer      = "yes"`, 1),
			"parse grove-plugin.toml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.manifest))
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantIn)
			}
		})
	}
}

// An unrecognised key INSIDE a view is a warning like any other: a manifest
// written for a later schema — one that grows `min_width` per view, say — must
// still install, and still say what it could not read.
func TestUnknownKeyInsideAViewIsReportedNotFatal(t *testing.T) {
	m, err := ParseManifest([]byte(viewManifest + "\n[panel.views.mini]\ndescription = \"tiny\"\nmin_width = 20\n"))
	if err != nil {
		t.Fatalf("an unknown key inside a view must not fail the parse: %v", err)
	}
	if !strings.Contains(strings.Join(m.Unknown, ","), "panel.views.mini.min_width") {
		t.Errorf("unknown = %v, want it to name panel.views.mini.min_width", m.Unknown)
	}
	// And the view it appeared in is still declared, with the keys that did read.
	if got := m.Panel.Views["mini"]; got.Description != "tiny" {
		t.Errorf("mini = %+v", got)
	}
}

// ViewNames falls back to sorted names when there is no recovered order — a
// Manifest assembled in memory rather than parsed. The order is a refinement of
// the map, never its authority, and the fallback has to be deterministic because
// what it feeds ends up in an approval digest.
func TestViewNamesFallsBackToSortedNames(t *testing.T) {
	p := Panel{Views: map[string]View{
		"wide":    {Description: "wide"},
		"compact": {Description: "small", Drawer: true},
		"full":    {Description: "big"},
	}}
	if got := p.ViewNames(); len(got) != 3 || got[0] != "compact" || got[1] != "full" || got[2] != "wide" {
		t.Fatalf("view names = %v, want them sorted", got)
	}

	// A partial order is honored as far as it goes, and the rest is appended
	// sorted rather than dropped: every declared view is validated and rendered.
	p.ViewOrder = []string{"wide", "gone"}
	if got := p.ViewNames(); len(got) != 3 || got[0] != "wide" || got[1] != "compact" || got[2] != "full" {
		t.Errorf("view names = %v, want [wide compact full]", got)
	}
}

// The fragment is the contract between the installer and the host: the manifest's
// named tables have to arrive as something core/config decodes, in the order the
// author wrote, because the host's default reads the FIRST drawer-suitable entry.
func TestFragmentCarriesTheViewDeclarationInOrder(t *testing.T) {
	m, err := ParseManifest([]byte(viewManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, err := RenderFragment(m, "/opt/grove/bin/breaktimer", &Pin{Spec: "x", Commit: "abc"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var frag struct {
		TUI struct {
			Plugins map[string]*config.PluginConfig `toml:"plugins"`
		} `toml:"tui"`
	}
	if err := toml.Unmarshal(data, &frag); err != nil {
		t.Fatalf("the fragment does not decode into core's own config type: %v\n%s", err, data)
	}
	entry := frag.TUI.Plugins["breaktimer"]
	if entry == nil {
		t.Fatalf("no [tui.plugins.breaktimer]:\n%s", data)
	}
	if len(entry.Views) != 2 {
		t.Fatalf("declared views = %+v, want 2:\n%s", entry.Views, data)
	}
	if entry.Views[0].Name != "full" || entry.Views[1].Name != "compact" {
		t.Errorf("fragment order = %q, %q, want full, compact", entry.Views[0].Name, entry.Views[1].Name)
	}
	if entry.Views[0].Drawer || !entry.Views[1].Drawer {
		t.Errorf("drawer bools = %v, %v, want false, true", entry.Views[0].Drawer, entry.Views[1].Drawer)
	}
	if entry.Views[1].Description != "one line: state and time remaining" {
		t.Errorf("description = %q", entry.Views[1].Description)
	}

	// The end of the trip: those entries copied onto a drawer pane declaration
	// resolve to the view the author preferred, which is the whole point of
	// carrying the order this far.
	pane := &config.DrawerPaneConfig{Backend: config.DrawerBackendSidecar, Command: "breaktimer", Views: entry.Views}
	if got := pane.EffectiveView(); got != "compact" {
		t.Errorf("a pane naming no view resolved to %q, want compact", got)
	}
}

// The consent screen reports the declaration, and the approval binds it: an
// update that stops offering `compact` to the drawer changes what the user sees
// in their drawer, and must not pass as "nothing you approved has changed".
func TestConsentReportsAndBindsTheViewDeclaration(t *testing.T) {
	m, err := ParseManifest([]byte(viewManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	facts := NewConsentFacts(m, ResolvedSource{Commit: "abc"}, []byte(viewManifest), "/opt/grove/bin/breaktimer")
	if len(facts.Views) != 2 {
		t.Fatalf("consent views = %v, want 2", facts.Views)
	}
	if !strings.HasPrefix(facts.Views[0], "full — clock, history and help") {
		t.Errorf("first line = %q", facts.Views[0])
	}
	// The line says which one a drawer would get, because that is the fact the
	// bool decides and the user cannot infer it from the names.
	if !strings.Contains(facts.Views[1], "by default") {
		t.Errorf("the drawer default is not marked: %q", facts.Views[1])
	}

	// Flipping the bool is a change to the approval, and Diff names it.
	flipped := strings.Replace(viewManifest, "drawer      = true", "drawer      = false", 1)
	m2, err := ParseManifest([]byte(flipped))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	next := NewConsentFacts(m2, ResolvedSource{Commit: "abc"}, []byte(flipped), "/opt/grove/bin/breaktimer")
	if facts.Digest() == next.Digest() {
		t.Error("withdrawing a view from the drawer did not change the approval digest")
	}
	var named bool
	for _, c := range Diff(facts, next) {
		if c.Field == "views" {
			named = true
		}
	}
	if !named {
		t.Errorf("Diff did not report the view change: %+v", Diff(facts, next))
	}
}

// A manifest that declares no views hashes exactly as it did before views
// existed, so every plugin already installed stays approved. The parts list is
// spelled out rather than compared against a sibling ConsentFacts, because the
// claim is about a specific historical string and not about two values differing.
func TestDigestIsUnchangedForAManifestWithoutViews(t *testing.T) {
	facts := ConsentFacts{
		Name: "timer", Source: "src", Commit: "abc",
		ManifestDigest: "sha256:dead", Build: []string{"go", "build"},
		Run: []string{"/opt/grove/bin/timer"}, Protocol: "embed/v1",
		Keys: []string{"ctrl+f — start a break"}, Label: "Timer",
		Settings: []string{"work_minutes = 25"},
	}
	want := exectrust.Digest([]string{
		"source=src",
		"commit=abc",
		"manifest=sha256:dead",
		"build=go\x1fbuild",
		"run=/opt/grove/bin/timer",
		"env=",
		"protocol=embed/v1",
		"keys=ctrl+f — start a break",
		"label=Timer",
		"settings=work_minutes = 25",
	})
	if got := facts.Digest(); got != want {
		t.Errorf("digest = %q, want %q — an approval recorded before views existed would read as edited", got, want)
	}
}
