package plugin

import (
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/exectrust"
	"github.com/pelletier/go-toml/v2"
)

// `[panel.notebook]` is the panel's disclosure of the notebook subtree it
// writes. It grants nothing and forbids nothing — the host never resolves,
// creates or fences the path — so what these tests pin is the disclosure
// itself: that it parses, that a half-declared one is refused, that the
// approval digest binds it, and above all that a manifest WITHOUT it hashes
// exactly as it always did.

// notebookManifest is the documented shape: a panel that clips stories into
// the user's notebook and says where.
const notebookManifest = `
schema_version = 1

[plugin]
name        = "hn"
description = "A Hacker News reader"

[build]
command = ["go", "build", "-o", "bin/grove-panel-hn", "."]
binary  = "bin/grove-panel-hn"

[panel]
protocol = "embed/v1"

[panel.notebook]
subtree     = "hn/clippings"
description = "stories you clip from the feed"
`

func TestManifestNotebookParses(t *testing.T) {
	m, err := ParseManifest([]byte(notebookManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	nb := m.Panel.Notebook
	if nb == nil {
		t.Fatal("[panel.notebook] did not parse")
	}
	if nb.Subtree != "hn/clippings" || nb.Description != "stories you clip from the feed" {
		t.Errorf("notebook = %+v", nb)
	}
	// The section is a known key now, not a forward-compat warning.
	if len(m.Unknown) != 0 {
		t.Errorf("unexpected unknown keys: %v", m.Unknown)
	}
}

// A manifest that declares no notebook stays valid and carries none: most
// panels save nothing, and absence must remain the ordinary case.
func TestManifestWithoutNotebookParsesToNil(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Panel.Notebook != nil {
		t.Errorf("notebook = %+v, want nil for a manifest that declares none", m.Panel.Notebook)
	}
}

func TestManifestNotebookRejects(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		wantIn   string
	}{
		{"missing subtree", strings.Replace(notebookManifest, `subtree     = "hn/clippings"`, "", 1), "panel.notebook.subtree is required"},
		{"missing description", strings.Replace(notebookManifest, `description = "stories you clip from the feed"`, "", 1), "panel.notebook.description is required"},
		{"escaping subtree", strings.Replace(notebookManifest, `"hn/clippings"`, `"../outside"`, 1), "must stay inside"},
		{"absolute subtree", strings.Replace(notebookManifest, `"hn/clippings"`, `"/etc/hn"`, 1), "must be relative"},
		{"trailing slash", strings.Replace(notebookManifest, `"hn/clippings"`, `"hn/clippings/"`, 1), "must not begin or end with a slash"},
		{"overlong subtree", strings.Replace(notebookManifest, `"hn/clippings"`, `"hn/`+strings.Repeat("x", 130)+`"`, 1), "longer than a path"},
		{"control character in the description", strings.Replace(notebookManifest, `"stories you clip from the feed"`, `"stories\u001b[2J"`, 1), "control character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.manifest))
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantIn)
			}
		})
	}
}

// The consent screen reports the declaration and the approval binds it: an
// update that moves the subtree changes what appears in the user's notebook,
// and must not pass as "nothing you approved has changed".
func TestConsentReportsAndBindsTheNotebookDeclaration(t *testing.T) {
	m, err := ParseManifest([]byte(notebookManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	facts := NewConsentFacts(m, ResolvedSource{Commit: "abc"}, []byte(notebookManifest), "/opt/grove/bin/grove-panel-hn")
	if facts.NotebookSubtree != "hn/clippings" || facts.NotebookDescription != "stories you clip from the feed" {
		t.Errorf("consent notebook = %q / %q", facts.NotebookSubtree, facts.NotebookDescription)
	}

	moved := strings.Replace(notebookManifest, `"hn/clippings"`, `"hn/archive"`, 1)
	m2, err := ParseManifest([]byte(moved))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	next := NewConsentFacts(m2, ResolvedSource{Commit: "abc"}, []byte(moved), "/opt/grove/bin/grove-panel-hn")
	if facts.Digest() == next.Digest() {
		t.Error("moving the notebook subtree did not change the approval digest")
	}
	var named bool
	for _, c := range Diff(facts, next) {
		if c.Field == "notebook" {
			named = true
			if !strings.Contains(c.Old, "hn/clippings") || !strings.Contains(c.New, "hn/archive") {
				t.Errorf("notebook diff = %+v, want both subtrees named", c)
			}
		}
	}
	if !named {
		t.Errorf("Diff did not report the notebook change: %+v", Diff(facts, next))
	}

	// Withdrawing the declaration entirely is also a change: the panel stops
	// saying what it does with the notebook, which the user should see.
	if changes := Diff(facts, ConsentFacts{}); len(changes) == 0 {
		t.Error("removing the notebook declaration diffed as nothing")
	}
}

// THE critical property: a manifest that declares no notebook hashes exactly
// as it did before the section existed, so every plugin already installed
// stays approved. The parts list is spelled out rather than compared against
// a sibling ConsentFacts, because the claim is about a specific historical
// string and not about two values differing. Views are present deliberately:
// the notebook line must add nothing even when other conditional lines fire.
func TestDigestIsUnchangedForAManifestWithoutNotebook(t *testing.T) {
	facts := ConsentFacts{
		Name: "timer", Source: "src", Commit: "abc",
		ManifestDigest: "sha256:dead", Build: []string{"go", "build"},
		Run: []string{"/opt/grove/bin/timer"}, Protocol: "embed/v1",
		Keys: []string{"ctrl+f — start a break"}, Label: "Timer",
		Settings: []string{"work_minutes = 25"},
		Views:    []string{"compact — one line (what a drawer pane gets by default)"},
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
		"views=compact — one line (what a drawer pane gets by default)",
	})
	if got := facts.Digest(); got != want {
		t.Errorf("digest = %q, want %q — an approval recorded before [panel.notebook] existed would read as edited", got, want)
	}

	// And the converse: the same facts WITH a declaration hash differently,
	// which is what re-opens the prompt for a panel that starts saving.
	declared := facts
	declared.NotebookSubtree = "hn/clippings"
	declared.NotebookDescription = "stories you clip from the feed"
	if declared.Digest() == want {
		t.Error("declaring a notebook subtree did not change the approval digest")
	}
}

// The fragment is the contract between the installer and the host, so the
// declaration has to arrive as something core/config actually decodes — and
// it has to NEST correctly: [tui.plugins.hn.notebook], not a [notebook]
// stranded beside [tui].
func TestFragmentCarriesTheNotebookDeclarationToTheHost(t *testing.T) {
	m, err := ParseManifest([]byte(notebookManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, err := RenderFragment(m, "/opt/grove/bin/grove-panel-hn", &Pin{Spec: "x", Commit: "abc"})
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
	entry := frag.TUI.Plugins["hn"]
	if entry == nil {
		t.Fatalf("no [tui.plugins.hn]:\n%s", data)
	}
	if entry.Notebook == nil {
		t.Fatalf("the notebook declaration did not survive the fragment:\n%s", data)
	}
	if entry.Notebook.Subtree != "hn/clippings" || entry.Notebook.Description != "stories you clip from the feed" {
		t.Errorf("notebook = %+v, want the manifest's", entry.Notebook)
	}
}
