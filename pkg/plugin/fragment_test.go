package plugin

import (
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/pelletier/go-toml/v2"
)

// The fragment is the "Declare" stage's output, and these are the tests that a
// manifest's declarations survive the trip into it. The manifest schema itself,
// its validation and the approval digest moved to core/pkg/plugin and are tested
// there; what is left here is the half core cannot see — that what grove writes
// decodes into core/config's own types, correctly nested.

// The fragment is the contract between the installer and the host, so what a
// manifest declares has to arrive as something core/config actually decodes —
// not merely as valid TOML.
func TestFragmentCarriesSettingsLabelAndKeysToTheHost(t *testing.T) {
	m, err := ParseManifest([]byte(`
schema_version = 1

[plugin]
name        = "timer"
description = "A break timer"

[build]
command = ["go", "build", "-o", "bin/timer", "."]
binary  = "bin/timer"

[panel]
label    = "Break timer"
icon     = "T"
protocol = "embed/v1"

[panel.settings]
work_minutes = 25

[[panel.keys]]
key         = "ctrl+f"
description = "start a break now"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data, err := RenderFragment(m, "/opt/grove/bin/timer", &Pin{Spec: "x", Commit: "abc"})
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
	entry := frag.TUI.Plugins["timer"]
	if entry == nil {
		t.Fatalf("no [tui.plugins.timer]:\n%s", data)
	}
	if entry.Label != "Break timer" {
		t.Errorf("label = %q, want the manifest's", entry.Label)
	}
	if got := entry.Settings["work_minutes"]; got != int64(25) {
		t.Errorf("settings work_minutes = %#v, want 25", got)
	}
	if len(entry.Keys) != 1 || entry.Keys[0].Key != "ctrl+f" ||
		entry.Keys[0].Description != "start a break now" {
		t.Errorf("declared keys = %+v, want the manifest's ctrl+f row", entry.Keys)
	}
}

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

// digestFragmentManifest is the documented shape: a panel that publishes a
// projection of itself and says what it shows.
const digestFragmentManifest = `
schema_version = 1

[plugin]
name        = "breaktimer"
description = "A work/break interval timer"

[build]
command = ["go", "build", "-o", "bin/grove-panel-breaktimer", "."]
binary  = "bin/grove-panel-breaktimer"

[panel]
protocol = "embed/v1"

[panel.digest]
description = "the timer's state and how long is left of it"
`

// The declaration has to arrive as something core/config decodes, correctly
// nested — [tui.plugins.breaktimer.digest], not a [digest] stranded beside
// [tui]. That is the failure the whole-document marshal in RenderFragment
// exists to prevent, and every sub-table added since owes it a test.
func TestFragmentCarriesTheDigestDeclarationToTheHost(t *testing.T) {
	m, err := ParseManifest([]byte(digestFragmentManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, err := RenderFragment(m, "/opt/grove/bin/grove-panel-breaktimer", &Pin{Spec: "x", Commit: "abc"})
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
	if entry.Digest == nil {
		t.Fatalf("the digest declaration did not survive the fragment:\n%s", data)
	}
	if entry.Digest.Description != "the timer's state and how long is left of it" {
		t.Errorf("digest = %+v, want the manifest's", entry.Digest)
	}
}

// A manifest that declares no digest writes no sub-table. Absence has to stay
// absent rather than become an empty `[digest]`: the field's whole contract is
// that it is read in the affirmative only, and a table with a blank description
// would be a declaration nobody made.
func TestFragmentOmitsTheDigestTableWhenTheManifestDeclaresNone(t *testing.T) {
	m, err := ParseManifest([]byte(notebookManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, err := RenderFragment(m, "/opt/grove/bin/grove-panel-hn", &Pin{Spec: "x", Commit: "abc"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(data), "digest") {
		t.Errorf("a manifest declaring no digest still wrote one:\n%s", data)
	}
}

// optionsFragmentManifest declares a browser selector: a closed list, one entry
// of which hands the decision to a free-text setting beside it, plus a second
// setting whose list is only a suggestion.
const optionsFragmentManifest = `
schema_version = 1

[plugin]
name        = "hn"
description = "A Hacker News reader"

[build]
binary = "bin/grove-panel-hn"

[panel]
protocol = "embed/v1"

[panel.settings]
open_url_command        = "system"
open_url_custom_command = ""
palette                 = "hn"

[[panel.setting_options]]
setting        = "open_url_command"
description    = "which browser opens a story"
options        = ["system", "firefox", "custom"]
custom_option  = "custom"
custom_setting = "open_url_custom_command"

[[panel.setting_options]]
setting      = "palette"
options      = ["hn", "host"]
allow_custom = true
`

// The declaration has to arrive as something core/config decodes, in the
// author's order, because that order is the order the host's editor offers the
// values in — and it has to nest under the panel rather than strand a
// [setting_options] beside [tui].
func TestFragmentCarriesTheSettingOptionsToTheHost(t *testing.T) {
	m, err := ParseManifest([]byte(optionsFragmentManifest))
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
	if len(entry.SettingOptions) != 2 {
		t.Fatalf("setting options = %+v, wanted both:\n%s", entry.SettingOptions, data)
	}
	first, second := entry.SettingOptions[0], entry.SettingOptions[1]
	if first.Setting != "open_url_command" || second.Setting != "palette" {
		t.Errorf("fragment order = %q, %q, want the author's", first.Setting, second.Setting)
	}
	if strings.Join(first.Options, ",") != "system,firefox,custom" {
		t.Errorf("options = %v", first.Options)
	}
	if first.Description != "which browser opens a story" {
		t.Errorf("description = %q", first.Description)
	}
	if first.CustomOption != "custom" || first.CustomSetting != "open_url_custom_command" {
		t.Errorf("custom pair = %+v", first)
	}
	if first.AllowCustom {
		t.Error("allow_custom was written for a declaration that does not set it")
	}
	if !second.AllowCustom {
		t.Errorf("allow_custom did not survive: %+v", second)
	}
}

// A manifest that declares no options writes no array. Absence stays absent for
// the reason the digest table's does: an empty declaration is one nobody made.
func TestFragmentOmitsSettingOptionsWhenTheManifestDeclaresNone(t *testing.T) {
	m, err := ParseManifest([]byte(notebookManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, err := RenderFragment(m, "/opt/grove/bin/grove-panel-hn", &Pin{Spec: "x", Commit: "abc"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(data), "setting_options") {
		t.Errorf("a manifest declaring no options still wrote some:\n%s", data)
	}
}
