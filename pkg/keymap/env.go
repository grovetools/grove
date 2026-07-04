// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
)

// EnvKeyMap defines key bindings for the `grove env tui` dashboard.
//
// The env TUI is a single-screen live grid: it refreshes, opens the browser
// dashboard, and quits. It reuses Base.Refresh (with an "r" alias),
// Base.Back (esc), Base.Quit and Base.Help; the only genuinely new binding is
// OpenDashboard.
type EnvKeyMap struct {
	keymap.Base
	OpenDashboard key.Binding // "d" — open the browser dashboard
}

// NewEnvKeyMap creates a new EnvKeyMap with user configuration applied.
func NewEnvKeyMap(cfg *config.Config) EnvKeyMap {
	km := EnvKeyMap{
		Base: keymap.Load(cfg, "grove.env"),
		OpenDashboard: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "open browser dashboard"),
		),
	}

	// Disable the whole Base vocabulary, then re-enable only what the env TUI
	// handles: refresh (ctrl+r, plus an "r" alias), esc-as-Back, quit, help.
	disableAllBase(&km.Base)
	km.Refresh = key.NewBinding(
		key.WithKeys("ctrl+r", "r"),
		key.WithHelp("ctrl+r/r", "refresh"),
	)
	enableBindings(&km.Quit, &km.Help, &km.Back)

	// Apply TUI-specific overrides from config
	keymap.ApplyTUIOverrides(cfg, "grove", "env", &km)

	return km
}

// ShortHelp returns keybindings to be shown in the mini help view.
func (k EnvKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Refresh, k.OpenDashboard, k.Base.Help, k.Base.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k EnvKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Refresh, k.OpenDashboard},
		{k.Base.Help, k.Base.Quit, k.Base.Back},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
func (k EnvKeyMap) Sections() []keymap.Section {
	return []keymap.Section{
		keymap.ActionsSection(k.Refresh, k.OpenDashboard),
		keymap.SystemSection(k.Help, k.Quit, k.Back),
	}
}

// EnvKeymapInfo returns the keymap metadata for the env dashboard TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func EnvKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-env",
		"grove",
		"Environment dashboard TUI",
		NewEnvKeyMap(nil),
	)
}
