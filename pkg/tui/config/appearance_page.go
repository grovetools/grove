package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/theme"
)

// Shipped appearance defaults. These mirror the wiring-layer synthesis in
// treemux (internal/app defaultFocusConfig) and the FocusConfig jsonschema
// defaults in core/config/types.go: when a key is unset in every layer the
// page displays — and cycling starts from — the value the app actually uses.
const (
	defaultFocusStyle         = "gutter"
	defaultFocusActiveColor   = "cyan"
	defaultFocusInactiveColor = "none"
	defaultFocusThickness     = 1
	defaultIconsMode          = "nerd"
)

// previewOverrides holds transient per-setting candidate values staged by
// the CuratedPage preview lifecycle (Setting.Preview/Revert hooks write and
// clear them). The focus swatch reads through it so a cycled-but-uncommitted
// style/color/thickness renders immediately.
type previewOverrides struct {
	values map[string]string
}

func newPreviewOverrides() *previewOverrides {
	return &previewOverrides{values: make(map[string]string)}
}

func (o *previewOverrides) set(id, v string) { o.values[id] = v }
func (o *previewOverrides) clear(id string)  { delete(o.values, id) }

func (o *previewOverrides) get(id string) (string, bool) {
	v, ok := o.values[id]
	return v, ok
}

// focusField extracts one string field from the layered [tui.focus] block,
// falling back when the block or field is unset.
func focusField(lc *config.LayeredConfig, get func(*config.FocusConfig) string, fallback string) string {
	if lc != nil && lc.Final != nil && lc.Final.TUI != nil && lc.Final.TUI.Focus != nil {
		if v := get(lc.Final.TUI.Focus); v != "" {
			return v
		}
	}
	return fallback
}

// focusThicknessValue returns the effective [tui.focus] thickness.
func focusThicknessValue(lc *config.LayeredConfig) int {
	if lc != nil && lc.Final != nil && lc.Final.TUI != nil && lc.Final.TUI.Focus != nil {
		if t := lc.Final.TUI.Focus.Thickness; t > 0 {
			return t
		}
	}
	return defaultFocusThickness
}

// persistedIconsMode returns the configured tui.icons value ("nerd" when
// unset) — the value the icons row displays and cycles from.
func persistedIconsMode(lc *config.LayeredConfig) string {
	if lc != nil && lc.Final != nil && lc.Final.TUI != nil && lc.Final.TUI.Icons != "" {
		return lc.Final.TUI.Icons
	}
	return defaultIconsMode
}

// effectiveIconsMode is the icon set the process is actually using as its
// baseline: GROVE_ICONS pins it (mirroring the icons.go init precedence),
// otherwise the persisted config value. Used to revert an abandoned preview.
func effectiveIconsMode(lc *config.LayeredConfig) string {
	if os.Getenv("GROVE_ICONS") == "ascii" {
		return "ascii"
	}
	return persistedIconsMode(lc)
}

// focusPreviewSpec is the resolved input of the pure focus-swatch renderer.
type focusPreviewSpec struct {
	Style         string
	Thickness     int
	ActiveColor   lipgloss.TerminalColor
	InactiveColor lipgloss.TerminalColor
}

// effectiveFocusSpec resolves the swatch inputs: preview override first,
// then the layered config, then the shipped defaults. Colors resolve through
// theme.Colors.ResolveColor, which maps "none" to lipgloss.NoColor{}.
func effectiveFocusSpec(lc *config.LayeredConfig, ov *previewOverrides) focusPreviewSpec {
	styleGet := func(fc *config.FocusConfig) string { return fc.Style }
	activeGet := func(fc *config.FocusConfig) string { return fc.ActiveColor }
	inactiveGet := func(fc *config.FocusConfig) string { return fc.InactiveColor }

	style := focusField(lc, styleGet, defaultFocusStyle)
	activeName := focusField(lc, activeGet, defaultFocusActiveColor)
	inactiveName := focusField(lc, inactiveGet, defaultFocusInactiveColor)
	thickness := focusThicknessValue(lc)

	if v, ok := ov.get("focus_style"); ok && v != "" {
		style = v
	}
	if v, ok := ov.get("focus_active_color"); ok && v != "" {
		activeName = v
	}
	if v, ok := ov.get("focus_inactive_color"); ok && v != "" {
		inactiveName = v
	}
	if v, ok := ov.get("focus_thickness"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			thickness = n
		}
	}
	if thickness < 1 {
		thickness = 1
	}
	if thickness > 4 {
		thickness = 4
	}

	c := theme.DefaultTheme.Colors
	return focusPreviewSpec{
		Style:         style,
		Thickness:     thickness,
		ActiveColor:   c.ResolveColor(activeName, c.Cyan),
		InactiveColor: c.ResolveColor(inactiveName, lipgloss.NoColor{}),
	}
}

// gutterGlyph mirrors tuimux ViewPane's thickness-to-glyph mapping for the
// gutter focus style: 1 → "▎", 2 → "▌", 3+ → that many "█" columns.
func gutterGlyph(thickness int) string {
	switch {
	case thickness <= 1:
		return "▎"
	case thickness == 2:
		return "▌"
	default:
		return strings.Repeat("█", thickness)
	}
}

// renderFocusPreview is a PURE renderer of a two-pane focus-indicator swatch
// (focused left pane, unfocused right pane) that mimics the three styles of
// tuimux's ViewPane without touching *Model/*PaneNode:
//
//   - gutter: a per-line colored bar on each pane's left edge, glyph chosen
//     by thickness; a lipgloss.NoColor color renders as spaces (unfocused
//     indicator hidden), matching ViewPane's isNone branch.
//   - title: an inverted (background-colored) title bar above each pane.
//   - border: a mid-split vertical separator between the panes whose
//     focused-adjacent half takes the active color, matching ViewPane's
//     simpleSplit/mid := node.H/2 behavior.
func renderFocusPreview(spec focusPreviewSpec, width int) string {
	t := theme.DefaultTheme

	const paneH = 4
	paneW := (width - 8) / 2
	if paneW > 22 {
		paneW = 22
	}
	if paneW < 12 {
		paneW = 12
	}

	focused := renderFocusPreviewPane("focused", true, spec, paneW, paneH)
	unfocused := renderFocusPreviewPane("unfocused", false, spec, paneW, paneH)

	var body string
	if spec.Style == "border" {
		activeStyle := lipgloss.NewStyle().Foreground(spec.ActiveColor)
		inactiveStyle := lipgloss.NewStyle().Foreground(spec.InactiveColor)
		mid := paneH / 2
		var sep []string
		for i := 0; i < paneH; i++ {
			// Focused pane is the left leaf: its half of the separator
			// (top, i < mid) takes the active color.
			style := inactiveStyle
			if i < mid {
				style = activeStyle
			}
			sep = append(sep, style.Render("│"))
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, focused, strings.Join(sep, "\n"), unfocused)
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, focused, " ", unfocused)
	}

	header := t.Muted.Render("Preview")
	return lipgloss.NewStyle().MaxWidth(width).Render(header + "\n" + body)
}

// renderFocusPreviewPane renders one mock pane of the swatch with its focus
// indicator per the spec's style.
func renderFocusPreviewPane(label string, isActive bool, spec focusPreviewSpec, w, h int) string {
	t := theme.DefaultTheme

	color := spec.InactiveColor
	if isActive {
		color = spec.ActiveColor
	}

	nameStyle := t.Muted
	if isActive {
		nameStyle = t.Normal
	}
	bodyLines := []string{
		nameStyle.Render(" " + label),
		t.Muted.Render(" ~"),
	}
	content := lipgloss.NewStyle().
		Width(w).MaxWidth(w).
		Height(h).MaxHeight(h).
		Render(strings.Join(bodyLines, "\n"))

	switch spec.Style {
	case "gutter":
		_, isNone := color.(lipgloss.NoColor)
		var gutterLines []string
		for i := 0; i < h; i++ {
			if isNone {
				gutterLines = append(gutterLines, strings.Repeat(" ", spec.Thickness))
			} else {
				gutterLines = append(gutterLines, lipgloss.NewStyle().Foreground(color).Render(gutterGlyph(spec.Thickness)))
			}
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(gutterLines, "\n"), content)
	case "title":
		title := lipgloss.NewStyle().
			Width(w).MaxWidth(w).
			Foreground(t.Colors.DarkText).
			Background(color).
			Align(lipgloss.Center).
			Render(label)
		return lipgloss.JoinVertical(lipgloss.Left, title, content)
	default: // border: panes are plain, the separator between them carries the indicator
		return content
	}
}

// renderIconsPreview renders the icons sample row — a handful of glyphs read
// live from the theme icon variables, so a broken font (or an active
// theme.SetIcons preview) is immediately visible — plus, in ascii mode, a
// static nerd-font recommendation with per-OS install pointers.
func renderIconsPreview(mode string, width int) string {
	t := theme.DefaultTheme

	samples := []struct {
		icon  string
		label string
	}{
		{theme.IconTree, "tree"},
		{theme.IconGitBranch, "branch"},
		{theme.IconWarning, "warn"},
		{theme.IconFolder, "folder"},
		{theme.IconPlan, "plan"},
	}
	var parts []string
	for _, s := range samples {
		parts = append(parts, fmt.Sprintf("%s %s", t.Normal.Render(s.icon), t.Muted.Render(s.label)))
	}

	lines := []string{
		t.Muted.Render("Sample glyphs"),
		"  " + strings.Join(parts, "   "),
	}
	if mode == "ascii" {
		lines = append(lines, "",
			t.Muted.Render("Tip: a Nerd Font (https://www.nerdfonts.com) unlocks the full icon set."),
			t.Muted.Render("macOS: brew install --cask font-jetbrains-mono-nerd-font • Linux: unzip into ~/.local/share/fonts, run fc-cache • Windows: winget install DEVCOM.JetBrainsMonoNerdFont"))
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// AppearanceSettings returns the Appearance page's setting descriptors:
// the Theme link (→ Themes gallery), the [tui.focus] indicator controls with
// a live two-pane preview swatch, and the icons nerd/ascii toggle with a
// sample glyph row. All rows write typed values to the global layer via the
// CuratedPage framework; the focus rows share the "focus" apply domain and
// icons uses "icons" (theme.SetIcons + refreshTheme in treemux).
func AppearanceSettings() []Setting {
	ov := newPreviewOverrides()

	focusSwatch := func(lc *config.LayeredConfig, width int) string {
		return renderFocusPreview(effectiveFocusSpec(lc, ov), width)
	}
	stage := func(id string) func(string) {
		return func(v string) { ov.set(id, v) }
	}
	unstage := func(id string) func(*config.LayeredConfig) {
		return func(*config.LayeredConfig) { ov.clear(id) }
	}

	return []Setting{
		{
			ID:          "theme",
			Label:       "Theme",
			Description: "Color theme — browse and apply in the Themes gallery",
			Control:     ControlLink,
			Options:     []string{"themes"},
			Read:        persistedThemeName,
		},
		{
			ID:          "focus_style",
			Label:       "Focus style",
			Description: "How the focused pane is marked: colored side bar (gutter), inverted header (title), or highlighted separators (border)",
			Path:        []string{"tui", "focus", "style"},
			Control:     ControlSelect,
			Options:     []string{"gutter", "title", "border"},
			Read: func(lc *config.LayeredConfig) string {
				return focusField(lc, func(fc *config.FocusConfig) string { return fc.Style }, defaultFocusStyle)
			},
			PreviewFn:   focusSwatch,
			Preview:     stage("focus_style"),
			Revert:      unstage("focus_style"),
			ApplyDomain: embed.SettingDomainFocus,
		},
		{
			ID:          "focus_active_color",
			Label:       "Focus active color",
			Description: "Indicator color for the focused pane — a theme color name (cyan, accent, …) or #hex",
			Path:        []string{"tui", "focus", "active_color"},
			Control:     ControlColor,
			Read: func(lc *config.LayeredConfig) string {
				return focusField(lc, func(fc *config.FocusConfig) string { return fc.ActiveColor }, defaultFocusActiveColor)
			},
			PreviewFn:   focusSwatch,
			Preview:     stage("focus_active_color"),
			Revert:      unstage("focus_active_color"),
			ApplyDomain: embed.SettingDomainFocus,
		},
		{
			ID:          "focus_inactive_color",
			Label:       "Focus inactive color",
			Description: "Indicator color for unfocused panes — \"none\" hides it entirely",
			Path:        []string{"tui", "focus", "inactive_color"},
			Control:     ControlColor,
			Read: func(lc *config.LayeredConfig) string {
				return focusField(lc, func(fc *config.FocusConfig) string { return fc.InactiveColor }, defaultFocusInactiveColor)
			},
			PreviewFn:   focusSwatch,
			Preview:     stage("focus_inactive_color"),
			Revert:      unstage("focus_inactive_color"),
			ApplyDomain: embed.SettingDomainFocus,
		},
		{
			ID:          "focus_thickness",
			Label:       "Focus thickness",
			Description: "Indicator width in cells, 1–4 (gutter/title styles)",
			Path:        []string{"tui", "focus", "thickness"},
			Control:     ControlInt,
			Read: func(lc *config.LayeredConfig) string {
				return strconv.Itoa(focusThicknessValue(lc))
			},
			PreviewFn:   focusSwatch,
			Preview:     stage("focus_thickness"),
			Revert:      unstage("focus_thickness"),
			ApplyDomain: embed.SettingDomainFocus,
		},
		{
			ID:          "icons",
			Label:       "Icons",
			Description: "Icon set: nerd (requires a Nerd Font) or ascii (plain-text fallbacks)",
			Path:        []string{"tui", "icons"},
			Control:     ControlSelect,
			Options:     []string{"nerd", "ascii"},
			Read:        persistedIconsMode,
			PreviewFn: func(_ *config.LayeredConfig, width int) string {
				// Derive the displayed mode from the live icon state so an
				// active preview (theme.SetIcons already ran) shows its own
				// glyphs and, for ascii, the font recommendation.
				mode := defaultIconsMode
				if theme.ASCIIIcons {
					mode = "ascii"
				}
				return renderIconsPreview(mode, width)
			},
			Preview: func(v string) {
				ov.set("icons", v)
				theme.SetIcons(v)
			},
			Revert: func(lc *config.LayeredConfig) {
				ov.clear("icons")
				theme.SetIcons(effectiveIconsMode(lc))
			},
			ApplyDomain: embed.SettingDomainIcons,
		},
	}
}
