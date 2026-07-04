// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
)

// SetupKeyMap defines key bindings for the setup wizard TUI.
type SetupKeyMap struct {
	keymap.Base
	Select  key.Binding
	Confirm key.Binding
	Back    key.Binding
}

// NewSetupKeyMap creates a new SetupKeyMap with user configuration applied.
// Base bindings (navigation, actions, search, selection) come from keymap.Load().
// Only TUI-specific bindings are defined here.
func NewSetupKeyMap(cfg *config.Config) SetupKeyMap {
	km := SetupKeyMap{
		Base: keymap.Load(cfg, "grove.setup"),
		Select: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "toggle selection"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "go back"),
		),
	}

	// The setup wizard drives bubbles/list widgets and step forms. It only
	// handles vertical navigation (in the agent-args step), plus its own
	// Select/Confirm/Back and quit. It never wires the "?" help overlay, so
	// Base.Help stays disabled too. Disable the rest of the Base vocabulary.
	disableAllBase(&km.Base)
	enableBindings(&km.Up, &km.Down, &km.Quit)

	// Apply TUI-specific overrides from config
	keymap.ApplyTUIOverrides(cfg, "grove", "setup", &km)

	return km
}

// ShortHelp returns keybindings to be shown in the mini help view.
func (k SetupKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Select, k.Confirm, k.Base.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k SetupKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Base.Up, k.Base.Down, k.Select},
		{k.Confirm, k.Back, k.Base.Quit},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes bindings the setup wizard actually implements.
func (k SetupKeyMap) Sections() []keymap.Section {
	return []keymap.Section{
		keymap.NavigationSection(k.Up, k.Down),
		keymap.SelectionSection(k.Select),
		keymap.ActionsSection(k.Confirm, k.Back),
		keymap.SystemSection(k.Quit),
	}
}

// SetupKeymapInfo returns the keymap metadata for the setup wizard TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func SetupKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-wizard",
		"grove",
		"Initial setup wizard",
		NewSetupKeyMap(nil),
	)
}
