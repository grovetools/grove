// Package keymap contains extracted TUI keymaps for registry integration.
package keymap

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/tui/keymap"
)

// disableAllBase disables every promoted keymap.Base binding on b.
//
// TUIs that give themselves a scoped Sections() must ensure every enabled
// Base binding is either in a section or disabled (keymap.AuditCoverage
// enforces this). The simplest truthful pattern is: disable the whole Base
// vocabulary, then re-enable only the bindings the TUI actually handles. This
// stops the generic Base keys (e/dd/yy/zo/zR/search/…) from leaking into the
// help overlay and the keys registry for TUIs that never implement them.
func disableAllBase(b *keymap.Base) {
	for _, bind := range []*key.Binding{
		&b.Up, &b.Down, &b.Left, &b.Right, &b.PageUp, &b.PageDown, &b.Home, &b.End, &b.Top, &b.Bottom,
		&b.Quit, &b.Help, &b.Confirm, &b.Cancel, &b.Back, &b.Edit, &b.Delete, &b.Yank, &b.Rename, &b.Refresh, &b.CopyPath,
		&b.Search, &b.SearchNext, &b.SearchPrev, &b.ClearSearch, &b.Grep,
		&b.SwitchView, &b.NextTab, &b.PrevTab, &b.FocusNext, &b.FocusPrev, &b.TogglePreview,
		&b.Tab1, &b.Tab2, &b.Tab3, &b.Tab4, &b.Tab5, &b.Tab6, &b.Tab7, &b.Tab8, &b.Tab9,
		&b.Select, &b.SelectAll, &b.SelectNone,
		&b.FoldOpen, &b.FoldClose, &b.FoldToggle, &b.FoldOpenAll, &b.FoldCloseAll,
	} {
		bind.SetEnabled(false)
	}
}

// enableBindings re-enables a set of bindings previously disabled by
// disableAllBase. Callers pass the bindings their TUI actually handles.
func enableBindings(binds ...*key.Binding) {
	for _, b := range binds {
		b.SetEnabled(true)
	}
}
