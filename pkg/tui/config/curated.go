package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/theme"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// ControlKind describes how a curated Setting is edited — and, critically,
// which Go type its value must be written as (see Setting.TypedValue): TOML
// values are typed, and a bool/int written as a quoted string fails core's
// strict decode, silently dropping the entire config file on next load.
type ControlKind int

const (
	// ControlBool is an on/off toggle. Writes a Go bool.
	ControlBool ControlKind = iota
	// ControlSelect cycles through Options. Writes a string.
	ControlSelect
	// ControlText is free-form text input. Writes a string.
	ControlText
	// ControlColor is a color name/hex input. Writes a string.
	ControlColor
	// ControlInt is numeric input. Writes a Go int.
	ControlInt
	// ControlKeyCapture captures the next keystroke as the value (Phase 5
	// wires the capture mode; the kind exists now so descriptors are stable).
	// Writes a string.
	ControlKeyCapture
	// ControlLink is not a value at all: activating it switches the pager to
	// the tab named by Options[0] (e.g. Appearance → Themes). Never written.
	ControlLink
)

// Setting is one row of a curated config page: a described, typed handle on
// a single global-layer TOML key. The same descriptors serve both the full
// config-panel pages and onboarding's essentials subset (spec 23) — the
// Essential tag plus CuratedOpts.EssentialsOnly select the subset, and both
// densities share the identical write path (setSettingMsg → typed global
// save).
type Setting struct {
	// ID is a stable identifier ("focus_style"), useful for tests and lookups.
	ID string
	// Label is the row's display name.
	Label string
	// Description is the one-line explanation shown for the selected row.
	Description string
	// Essential marks the setting for onboarding's essentials-only rendering.
	Essential bool
	// Path is the TOML key path in the global layer (e.g. ["tui","focus","style"]).
	Path []string
	// Control selects the edit widget and the written Go type.
	Control ControlKind
	// Options are the allowed values for ControlSelect, or the target tab ID
	// (Options[0]) for ControlLink.
	Options []string
	// Read returns the setting's current display value from the layered
	// config. nil renders as unset.
	Read func(*config.LayeredConfig) string
	// PreviewFn renders an optional live preview block below the list (e.g.
	// the focus-style two-pane swatch). It receives the layered config so a
	// pure renderer can show effective values without capturing page state.
	// nil means no preview.
	PreviewFn func(layered *config.LayeredConfig, width int) string
	// Preview applies a transient in-process effect for a candidate value —
	// cycled with h/l on selects, or typed into the inline editor — before
	// anything is written (e.g. theme.SetIcons for the icons row, or a
	// preview-override for the focus swatch). Also invoked on commit so
	// standalone `grove config` (where SettingAppliedMsg is inert) still
	// applies the effect in-process. nil means no live preview effect.
	Preview func(value string)
	// Revert undoes the Preview effect when the candidate is abandoned
	// (esc, cursor move, page blur) — the ThemesPage.RevertPreview pattern.
	// It receives the layered config so it can restore the persisted
	// baseline. nil means nothing to undo.
	Revert func(layered *config.LayeredConfig)
	// ApplyDomain names the host's live-apply seam for this setting (the
	// Domain of the embed.SettingAppliedMsg emitted after a save). Empty
	// means no live apply (startup-only settings).
	ApplyDomain string
}

// TypedValue converts the control's string form into the Go-typed value that
// must be written to the config file: bool → Go bool, int → Go int,
// everything else → string. Never returns a typed value for ControlLink
// (links are navigation, not values).
func (s Setting) TypedValue(raw string) (interface{}, error) {
	switch s.Control {
	case ControlBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid boolean %q for %s", raw, s.ID)
		}
		return b, nil
	case ControlInt:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q for %s", raw, s.ID)
		}
		return n, nil
	case ControlLink:
		return nil, fmt.Errorf("link setting %s has no value", s.ID)
	default:
		return raw, nil
	}
}

// setSettingMsg asks the outer Model to persist a curated setting's new
// value to the global config layer (typed, per setting.Control) and to
// notify the host via embed.SettingAppliedMsg. The applyThemeMsg pattern,
// generalized.
type setSettingMsg struct {
	setting Setting
	value   string
}

// LayoutSettings returns the Layout page's setting descriptors. Phase 4
// populates it (drawer orientation/expanded, rail expanded, home-on-startup).
func LayoutSettings() []Setting { return nil }

// KeysSettings returns the Keys page's setting descriptors. Phase 5
// populates it (leader/action capture, pane-nav choice).
func KeysSettings() []Setting { return nil }

// CuratedOpts configures a CuratedPage.
type CuratedOpts struct {
	// EssentialsOnly filters the settings to the Essential-tagged subset —
	// onboarding's density. The write path is identical either way.
	EssentialsOnly bool
}

// CuratedPage is the shared frame for the curated config tabs (Appearance,
// Layout, Keys): a list of Setting rows with per-kind inline editing.
// Values are always read live from the layered config, so a reload (via
// Refresh) is all it takes to reflect a save. Editing emits setSettingMsg;
// the outer Model owns persistence and live-apply notification.
type CuratedPage struct {
	name     string
	tabID    string
	settings []Setting
	cursor   int

	// editing is true while a text-ish control (text/color/int) has the
	// inline input open. IsTextEntryActive reports it so the pager does not
	// steal keys for tab navigation.
	editing bool
	input   textinput.Model

	// pendingIdx/pendingValue hold an uncommitted candidate value staged by
	// h/l cycling on a select row (ThemesPage's preview-then-commit
	// lifecycle). -1 means nothing is staged. Enter commits the candidate;
	// esc, moving the cursor, or leaving the page reverts it.
	pendingIdx   int
	pendingValue string

	layered *config.LayeredConfig
	keys    grovekeymap.ConfigKeyMap
	width   int
	height  int
	active  bool
}

// Compile-time interface checks.
var (
	_ pager.Page              = (*CuratedPage)(nil)
	_ pager.PageWithTitle     = (*CuratedPage)(nil)
	_ pager.PageWithID        = (*CuratedPage)(nil)
	_ pager.PageWithTextInput = (*CuratedPage)(nil)
)

// NewCuratedPage builds a curated page named name over the given setting
// descriptors, filtered by opts. The tab ID is the lowercased name, matching
// the stub pages this framework replaces ("appearance"/"layout"/"keys").
func NewCuratedPage(name string, settings []Setting, layered *config.LayeredConfig, keys grovekeymap.ConfigKeyMap, width, height int, opts CuratedOpts) *CuratedPage {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 200
	ti.Width = 40

	return &CuratedPage{
		name:       name,
		tabID:      strings.ToLower(name),
		settings:   filterSettings(settings, opts),
		input:      ti,
		layered:    layered,
		keys:       keys,
		width:      width,
		height:     height,
		pendingIdx: -1,
	}
}

// filterSettings applies the essentials filter.
func filterSettings(settings []Setting, opts CuratedOpts) []Setting {
	if !opts.EssentialsOnly {
		return settings
	}
	var out []Setting
	for _, s := range settings {
		if s.Essential {
			out = append(out, s)
		}
	}
	return out
}

// Settings returns the page's (post-filter) setting descriptors.
func (p *CuratedPage) Settings() []Setting { return p.settings }

// Name implements pager.Page.
func (p *CuratedPage) Name() string { return p.name }

// TabID implements pager.PageWithID.
func (p *CuratedPage) TabID() string { return p.tabID }

// Title implements pager.PageWithTitle.
func (p *CuratedPage) Title() string {
	title := "  saves to " + setup.AbbreviatePath(globalConfigDisplayPath(p.layered))
	return theme.DefaultTheme.Muted.Render(title)
}

// globalConfigDisplayPath resolves the global config file path for display.
func globalConfigDisplayPath(layered *config.LayeredConfig) string {
	if layered != nil && layered.FilePaths != nil {
		if path := layered.FilePaths[config.SourceGlobal]; path != "" {
			return path
		}
	}
	return setup.GlobalTOMLConfigPath()
}

// Init implements pager.Page.
func (p *CuratedPage) Init() tea.Cmd { return nil }

// Focus implements pager.Page.
func (p *CuratedPage) Focus() tea.Cmd {
	p.active = true
	return nil
}

// Blur implements pager.Page. Leaving the page cancels any open editor and
// reverts any staged (previewed but uncommitted) candidate value.
func (p *CuratedPage) Blur() {
	p.active = false
	p.clearPending(true)
	p.stopEditing()
}

// SetSize implements pager.Page.
func (p *CuratedPage) SetSize(width, height int) {
	p.width = width
	p.height = height
}

// Refresh re-points the page at a reloaded layered config; row values are
// read from it at render time so nothing else needs recomputing.
func (p *CuratedPage) Refresh(layered *config.LayeredConfig) {
	p.layered = layered
}

// IsTextEntryActive implements pager.PageWithTextInput.
func (p *CuratedPage) IsTextEntryActive() bool { return p.editing }

// currentSetting returns the setting under the cursor, or nil.
func (p *CuratedPage) currentSetting() *Setting {
	if p.cursor < 0 || p.cursor >= len(p.settings) {
		return nil
	}
	return &p.settings[p.cursor]
}

// currentValue returns a setting's current display value.
func (p *CuratedPage) currentValue(s Setting) string {
	if s.Read == nil {
		return ""
	}
	return s.Read(p.layered)
}

// stopEditing closes the inline editor without committing.
func (p *CuratedPage) stopEditing() {
	p.editing = false
	p.input.Blur()
}

// pendingFor returns the staged candidate for setting index i, or "".
func (p *CuratedPage) pendingFor(i int) string {
	if p.pendingIdx == i {
		return p.pendingValue
	}
	return ""
}

// stagePending records a candidate value for the setting at index i and
// applies its live preview effect, without committing anything.
func (p *CuratedPage) stagePending(i int, s Setting, value string) {
	p.pendingIdx = i
	p.pendingValue = value
	if s.Preview != nil {
		s.Preview(value)
	}
}

// clearPending drops any staged candidate. When revert is true the owning
// setting's Revert hook restores the persisted baseline (esc/cursor-move/
// blur); commits pass false because the previewed effect is now the saved
// state.
func (p *CuratedPage) clearPending(revert bool) {
	if p.pendingIdx < 0 {
		return
	}
	idx := p.pendingIdx
	p.pendingIdx = -1
	p.pendingValue = ""
	if revert && idx < len(p.settings) && p.settings[idx].Revert != nil {
		p.settings[idx].Revert(p.layered)
	}
}

// cyclePending advances the current select row's staged candidate by dir and
// applies its live preview — the ThemesPage setCursor preview pattern mapped
// onto option cycling. No-op for non-select rows.
func (p *CuratedPage) cyclePending(dir int) {
	s := p.currentSetting()
	if s == nil || s.Control != ControlSelect || len(s.Options) == 0 {
		return
	}
	base := p.pendingFor(p.cursor)
	if base == "" {
		base = p.currentValue(*s)
	}
	idx := -1
	for i, opt := range s.Options {
		if opt == base {
			idx = i
			break
		}
	}
	n := len(s.Options)
	next := s.Options[((idx+dir)%n+n)%n]
	p.stagePending(p.cursor, *s, next)
}

// Update implements pager.Page.
func (p *CuratedPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if !p.active {
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	if p.editing {
		return p.updateEditing(keyMsg)
	}

	switch {
	case key.Matches(keyMsg, p.keys.Up):
		if p.cursor > 0 {
			p.clearPending(true)
			p.cursor--
		}
	case key.Matches(keyMsg, p.keys.Down):
		if p.cursor < len(p.settings)-1 {
			p.clearPending(true)
			p.cursor++
		}
	case key.Matches(keyMsg, p.keys.Top):
		p.clearPending(true)
		p.cursor = 0
	case key.Matches(keyMsg, p.keys.Bottom):
		if len(p.settings) > 0 {
			p.clearPending(true)
			p.cursor = len(p.settings) - 1
		}
	case key.Matches(keyMsg, p.keys.Expand):
		p.cyclePending(1)
	case key.Matches(keyMsg, p.keys.Collapse):
		p.cyclePending(-1)
	case key.Matches(keyMsg, p.keys.Cancel):
		p.clearPending(true)
	case key.Matches(keyMsg, p.keys.Edit), key.Matches(keyMsg, p.keys.Toggle):
		if s := p.currentSetting(); s != nil {
			return p, p.activate(*s)
		}
	}
	return p, nil
}

// activate performs the enter/space action for a setting, per control kind:
// bools commit their toggled value immediately, selects commit the staged
// (h/l-previewed) candidate if one exists or cycle to the next option and
// commit, text-ish kinds open the inline editor, links switch the pager tab.
// Key capture is inert until Phase 5 wires the capture mode.
func (p *CuratedPage) activate(s Setting) tea.Cmd {
	switch s.Control {
	case ControlBool:
		next := "true"
		if p.currentValue(s) == "true" {
			next = "false"
		}
		return p.commitWithPreview(s, next)
	case ControlSelect:
		if len(s.Options) == 0 {
			return nil
		}
		if staged := p.pendingFor(p.cursor); staged != "" {
			return p.commitWithPreview(s, staged)
		}
		current := p.currentValue(s)
		next := s.Options[0]
		for i, opt := range s.Options {
			if opt == current {
				next = s.Options[(i+1)%len(s.Options)]
				break
			}
		}
		return p.commitWithPreview(s, next)
	case ControlText, ControlColor, ControlInt:
		p.editing = true
		p.input.SetValue(p.currentValue(s))
		p.input.CursorEnd()
		return p.input.Focus()
	case ControlLink:
		if len(s.Options) == 0 {
			return nil
		}
		target := s.Options[0]
		return func() tea.Msg { return embed.SwitchTabMsg{TabID: target} }
	}
	return nil
}

// commitWithPreview applies the setting's in-process preview effect for the
// final value (so standalone `grove config` — where SettingAppliedMsg has no
// handler — still live-applies icons etc.), drops the staged candidate
// WITHOUT reverting (the previewed effect is becoming the saved state), and
// emits the persistence request.
func (p *CuratedPage) commitWithPreview(s Setting, value string) tea.Cmd {
	if s.Preview != nil {
		s.Preview(value)
	}
	p.clearPending(false)
	return commitSetting(s, value)
}

// commitSetting emits the persistence request for a new value.
func commitSetting(s Setting, value string) tea.Cmd {
	return func() tea.Msg { return setSettingMsg{setting: s, value: value} }
}

// updateEditing handles keys while the inline text editor is open. Settings
// with a Preview hook see every keystroke as a staged candidate (live color/
// thickness in the focus swatch); esc reverts it, enter commits it.
func (p *CuratedPage) updateEditing(keyMsg tea.KeyMsg) (pager.Page, tea.Cmd) {
	switch {
	case key.Matches(keyMsg, p.keys.Cancel):
		p.clearPending(true)
		p.stopEditing()
		return p, nil
	case key.Matches(keyMsg, p.keys.Confirm):
		s := p.currentSetting()
		if s == nil {
			p.clearPending(true)
			p.stopEditing()
			return p, nil
		}
		value := p.input.Value()
		p.stopEditing()
		return p, p.commitWithPreview(*s, value)
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(keyMsg)
	if s := p.currentSetting(); s != nil && s.Preview != nil {
		p.stagePending(p.cursor, *s, p.input.Value())
	}
	return p, cmd
}

// View implements pager.Page: the settings list, the selected row's
// description, an optional preview block, and a footer of key hints.
func (p *CuratedPage) View() string {
	t := theme.DefaultTheme

	if len(p.settings) == 0 {
		body := t.Muted.Render("No settings on this page yet")
		return lipgloss.Place(p.width, p.height-2, lipgloss.Center, lipgloss.Center, body)
	}

	labelWidth := 0
	for _, s := range p.settings {
		if w := lipgloss.Width(s.Label); w > labelWidth {
			labelWidth = w
		}
	}

	var lines []string
	for i, s := range p.settings {
		lines = append(lines, p.renderRow(s, labelWidth, i == p.cursor, p.pendingFor(i)))
	}

	if s := p.currentSetting(); s != nil {
		if s.Description != "" {
			lines = append(lines, "", t.Muted.Render(s.Description))
		}
		if p.editing {
			lines = append(lines, "", p.input.View())
		}
		if s.PreviewFn != nil {
			lines = append(lines, "", s.PreviewFn(p.layered, p.width))
		}
	}

	body := strings.Join(lines, "\n")
	return lipgloss.JoinVertical(lipgloss.Left, body, p.renderCuratedFooter())
}

// renderRow renders one setting row: cursor, label, current value. A staged
// (previewed, uncommitted) candidate replaces the value with a highlighted
// "candidate (preview)" marker until it is committed or reverted.
func (p *CuratedPage) renderRow(s Setting, labelWidth int, selected bool, pending string) string {
	t := theme.DefaultTheme

	cursor := "  "
	if selected {
		cursor = t.Highlight.Render(theme.IconArrowRightBold) + " "
	}

	label := fmt.Sprintf("%-*s", labelWidth, s.Label)
	if selected {
		label = t.Bold.Render(label)
	} else {
		label = t.Normal.Render(label)
	}

	value := p.currentValue(s)
	switch {
	case s.Control == ControlLink:
		value = t.Path.Render("→ " + firstOption(s))
	case pending != "":
		value = t.Highlight.Render(pending) + " " + t.Muted.Render("(preview)")
	case value == "":
		value = t.Muted.Render("(unset)")
	default:
		value = t.Normal.Render(value)
	}

	return cursor + label + "  " + value
}

// firstOption returns Options[0] or "".
func firstOption(s Setting) string {
	if len(s.Options) == 0 {
		return ""
	}
	return s.Options[0]
}

// renderCuratedFooter renders the key-hint footer.
func (p *CuratedPage) renderCuratedFooter() string {
	t := theme.DefaultTheme
	hints := "enter: change • h/l: preview options • j/k: browse"
	switch {
	case p.editing:
		hints = "enter: save • esc: cancel"
	case p.pendingIdx >= 0:
		hints = "enter: apply & save • esc: revert • h/l: preview options"
	}
	return lipgloss.NewStyle().PaddingTop(1).Render(t.Muted.Render(hints))
}
