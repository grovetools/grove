package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/keybind"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/theme"
)

// Shipped chord defaults, mirroring the TUIConfig jsonschema tags in
// core/config/types.go. Reset-to-default writes these EXPLICITLY (never a
// key deletion): the treemux leader/action apply handlers guard on empty
// strings, so an absent key would silently not hot-apply.
const (
	defaultLeaderKey = "ctrl+b"
	defaultActionKey = "ctrl+g"
)

// Pane-navigation choice labels. The select stores these strings in the UI;
// the row's WriteTransform maps them onto the tui.vim_control_hjkl_pane_nav
// Go bool (the A1 typed-write rule).
const (
	paneNavPrefixOnly = "prefix-only"
	paneNavDirect     = "prefix + ctrl+hjkl"
)

// buildConflictStack assembles the keybinding stack the capture widgets
// check candidates against: the standard environment collectors (macOS,
// shell, tmux, grove, nvim) plus the tuimux config collector, which
// DefaultCollectors does not include. Package var so tests can substitute a
// synthetic stack (no live shell probing). ContinueOnError + a short
// timeout: a half-collected stack still warns usefully, and the build must
// never wedge the TUI.
var buildConflictStack = func() *keybind.Stack {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	collectors := append(keybind.DefaultCollectors(ctx), keybind.NewTuimuxCollector())
	stack, _ := keybind.BuildStackWithOpts(ctx, keybind.BuildStackOptions{ContinueOnError: true}, collectors...)
	return stack
}

// keyConflictChecker lazily builds the keybinding stack (first captured
// candidate pays the collector cost, once per page instance) and holds the
// per-setting captured candidates (raw bubbletea form) the capture rows'
// PreviewFn turns into a status line: neutral for the status quo (the row's
// default or currently saved key), a conflict warning otherwise. Warn, don't
// block: a conflicting candidate can still be committed.
type keyConflictChecker struct {
	stack      *keybind.Stack
	built      bool
	candidates map[string]string
}

func newKeyConflictChecker() *keyConflictChecker {
	return &keyConflictChecker{candidates: make(map[string]string)}
}

func (c *keyConflictChecker) ensure() *keybind.Stack {
	if !c.built {
		c.stack = buildConflictStack()
		c.built = true
	}
	return c.stack
}

// rawKeyDisplay converts a normalized key ("C-M-B") to the raw bubbletea
// spelling the page persists and displays everywhere ("ctrl+alt+b") — the
// conflict note speaks one dialect, and it's the one the user's config file
// stores.
func rawKeyDisplay(normalized string) string {
	var mods strings.Builder
	rest := normalized
	for {
		switch {
		case strings.HasPrefix(rest, "C-"):
			mods.WriteString("ctrl+")
			rest = rest[2:]
		case strings.HasPrefix(rest, "M-"):
			mods.WriteString("alt+")
			rest = rest[2:]
		case strings.HasPrefix(rest, "S-"):
			mods.WriteString("shift+")
			rest = rest[2:]
		default:
			return mods.String() + strings.ToLower(rest)
		}
	}
}

// conflictNote formats the status line for a captured candidate key, spoken
// entirely in raw bubbletea spelling ("ctrl+b") — normalization is used only
// to query the stack. conflict reports whether the key is already taken
// (Warning styling) as opposed to looking free (informational).
// FindBindingForKey's layer order skips the tuimux layers, so they are
// probed explicitly; without that a clash with tuimux's own global/leader
// binds would go unreported.
func conflictNote(stack *keybind.Stack, rawKey string) (note string, conflict bool) {
	if stack == nil {
		return "", false
	}
	normalized := keybind.Normalize(rawKey, "tuimux")
	b := stack.FindBindingForKey(normalized)
	if b == nil {
		for _, layer := range []keybind.Layer{keybind.LayerTuimuxGlobal, keybind.LayerTuimuxLeader} {
			if hit := stack.FindBindingInLayer(normalized, layer); hit != nil {
				b = hit
				break
			}
		}
	}
	if b == nil {
		return rawKey + " looks free — no conflicts found", false
	}
	note = fmt.Sprintf("%s is taken by %s: %s (%s binding)", rawKey, b.Layer, b.Action, b.Provenance)
	if alts := stack.SuggestAlternatives(normalized, 3); len(alts) > 0 {
		shown := make([]string, 0, len(alts))
		for _, a := range alts {
			shown = append(shown, rawKeyDisplay(a))
		}
		note += " — alternatives: " + strings.Join(shown, ", ")
	}
	return note, true
}

// BindingRow is one row of the Keys page's read-only effective-bindings
// summary. The host adapts its live resolver into this shape (treemux:
// TerminalModel.EffectiveBindings via panels/config.go); when the ecosystem
// keys registry becomes truthful for treemux/tuimux, only the provider
// implementation changes.
type BindingRow struct {
	// Key is the human-readable key ("Ctrl+G o", "<leader> z").
	Key string
	// Action is what the key does ("Nav · workspaces", "zoom pane").
	Action string
	// Scope classifies the binding's origin ("global", "action", "leader",
	// "panels", "shortcut").
	Scope string
	// Shadowed marks a user binding whose key a built-in consumes first —
	// rendered struck-through-ish (muted) as an honest dead row.
	Shadowed bool
}

// keysBindingsProvider is the process-wide live source for the summary,
// installed by the host via Model.SetBindingsProvider. Package-level because
// the Setting descriptors are closures built before any host attaches (and a
// process hosts at most one config UI). nil falls back to the static leader
// excerpt below.
var keysBindingsProvider func() []BindingRow

// SetBindingsProvider installs the host's live effective-bindings source for
// the Keys page summary. treemux calls this (through configtui.Model) with
// an adapter over TerminalModel.EffectiveBindings; standalone `grove config`
// leaves it unset and renders the static default-leader excerpt.
func (m *Model) SetBindingsProvider(f func() []BindingRow) {
	keysBindingsProvider = f
}

// fallbackBindingRows is the standalone summary: a short static excerpt of
// tuimux's default leader vocabulary. Deliberately DUPLICATED from
// tuimux/bindings.DefaultLeader rather than imported — grove's module does
// not depend on tuimux, and adding that edge for one display table wasn't
// warranted. Keep in sync with tuimux/bindings/bindings.go by hand.
func fallbackBindingRows() []BindingRow {
	rows := []struct{ key, action string }{
		{`"`, "split horizontal"},
		{"%", "split vertical"},
		{"z", "zoom pane"},
		{"x", "close pane"},
		{"h/j/k/l", "navigate panes"},
		{"1-9", "jump to window"},
		{"c", "new window"},
		{"n / p", "next / previous window"},
		{"[", "copy mode"},
		{"?", "help overlay"},
	}
	out := make([]BindingRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, BindingRow{Key: "<leader> " + r.key, Action: r.action, Scope: "leader"})
	}
	return out
}

// maxSummaryRows caps the summary block (the curated pages have no
// scrolling); overflow is summarized in one trailing line.
const maxSummaryRows = 12

// renderBindingRows renders the effective-bindings summary block.
func renderBindingRows(rows []BindingRow, width int) string {
	t := theme.DefaultTheme

	shown := rows
	overflow := 0
	if len(shown) > maxSummaryRows {
		overflow = len(shown) - maxSummaryRows
		shown = shown[:maxSummaryRows]
	}

	keyWidth := 0
	for _, r := range shown {
		if w := lipgloss.Width(r.Key); w > keyWidth {
			keyWidth = w
		}
	}

	var lines []string
	for _, r := range shown {
		keyCell := fmt.Sprintf("%-*s", keyWidth, r.Key)
		line := "  " + t.Normal.Render(keyCell) + "  " + t.Muted.Render(r.Action)
		if r.Shadowed {
			line = "  " + t.Muted.Render(keyCell+"  "+r.Action+" (shadowed)")
		}
		lines = append(lines, line)
	}
	if overflow > 0 {
		lines = append(lines, "  "+t.Muted.Render(fmt.Sprintf("… %d more — see the Help panel (<action> ?)", overflow)))
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// keysTUI returns the merged [tui] section, or nil when absent.
func keysTUI(lc *config.LayeredConfig) *config.TUIConfig {
	if lc != nil && lc.Final != nil {
		return lc.Final.TUI
	}
	return nil
}

// effectiveChord reads a configured chord key, falling back to its shipped
// default.
func effectiveChord(lc *config.LayeredConfig, get func(*config.TUIConfig) string, fallback string) string {
	if t := keysTUI(lc); t != nil {
		if v := get(t); v != "" {
			return v
		}
	}
	return fallback
}

// KeysSettings returns the Keys page's setting descriptors: leader/action
// key-capture rows with conflict warnings and reset-to-default, the
// pane-navigation choice, the two-chord explainer, the effective-bindings
// summary, and a deep link to the keymap debugger. Values are written to the
// global layer via the CuratedPage framework (typed per ControlKind);
// captured chords are persisted in RAW bubbletea form ("ctrl+b") because
// that is what tuimux compares against Config.LeaderKey each keypress —
// normalization ("C-B") is used only for conflict lookup and warning copy.
func KeysSettings() []Setting {
	chk := newKeyConflictChecker()

	readLeader := func(lc *config.LayeredConfig) string {
		return effectiveChord(lc, func(t *config.TUIConfig) string { return t.LeaderKey }, defaultLeaderKey)
	}
	readAction := func(lc *config.LayeredConfig) string {
		return effectiveChord(lc, func(t *config.TUIConfig) string { return t.ActionKey }, defaultActionKey)
	}

	check := func(id string) func(string) {
		return func(v string) {
			chk.candidates[id] = v
			chk.ensure() // pay the collector cost at stage time, not render time
		}
	}
	uncheck := func(id string) func(*config.LayeredConfig) {
		return func(*config.LayeredConfig) { delete(chk.candidates, id) }
	}
	// capturePreview renders the capture row's helper block. A staged
	// candidate that matches the shipped default or the currently saved key
	// gets a neutral status-quo line — re-picking what you already have is
	// never scolded with a conflict warning, even though the default leader
	// (ctrl+b) does collide with readline's backward-char.
	capturePreview := func(id, def, tomlKey, label string, current func(*config.LayeredConfig) string) func(*config.LayeredConfig, int) string {
		t := theme.DefaultTheme
		return func(lc *config.LayeredConfig, width int) string {
			var lines []string
			if raw := chk.candidates[id]; raw != "" {
				switch {
				case raw == def:
					lines = append(lines, t.Muted.Render(raw+" — the default "+label))
				case raw == current(lc):
					lines = append(lines, t.Muted.Render(raw+" — already your "+label))
				default:
					if note, conflict := conflictNote(chk.ensure(), raw); note != "" {
						style := t.Muted
						if conflict {
							style = t.Warning
						}
						lines = append(lines, style.Render(note))
					}
				}
			}
			lines = append(lines,
				t.Muted.Render("enter captures the next keystroke • backspace resets to the default ("+def+")"),
				t.Muted.Render("prefer a file? `grove config edit` opens the global grove.toml ("+tomlKey+")"))
			return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
		}
	}

	return []Setting{
		{
			ID:          "leader",
			Label:       "Leader key",
			Description: "Chord prefix for tmux-standard pane & window operations",
			Essential:   true,
			Path:        []string{"tui", "leader_key"},
			Control:     ControlKeyCapture,
			Options:     []string{defaultLeaderKey},
			Read:        readLeader,
			PreviewFn:   capturePreview("leader", defaultLeaderKey, "tui.leader_key", "leader key", readLeader),
			Preview:     check("leader"),
			Revert:      uncheck("leader"),
			ApplyDomain: embed.SettingDomainLeaderKey,
		},
		{
			ID:          "action_key",
			Label:       "Action key",
			Description: "Chord prefix for grove app actions (panel jumps, palette, rail)",
			Path:        []string{"tui", "action_key"},
			Control:     ControlKeyCapture,
			Options:     []string{defaultActionKey},
			Read:        readAction,
			PreviewFn:   capturePreview("action_key", defaultActionKey, "tui.action_key", "action key", readAction),
			Preview:     check("action_key"),
			Revert:      uncheck("action_key"),
			ApplyDomain: embed.SettingDomainActionKey,
		},
		{
			ID:          "pane_nav",
			Label:       "Pane navigation",
			Description: "How you move between split panes",
			Essential:   true,
			Path:        []string{"tui", "vim_control_hjkl_pane_nav"},
			Control:     ControlSelect,
			Options:     []string{paneNavPrefixOnly, paneNavDirect},
			Read: func(lc *config.LayeredConfig) string {
				if t := keysTUI(lc); t != nil && t.VimControlHjklPaneNav {
					return paneNavDirect
				}
				return paneNavPrefixOnly
			},
			// The select stores a label; the persisted key is a Go bool.
			WriteTransform: func(v interface{}) interface{} {
				if s, ok := v.(string); ok {
					return s == paneNavDirect
				}
				return v
			},
			PreviewFn: func(_ *config.LayeredConfig, width int) string {
				t := theme.DefaultTheme
				lines := []string{
					t.Muted.Render(paneNavPrefixOnly + " (default): <leader> h/j/k/l always moves between panes,"),
					t.Muted.Render("<leader> 1-9 jumps windows, and the rail stays reachable."),
					t.Muted.Render(paneNavDirect + ": adds direct ctrl+h/j/k/l pane moves — but steals ctrl+h/l"),
					t.Muted.Render("from plain shells. TUI apps (nvim, helix, fzf) are unaffected: the key"),
					t.Muted.Render("passes through whenever the pane is running a TUI."),
				}
				return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
			},
			ApplyDomain: embed.SettingDomainVimPaneNav,
		},
		{
			ID:          "chords_help",
			Label:       "About the two chords",
			Description: "Why treemux has both a leader and an action key",
			Control:     ControlStatic,
			PreviewFn: func(lc *config.LayeredConfig, width int) string {
				t := theme.DefaultTheme
				leader := effectiveChord(lc, func(tc *config.TUIConfig) string { return tc.LeaderKey }, defaultLeaderKey)
				action := effectiveChord(lc, func(tc *config.TUIConfig) string { return tc.ActionKey }, defaultActionKey)
				lines := []string{
					t.Normal.Render("Leader ("+leader+")") + t.Muted.Render(" — tmux-standard pane & window ops: splits, zoom, resize, window jumps."),
					t.Normal.Render("Action ("+action+")") + t.Muted.Render(" — grove app actions: panel jumps, palette, rail, reload."),
					t.Muted.Render("A chord waits indefinitely for its next key; esc cancels it."),
				}
				return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
			},
		},
		{
			ID:          "bindings_summary",
			Label:       "Effective bindings",
			Description: "The key table currently in effect (read-only)",
			Control:     ControlStatic,
			PreviewFn: func(_ *config.LayeredConfig, width int) string {
				if keysBindingsProvider != nil {
					return renderBindingRows(keysBindingsProvider(), width)
				}
				return renderBindingRows(fallbackBindingRows(), width)
			},
		},
		{
			ID:          "keymap_link",
			Label:       "Keymap debugger",
			Description: "Inspect the full key-binding stack (or run `grove keys` in a shell)",
			Control:     ControlHostLink,
			Options:     []string{"keymap"},
		},
	}
}
