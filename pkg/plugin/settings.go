package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Editing a managed plugin's settings is grove's job, and that is a
// consequence of two things that were already true.
//
// First, `[tui.plugins]` merges one ENTRY at a time, with whole-entry
// replacement (core/config/merge.go). A user layer that sets one option does
// not add to the installed fragment — it replaces the whole panel entry,
// `command` and all. So there is no overlay to write the edit into: the
// fragment is the only place a managed plugin's settings can live.
//
// Second, the fragment is a file grove owns. Its settings are part of what the
// install approval is bound to (ConsentFacts.Settings, hashed into the consent
// digest), and the lockfile keeps a snapshot of them. Editing the file by hand
// leaves the recorded consent describing a file that no longer says that, and
// the next `update` silently reverts the edit.
//
// So `set` rewrites the fragment through the same RenderFragment the installer
// uses, recomputes the consent facts with the new settings, and re-records the
// approval against them. Nothing is prompted: this is a user editing their own
// settings in their own layer, not a stranger's process asking to run. But the
// change is printed as a diff, because a settings change IS a change to what
// was approved.

// SetResult reports what a settings edit changed.
type SetResult struct {
	Name string
	Pin  *Pin
	// Changes is the approved state -> the written state, one row per setting
	// that moved. Empty means every assignment named a value it already had.
	Changes []FactChange
}

// Set applies "key=value" assignments to an installed plugin's settings.
//
// Keys are the dotted paths the user has already read: the consent screen and
// the update diff both flatten nested settings to "timer.work_minutes = 25", so
// the path they saw is the path they can type. Values are read as the type the
// setting already holds; allowNew (`--new`) is what lets an assignment
// introduce a key the panel's manifest never declared.
func (in *Installer) Set(name string, assignments []string, allowNew bool) (*SetResult, error) {
	if len(assignments) == 0 {
		return nil, errors.New("nothing to set — give at least one key=value")
	}
	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	pin := lock.Get(name)
	if pin == nil {
		return nil, fmt.Errorf("%q was not installed by `grove plugin`, so there is no approval to re-record and nothing here can safely rewrite it — a hand-configured [tui.plugins.%s] is edited in the config file that declares it", name, name)
	}

	panel, err := readFragmentPanel(pin.Fragment, name)
	if err != nil {
		return nil, err
	}
	settings := panel.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	for _, assignment := range assignments {
		if err := applyAssignment(settings, assignment, allowNew); err != nil {
			return nil, err
		}
	}

	manifest := panel.manifest(name, pin)
	manifest.Panel.Settings = settings
	fragment, err := RenderFragment(manifest, panel.runBinary(pin), pin)
	if err != nil {
		return nil, err
	}

	facts := pin.Consent
	facts.Settings = FlattenSettings(settings)
	digest := facts.Digest()
	changes := Diff(pin.Consent, facts)

	// The approval is recorded BEFORE the fragment is written, and that order is
	// deliberate. `grove plugin list` reads the pin's digest against the store's,
	// so a failure part way through this sequence leaves the two disagreeing —
	// which reads as `edited` and says out loud that something is half done.
	// Writing the fragment first would leave the opposite: new settings on disk
	// under an approval describing the old ones, and a list that says `ok`.
	if err := RecordApproval(pin.Fragment, digest, in.now()); err != nil {
		return nil, err
	}
	if err := WriteFragment(pin.Fragment, fragment); err != nil {
		return nil, err
	}
	pin.Consent = facts
	pin.ConsentDigest = digest
	// lock.Save rather than lock.Set: Set restamps InstalledAt, and nothing was
	// installed here.
	if err := lock.Save(); err != nil {
		return nil, err
	}
	return &SetResult{Name: name, Pin: pin, Changes: changes}, nil
}

// fragmentPanel is [tui.plugins.<name>] exactly as RenderFragment writes it.
//
// `set` rebuilds the fragment FROM THE FRAGMENT rather than from the manifest in
// the checkout, and that is what makes the command dependable. Nothing needs the
// checkout after an install, so it may be gone; and on a dev entry it is the
// user's own working tree, which has very likely moved on since the approval —
// rendering from either would quietly perform half an update. What is in the
// fragment came from the manifest the user approved, so round-tripping it can
// only change the settings.
type fragmentPanel struct {
	Command         string           `toml:"command"`
	Args            []string         `toml:"args"`
	Icon            string           `toml:"icon"`
	Label           string           `toml:"label"`
	Env             []string         `toml:"env"`
	Restart         bool             `toml:"restart"`
	Protocol        string           `toml:"protocol"`
	ProtocolTimeout string           `toml:"protocol_timeout"`
	Keys            []Key            `toml:"keys"`
	Views           []fragmentView   `toml:"views"`
	Notebook        *Notebook        `toml:"notebook"`
	Settings        map[string]any   `toml:"settings"`
	SettingOptions  []SettingOptions `toml:"setting_options"`
}

// fragmentView is one entry of the fragment's `views` ARRAY — the form the
// manifest's `[panel.views.<name>]` tables take once the installer has frozen
// their declaration order into the config.
type fragmentView struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Drawer      bool   `toml:"drawer"`
}

// readFragmentSettings reads only the user-owned settings subtree from an
// installed fragment. Unlike readFragmentPanel it is intentionally tolerant of
// other panel keys: an update re-renders those from the new manifest and only
// needs to carry settings forward. A missing fragment is the install repair
// case and reports ok=false; malformed TOML is refused rather than overwritten.
func readFragmentSettings(path, name string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: lockfile-owned config path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		TUI struct {
			Plugins map[string]struct {
				Settings map[string]any `toml:"settings"`
			} `toml:"plugins"`
		} `toml:"tui"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parse %s before preserving its settings: %w", path, err)
	}
	panel, ok := doc.TUI.Plugins[name]
	if !ok {
		return nil, false, nil
	}
	return panel.Settings, true, nil
}

// overlaySettings applies current user values over a new manifest's defaults.
// Tables merge recursively so a release can add a nested default without
// deleting a sibling the user configured; at leaves, the current value wins.
func overlaySettings(defaults, current map[string]any) map[string]any {
	if defaults == nil {
		defaults = map[string]any{}
	}
	for key, value := range current {
		currentTable, currentIsTable := value.(map[string]any)
		defaultTable, defaultIsTable := defaults[key].(map[string]any)
		if currentIsTable && defaultIsTable {
			defaults[key] = overlaySettings(defaultTable, currentTable)
			continue
		}
		defaults[key] = value
	}
	return defaults
}

func readFragmentPanel(path, name string) (*fragmentPanel, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the lockfile's own record of grove's config dir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s is gone, so this panel declares nothing to edit — reinstall it with\n    grove plugin update %s --force", path, name)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var doc struct {
		TUI struct {
			Plugins map[string]fragmentPanel `toml:"plugins"`
		} `toml:"tui"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		var strict *toml.StrictMissingError
		if !errors.As(err, &strict) {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		// Rewriting the fragment means re-rendering every key in it, so a key
		// the installer does not write is one this command would silently drop.
		// `view`, `cwd` and `position` are real config keys with no manifest
		// source, and losing one to a settings edit would be a worse surprise
		// than being told to make that edit by hand.
		keys := make([]string, 0, len(strict.Errors))
		for _, e := range strict.Errors {
			keys = append(keys, strings.Join(e.Key(), "."))
		}
		return nil, fmt.Errorf("%s carries keys `grove plugin` does not write (%s), and rewriting it here would drop them — set the panel's settings by hand in that file, or remove those keys first", path, strings.Join(keys, ", "))
	}

	panel, ok := doc.TUI.Plugins[name]
	if !ok {
		return nil, fmt.Errorf("%s does not declare [tui.plugins.%s] — reinstall it with\n    grove plugin update %s --force", path, name, name)
	}
	return &panel, nil
}

// manifest reassembles the parts of a manifest RenderFragment reads, from the
// fragment that manifest produced. Build is left empty: it never reaches the
// fragment and the renderer never asks for it.
func (p *fragmentPanel) manifest(name string, pin *Pin) *Manifest {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Plugin: Plugin{
			Name: name,
			// The header's description and homepage come from the approval
			// rather than from the fragment, which carries them in a comment
			// this decoder cannot see.
			Description: pin.Consent.Description,
			Homepage:    pin.Consent.Homepage,
		},
		Panel: Panel{
			Label:           p.Label,
			Icon:            p.Icon,
			Protocol:        p.Protocol,
			ProtocolTimeout: p.ProtocolTimeout,
			Args:            p.Args,
			Env:             p.Env,
			Restart:         p.Restart,
			Keys:            p.Keys,
			Settings:        p.Settings,
			SettingOptions:  p.SettingOptions,
		},
	}
	if len(p.Views) > 0 {
		m.Panel.Views = make(map[string]View, len(p.Views))
		m.Panel.ViewOrder = make([]string, 0, len(p.Views))
		for _, v := range p.Views {
			m.Panel.Views[v.Name] = View{Description: v.Description, Drawer: v.Drawer}
			m.Panel.ViewOrder = append(m.Panel.ViewOrder, v.Name)
		}
	}
	if p.Notebook != nil {
		notebook := *p.Notebook
		m.Panel.Notebook = &notebook
	}
	return m
}

// runBinary is the command the fragment already names, so a rewrite cannot
// repoint the panel at a different program. The pin is the fallback for a
// fragment that somehow has none.
func (p *fragmentPanel) runBinary(pin *Pin) string {
	if p.Command != "" {
		return p.Command
	}
	return pin.Binary
}

// applyAssignment applies one "dotted.key=value" to a settings table, in place.
func applyAssignment(settings map[string]any, assignment string, allowNew bool) error {
	key, raw, ok := strings.Cut(assignment, "=")
	if !ok {
		return fmt.Errorf("%q is not an assignment — write it as key=value", assignment)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%q has no setting name on the left of the =", assignment)
	}

	path := strings.Split(key, ".")
	table := settings
	for i, part := range path[:len(path)-1] {
		existing, ok := table[part]
		if !ok {
			if !allowNew {
				return unknownSettingErr(key, settings)
			}
			created := map[string]any{}
			table[part] = created
			table = created
			continue
		}
		nested, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is a setting, not a table of them, so %s names nothing", strings.Join(path[:i+1], "."), key)
		}
		table = nested
	}

	leaf := path[len(path)-1]
	current, exists := table[leaf]
	if !exists {
		if !allowNew {
			return unknownSettingErr(key, settings)
		}
		table[leaf] = inferValue(raw)
		return nil
	}
	value, err := coerce(key, current, raw)
	if err != nil {
		return err
	}
	table[leaf] = value
	return nil
}

// coerce reads a command-line string as the TYPE the setting already holds.
//
// The declared default is the only type reference there is: grove does not know
// what a third-party panel's options mean, but it knows the author wrote 25 and
// not "25", and a panel reading an int would break on a string it never expects.
// A duration is a string the default already parses as one — "2s", "25m" — and
// is held to that, so a typo is caught here rather than by the panel at spawn.
func coerce(key string, current any, raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch current := current.(type) {
	case bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is a true/false setting, and %q is not one", key, raw)
		}
		return v, nil
	case int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is a whole number, and %q is not one", key, raw)
		}
		return v, nil
	case float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is a number, and %q is not one", key, raw)
		}
		return v, nil
	case string:
		if _, err := time.ParseDuration(current); err == nil {
			if _, err := time.ParseDuration(raw); err != nil {
				return nil, fmt.Errorf("%s is a duration like %q, and %q is not one", key, current, raw)
			}
		}
		return raw, nil
	case []any:
		return nil, fmt.Errorf("%s is a list, and `grove plugin set` sets one value at a time — edit that list in %s", key, ManifestFile)
	case map[string]any:
		return nil, fmt.Errorf("%s is a table of settings rather than a setting — name one of the keys under it", key)
	default:
		return nil, fmt.Errorf("%s holds a %T, which `grove plugin set` cannot rewrite", key, current)
	}
}

// inferValue types a value for a key the panel never declared (--new). There is
// no default to take a type from, so the literal decides — read the way TOML
// would have read it in the manifest.
func inferValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "true" || raw == "false" {
		return raw == "true"
	}
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return v
	}
	if v, err := strconv.ParseFloat(raw, 64); err == nil {
		return v
	}
	return raw
}

// unknownSettingErr names the settings the panel does declare. A typo is the
// overwhelmingly likely cause and the list is short enough to print.
func unknownSettingErr(key string, settings map[string]any) error {
	declared := FlattenSettings(settings)
	for i, line := range declared {
		declared[i], _, _ = strings.Cut(line, " = ")
	}
	if len(declared) == 0 {
		return fmt.Errorf("this panel declares no settings, so %s is not one of them — pass --new to add it anyway", key)
	}
	return fmt.Errorf("%s is not a setting this panel declares (%s) — pass --new to add it anyway", key, strings.Join(declared, ", "))
}
