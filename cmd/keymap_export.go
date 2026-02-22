package cmd

import (
	"github.com/grovetools/core/tui/keymap"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
)

// KeysKeymapInfo returns the keymap metadata for the grove keys TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func KeysKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-keys",
		"grove",
		"Keybinding browser and analyzer",
		newKeysTUIKeyMap(nil),
	)
}

// ReleaseKeymapInfo re-exports the release TUI keymap info for the registry generator.
func ReleaseKeymapInfo() keymap.TUIInfo {
	return grovekeymap.ReleaseKeymapInfo()
}

// ConfigKeymapInfo re-exports the config TUI keymap info for the registry generator.
func ConfigKeymapInfo() keymap.TUIInfo {
	return grovekeymap.ConfigKeymapInfo()
}

// SetupKeymapInfo re-exports the setup TUI keymap info for the registry generator.
func SetupKeymapInfo() keymap.TUIInfo {
	return grovekeymap.SetupKeymapInfo()
}

// OnboardKeymapInfo re-exports the onboard TUI keymap info for the registry generator.
func OnboardKeymapInfo() keymap.TUIInfo {
	return grovekeymap.OnboardKeymapInfo()
}
