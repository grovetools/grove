// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
)

// ConfigKeyMap defines key bindings for the config editor TUI.
type ConfigKeyMap struct {
	keymap.Base
	Edit               key.Binding
	Delete             key.Binding // Delete key from its layer file
	Info               key.Binding
	Sources            key.Binding // Show config source files
	Confirm            key.Binding
	Cancel             key.Binding
	SwitchLayer        key.Binding
	Toggle             key.Binding // Space to toggle expand/collapse
	Expand             key.Binding // Right/l to expand
	Collapse           key.Binding // Left/h to collapse
	NextPage           key.Binding // Tab to next page
	PrevPage           key.Binding // Shift+Tab to prev page
	Preview            key.Binding // Toggle preview mode
	ViewMode           key.Binding // Toggle view (configured/all)
	MaturityFilter     key.Binding // Cycle maturity filter forward
	MaturityFilterBack key.Binding // Cycle maturity filter backward (M)
	SortMode           key.Binding // Cycle sort mode forward
	SortModeBack       key.Binding // Cycle sort mode backward (S)
}

// NewConfigKeyMap creates a new ConfigKeyMap with user configuration applied.
// Base bindings (navigation, actions, search, selection) come from keymap.Load().
// Only TUI-specific bindings are defined here.
func NewConfigKeyMap(cfg *config.Config) ConfigKeyMap {
	km := ConfigKeyMap{
		Base: keymap.Load(cfg, "grove.config"),
		Edit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "edit value"),
		),
		Delete: key.NewBinding(
			key.WithKeys("D", "shift+d"),
			key.WithHelp("D", "delete from layer"),
		),
		Info: key.NewBinding(
			key.WithKeys("i"),
			key.WithHelp("i", "field info"),
		),
		Sources: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "config sources"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "save"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		SwitchLayer: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "change layer"),
		),
		Toggle: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "expand/collapse"),
		),
		Expand: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("l/→", "expand"),
		),
		Collapse: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("h/←", "collapse/parent"),
		),
		NextPage: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next page"),
		),
		PrevPage: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev page"),
		),
		Preview: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "toggle preview"),
		),
		ViewMode: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("v", "toggle view"),
		),
		MaturityFilter: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "cycle maturity"),
		),
		MaturityFilterBack: key.NewBinding(
			key.WithKeys("M"),
			key.WithHelp("M", "cycle maturity back"),
		),
		SortMode: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "cycle sort"),
		),
		SortModeBack: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "cycle sort back"),
		),
	}

	// Truthfulness: the config TUI is a tabbed tree editor. Disable the whole
	// generic Base vocabulary, then re-enable only the Base bindings it really
	// handles — vertical navigation, page/half-page scroll, gg/G jumps, the
	// vim fold chords (zR/zM/zo/zc/za, routed through key.Matches in the TUI),
	// the numeric tab jumps for its five tabs, and quit/help. Everything else
	// (e-edit, dd/yy, search, select, etc.) is not implemented here.
	disableAllBase(&km.Base)
	enableBindings(
		&km.Up, &km.Down, &km.PageUp, &km.PageDown, &km.Top, &km.Bottom,
		&km.Quit, &km.Help,
		&km.FoldOpen, &km.FoldClose, &km.FoldToggle, &km.FoldOpenAll, &km.FoldCloseAll,
		&km.Tab1, &km.Tab2, &km.Tab3, &km.Tab4, &km.Tab5,
	)

	// Apply TUI-specific overrides from config
	keymap.ApplyTUIOverrides(cfg, "grove", "config", &km)

	return km
}

// ShortHelp returns keybindings to be shown in the mini help view.
func (k ConfigKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Base.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k ConfigKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		// Page navigation
		{k.NextPage, k.PrevPage},
		// Navigation
		{k.Base.Up, k.Base.Down, k.Base.PageUp, k.Base.PageDown},
		// Tree navigation
		{k.Toggle, k.Expand, k.Collapse},
		// Folds
		{k.Base.FoldOpenAll, k.Base.FoldCloseAll, k.Base.FoldOpen, k.Base.FoldClose, k.Base.FoldToggle},
		// Actions
		{k.Edit, k.Delete, k.Info, k.Sources, k.Preview},
		// Filtering/Sorting
		{k.ViewMode, k.MaturityFilter, k.MaturityFilterBack, k.SortMode, k.SortModeBack},
		// Exit
		{k.Base.Quit, k.Base.Help},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes bindings the config TUI actually implements, so the help
// overlay and the keys registry stay truthful (see NewConfigKeyMap for the
// Base-disable rationale).
func (k ConfigKeyMap) Sections() []keymap.Section {
	return []keymap.Section{
		keymap.NavigationSection(
			k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom,
			k.NextPage, k.PrevPage,
			k.Tab1, k.Tab2, k.Tab3, k.Tab4, k.Tab5,
		),
		keymap.NewSection("Tree Actions",
			k.Edit, k.Delete, k.Info, k.Sources, k.Toggle, k.Expand, k.Collapse,
		),
		// Fold vocabulary is Base's vim chords (zR/zM/zo/zc/za); the TUI routes
		// its ad-hoc z-chord through these bindings via key.Matches.
		keymap.FoldSection(
			k.FoldOpen, k.FoldClose, k.FoldToggle, k.FoldOpenAll, k.FoldCloseAll,
		),
		keymap.NewSection(keymap.SectionFilter,
			k.Preview, k.ViewMode, k.MaturityFilter, k.MaturityFilterBack, k.SortMode, k.SortModeBack,
		),
		// Edit-modal bindings share physical keys (enter/tab) with tree/page
		// bindings but live in a disjoint context; distinct fields keep their
		// override identities unambiguous.
		keymap.NewSection("Edit Dialog",
			k.Confirm, k.Cancel, k.SwitchLayer,
		),
		k.Base.SystemSection(),
	}
}

// ConfigKeymapInfo returns the keymap metadata for the config editor TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func ConfigKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-config",
		"grove",
		"Interactive configuration editor",
		NewConfigKeyMap(nil),
	)
}
