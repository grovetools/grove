package config

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/theme"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
)

// restoreIcons pins the icon set for a test and restores the process's
// initial set at cleanup (the icon variables are package globals).
func restoreIcons(t *testing.T) {
	t.Helper()
	initialASCII := theme.ASCIIIcons
	t.Cleanup(func() {
		mode := "nerd"
		if initialASCII {
			mode = "ascii"
		}
		theme.SetIcons(mode)
	})
}

// TestAppearanceSettingsRows pins the Appearance page's descriptor table:
// row identity, TOML paths, control kinds, options, and apply domains.
func TestAppearanceSettingsRows(t *testing.T) {
	settings := AppearanceSettings()

	type wantRow struct {
		id      string
		path    string
		control ControlKind
		domain  string
		options []string
	}
	wants := []wantRow{
		{"theme", "", ControlLink, "", []string{"themes"}},
		{"focus_style", "tui.focus.style", ControlSelect, embed.SettingDomainFocus, []string{"gutter", "title", "border"}},
		{"focus_active_color", "tui.focus.active_color", ControlColor, embed.SettingDomainFocus, nil},
		{"focus_inactive_color", "tui.focus.inactive_color", ControlColor, embed.SettingDomainFocus, nil},
		{"focus_thickness", "tui.focus.thickness", ControlInt, embed.SettingDomainFocus, nil},
		{"icons", "tui.icons", ControlSelect, embed.SettingDomainIcons, []string{"nerd", "ascii"}},
	}

	if len(settings) != len(wants) {
		t.Fatalf("AppearanceSettings has %d rows, want %d", len(settings), len(wants))
	}
	for i, want := range wants {
		s := settings[i]
		if s.ID != want.id {
			t.Errorf("row %d: ID = %q, want %q", i, s.ID, want.id)
		}
		if got := strings.Join(s.Path, "."); got != want.path {
			t.Errorf("row %s: path = %q, want %q", want.id, got, want.path)
		}
		if s.Control != want.control {
			t.Errorf("row %s: control = %d, want %d", want.id, s.Control, want.control)
		}
		if s.ApplyDomain != want.domain {
			t.Errorf("row %s: domain = %q, want %q", want.id, s.ApplyDomain, want.domain)
		}
		if want.options != nil {
			if got := strings.Join(s.Options, ","); got != strings.Join(want.options, ",") {
				t.Errorf("row %s: options = %q, want %q", want.id, got, strings.Join(want.options, ","))
			}
		}
	}

	// The focus rows must render the shared preview swatch; icons has its own.
	for _, s := range settings[1:] {
		if s.PreviewFn == nil {
			t.Errorf("row %s: missing PreviewFn", s.ID)
		}
		if s.Preview == nil || s.Revert == nil {
			t.Errorf("row %s: missing Preview/Revert hooks", s.ID)
		}
	}
}

// TestAppearanceDefaultsDisplayed: with an empty layered config every row
// reads the shipped default, so cycling starts from what the app actually
// uses (gutter/cyan/none/1, nerd icons).
func TestAppearanceDefaultsDisplayed(t *testing.T) {
	wants := map[string]string{
		"focus_style":          "gutter",
		"focus_active_color":   "cyan",
		"focus_inactive_color": "none",
		"focus_thickness":      "1",
		"icons":                "nerd",
	}
	for _, s := range AppearanceSettings() {
		want, ok := wants[s.ID]
		if !ok {
			continue
		}
		if got := s.Read(nil); got != want {
			t.Errorf("%s default = %q, want %q", s.ID, got, want)
		}
	}
}

// TestGutterGlyphByThickness pins the ViewPane glyph mapping the swatch
// mimics: 1 → "▎" (thin), 2+ → "▌" (thick) — always exactly one column.
func TestGutterGlyphByThickness(t *testing.T) {
	cases := []struct {
		thickness int
		want      string
	}{
		{1, "▎"},
		{2, "▌"},
		{3, "▌"},
		{4, "▌"},
	}
	for _, tc := range cases {
		if got := gutterGlyph(tc.thickness); got != tc.want {
			t.Errorf("gutterGlyph(%d) = %q, want %q", tc.thickness, got, tc.want)
		}
	}
}

// TestBorderGlyphsByThickness pins the weight-based border set the swatch
// mimics: light box-drawing at thickness 1, heavy at 2+.
func TestBorderGlyphsByThickness(t *testing.T) {
	tl, hz, tr, vt, bl, br := borderGlyphs(1)
	if got := tl + hz + tr + vt + bl + br; got != "┌─┐│└┘" {
		t.Errorf("borderGlyphs(1) = %q, want light set", got)
	}
	for _, thickness := range []int{2, 3} {
		tl, hz, tr, vt, bl, br = borderGlyphs(thickness)
		if got := tl + hz + tr + vt + bl + br; got != "┏━┓┃┗┛" {
			t.Errorf("borderGlyphs(%d) = %q, want heavy set", thickness, got)
		}
	}
}

// TestRenderFocusPreviewGutter: the gutter style draws the thickness glyph
// once per pane line for each colored pane, and a lipgloss.NoColor inactive
// color renders spaces instead (the unfocused indicator disappears).
func TestRenderFocusPreviewGutter(t *testing.T) {
	const paneH = 4

	for _, thickness := range []int{1, 2, 3} {
		glyph := gutterGlyph(thickness)

		both := renderFocusPreview(focusPreviewSpec{
			Style:         "gutter",
			Thickness:     thickness,
			ActiveColor:   lipgloss.Color("6"),
			InactiveColor: lipgloss.Color("8"),
		}, 80)
		if got := strings.Count(both, glyph); got != 2*paneH {
			t.Errorf("thickness %d, both colored: %d %q glyphs, want %d", thickness, got, glyph, 2*paneH)
		}

		noneInactive := renderFocusPreview(focusPreviewSpec{
			Style:         "gutter",
			Thickness:     thickness,
			ActiveColor:   lipgloss.Color("6"),
			InactiveColor: lipgloss.NoColor{},
		}, 80)
		if got := strings.Count(noneInactive, glyph); got != paneH {
			t.Errorf("thickness %d, none inactive: %d %q glyphs, want %d (active pane only)", thickness, got, glyph, paneH)
		}
	}

	// Both none: no glyphs at all — pure spacing.
	allNone := renderFocusPreview(focusPreviewSpec{
		Style:         "gutter",
		Thickness:     1,
		ActiveColor:   lipgloss.NoColor{},
		InactiveColor: lipgloss.NoColor{},
	}, 80)
	if strings.Contains(allNone, "▎") {
		t.Error("both none: gutter glyph still rendered")
	}
}

// TestRenderFocusPreviewTitleAndBorder: title renders a bar with the pane
// labels and no gutter glyphs; border renders a four-edge frame around each
// pane — light at thickness 1, heavy at 2, blank (reserved) for NoColor.
func TestRenderFocusPreviewTitleAndBorder(t *testing.T) {
	title := renderFocusPreview(focusPreviewSpec{
		Style:         "title",
		Thickness:     1,
		ActiveColor:   lipgloss.Color("6"),
		InactiveColor: lipgloss.NoColor{},
	}, 80)
	if !strings.Contains(title, "focused") || !strings.Contains(title, "unfocused") {
		t.Error("title style: pane labels missing")
	}
	if strings.Contains(title, "▎") {
		t.Error("title style: unexpected gutter glyph")
	}

	// Both colors real: both panes carry a light frame → each corner glyph
	// appears once per pane.
	border := renderFocusPreview(focusPreviewSpec{
		Style:         "border",
		Thickness:     1,
		ActiveColor:   lipgloss.Color("6"),
		InactiveColor: lipgloss.Color("8"),
	}, 80)
	for _, g := range []string{"┌", "┐", "└", "┘"} {
		if got := strings.Count(border, g); got != 2 {
			t.Errorf("border both colored: %d %q corners, want 2 (one per pane)", got, g)
		}
	}
	if strings.Contains(border, "┏") {
		t.Error("border thickness 1: heavy glyphs rendered")
	}
	if strings.Contains(border, "▎") {
		t.Error("border style: unexpected gutter glyph")
	}

	// Thickness 2 switches to the heavy set.
	heavy := renderFocusPreview(focusPreviewSpec{
		Style:         "border",
		Thickness:     2,
		ActiveColor:   lipgloss.Color("6"),
		InactiveColor: lipgloss.Color("8"),
	}, 80)
	if got := strings.Count(heavy, "┏"); got != 2 {
		t.Errorf("border thickness 2: %d ┏ corners, want 2", got)
	}
	if strings.Contains(heavy, "┌") {
		t.Error("border thickness 2: light glyphs rendered")
	}

	// NoColor inactive: only the focused pane's frame is visible; the
	// unfocused frame is blank but still reserved (pane blocks stay the
	// same size, so the two panes still render side by side).
	noneInactive := renderFocusPreview(focusPreviewSpec{
		Style:         "border",
		Thickness:     1,
		ActiveColor:   lipgloss.Color("6"),
		InactiveColor: lipgloss.NoColor{},
	}, 80)
	for _, g := range []string{"┌", "┐", "└", "┘"} {
		if got := strings.Count(noneInactive, g); got != 1 {
			t.Errorf("border none inactive: %d %q corners, want 1 (focused pane only)", got, g)
		}
	}

	// NoColor active too: no frame glyphs anywhere.
	allNone := renderFocusPreview(focusPreviewSpec{
		Style:         "border",
		Thickness:     1,
		ActiveColor:   lipgloss.NoColor{},
		InactiveColor: lipgloss.NoColor{},
	}, 80)
	if strings.ContainsAny(allNone, "┌┐└┘┏┓┗┛│┃─━") {
		t.Error("border all none: frame glyphs still rendered")
	}
}

// TestEffectiveFocusSpecOverrides: preview overrides beat the layered
// config, "none" resolves to lipgloss.NoColor, and thickness is clamped to
// the effective 1–2 weight range.
func TestEffectiveFocusSpecOverrides(t *testing.T) {
	ov := newPreviewOverrides()

	spec := effectiveFocusSpec(nil, ov)
	if spec.Style != "gutter" || spec.Thickness != 1 {
		t.Errorf("defaults: %+v, want gutter/1", spec)
	}
	if _, isNone := spec.InactiveColor.(lipgloss.NoColor); !isNone {
		t.Errorf("default inactive = %v, want NoColor", spec.InactiveColor)
	}

	ov.set("focus_style", "title")
	ov.set("focus_active_color", "none")
	ov.set("focus_thickness", "9")
	spec = effectiveFocusSpec(nil, ov)
	if spec.Style != "title" {
		t.Errorf("override style = %q, want title", spec.Style)
	}
	if _, isNone := spec.ActiveColor.(lipgloss.NoColor); !isNone {
		t.Errorf("override active \"none\" = %v, want NoColor", spec.ActiveColor)
	}
	if spec.Thickness != 2 {
		t.Errorf("thickness 9 clamped to %d, want 2", spec.Thickness)
	}

	ov.clear("focus_style")
	if spec = effectiveFocusSpec(nil, ov); spec.Style != "gutter" {
		t.Errorf("cleared override: style = %q, want gutter", spec.Style)
	}
}

// TestIconsPreviewLifecycle drives the CuratedPage preview/commit/revert
// lifecycle on the icons row: h/l cycling live-applies theme.SetIcons
// without committing, esc reverts to the persisted baseline, and enter
// commits the staged candidate.
func TestIconsPreviewLifecycle(t *testing.T) {
	restoreIcons(t)
	t.Setenv("GROVE_ICONS", "")
	theme.SetIcons("nerd")

	keys := grovekeymap.NewConfigKeyMap(nil)
	p := NewCuratedPage("Appearance", AppearanceSettings(), nil, keys, 80, 24, CuratedOpts{})
	p.Focus()

	// Move to the icons row (last).
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if s := p.currentSetting(); s == nil || s.ID != "icons" {
		t.Fatalf("cursor not on icons row: %+v", p.currentSetting())
	}

	// l cycles nerd → ascii as a staged preview: applied live, not committed.
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if cmd != nil {
		t.Error("preview cycle: expected no command (nothing committed)")
	}
	if !theme.ASCIIIcons {
		t.Error("preview cycle: theme.SetIcons(\"ascii\") not applied")
	}
	if p.pendingFor(p.cursor) != "ascii" {
		t.Errorf("staged candidate = %q, want ascii", p.pendingFor(p.cursor))
	}

	// esc reverts the preview to the persisted baseline (nerd).
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if theme.ASCIIIcons {
		t.Error("esc: preview not reverted to nerd")
	}
	if p.pendingIdx != -1 {
		t.Error("esc: staged candidate not cleared")
	}

	// l then enter commits the staged candidate.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("commit: expected a command")
	}
	msg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("commit: expected setSettingMsg, got %T", cmd())
	}
	if msg.setting.ID != "icons" || msg.value != "ascii" {
		t.Errorf("commit: got %s=%q, want icons=ascii", msg.setting.ID, msg.value)
	}
	if !theme.ASCIIIcons {
		t.Error("commit: live preview effect lost")
	}
	if p.pendingIdx != -1 {
		t.Error("commit: staged candidate not cleared")
	}

	// Blur after a commit must NOT revert (the previewed value is saved).
	p.Blur()
	if !theme.ASCIIIcons {
		t.Error("blur after commit: committed icon set reverted")
	}
}

// TestBlurRevertsStagedPreview: leaving the page with an uncommitted staged
// candidate restores the baseline (ThemesPage Blur/RevertPreview parity).
func TestBlurRevertsStagedPreview(t *testing.T) {
	restoreIcons(t)
	t.Setenv("GROVE_ICONS", "")
	theme.SetIcons("nerd")

	keys := grovekeymap.NewConfigKeyMap(nil)
	p := NewCuratedPage("Appearance", AppearanceSettings(), nil, keys, 80, 24, CuratedOpts{})
	p.Focus()
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !theme.ASCIIIcons {
		t.Fatal("preview not applied")
	}

	p.Blur()
	if theme.ASCIIIcons {
		t.Error("Blur did not revert the staged icons preview")
	}
}

// isNerdPrivateUseRune reports whether r sits in the private-use areas Nerd
// Fonts occupy (BMP PUA U+E000–U+F8FF and plane-15/16 supplementary PUA).
func isNerdPrivateUseRune(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) || r >= 0xF0000
}

// TestASCIIModeNoNerdGlyphLeaks is the Phase-3 ASCII acceptance spot check:
// with the ASCII icon set active, the Appearance page view and the Data page
// view must contain no private-use (nerd-only) glyphs. (The full splash/rail
// audit is Phase 5+.)
func TestASCIIModeNoNerdGlyphLeaks(t *testing.T) {
	restoreIcons(t)
	t.Setenv("GROVE_ICONS", "")
	theme.SetIcons("ascii")

	// Appearance page (freshly built so any construction-time icon caching
	// would be caught too).
	keys := grovekeymap.NewConfigKeyMap(nil)
	ap := NewCuratedPage("Appearance", AppearanceSettings(), nil, keys, 100, 30, CuratedOpts{})
	ap.Focus()
	assertNoNerdGlyphs(t, "Appearance page", ap.View())

	// Walk every row so each description/preview block renders once.
	for range AppearanceSettings() {
		_, _ = ap.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		assertNoNerdGlyphs(t, "Appearance page (row walk)", ap.View())
	}

	// Data page over a real two-layer fixture.
	layered, _, _ := writeGlobalAndProjectLayers(t)
	m := newDataModel(t, layered, t.TempDir())
	assertNoNerdGlyphs(t, "Data page", m.View())
}

// TestAppearanceWriteThrough drives two REAL Appearance rows through the
// Model's typed write path: the select writes a string, the int row writes a
// TOML integer, both land in the global layer, survive an independent
// LoadLayered, and emit the focus apply domain.
func TestAppearanceWriteThrough(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	settings := AppearanceSettings()

	var styleRow, thicknessRow *Setting
	for i := range settings {
		switch settings[i].ID {
		case "focus_style":
			styleRow = &settings[i]
		case "focus_thickness":
			thicknessRow = &settings[i]
		}
	}
	if styleRow == nil || thicknessRow == nil {
		t.Fatal("focus rows missing from AppearanceSettings")
	}

	m2, applied := applySetting(t, m, *styleRow, "title")
	if applied == nil || applied.Domain != embed.SettingDomainFocus {
		t.Fatalf("style: applied = %+v, want focus domain", applied)
	}
	if got := styleRow.Read(m2.layered); got != "title" {
		t.Errorf("style displayed = %q, want title", got)
	}

	m3, applied := applySetting(t, m2, *thicknessRow, "3")
	if applied == nil || applied.Domain != embed.SettingDomainFocus {
		t.Fatalf("thickness: applied = %+v, want focus domain", applied)
	}
	final := m3.layered.Final
	if final.TUI == nil || final.TUI.Focus == nil || final.TUI.Focus.Thickness != 3 || final.TUI.Focus.Style != "title" {
		t.Errorf("reloaded Final focus = %+v, want style=title thickness=3", final.TUI)
	}
	if final.TUI.Theme != "kanagawa-dark" {
		t.Error("seeded theme lost — global file dropped by a mistyped write")
	}
}

func assertNoNerdGlyphs(t *testing.T, context, view string) {
	t.Helper()
	for _, r := range view {
		if isNerdPrivateUseRune(r) {
			t.Fatalf("%s: nerd-only glyph %q (U+%04X) leaked into ASCII-mode output:\n%s", context, string(r), r, view)
		}
	}
}
