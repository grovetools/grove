package plugin

import (
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// settingsManifest is a panel with one of every settable type, including a
// nested table and a duration string — the four coercions plus the dotted path.
func settingsManifest(name string) string {
	return `schema_version = 1

[plugin]
name        = "` + name + `"
description = "a test panel with settings"

[build]
command = ["cp", "panel.sh", "built-panel"]
binary  = "built-panel"

[panel]
icon     = "D"
protocol = "embed/v1"

[panel.settings]
work_minutes = 25
ratio        = 1.5
chime        = true
refresh      = "2s"
title        = "Focus"

[panel.settings.feed]
limit = 30

[panel.views.full]
description = "clock, history and help"
drawer      = false

[panel.views.compact]
description = "one line"
drawer      = true

[[panel.keys]]
key         = "ctrl+f"
description = "a claimed chord"

[panel.notebook]
subtree     = "demo/clippings"
description = "what you clip"
`
}

// installWithSettings installs the settings fixture and returns its pin.
func installWithSettings(t *testing.T) *Pin {
	t.Helper()
	repo := newFixtureRepo(t, settingsManifest("demo"))
	var seen []*ConsentRequest
	if _, err := approving(t, &seen).Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	return mustPin(t, "demo")
}

// fragmentSettings decodes [tui.plugins.<name>.settings] out of a written
// fragment.
func fragmentSettings(t *testing.T, path, name string) map[string]any {
	t.Helper()
	panel := fragmentEntry(t, path, name)
	return panel["settings"].(map[string]any)
}

func fragmentEntry(t *testing.T, path, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the fragment is not valid TOML: %v\n%s", err, data)
	}
	tui, ok := doc["tui"].(map[string]any)
	if !ok {
		t.Fatalf("fragment has no [tui]:\n%s", data)
	}
	plugins, ok := tui["plugins"].(map[string]any)
	if !ok {
		t.Fatalf("fragment has no [tui.plugins]:\n%s", data)
	}
	entry, ok := plugins[name].(map[string]any)
	if !ok {
		t.Fatalf("fragment has no [tui.plugins.%s]:\n%s", name, data)
	}
	return entry
}

func setter(t *testing.T) *Installer {
	t.Helper()
	return &Installer{Out: io.Discard}
}

// The round trip the panel depends on: after a settings edit the plugin is
// still approved, still intact, and `grove plugin list` still calls it ok
// rather than edited.
func TestSetRewritesSettingsAndKeepsTheApproval(t *testing.T) {
	isolate(t)
	pin := installWithSettings(t)
	before := pin.ConsentDigest

	res, err := setter(t).Set("demo", []string{"work_minutes=30", "feed.limit=50"}, false)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	settings := fragmentSettings(t, pin.Fragment, "demo")
	if settings["work_minutes"] != int64(30) {
		t.Errorf("work_minutes = %#v, want the int64 30", settings["work_minutes"])
	}
	feed, ok := settings["feed"].(map[string]any)
	if !ok || feed["limit"] != int64(50) {
		t.Errorf("feed.limit = %#v, want the int64 50", settings["feed"])
	}

	after := mustPin(t, "demo")
	if after.ConsentDigest == before {
		t.Error("the consent digest must move with the settings — they are hashed into it")
	}
	if after.ConsentDigest != res.Pin.ConsentDigest {
		t.Errorf("the result's pin (%s) disagrees with the lockfile (%s)", res.Pin.ConsentDigest, after.ConsentDigest)
	}
	if !IsApproved(after.Fragment, after.ConsentDigest) {
		t.Fatal("a settings edit grove made must leave the plugin approved")
	}

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d rows, want 1", len(list))
	}
	if st := list[0]; !st.Approved || !st.FragmentPresent || !st.BinaryPresent {
		t.Errorf("after set, list reads %+v — want ok, not edited", st)
	}

	// The lockfile's consent snapshot has to move too, or the next update would
	// diff against settings that are no longer in the file.
	if !contains(after.Consent.Settings, "work_minutes = 30") {
		t.Errorf("the recorded consent still says %v", after.Consent.Settings)
	}
	if after.ManifestDigest != pin.ManifestDigest {
		t.Error("a settings edit must not change the manifest digest — the manifest did not change")
	}
	if after.InstalledAt != pin.InstalledAt {
		t.Errorf("installed_at moved from %q to %q — nothing was installed", pin.InstalledAt, after.InstalledAt)
	}
}

// The diff is the whole user-facing output, and it has to name the setting and
// both of its values rather than saying "the settings changed".
func TestSetReportsWhatMoved(t *testing.T) {
	isolate(t)
	installWithSettings(t)

	res, err := setter(t).Set("demo", []string{"work_minutes=30"}, false)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %+v, want exactly the one setting that moved", res.Changes)
	}
	c := res.Changes[0]
	if c.Field != "settings.work_minutes" || c.Old != "25" || c.New != "30" {
		t.Errorf("change = %+v, want settings.work_minutes 25 -> 30", c)
	}

	// Setting a value to what it already is changes nothing, and says so.
	same, err := setter(t).Set("demo", []string{"work_minutes=30"}, false)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(same.Changes) != 0 {
		t.Errorf("re-setting the same value reported %+v", same.Changes)
	}
}

// `set` is a settings edit and nothing else: everything the installer wrote
// into the fragment has to survive it byte for byte.
func TestSetChangesNothingButTheSettings(t *testing.T) {
	isolate(t)
	pin := installWithSettings(t)
	before := fragmentEntry(t, pin.Fragment, "demo")

	if _, err := setter(t).Set("demo", []string{"work_minutes=30"}, false); err != nil {
		t.Fatalf("Set: %v", err)
	}
	after := fragmentEntry(t, pin.Fragment, "demo")

	delete(before, "settings")
	delete(after, "settings")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a settings edit rewrote something else\nbefore: %#v\nafter:  %#v", before, after)
	}
	if len(before) < 6 {
		t.Fatalf("the fixture stopped exercising the fields this is guarding: %#v", before)
	}
}

// A value is read as the type the panel declared. A panel that reads an int
// would break on a string it never expected, and the manifest default is the
// only type reference grove has.
func TestSetCoercesToTheDeclaredType(t *testing.T) {
	isolate(t)
	pin := installWithSettings(t)

	if _, err := setter(t).Set("demo", []string{
		"work_minutes=45", "ratio=2.25", "chime=false", "refresh=10m", "title=Deep work",
	}, false); err != nil {
		t.Fatalf("Set: %v", err)
	}

	settings := fragmentSettings(t, pin.Fragment, "demo")
	for key, want := range map[string]any{
		"work_minutes": int64(45),
		"ratio":        2.25,
		"chime":        false,
		"refresh":      "10m",
		"title":        "Deep work",
	} {
		if got := settings[key]; got != want {
			t.Errorf("%s = %#v (%T), want %#v (%T)", key, got, got, want, want)
		}
	}
}

func TestSetRefusesValuesTheSettingCannotHold(t *testing.T) {
	isolate(t)
	installWithSettings(t)

	for _, tc := range []struct{ assignment, wants string }{
		{"work_minutes=soon", "whole number"},
		{"ratio=lots", "number"},
		{"chime=maybe", "true/false"},
		{"refresh=soon", "duration"},
		{"feed=12", "table of settings"},
		{"work_minutes", "key=value"},
		{"=30", "no setting name"},
	} {
		_, err := setter(t).Set("demo", []string{tc.assignment}, false)
		if err == nil {
			t.Errorf("Set(%q) succeeded, want a refusal", tc.assignment)
			continue
		}
		if !strings.Contains(err.Error(), tc.wants) {
			t.Errorf("Set(%q) = %v, want a message mentioning %q", tc.assignment, err, tc.wants)
		}
	}

	// A refused assignment must not have written half of a batch.
	pin := mustPin(t, "demo")
	if got := fragmentSettings(t, pin.Fragment, "demo")["work_minutes"]; got != int64(25) {
		t.Errorf("work_minutes = %#v after refusals, want the untouched default", got)
	}
	if !IsApproved(pin.Fragment, pin.ConsentDigest) {
		t.Error("a refused edit must leave the approval intact")
	}
}

func TestSetRejectsUnknownKeysUnlessNew(t *testing.T) {
	isolate(t)
	pin := installWithSettings(t)

	_, err := setter(t).Set("demo", []string{"loudness=11"}, false)
	if err == nil || !strings.Contains(err.Error(), "--new") {
		t.Fatalf("Set = %v, want a refusal naming --new", err)
	}
	if !strings.Contains(err.Error(), "work_minutes") {
		t.Errorf("the refusal should list the settings that DO exist: %v", err)
	}

	if _, err := setter(t).Set("demo", []string{"loudness=11", "feed.window=7d"}, true); err != nil {
		t.Fatalf("Set --new: %v", err)
	}
	settings := fragmentSettings(t, pin.Fragment, "demo")
	if settings["loudness"] != int64(11) {
		t.Errorf("loudness = %#v, want the int64 11 inferred from the literal", settings["loudness"])
	}
	feed := settings["feed"].(map[string]any)
	if feed["window"] != "7d" {
		t.Errorf("feed.window = %#v, want the string 7d", feed["window"])
	}
	if !IsApproved(mustPin(t, "demo").Fragment, mustPin(t, "demo").ConsentDigest) {
		t.Error("--new must re-record the approval like any other edit")
	}
}

// Hand-configured [tui.plugins] entries are not in the lockfile, have no
// approval to re-record, and are edited in the file that declares them.
func TestSetRefusesWhatItDoesNotManage(t *testing.T) {
	isolate(t)
	installWithSettings(t)

	_, err := setter(t).Set("btop", []string{"x=1"}, false)
	if err == nil || !strings.Contains(err.Error(), "grove plugin") {
		t.Fatalf("Set = %v, want a refusal explaining that the entry is not managed", err)
	}

	if _, err := setter(t).Set("demo", nil, false); err == nil {
		t.Error("Set with no assignments must be an error, not a silent rewrite")
	}
}

// The fragment is grove's to rewrite, so a key grove does not write is one this
// command would drop. It refuses and names it instead.
func TestSetRefusesAFragmentItCannotReproduce(t *testing.T) {
	isolate(t)
	pin := installWithSettings(t)

	data, err := os.ReadFile(pin.Fragment)
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	write(t, pin.Fragment, string(data)+"\ncwd = \"/tmp\"\n")

	_, err = setter(t).Set("demo", []string{"work_minutes=30"}, false)
	if err == nil || !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("Set = %v, want a refusal naming the key it would have dropped", err)
	}
}

func TestSetRefusesWhenTheFragmentIsGone(t *testing.T) {
	isolate(t)
	pin := installWithSettings(t)
	if err := os.Remove(pin.Fragment); err != nil {
		t.Fatalf("remove fragment: %v", err)
	}

	_, err := setter(t).Set("demo", []string{"work_minutes=30"}, false)
	if err == nil || !strings.Contains(err.Error(), "update demo --force") {
		t.Fatalf("Set = %v, want a refusal naming the repair", err)
	}
}

// optionsInstallManifest is the settings fixture plus the option declarations,
// as the HN panel ships them.
func optionsInstallManifest(name string) string {
	return `schema_version = 1

[plugin]
name        = "` + name + `"
description = "a test panel with a declared vocabulary"

[build]
command = ["cp", "panel.sh", "built-panel"]
binary  = "built-panel"

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
`
}

// `set` rebuilds the fragment FROM THE FRAGMENT, and refuses one carrying keys
// it does not write rather than dropping them silently. So a declaration the
// installer writes and this command cannot read back is not a cosmetic gap: it
// is every settings edit on that panel failing, and the option declaration
// vanishing if it did not.
func TestSetKeepsTheOptionDeclarationInTheFragment(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, optionsInstallManifest("choosy"))
	var seen []*ConsentRequest
	if _, err := approving(t, &seen).Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	pin := mustPin(t, "choosy")

	declared := func(where string) []any {
		t.Helper()
		entry := fragmentEntry(t, pin.Fragment, "choosy")
		options, ok := entry["setting_options"].([]any)
		if !ok || len(options) != 1 {
			t.Fatalf("%s: setting_options = %#v", where, entry["setting_options"])
		}
		return options
	}
	declared("after the install")

	res, err := setter(t).Set("choosy", []string{"open_url_command=firefox"}, false)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	first, ok := declared("after the edit")[0].(map[string]any)
	if !ok {
		t.Fatalf("the declaration did not round-trip: %#v", declared("after the edit")[0])
	}
	if first["setting"] != "open_url_command" || first["custom_setting"] != "open_url_custom_command" {
		t.Errorf("declaration = %#v", first)
	}
	if got := fragmentSettings(t, pin.Fragment, "choosy")["open_url_command"]; got != "firefox" {
		t.Errorf("open_url_command = %v, want the value that was set", got)
	}
	// The edit is the settings' alone: the vocabulary is not something `set`
	// writes, so it must not show up as something that moved.
	for _, c := range res.Changes {
		if strings.HasPrefix(c.Field, "options.") {
			t.Errorf("a settings edit reported a changed vocabulary: %+v", c)
		}
	}
}

// A value outside the declared list is still writable. The vocabulary is a
// DECLARATION and not a gate — the author's list is what they tested, and a user
// who knows about a browser they did not is not being protected by a refusal
// here. The panel is the only party that decides what an unfamiliar value means.
func TestSetWritesAValueOutsideTheDeclaredVocabulary(t *testing.T) {
	isolate(t)
	repo := newFixtureRepo(t, optionsInstallManifest("choosy"))
	var seen []*ConsentRequest
	if _, err := approving(t, &seen).Install(context.Background(), repo+"@v1.0.0", Options{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	pin := mustPin(t, "choosy")
	if _, err := setter(t).Set("choosy", []string{"open_url_command=vivaldi"}, false); err != nil {
		t.Fatalf("Set refused a value outside the list: %v", err)
	}
	if got := fragmentSettings(t, pin.Fragment, "choosy")["open_url_command"]; got != "vivaldi" {
		t.Errorf("open_url_command = %v", got)
	}
}
