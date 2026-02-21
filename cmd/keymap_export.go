package cmd

import (
	"github.com/grovetools/core/tui/keymap"
)

// KeysKeymapInfo returns the keymap metadata for the grove keys TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func KeysKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-keys",
		"grove",
		"Keybinding browser and analyzer",
		newKeysTUIKeyMap(),
	)
}
