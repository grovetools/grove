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
	Edit           key.Binding
	Info           key.Binding
	Sources        key.Binding // Show config source files
	Confirm        key.Binding
	Cancel         key.Binding
	SwitchLayer    key.Binding
	Toggle         key.Binding // Space to toggle expand/collapse
	Expand         key.Binding // Right/l to expand
	Collapse       key.Binding // Left/h to collapse
	NextPage       key.Binding // Tab to next page
	PrevPage       key.Binding // Shift+Tab to prev page
	Preview        key.Binding // Toggle preview mode
	ViewMode       key.Binding // Toggle view (configured/all)
	MaturityFilter key.Binding // Cycle maturity filter
	SortMode       key.Binding // Cycle sort mode
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
		SortMode: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "cycle sort"),
		),
	}

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
		// Actions
		{k.Edit, k.Info, k.Sources, k.Preview},
		// Filtering/Sorting
		{k.ViewMode, k.MaturityFilter, k.SortMode},
		// Exit
		{k.Base.Quit, k.Base.Help},
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
