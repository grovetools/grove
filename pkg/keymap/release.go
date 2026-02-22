// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/tui/keymap"
)

// ReleaseKeyMap defines key bindings for the release TUI.
type ReleaseKeyMap struct {
	keymap.Base
	Toggle            key.Binding
	Tab               key.Binding
	SelectAll         key.Binding
	DeselectAll       key.Binding
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
	EditRules         key.Binding
	ResetRules        key.Binding
	ToggleDryRun      key.Binding
	TogglePush        key.Binding
	ToggleSyncDeps    key.Binding
	Approve           key.Binding
	Back              key.Binding
}

// NewReleaseKeyMap creates a new ReleaseKeyMap with default bindings.
func NewReleaseKeyMap() ReleaseKeyMap {
	return ReleaseKeyMap{
		Base: keymap.NewBase(),
		Toggle: key.NewBinding(
			key.WithKeys(" ", "x"),
			key.WithHelp("space/x", "toggle selection"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch view"),
		),
		SelectAll: key.NewBinding(
			key.WithKeys("ctrl+a"),
			key.WithHelp("ctrl+a", "select all"),
		),
		DeselectAll: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "deselect all"),
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
		ViewChangelog: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("v", "view changelog"),
		),
		EditChangelog: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit staged changelog"),
		),
		EditRepoChangelog: key.NewBinding(
			key.WithKeys("E"),
			key.WithHelp("E", "edit repo CHANGELOG.md"),
		),
		GenerateChangelog: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", "generate changelog (LLM)"),
		),
		GenerateAll: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "generate all changelogs"),
		),
		WriteChangelog: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "write changelog to repo"),
		),
		EditRules: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "edit LLM rules"),
		),
		ResetRules: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "reset all rules to *"),
		),
		ToggleDryRun: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "toggle dry-run mode"),
		),
		TogglePush: key.NewBinding(
			key.WithKeys("P"),
			key.WithHelp("P", "toggle push to remote"),
		),
		ToggleSyncDeps: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "toggle sync dependencies"),
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
}

// ShortHelp returns key bindings for the short help view.
func (k ReleaseKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Toggle, k.Approve, k.Base.Quit,
	}
}

// FullHelp returns all key bindings for the full help view.
func (k ReleaseKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		// Navigation
		{k.Base.Up, k.Base.Down, k.Tab},
		// Selection
		{k.Toggle, k.SelectAll, k.DeselectAll},
		// Version bumps
		{k.SelectMajor, k.SelectMinor, k.SelectPatch, k.ApplySuggestion},
		// Changelog
		{k.ViewChangelog, k.EditChangelog, k.EditRepoChangelog},
		{k.GenerateChangelog, k.GenerateAll, k.WriteChangelog},
		// LLM rules
		{k.EditRules, k.ResetRules},
		// Settings
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

	return []keymap.Section{
		nav,
		{
			Name:     "Selection",
			Bindings: []key.Binding{k.Toggle, k.SelectAll, k.DeselectAll},
		},
		{
			Name:     "Version Bumps",
			Bindings: []key.Binding{k.SelectMajor, k.SelectMinor, k.SelectPatch, k.ApplySuggestion},
		},
		{
			Name:     "Changelog",
			Bindings: []key.Binding{k.ViewChangelog, k.EditChangelog, k.EditRepoChangelog, k.GenerateChangelog, k.GenerateAll, k.WriteChangelog},
		},
		{
			Name:     "LLM Rules",
			Bindings: []key.Binding{k.EditRules, k.ResetRules},
		},
		{
			Name:     "Settings",
			Bindings: []key.Binding{k.ToggleDryRun, k.TogglePush, k.ToggleSyncDeps},
		},
		{
			Name:     "Actions",
			Bindings: []key.Binding{k.Approve, k.Back},
		},
		k.Base.SystemSection(),
	}
}

// KeymapInfo returns the keymap metadata for the release TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func ReleaseKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-release",
		"grove",
		"Release management with changelog generation",
		NewReleaseKeyMap(),
	)
}
