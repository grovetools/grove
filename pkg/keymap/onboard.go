// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/tui/keymap"
)

// OnboardKeyMap defines key bindings for the onboard wizard TUI.
type OnboardKeyMap struct {
	keymap.Base
	Confirm key.Binding
	Skip    key.Binding
	Yes     key.Binding
	No      key.Binding
}

// NewOnboardKeyMap creates a new OnboardKeyMap with default bindings.
func NewOnboardKeyMap() OnboardKeyMap {
	return OnboardKeyMap{
		Base: keymap.NewBase(),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Skip: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "skip"),
		),
		Yes: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "yes"),
		),
		No: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "no"),
		),
	}
}

// ShortHelp returns keybindings to be shown in the mini help view.
func (k OnboardKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Confirm, k.Skip, k.Base.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k OnboardKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Base.Up, k.Base.Down},
		{k.Confirm, k.Skip, k.Yes, k.No},
		{k.Base.Quit},
	}
}

// OnboardKeymapInfo returns the keymap metadata for the onboard wizard TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func OnboardKeymapInfo() keymap.TUIInfo {
	return keymap.MakeTUIInfo(
		"grove-onboard",
		"grove",
		"Initial system onboarding wizard",
		NewOnboardKeyMap(),
	)
}
