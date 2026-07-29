package plugin

import (
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/pelletier/go-toml/v2"
)

// validManifest is the smallest manifest that passes, and the base every
// negative case below mutates one field of.
const validManifest = `
schema_version = 1

[plugin]
name        = "hello"
description = "A hello-world sidecar panel"

[build]
command = ["go", "build", "-o", "bin/grove-panel-hello", "."]
binary  = "bin/grove-panel-hello"

[panel]
icon     = "H"
protocol = "embed/v1"

[[panel.keys]]
key         = "ctrl+f"
description = "jump to the notebook"
`

func TestParseManifestAcceptsTheDocumentedShape(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Plugin.Name != "hello" {
		t.Errorf("name = %q", m.Plugin.Name)
	}
	if got, want := strings.Join(m.Build.Command, " "), "go build -o bin/grove-panel-hello ."; got != want {
		t.Errorf("build command = %q, want %q", got, want)
	}
	if m.BinaryName() != "grove-panel-hello" {
		t.Errorf("BinaryName = %q", m.BinaryName())
	}
	if len(m.Panel.Keys) != 1 || m.Panel.Keys[0].Key != "ctrl+f" {
		t.Errorf("keys = %+v", m.Panel.Keys)
	}
	if len(m.Unknown) != 0 {
		t.Errorf("unexpected unknown keys: %v", m.Unknown)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		wantIn   string
	}{
		{"no schema version", strings.Replace(validManifest, "schema_version = 1", "", 1), "schema_version is required"},
		{"future schema version", strings.Replace(validManifest, "schema_version = 1", "schema_version = 2", 1), "newer than this grove"},
		{"no name", strings.Replace(validManifest, `name        = "hello"`, "", 1), "plugin.name is required"},
		{"name with a slash", strings.Replace(validManifest, `"hello"`, `"../../etc/hello"`, 1), "must be lowercase letters"},
		{"uppercase name", strings.Replace(validManifest, `"hello"`, `"Hello"`, 1), "must be lowercase letters"},
		{"no description", strings.Replace(validManifest, `description = "A hello-world sidecar panel"`, "", 1), "plugin.description is required"},
		{"no binary", strings.Replace(validManifest, `binary  = "bin/grove-panel-hello"`, "", 1), "build.binary is required"},
		{"absolute binary", strings.Replace(validManifest, `"bin/grove-panel-hello"
`, `"/usr/bin/env"
`, 1), "must be relative"},
		{"escaping binary", strings.Replace(validManifest, `binary  = "bin/grove-panel-hello"`, `binary  = "../../../bin/sh"`, 1), "must stay inside"},
		{"unknown protocol", strings.Replace(validManifest, `"embed/v1"`, `"embed/v9"`, 1), "not a protocol this host speaks"},
		{"bad timeout", strings.Replace(validManifest, `protocol = "embed/v1"`, `protocol_timeout = "soon"`, 1), "not a Go duration"},
		{"bad env", strings.Replace(validManifest, `icon     = "H"`, `env = ["not-an-assignment"]`, 1), "must be KEY=VALUE"},
		{"empty argv element", strings.Replace(validManifest, `"go", "build"`, `"go", ""`, 1), "build.command[1] is empty"},
		{"key without a description", strings.Replace(validManifest, `description = "jump to the notebook"`, "", 1), "description is required"},
		{"control character in the icon", strings.Replace(validManifest, `icon     = "H"`, `icon = "\u001b[2J"`, 1), "control character"},
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

// A manifest written for a later grove must still install, with its
// unrecognized keys reported rather than swallowed or fatal. That is what
// keeps one plugin repo installable by two grove versions.
func TestParseManifestReportsUnknownKeysWithoutFailing(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest + "\n[panel.sandbox]\nnetwork = false\n"))
	if err != nil {
		t.Fatalf("an unknown key must not fail the parse: %v", err)
	}
	if len(m.Unknown) == 0 {
		t.Fatal("expected the unknown key to be reported")
	}
	if !strings.Contains(strings.Join(m.Unknown, ","), "sandbox") {
		t.Errorf("unknown = %v, want it to name panel.sandbox", m.Unknown)
	}
}

// An interpreted panel ships its program in the repo and needs no toolchain.
func TestParseManifestAllowsNoBuildStep(t *testing.T) {
	manifest := strings.Replace(validManifest, `command = ["go", "build", "-o", "bin/grove-panel-hello", "."]`, "", 1)
	m, err := ParseManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Build.Command) != 0 {
		t.Errorf("build.command = %v, want empty", m.Build.Command)
	}
}

// A panel's settings are the only thing on the consent screen the user is
// expected to go on to edit, so they have to survive the manifest, the digest
// and the fragment intact.
func TestSettingsAndLabelRoundTripThroughTheManifest(t *testing.T) {
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
protocol = "embed/v1"

[panel.settings]
work_minutes  = 25
break_minutes = 5
chime         = "bell"

[panel.settings.notify]
desktop = true
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Panel.Label != "Break timer" {
		t.Errorf("label = %q", m.Panel.Label)
	}

	flat := FlattenSettings(m.Panel.Settings)
	want := []string{
		"break_minutes = 5",
		"chime = bell",
		"notify.desktop = true",
		"work_minutes = 25",
	}
	if len(flat) != len(want) {
		t.Fatalf("flattened settings = %v, want %v", flat, want)
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Errorf("flattened[%d] = %q, want %q", i, flat[i], want[i])
		}
	}
}

// The flattening feeds a digest, so it must not depend on map iteration order:
// an unchanged manifest that re-hashed on every run would re-open the consent
// prompt forever.
func TestFlattenSettingsIsStable(t *testing.T) {
	settings := map[string]any{
		"z": 1, "a": 2, "m": 3, "nested": map[string]any{"q": 4, "b": 5},
	}
	first := strings.Join(FlattenSettings(settings), "|")
	for i := 0; i < 50; i++ {
		if got := strings.Join(FlattenSettings(settings), "|"); got != first {
			t.Fatalf("flattening is order-dependent:\n %s\n %s", first, got)
		}
	}
}

// Everything in a settings table is printed on a screen the user's approval
// depends on, so a value that could redraw that screen is refused rather than
// displayed — including one buried in a nested table or an array.
func TestSettingsRejectControlCharactersAtAnyDepth(t *testing.T) {
	for name, settings := range map[string]map[string]any{
		"top-level value": {"greeting": "hi\x1b[2Jbye"},
		"nested value":    {"a": map[string]any{"b": "x\rrewritten"}},
		"array element":   {"list": []any{"fine", "bad\x1b[A"}},
		"key":             {"we\x1bird": "fine"},
	} {
		if err := validateSettings("panel.settings", settings); err == nil {
			t.Errorf("%s: a control character was accepted", name)
		}
	}

	ok := map[string]any{
		"work_minutes": int64(25),
		"chime":        "bell",
		"tags":         []any{"a", "b"},
		"notify":       map[string]any{"desktop": true},
	}
	if err := validateSettings("panel.settings", ok); err != nil {
		t.Errorf("an ordinary settings table was refused: %v", err)
	}
}

// A changed default is a changed behavior, so an update diff has to name the
// setting and both values rather than saying "settings changed".
func TestDiffNamesTheSettingThatMoved(t *testing.T) {
	old := ConsentFacts{Settings: []string{"break_minutes = 5", "work_minutes = 25"}}
	next := ConsentFacts{Settings: []string{"break_minutes = 5", "chime = bell", "work_minutes = 50"}}

	changes := Diff(old, next)
	byField := map[string]FactChange{}
	for _, c := range changes {
		byField[c.Field] = c
	}
	if c, ok := byField["settings.work_minutes"]; !ok || c.Old != "25" || c.New != "50" {
		t.Errorf("work_minutes change = %+v, want 25 → 50", c)
	}
	if c, ok := byField["settings.chime"]; !ok || c.Old != "" || c.New != "bell" {
		t.Errorf("added setting = %+v, want an addition of bell", c)
	}
	if _, ok := byField["settings.break_minutes"]; ok {
		t.Error("an unchanged setting appeared in the diff")
	}
}

// The digest binds the approval, so a retuned default must re-open the prompt.
func TestDigestCoversSettingsAndLabel(t *testing.T) {
	base := ConsentFacts{Name: "timer", Settings: []string{"work_minutes = 25"}}
	retuned := ConsentFacts{Name: "timer", Settings: []string{"work_minutes = 50"}}
	if base.Digest() == retuned.Digest() {
		t.Error("changing a settings default did not change the approval digest")
	}
	relabeled := ConsentFacts{Name: "timer", Settings: []string{"work_minutes = 25"}, Label: "Timer"}
	if base.Digest() == relabeled.Digest() {
		t.Error("changing the label did not change the approval digest")
	}
}

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
