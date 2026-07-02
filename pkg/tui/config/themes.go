package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"

	"github.com/grovetools/grove/pkg/setup"
)

// applyThemeMsg asks the outer Model to persist the selected theme name to
// the global config layer (["tui", "theme"]).
type applyThemeMsg struct{ name string }

// themeItem is one row of the Themes page list. Rows with an empty value are
// non-selectable separators; family rows select the adaptive family entry
// while variant rows select a specific palette.
type themeItem struct {
	label   string
	value   string // registry name to preview/persist; "" = separator
	family  bool   // family-selector row (bold)
	variant bool   // indented variant row
	meta    theme.PaletteMeta
}

// ThemesPage is a bespoke pager.Page (not a LayerPage) that renders a theme
// gallery: a grouped family/variant list on the left and a live swatch
// preview pane on the right. Moving the cursor re-themes the running process
// via theme.SetTheme; Blur/esc reverts to the persisted theme; enter persists
// through the outer Model's saveToLayer path.
type ThemesPage struct {
	items  []themeItem
	cursor int

	// saved is the baseline theme name to revert to when the user leaves
	// without persisting. It tracks the persisted ["tui","theme"] value
	// (falling back to the theme active at construction).
	saved string
	// previewed is true while the in-process theme differs from saved.
	previewed bool

	viewport viewport.Model
	layered  *config.LayeredConfig
	width    int
	height   int
	active   bool

	lastGPress time.Time // gg chord
}

// Compile-time interface checks.
var (
	_ pager.Page          = (*ThemesPage)(nil)
	_ pager.PageWithTitle = (*ThemesPage)(nil)
	_ pager.PageWithID    = (*ThemesPage)(nil)
)

// NewThemesPage builds the Themes page from the core palette registry.
func NewThemesPage(layered *config.LayeredConfig, width, height int) *ThemesPage {
	p := &ThemesPage{
		items:   buildThemeItems(),
		layered: layered,
		width:   width,
		height:  height,
		saved:   theme.DefaultTheme.Name,
	}
	if name := persistedThemeName(layered); name != "" {
		p.saved = name
	}
	p.viewport = viewport.New(p.listWidth(), height)
	p.cursor = p.savedItemIndex()
	if p.cursor < 0 {
		p.cursor = p.firstSelectable()
	}
	p.updateContent()
	return p
}

// buildThemeItems groups the registry's palettes by family. Families with a
// single variant collapse to one row; multi-variant families get a
// selectable family row (adaptive light/dark colors when both appearances
// exist) followed by indented variant rows.
func buildThemeItems() []themeItem {
	metas := theme.List()

	// Group while preserving theme.List() order (family, dark-first, name).
	var families []string
	byFamily := make(map[string][]theme.PaletteMeta)
	for _, m := range metas {
		if _, seen := byFamily[m.Family]; !seen {
			families = append(families, m.Family)
		}
		byFamily[m.Family] = append(byFamily[m.Family], m)
	}

	var items []themeItem
	for i, family := range families {
		variants := byFamily[family]
		if i > 0 {
			items = append(items, themeItem{}) // separator
		}
		if len(variants) == 1 {
			items = append(items, themeItem{
				label:  family,
				value:  variants[0].Name,
				family: true,
				meta:   variants[0],
			})
			continue
		}
		items = append(items, themeItem{
			label:  family,
			value:  family,
			family: true,
			meta:   variants[0],
		})
		for _, v := range variants {
			label := v.Variant
			if v.Appearance == "light" && !strings.Contains(v.Variant, "light") {
				label += " (light)"
			}
			items = append(items, themeItem{
				label:   label,
				value:   v.Name,
				variant: true,
				meta:    v,
			})
		}
	}
	return items
}

// persistedThemeName returns the resolved ["tui","theme"] value from the
// layered config, or "".
func persistedThemeName(layered *config.LayeredConfig) string {
	if layered == nil || layered.Final == nil || layered.Final.TUI == nil {
		return ""
	}
	return strings.TrimSpace(layered.Final.TUI.Theme)
}

// savedItemIndex locates the list row matching the saved theme name: first
// by exact value, then by resolving the name through the palette registry.
func (p *ThemesPage) savedItemIndex() int {
	norm := normalizeName(p.saved)
	for i, it := range p.items {
		if it.value != "" && normalizeName(it.value) == norm {
			return i
		}
	}
	if pal, ok := theme.Lookup(p.saved); ok {
		for i, it := range p.items {
			if it.variant && it.meta.Name == pal.Meta.Name {
				return i
			}
		}
	}
	return -1
}

// normalizeName mirrors the theme registry's name normalization.
func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	return strings.ReplaceAll(name, "_", "-")
}

func (p *ThemesPage) firstSelectable() int {
	for i, it := range p.items {
		if it.value != "" {
			return i
		}
	}
	return 0
}

// Name implements pager.Page.
func (p *ThemesPage) Name() string { return "Themes" }

// TabID implements pager.PageWithID.
func (p *ThemesPage) TabID() string { return "themes" }

// Title implements pager.PageWithTitle.
func (p *ThemesPage) Title() string {
	saved := p.saved
	if saved == "" {
		saved = theme.DefaultTheme.Name
	}
	title := fmt.Sprintf("  theme: %s · saves to %s", saved, setup.AbbreviatePath(p.globalConfigPath()))
	return theme.DefaultTheme.Muted.Render(title)
}

func (p *ThemesPage) globalConfigPath() string {
	if p.layered != nil && p.layered.FilePaths != nil {
		if path := p.layered.FilePaths[config.SourceGlobal]; path != "" {
			return path
		}
	}
	return setup.GlobalTOMLConfigPath()
}

// Init implements pager.Page.
func (p *ThemesPage) Init() tea.Cmd { return nil }

// Focus implements pager.Page.
func (p *ThemesPage) Focus() tea.Cmd {
	p.active = true
	// Re-render with whatever theme is now active (a save elsewhere or a
	// prior revert may have changed it since the last visit).
	p.updateContent()
	return nil
}

// Blur implements pager.Page. Leaving the page without persisting reverts
// any live preview to the saved baseline.
func (p *ThemesPage) Blur() {
	p.active = false
	p.RevertPreview()
}

// RevertPreview restores the persisted theme if a live preview is active.
// Safe to call at any time (no-ops when nothing was previewed or when
// GROVE_THEME pins the process).
func (p *ThemesPage) RevertPreview() {
	if !p.previewed {
		return
	}
	p.previewed = false
	if p.saved != "" {
		_ = theme.SetTheme(p.saved)
	}
	p.updateContent()
}

// MarkSaved records name as the new persisted baseline after a successful
// save, so leaving the page no longer reverts.
func (p *ThemesPage) MarkSaved(name string) {
	p.saved = name
	p.previewed = false
	p.updateContent()
}

// Refresh re-reads the persisted theme from a reloaded layered config.
func (p *ThemesPage) Refresh(layered *config.LayeredConfig) {
	p.layered = layered
	if name := persistedThemeName(layered); name != "" && !p.previewed {
		p.saved = name
	}
	p.updateContent()
}

// SetSize implements pager.Page.
func (p *ThemesPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	vpHeight := height - 3 // footer (padding + hints) like LayerPage
	if vpHeight < 1 {
		vpHeight = 1
	}
	p.viewport.Width = p.listWidth()
	p.viewport.Height = vpHeight
	p.updateContent()
}

// listWidth is the width of the left list column.
func (p *ThemesPage) listWidth() int {
	w := 28
	if half := p.width / 2; half < w {
		w = half
	}
	if w < 16 {
		w = 16
	}
	return w
}

// Update implements pager.Page.
func (p *ThemesPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if !p.active {
		return p, nil
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	switch keyStr := keyMsg.String(); keyStr {
	case "up", "k":
		p.moveCursor(-1)
	case "down", "j":
		p.moveCursor(1)
	case "ctrl+u", "pgup", "ctrl+b":
		p.moveCursorBy(-p.viewport.Height / 2)
	case "ctrl+d", "pgdown", "ctrl+f":
		p.moveCursorBy(p.viewport.Height / 2)
	case "g":
		if time.Since(p.lastGPress) < 500*time.Millisecond {
			p.lastGPress = time.Time{}
			p.setCursor(p.firstSelectable())
		} else {
			p.lastGPress = time.Now()
		}
	case "G":
		for i := len(p.items) - 1; i >= 0; i-- {
			if p.items[i].value != "" {
				p.setCursor(i)
				break
			}
		}
	case "enter":
		if it := p.currentItem(); it != nil {
			name := it.value
			return p, func() tea.Msg { return applyThemeMsg{name: name} }
		}
	case "esc":
		p.RevertPreview()
		if idx := p.savedItemIndex(); idx >= 0 {
			p.cursor = idx
			p.updateContent()
		}
	}

	return p, nil
}

// currentItem returns the selectable item under the cursor, or nil.
func (p *ThemesPage) currentItem() *themeItem {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return nil
	}
	if p.items[p.cursor].value == "" {
		return nil
	}
	return &p.items[p.cursor]
}

// moveCursor advances the cursor by one selectable row in the given
// direction, then applies the live preview.
func (p *ThemesPage) moveCursor(dir int) {
	for i := p.cursor + dir; i >= 0 && i < len(p.items); i += dir {
		if p.items[i].value != "" {
			p.setCursor(i)
			return
		}
	}
}

// moveCursorBy moves up to n selectable rows (n may be negative).
func (p *ThemesPage) moveCursorBy(n int) {
	dir := 1
	if n < 0 {
		dir, n = -1, -n
	}
	idx := p.cursor
	for i := p.cursor + dir; i >= 0 && i < len(p.items) && n > 0; i += dir {
		if p.items[i].value != "" {
			idx = i
			n--
		}
	}
	if idx != p.cursor {
		p.setCursor(idx)
	}
}

// setCursor positions the cursor and applies the live in-process preview of
// the highlighted theme. Preview is skipped when GROVE_THEME pins the
// process (the environment always wins).
func (p *ThemesPage) setCursor(idx int) {
	p.cursor = idx
	if it := p.currentItem(); it != nil && !theme.IsPinned() {
		if err := theme.SetTheme(it.value); err == nil {
			p.previewed = normalizeName(it.value) != normalizeName(p.saved)
		}
	}
	p.updateContent()
}

// updateContent re-renders the list rows into the viewport with the
// currently active theme styles and keeps the cursor visible.
func (p *ThemesPage) updateContent() {
	var lines []string
	for i, it := range p.items {
		lines = append(lines, p.renderRow(it, i == p.cursor))
	}
	p.viewport.SetContent(strings.Join(lines, "\n"))

	targetOffset := p.cursor - p.viewport.Height/2
	if targetOffset < 0 {
		targetOffset = 0
	}
	maxOffset := len(p.items) - p.viewport.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if targetOffset > maxOffset {
		targetOffset = maxOffset
	}
	p.viewport.SetYOffset(targetOffset)
}

// renderRow renders one list row.
func (p *ThemesPage) renderRow(it themeItem, isSelected bool) string {
	if it.value == "" {
		return ""
	}
	t := theme.DefaultTheme

	cursor := "  "
	if isSelected {
		cursor = t.Highlight.Render(theme.IconArrowRightBold) + " "
	}

	indent := ""
	if it.variant {
		indent = "  "
	}

	label := it.label
	switch {
	case isSelected:
		label = t.Bold.Render(label)
	case it.family:
		label = t.Normal.Render(label)
	default:
		label = t.Muted.Render(label)
	}

	marker := ""
	if p.isSavedItem(it) {
		marker = " " + t.Success.Render("●")
	} else if it.variant && it.meta.Default {
		marker = " " + t.Muted.Render("✦")
	}

	return cursor + indent + label + marker
}

// isSavedItem reports whether this row is the persisted theme.
func (p *ThemesPage) isSavedItem(it themeItem) bool {
	return it.value != "" && normalizeName(it.value) == normalizeName(p.saved)
}

// View implements pager.Page: list column | preview pane, with a footer of
// key hints pinned below.
func (p *ThemesPage) View() string {
	list := p.viewport.View()

	previewWidth := p.width - p.listWidth() - 3
	if previewWidth < 20 {
		previewWidth = 20
	}
	preview := p.renderPreview(previewWidth)

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(p.listWidth()).Render(list),
		"   ",
		preview,
	)

	return lipgloss.JoinVertical(lipgloss.Left, body, p.renderThemesFooter())
}

func (p *ThemesPage) renderThemesFooter() string {
	t := theme.DefaultTheme
	hints := "enter: apply & save • esc: revert preview • j/k: browse"
	if theme.IsPinned() {
		hints = "enter: save • j/k: browse (preview disabled: GROVE_THEME is set)"
	}
	return lipgloss.NewStyle().PaddingTop(1).Render(t.Muted.Render(hints))
}

// renderPreview renders the swatch/preview pane for the highlighted theme
// using the palette's actual colors (independent of the active theme).
func (p *ThemesPage) renderPreview(width int) string {
	t := theme.DefaultTheme
	it := p.currentItem()
	if it == nil {
		return t.Muted.Render("No theme selected")
	}

	pal, ok := theme.Lookup(it.value)
	if !ok {
		return t.Muted.Render(fmt.Sprintf("No palette data for %q", it.value))
	}
	c := pal.Colors

	var lines []string

	// Header: name + appearance, provenance.
	header := t.Bold.Render(it.value)
	badge := t.Muted.Render(" · " + pal.Meta.Appearance)
	if it.family && it.value == pal.Meta.Family && pal.Meta.Name != pal.Meta.Family {
		badge = t.Muted.Render(" · adapts to terminal background")
	}
	lines = append(lines, header+badge)
	if pal.Meta.Author != "" || pal.Meta.License != "" {
		provenance := strings.TrimSpace(strings.Join(nonEmpty(pal.Meta.Author, pal.Meta.License), " · "))
		lines = append(lines, t.Muted.Render(provenance))
	}
	if pal.Meta.Upstream != "" {
		lines = append(lines, t.Path.Render(pal.Meta.Upstream))
	}
	lines = append(lines, "")

	// Pinned notice.
	if theme.IsPinned() {
		lines = append(lines,
			t.Warning.Render(fmt.Sprintf("GROVE_THEME=%s pins this process — live preview disabled.", os.Getenv("GROVE_THEME"))),
			t.Muted.Render("Saving still updates the config for new sessions."),
			"")
	}

	// Accent swatches.
	lines = append(lines, t.Muted.Render("Accents"))
	accents := []struct{ name, val string }{
		{"red", c.Red},
		{"green", c.Green},
		{"yellow", c.Yellow},
		{"blue", c.Blue},
		{"magenta", c.Magenta},
		{"cyan", c.Cyan},
		{"orange", c.Orange},
		{"purple", c.Purple},
	}
	lines = append(lines, swatchRows(accents, 4)...)
	lines = append(lines, "")

	// Surface swatches (backgrounds shown as filled blocks).
	lines = append(lines, t.Muted.Render("Surfaces"))
	surfaces := []struct{ name, val string }{
		{"bg", c.Bg}, {"bg_dark", c.BgDark}, {"bg_highlight", c.BgHighlight}, {"bg_visual", c.BgVisual},
	}
	var surfaceParts []string
	for _, s := range surfaces {
		block := lipgloss.NewStyle().Background(lipgloss.Color(s.val)).Render("    ")
		surfaceParts = append(surfaceParts, fmt.Sprintf("%s %s", block, t.Muted.Render(s.name)))
	}
	lines = append(lines, "  "+strings.Join(surfaceParts, "  "))
	lines = append(lines, "")

	// Text, git, and diagnostic roles rendered in their own colors.
	fgSample := func(name, val string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(val)).Render(name)
	}
	lines = append(lines, t.Muted.Render("Text")+"      "+
		fgSample("fg", c.Fg)+"  "+fgSample("fg_dark", c.FgDark)+"  "+
		fgSample("comment", c.Comment)+"  "+fgSample("border", c.Border))
	lines = append(lines, t.Muted.Render("Git")+"       "+
		fgSample("+ add", c.Git.Add)+"  "+fgSample("~ change", c.Git.Change)+"  "+fgSample("- delete", c.Git.Delete))
	lines = append(lines, t.Muted.Render("Diag")+"      "+
		fgSample("✗ error", c.Diagnostics.Error)+"  "+fgSample("⚠ warn", c.Diagnostics.Warning)+"  "+
		fgSample("ℹ info", c.Diagnostics.Info)+"  "+fgSample("✱ hint", c.Diagnostics.Hint))
	lines = append(lines, "")

	// Sample UI chrome rendered entirely with the highlighted palette.
	lines = append(lines, p.renderSampleChrome(pal, width))

	return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// swatchRows lays out fg-colored block swatches with labels, n per row.
func swatchRows(colors []struct{ name, val string }, perRow int) []string {
	t := theme.DefaultTheme
	var rows []string
	for start := 0; start < len(colors); start += perRow {
		end := start + perRow
		if end > len(colors) {
			end = len(colors)
		}
		var parts []string
		for _, sw := range colors[start:end] {
			block := lipgloss.NewStyle().Foreground(lipgloss.Color(sw.val)).Render("██")
			parts = append(parts, fmt.Sprintf("%s %s", block, t.Muted.Render(fmt.Sprintf("%-8s", sw.name))))
		}
		rows = append(rows, "  "+strings.Join(parts, " "))
	}
	return rows
}

// renderSampleChrome renders a small mock panel (border, title, selected
// row, muted row, status and git accents) using the palette's own colors so
// the user sees real UI chrome, not just swatches.
func (p *ThemesPage) renderSampleChrome(pal theme.Palette, width int) string {
	c := pal.Colors
	bg := lipgloss.Color(c.Bg)

	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}
	if innerW > 46 {
		innerW = 46
	}

	line := func(content string) string {
		return lipgloss.NewStyle().Background(bg).Width(innerW).Render(content)
	}
	on := func(val string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(val)).Background(bg)
	}

	title := line(on(c.Fg).Bold(true).Render(" Sample Panel ") + on(c.Comment).Render("· grove"))
	selected := lipgloss.NewStyle().
		Foreground(lipgloss.Color(c.Fg)).
		Background(lipgloss.Color(c.BgVisual)).
		Width(innerW).
		Render(" " + theme.IconArrowRightBold + " selected row")
	normal := line(on(c.Fg).Render("   normal row"))
	muted := line(on(c.Comment).Render("   muted row"))
	status := line(" " +
		on(c.Green).Render("✓ pass") + on(c.Fg).Render("  ") +
		on(c.Yellow).Render("⚠ warn") + on(c.Fg).Render("  ") +
		on(c.Red).Render("✗ fail"))
	git := line(" " +
		on(c.Git.Add).Render("+12") + on(c.Fg).Render(" ") +
		on(c.Git.Change).Render("~4") + on(c.Fg).Render(" ") +
		on(c.Git.Delete).Render("-3") + on(c.Fg).Render("  ") +
		on(c.Blue).Render(" main"))

	body := lipgloss.JoinVertical(lipgloss.Left, title, selected, normal, muted, status, git)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(c.Border)).
		Render(body)
}

// nonEmpty filters empty strings.
func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
