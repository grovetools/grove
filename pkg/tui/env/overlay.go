package env

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/grovetools/core/tui/theme"
)

// OverlayItem is the minimal shape of anything that can be shown in a quick-
// switcher overlay row. Phase 5c uses it for profiles; Phase 5e will reuse
// it for the worktree picker. Implementations are expected to be cheap
// value types owned by the caller — the overlay never mutates them.
type OverlayItem interface {
	Key() string
	Glyph() string
	GlyphStyle() lipgloss.Style
	Label() string
	Subtitle() string // e.g. "● running · ☁ applied 2d ago"
	Provider() string
}

// overlaySelectedMsg is emitted when the user presses Enter on a row. The
// parent model is responsible for acting on the selection and clearing the
// overlay (setting m.overlay = nil).
type overlaySelectedMsg struct{ key string }

// overlayClosedMsg is emitted when the user dismisses the overlay (Esc or
// toggle-close via the trigger key). The parent clears m.overlay.
type overlayClosedMsg struct{}

// OverlayModel is a reusable centered-popup component. It owns its cursor,
// renders a bordered box via lipgloss, and emits discrete messages on
// selection / dismissal so the parent doesn't need a callback field.
type OverlayModel struct {
	title  string
	hint   string
	items  []OverlayItem
	cursor int
	width  int
}

// NewOverlay constructs an overlay and positions the cursor on the row
// whose Key() matches selectedKey, falling back to 0 when not found. width
// is the inner content width (ex-border/padding) — the default of 50 is
// what matches the mockup.
func NewOverlay(title, hint string, items []OverlayItem, selectedKey string) *OverlayModel {
	o := &OverlayModel{
		title: title,
		hint:  hint,
		items: items,
		width: 50,
	}
	for i, it := range items {
		if it.Key() == selectedKey {
			o.cursor = i
			break
		}
	}
	if o.cursor >= len(items) {
		o.cursor = 0
	}
	return o
}

// Update handles the overlay's keystrokes. The parent routes every KeyMsg
// here while m.overlay != nil, so the pager/panel never see these keys.
func (o *OverlayModel) Update(msg tea.Msg) (*OverlayModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return o, nil
	}
	switch km.String() {
	case "j", "down":
		if len(o.items) > 0 {
			o.cursor = (o.cursor + 1) % len(o.items)
		}
	case "k", "up":
		if len(o.items) > 0 {
			o.cursor = (o.cursor - 1 + len(o.items)) % len(o.items)
		}
	case "enter":
		if len(o.items) == 0 {
			return o, func() tea.Msg { return overlayClosedMsg{} }
		}
		key := o.items[o.cursor].Key()
		return o, func() tea.Msg { return overlaySelectedMsg{key: key} }
	case "esc", "P":
		return o, func() tea.Msg { return overlayClosedMsg{} }
	}
	return o, nil
}

// View renders the overlay as a bordered opaque box. Opaque background is
// non-negotiable — without it the base view bleeds through the popup and
// the rows become unreadable.
func (o *OverlayModel) View() string {
	th := theme.DefaultTheme
	inner := o.width

	// Title bar: title on the left, hint right-aligned. We compute the
	// gap in cells (ansi-aware) so wide glyphs in the title don't throw
	// off the alignment.
	titleLeft := th.Accent.Render(o.title)
	titleRight := th.Muted.Render(o.hint)
	gap := inner - ansi.StringWidth(o.title) - ansi.StringWidth(o.hint)
	if gap < 1 {
		gap = 1
	}
	titleLine := titleLeft + strings.Repeat(" ", gap) + titleRight

	rowStyle := lipgloss.NewStyle().
		Background(theme.SubtleBackground).
		Width(inner)
	focusStyle := lipgloss.NewStyle().
		Background(theme.SelectedBackground).
		Width(inner).
		Bold(true)

	lines := []string{titleLine, ""}
	for i, it := range o.items {
		lines = append(lines, o.renderRow(it, i == o.cursor, inner, rowStyle, focusStyle))
	}

	body := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")). // violet-ish accent
		Background(theme.SubtleBackground).
		Padding(0, 1).
		Render(body)

	return box
}

// renderRow renders one item row: "  <glyph>  <label> · <subtitle>   <provider>".
// Provider is right-aligned inside the row's inner width. The whole row is
// wrapped in a Width()-forced style so the focus background fills edge-to-edge.
func (o *OverlayModel) renderRow(it OverlayItem, focused bool, inner int, base, focus lipgloss.Style) string {
	th := theme.DefaultTheme
	glyph := it.GlyphStyle().Render(it.Glyph())
	label := it.Label()
	sub := it.Subtitle()
	provider := it.Provider()

	// Left side: "<glyph>  <label>[ · <subtitle>]"
	labelStyle := th.Normal
	if focused {
		labelStyle = th.Bold
	}
	left := glyph + "  " + labelStyle.Render(label)
	if sub != "" {
		left += "  " + th.Muted.Render("· "+sub)
	}

	// Right side: provider name, muted.
	right := th.Muted.Render(provider)

	// Compute gap in cells between left and right so provider sits flush
	// with the right edge of the row. ansi.StringWidth ignores escape
	// codes, so we measure against it.Glyph() / the raw label directly
	// rather than the rendered segments.
	gap := inner - ansi.StringWidth(it.Glyph()) - 2 - ansi.StringWidth(label)
	if sub != "" {
		gap -= 2 + ansi.StringWidth("· "+sub)
	}
	gap -= ansi.StringWidth(provider)
	if gap < 1 {
		gap = 1
	}

	content := left + strings.Repeat(" ", gap) + right
	if focused {
		return focus.Render(content)
	}
	return base.Render(content)
}

// placeOverlay composes fg on top of bg at cell offset (x, y). The base
// view stays underneath for any area the overlay doesn't cover; overlay
// lines replace a rectangular window sized by fg's max line width.
//
// ANSI resets are injected around the overlay slice so background colors
// or faint attributes from the base view don't bleed into the popup and
// vice-versa. lipgloss v1.1.0 doesn't ship its own PlaceOverlay — this is
// the minimal substitute we need.
func placeOverlay(x, y int, fg, bg string) string {
	if fg == "" {
		return bg
	}
	if bg == "" {
		return fg
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	fgLines := strings.Split(fg, "\n")
	bgLines := strings.Split(bg, "\n")

	fgWidth := 0
	for _, l := range fgLines {
		if w := ansi.StringWidth(l); w > fgWidth {
			fgWidth = w
		}
	}
	// Pad every fg line out to fgWidth so the overlay rectangle stays
	// uniform — otherwise short lines let the bg leak through on the right.
	for i, line := range fgLines {
		if pad := fgWidth - ansi.StringWidth(line); pad > 0 {
			fgLines[i] = line + strings.Repeat(" ", pad)
		}
	}

	const reset = "\x1b[0m"
	for i, fgLine := range fgLines {
		idx := y + i
		if idx < 0 || idx >= len(bgLines) {
			continue
		}
		bgLine := bgLines[idx]
		bgWidth := ansi.StringWidth(bgLine)

		var left, right string
		if x > 0 {
			if x >= bgWidth {
				left = bgLine + strings.Repeat(" ", x-bgWidth)
			} else {
				left = ansi.Cut(bgLine, 0, x)
			}
		}
		if x+fgWidth < bgWidth {
			right = ansi.Cut(bgLine, x+fgWidth, bgWidth)
		}
		bgLines[idx] = left + reset + fgLine + reset + right
	}
	return strings.Join(bgLines, "\n")
}
