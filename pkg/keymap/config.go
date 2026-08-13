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
	CycleLayer         key.Binding // L to cycle the Data page's config layer
	Preview            key.Binding // Toggle preview mode
	ViewMode           key.Binding // Toggle view (configured/all)
	MaturityFilter     key.Binding // Cycle maturity filter forward
	MaturityFilterBack key.Binding // Cycle maturity filter backward (M)
	SortMode           key.Binding // Cycle sort mode forward
	SortModeBack       key.Binding // Cycle sort mode backward (S)

	// Notebook scope (P3 W3.7). The Notes and Join pages act through the
	// `grove notebook share|pull` / `grove notespace move` verbs, so their
	// keys are DECLARED here rather than matched as raw strings in the page:
	// a key the registry cannot see is a key `?` help cannot list, the
	// cross-TUI audit cannot compare, and `[keymaps.grove.config]` cannot
	// rebind. grove-config's registry entry is built from THIS struct
	// (ConfigKeymapInfo → MakeTUIInfo), so declaring them needs no other repo.
	MoveNotespace  key.Binding // m — move a notespace into another notebook (Notes)
	ShareNotebook  key.Binding // s — share the notebook under the cursor (Join)
	PullNotebook   key.Binding // p — pull the notebook under the cursor (Join)
	FetchJoinDelta key.Binding // r — ask the recorded sync server for its inventory (Join)
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
			key.WithKeys("dd"),
			key.WithHelp("dd", "delete from layer"),
		),
		// View (v…) namespace member. Chord-only (E4): the flat `i` is released
		// back to the Ring-1 `insert` vocabulary (canon 60 §4.1/§5.1).
		Info: key.NewBinding(
			key.WithKeys("vi"),
			key.WithHelp("vi", "field info"),
		),
		// View (v…) namespace member. Was the flat `c` squatter on the reserved
		// change prefix; sources is a VIEW of where values come from, not a
		// mutation, so it lands in v… rather than c… (canon 60 §10 group 2).
		Sources: key.NewBinding(
			key.WithKeys("vs"),
			key.WithHelp("vs", "config sources"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "save"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		// Edit-dialog only. `tab` is shared with NextPage, but the two live in
		// disjoint modes — SwitchLayer is matched solely in Model.updateEdit
		// (state == viewEdit) and NextPage solely via the pager in
		// Model.updateList — exactly like the already-sanctioned enter overlap
		// (Edit vs Confirm). Kept on `tab` rather than rebound to `ctrl+e`; see
		// the canon-60 §10 deviation note in the fan-out report.
		SwitchLayer: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "change layer"),
		),
		Toggle: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "expand/collapse"),
		),
		// `l`/`h` dropped (canon 60 §4.4): they meant "fold" only here while
		// meaning left/right in nine other TUIs. left/right stay (sanctioned via
		// ReservedAlternates) and the zo/zc fold chords cover the vim spelling.
		Expand: key.NewBinding(
			key.WithKeys("right"),
			key.WithHelp("→", "expand"),
		),
		Collapse: key.NewBinding(
			key.WithKeys("left"),
			key.WithHelp("←", "collapse/parent"),
		),
		NextPage: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next page"),
		),
		PrevPage: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev page"),
		),
		// Toggle (t…) namespace member — a layer CYCLE, so RULE T (canon 60
		// §4.2) puts it in t…, freeing the flat `L`.
		CycleLayer: key.NewBinding(
			key.WithKeys("tl"),
			key.WithHelp("tl", "cycle layer"),
		),
		// View (v…) namespace member (canon 60 §4.1).
		Preview: key.NewBinding(
			key.WithKeys("vp"),
			key.WithHelp("vp", "toggle preview"),
		),
		// View (v…) namespace member. Was the flat `v` squatter on the reserved
		// view prefix.
		ViewMode: key.NewBinding(
			key.WithKeys("vm"),
			key.WithHelp("vm", "toggle view"),
		),
		// Toggle (t…) namespace members — RULE T: every display/filter cycle
		// moves into t…. Uppercase-in-chord (tS/tM) is established house style.
		MaturityFilter: key.NewBinding(
			key.WithKeys("tm"),
			key.WithHelp("tm", "cycle maturity"),
		),
		MaturityFilterBack: key.NewBinding(
			key.WithKeys("tM"),
			key.WithHelp("tM", "cycle maturity back"),
		),
		SortMode: key.NewBinding(
			key.WithKeys("ts"),
			key.WithHelp("ts", "cycle sort"),
		),
		SortModeBack: key.NewBinding(
			key.WithKeys("tS"),
			key.WithHelp("tS", "cycle sort back"),
		),
		// Flat keys, deliberately: each is one act on the row under the
		// cursor, and none of m/s/p/r is a reserved Base key or a namespace
		// prefix (pkg/keys.FreeKeys). They are page-scoped — the Notes page
		// matches MoveNotespace, the Join page the other three — but the
		// registry is per-TUI, so they are declared once here.
		MoveNotespace: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "move notespace"),
		),
		ShareNotebook: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "share notebook"),
		),
		PullNotebook: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "pull notebook"),
		),
		FetchJoinDelta: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "fetch join delta"),
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

// Namespaces returns the which-key chord namespaces for the config editor,
// built from the NAMED KeyMap fields so ApplyTUIOverrides is reflected and
// MakeTUIInfo can match each member back to a stable snake_case ConfigKey
// (namespace.go's ConfigKey-stability rule — never construct members inline).
//
// v… is "show me a different rendering of the current thing" (view mode,
// preview pane, config sources, field info); t… is RULE T, every display or
// filter cycle (sort, maturity, layer). Order here is the wire order
// ProcessChord relies on.
func (k ConfigKeyMap) Namespaces() []keymap.Namespace {
	return []keymap.Namespace{
		{Prefix: "v", Label: "View", Bindings: []key.Binding{
			k.ViewMode, k.Preview, k.Sources, k.Info,
		}},
		{Prefix: "t", Label: "Toggle", Bindings: []key.Binding{
			k.SortMode, k.SortModeBack, k.MaturityFilter, k.MaturityFilterBack, k.CycleLayer,
		}},
	}
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
		{k.Edit, k.Delete},
		// Notebook scope (Notes / Join pages)
		{k.MoveNotespace, k.ShareNotebook, k.PullNotebook, k.FetchJoinDelta},
		// View (v…) chords
		{k.ViewMode, k.Preview, k.Sources, k.Info},
		// Toggle (t…) chords
		{k.SortMode, k.SortModeBack, k.MaturityFilter, k.MaturityFilterBack, k.CycleLayer},
		// Exit
		{k.Base.Quit, k.Base.Help},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes bindings the config TUI actually implements, so the help
// overlay and the keys registry stay truthful (see NewConfigKeyMap for the
// Base-disable rationale).
func (k ConfigKeyMap) Sections() []keymap.Section {
	ns := k.Namespaces()
	return []keymap.Section{
		keymap.NavigationSection(
			k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom,
			k.NextPage, k.PrevPage,
			k.Tab1, k.Tab2, k.Tab3, k.Tab4, k.Tab5,
		),
		keymap.NewSection("Tree Actions",
			k.Edit, k.Delete, k.Toggle, k.Expand, k.Collapse,
		),
		// Fold vocabulary is Base's vim chords (zR/zM/zo/zc/za); the TUI routes
		// its ad-hoc z-chord through these bindings via key.Matches.
		keymap.FoldSection(
			k.FoldOpen, k.FoldClose, k.FoldToggle, k.FoldOpenAll, k.FoldCloseAll,
		),
		// The v… and t… namespaces ARE the former Filter section. Each member is
		// exported exactly once (here, via Namespace.Section()) so the merged
		// registry carries one ConfigKey per binding.
		ns[0].Section(),
		ns[1].Section(),
		// Edit-modal bindings share physical keys (enter/tab) with tree/page
		// bindings but live in a disjoint context; distinct fields keep their
		// override identities unambiguous.
		keymap.NewSection("Edit Dialog",
			k.Confirm, k.Cancel, k.SwitchLayer,
		),
		// P3 notebook scope. These four run verbs rather than editing config,
		// so they get their own section instead of joining "Tree Actions".
		keymap.NewSection("Notebook Scope",
			k.MoveNotespace, k.ShareNotebook, k.PullNotebook, k.FetchJoinDelta,
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
