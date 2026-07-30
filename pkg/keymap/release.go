// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
)

// ReleaseKeyMap defines key bindings for the release TUI.
type ReleaseKeyMap struct {
	keymap.Base
	Toggle            key.Binding
	Tab               key.Binding
	SelectAll         key.Binding
	SelectNone        key.Binding
	SelectMajor       key.Binding
	SelectMinor       key.Binding
	SelectPatch       key.Binding
	ApplySuggestion   key.Binding
	ViewChangelog     key.Binding
	EditChangelog     key.Binding
	EditRepoChangelog key.Binding
	GenerateChangelog key.Binding
	GenerateAll       key.Binding
	WriteChangelog    key.Binding
	ViewDocs          key.Binding
	DiffDocs          key.Binding
	RegenDocs         key.Binding
	EditRules         key.Binding
	ResetRules        key.Binding
	ToggleDryRun      key.Binding
	TogglePush        key.Binding
	ToggleSyncDeps    key.Binding
	Approve           key.Binding
	Back              key.Binding
}

// NewReleaseKeyMap creates a new ReleaseKeyMap with user configuration applied.
// Base bindings (navigation, actions, search, selection) come from keymap.Load().
// Only TUI-specific bindings are defined here.
func NewReleaseKeyMap(cfg *config.Config) ReleaseKeyMap {
	km := ReleaseKeyMap{
		Base: keymap.Load(cfg, "grove.release"),
		// `x` dropped (E4): space is the canonical select key and `x` means
		// cut/discard elsewhere in the fleet.
		Toggle: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "toggle selection"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch view"),
		),
		SelectAll: key.NewBinding(
			key.WithKeys("ctrl+a"),
			key.WithHelp("ctrl+a", "select all"),
		),
		SelectNone: key.NewBinding(
			key.WithKeys("-"),
			key.WithHelp("-", "deselect all"),
		),
		SelectMajor: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "set major"),
		),
		SelectMinor: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "set minor"),
		),
		SelectPatch: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "set patch"),
		),
		ApplySuggestion: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "apply suggestion"),
		),
		// View (v…) namespace member. Was the flat `v` squatter on the reserved
		// view prefix.
		ViewChangelog: key.NewBinding(
			key.WithKeys("vc"),
			key.WithHelp("vc", "view changelog"),
		),
		// Stays flat: `e` IS the canonical edit key (StandardActions).
		EditChangelog: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit staged changelog"),
		),
		// Change (c…) namespace member (canon 60 §4.3).
		EditRepoChangelog: key.NewBinding(
			key.WithKeys("ce"),
			key.WithHelp("ce", "edit repo CHANGELOG.md"),
		),
		// Change (c…) namespace member. Was the flat `g` squatter on the
		// reserved goto prefix — this rebind is what fully vacates flat `g`
		// fleet-wide (canon 60 §4.4).
		GenerateChangelog: key.NewBinding(
			key.WithKeys("cg"),
			key.WithHelp("cg", "generate changelog (LLM)"),
		),
		// Change (c…) namespace member; uppercase-in-chord marks the
		// all-repos variant of cg, matching flow-status' cM/cA house style.
		GenerateAll: key.NewBinding(
			key.WithKeys("cG"),
			key.WithHelp("cG", "generate all changelogs"),
		),
		// Change (c…) namespace member (canon 60 §4.3).
		WriteChangelog: key.NewBinding(
			key.WithKeys("cw"),
			key.WithHelp("cw", "write changelog to repo"),
		),
		// View (v…) namespace member (canon 60 §4.1).
		ViewDocs: key.NewBinding(
			key.WithKeys("vs"),
			key.WithHelp("vs", "view docs sections"),
		),
		// View (v…) namespace member (canon 60 §4.1).
		DiffDocs: key.NewBinding(
			key.WithKeys("vd"),
			key.WithHelp("vd", "docs diff (notebook vs repo)"),
		),
		// Change (c…) namespace member. `G` is a RESERVED key (bottom) and this
		// was the fleet's only real reserved-key violation; cd retires it.
		RegenDocs: key.NewBinding(
			key.WithKeys("cd"),
			key.WithHelp("cd", "regenerate docs section"),
		),
		EditRules: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "edit LLM rules"),
		),
		ResetRules: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "reset all rules to *"),
		),
		// Toggle (t…) namespace members — RULE T (canon 60 §4.2): every
		// display/mode toggle moves into t…, no exceptions.
		ToggleDryRun: key.NewBinding(
			key.WithKeys("td"),
			key.WithHelp("td", "toggle dry-run mode"),
		),
		TogglePush: key.NewBinding(
			key.WithKeys("tp"),
			key.WithHelp("tp", "toggle push to remote"),
		),
		ToggleSyncDeps: key.NewBinding(
			key.WithKeys("ts"),
			key.WithHelp("ts", "toggle sync dependencies"),
		),
		Approve: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "approve"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
	}

	// Apply TUI-specific overrides from config
	keymap.ApplyTUIOverrides(cfg, "grove", "release", &km)

	return km
}

// ShortHelp returns key bindings for the short help view.
func (k ReleaseKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Toggle, k.Approve, k.Base.Quit,
	}
}

// Namespaces returns the which-key chord namespaces for the release TUI, built
// from the NAMED KeyMap fields so ApplyTUIOverrides is reflected and
// MakeTUIInfo can match each member back to a stable snake_case ConfigKey
// (namespace.go's ConfigKey-stability rule — never construct members inline).
//
// v… renders something about the selected repo (changelog, docs sections, docs
// diff); t… is RULE T, the run-mode toggles; c… mutates what will be released
// (generate/write changelogs, edit the repo CHANGELOG, regenerate docs). The
// semver triad (m/n/p/s/a) deliberately stays flat — grove-release IS a version
// picker, so those are its subject matter (canon 60 §5.3). Order here is the
// wire order ProcessChord relies on.
func (k ReleaseKeyMap) Namespaces() []keymap.Namespace {
	return []keymap.Namespace{
		{Prefix: "v", Label: "View", Bindings: []key.Binding{
			k.ViewChangelog, k.ViewDocs, k.DiffDocs,
		}},
		{Prefix: "t", Label: "Toggle", Bindings: []key.Binding{
			k.ToggleDryRun, k.TogglePush, k.ToggleSyncDeps,
		}},
		{Prefix: "c", Label: "Change", Bindings: []key.Binding{
			k.GenerateChangelog, k.GenerateAll, k.WriteChangelog,
			k.EditRepoChangelog, k.RegenDocs,
		}},
	}
}

// FullHelp returns all key bindings for the full help view.
func (k ReleaseKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		// Navigation
		{k.Base.Up, k.Base.Down, k.Tab},
		// Selection
		{k.Toggle, k.SelectAll, k.SelectNone},
		// Version bumps
		{k.SelectMajor, k.SelectMinor, k.SelectPatch, k.ApplySuggestion},
		// Changelog
		{k.EditChangelog},
		// View (v…) chords
		{k.ViewChangelog, k.ViewDocs, k.DiffDocs},
		// Change (c…) chords
		{k.GenerateChangelog, k.GenerateAll, k.WriteChangelog, k.EditRepoChangelog, k.RegenDocs},
		// LLM rules
		{k.EditRules, k.ResetRules},
		// Toggle (t…) chords
		{k.ToggleDryRun, k.TogglePush, k.ToggleSyncDeps},
		// Actions
		{k.Approve, k.Back, k.Base.Quit, k.Base.Help},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes sections that the release TUI actually implements.
func (k ReleaseKeyMap) Sections() []keymap.Section {
	// Customize navigation for release TUI
	nav := k.Base.NavigationSection()
	nav.Bindings = []key.Binding{k.Up, k.Down, k.Tab}

	// The v…/t…/c… namespaces replace the old Changelog / Docs / Settings
	// sections. Each member is exported exactly once (via Namespace.Section())
	// so the merged registry carries one ConfigKey per binding.
	ns := k.Namespaces()

	return []keymap.Section{
		nav,
		keymap.SelectionSection(k.Toggle, k.SelectAll, k.SelectNone),
		keymap.NewSectionWithIcon("Version Bumps", theme.IconArrowUpBold,
			k.SelectMajor, k.SelectMinor, k.SelectPatch, k.ApplySuggestion,
		),
		keymap.NewSectionWithIcon("Changelog", theme.IconDiff,
			k.EditChangelog,
		),
		ns[0].Section(),
		ns[2].Section(),
		keymap.NewSectionWithIcon("LLM Rules", theme.IconRobot,
			k.EditRules, k.ResetRules,
		),
		ns[1].Section(),
		keymap.ActionsSection(k.Approve, k.Back),
		k.Base.SystemSection(),
	}
}

// ReleaseKeymapInfo returns the keymap metadata for the release TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func ReleaseKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-release",
		"grove",
		"Release management with changelog generation",
		NewReleaseKeyMap(nil),
	)
}
