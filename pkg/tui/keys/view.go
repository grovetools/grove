package keys

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	pkgkeys "github.com/grovetools/grove/pkg/keys"

	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// ---------------------------------------------------------------------------
// DomainPage
// ---------------------------------------------------------------------------

// DomainPage shows bindings grouped by domain. It has its own sub-tab
// navigation for TMUX / NAV / NVIM / TUI domains, rendered inside its View().
type DomainPage struct {
	model  *Model
	vp     viewport.Model
	width  int
	height int
}

func newDomainPage(m *Model) *DomainPage {
	return &DomainPage{
		model: m,
		vp:    viewport.New(80, 20),
	}
}

func (p *DomainPage) Name() string  { return "Domain" }
func (p *DomainPage) Title() string { return theme.IconGear + " Keybindings by Domain" }

func (p *DomainPage) Init() tea.Cmd { return nil }

var _ pager.PageWithTitle = (*DomainPage)(nil)

func (p *DomainPage) Focus() tea.Cmd {
	p.refreshContent()
	return nil
}

func (p *DomainPage) Blur() {}

func (p *DomainPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.vp.Width = width
	// Reserve 2 rows for domain sub-tabs.
	p.vp.Height = height - 2
	if p.vp.Height < 1 {
		p.vp.Height = 1
	}
}

func (p *DomainPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Domain sub-tab navigation with ] / [ or Right / Left.
		if key.Matches(msg, p.model.keys.Right) {
			p.model.activeTab = (p.model.activeTab + 1) % len(p.model.domains)
			p.vp.GotoTop()
			p.refreshContent()
			return p, nil
		}
		if key.Matches(msg, p.model.keys.Left) {
			p.model.activeTab--
			if p.model.activeTab < 0 {
				p.model.activeTab = len(p.model.domains) - 1
			}
			p.vp.GotoTop()
			p.refreshContent()
			return p, nil
		}
	case tea.WindowSizeMsg:
		p.SetSize(msg.Width, msg.Height)
		p.refreshContent()
		return p, nil
	}

	// Let viewport handle scrolling.
	p.vp, cmd = p.vp.Update(msg)
	return p, cmd
}

func (p *DomainPage) View() string {
	p.refreshContent()

	t := theme.DefaultTheme

	// Domain sub-tabs.
	var tabs []string
	for i, dom := range p.model.domains {
		name := strings.ToUpper(dom.String())

		conflictCount := pkgkeys.CountConflicts(p.model.conflicts, dom)
		if conflictCount > 0 {
			name = fmt.Sprintf("%s (%d)", name, conflictCount)
		}

		if i == p.model.activeTab {
			tabs = append(tabs, t.Selected.Render(fmt.Sprintf(" %s ", name)))
		} else {
			tabs = append(tabs, t.Muted.Render(fmt.Sprintf(" %s ", name)))
		}
	}
	hint := t.Muted.Render("  \u2190/\u2192 switch domain")
	tabBar := " " + strings.Join(tabs, " \u2502 ") + hint + "\n"

	return tabBar + p.vp.View()
}

func (p *DomainPage) refreshContent() {
	var b strings.Builder
	t := theme.DefaultTheme
	searchQuery := p.model.SearchQuery()
	domain := p.model.domains[p.model.activeTab]

	if domain == pkgkeys.DomainTmux {
		var keysExt pkgkeys.KeysExtension
		if p.model.cfg != nil {
			_ = p.model.cfg.UnmarshalExtension("keys", &keysExt)
		}
		if keysExt.Tmux.Prefix != "" {
			b.WriteString("\n  " + t.Muted.Render(fmt.Sprintf("Active Prefix: %s (Table: grove-popups)", keysExt.Tmux.Prefix)) + "\n")
		}
	}

	// Group by section.
	sections := make(map[string][]pkgkeys.KeyBinding)
	var orderedSections []string

	for _, bind := range p.model.bindings {
		if bind.Domain != domain {
			continue
		}

		if searchQuery != "" {
			match := strings.Contains(strings.ToLower(bind.Action), searchQuery) ||
				strings.Contains(strings.ToLower(bind.Section), searchQuery) ||
				strings.Contains(strings.ToLower(bind.Description), searchQuery)
			for _, k := range bind.Keys {
				if strings.Contains(strings.ToLower(k), searchQuery) {
					match = true
				}
			}
			if !match {
				continue
			}
		}

		if len(sections[bind.Section]) == 0 {
			orderedSections = append(orderedSections, bind.Section)
		}
		sections[bind.Section] = append(sections[bind.Section], bind)
	}

	if len(orderedSections) == 0 {
		b.WriteString("\n  " + t.Muted.Render("No keybindings found in this domain."))
		if searchQuery != "" {
			b.WriteString("\n  " + t.Muted.Render("Try a different search query."))
		}
	} else {
		for _, secName := range orderedSections {
			b.WriteString("\n" + t.Header.Render(fmt.Sprintf(" %s ", secName)) + "\n")
			for _, bind := range sections[secName] {
				keyCombo := strings.Join(bind.Keys, ", ")

				isConflict := false
				for _, c := range p.model.conflicts {
					if c.Domain == bind.Domain {
						for _, ck := range bind.Keys {
							if ck == c.Key {
								isConflict = true
								break
							}
						}
					}
					if isConflict {
						break
					}
				}

				keyStyle := t.Highlight
				keyStr := keyStyle.Render(keyCombo)
				if isConflict {
					keyStr = t.Error.Bold(true).Render(keyCombo + " (CONFLICT)")
				}

				actionStr := bind.Action
				if bind.Description != "" && bind.Description != bind.Action {
					actionStr = bind.Description
				}

				b.WriteString(fmt.Sprintf("   %-25s  %s\n", keyStr, t.Muted.Render(actionStr)))
			}
		}
	}

	p.vp.SetContent(b.String())
}

// ---------------------------------------------------------------------------
// CanonicalPage
// ---------------------------------------------------------------------------

// CanonicalPage shows canonical action consistency analysis.
type CanonicalPage struct {
	model  *Model
	vp     viewport.Model
	width  int
	height int
}

func newCanonicalPage(m *Model) *CanonicalPage {
	return &CanonicalPage{
		model: m,
		vp:    viewport.New(80, 20),
	}
}

func (p *CanonicalPage) Name() string { return "Canonical" }
func (p *CanonicalPage) Title() string {
	return theme.IconViewDashboard + " Canonical Actions & Consistency"
}

func (p *CanonicalPage) Init() tea.Cmd { return nil }

var _ pager.PageWithTitle = (*CanonicalPage)(nil)

func (p *CanonicalPage) Focus() tea.Cmd {
	p.refreshContent()
	return nil
}

func (p *CanonicalPage) Blur() {}

func (p *CanonicalPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.vp.Width = width
	p.vp.Height = height
	if p.vp.Height < 1 {
		p.vp.Height = 1
	}
}

func (p *CanonicalPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	var cmd tea.Cmd
	switch msg.(type) {
	case tea.WindowSizeMsg:
		// Already handled by SetSize.
		p.refreshContent()
		return p, nil
	}
	p.vp, cmd = p.vp.Update(msg)
	return p, cmd
}

func (p *CanonicalPage) View() string {
	p.refreshContent()
	return p.vp.View()
}

func (p *CanonicalPage) refreshContent() {
	var b strings.Builder
	t := theme.DefaultTheme
	searchQuery := p.model.SearchQuery()

	b.WriteString("\n" + t.Header.Render(" CANONICAL ACTIONS & CONSISTENCY ") + "\n\n")

	for _, canon := range pkgkeys.StandardActions {
		if searchQuery != "" && !strings.Contains(strings.ToLower(canon.Name), searchQuery) {
			continue
		}

		res, exists := p.model.analysis.Consistency[canon.Name]
		if !exists {
			continue
		}

		b.WriteString(fmt.Sprintf("  %s (Standard: %s)\n",
			t.Bold.Render(canon.Name),
			t.Highlight.Render(strings.Join(res.CanonicalKeys, ", "))))

		// Sort TUI names for stable rendering.
		tuiNames := make([]string, 0, len(res.TUIs))
		for tui := range res.TUIs {
			tuiNames = append(tuiNames, tui)
		}
		sort.Strings(tuiNames)

		for _, tui := range tuiNames {
			ks := res.TUIs[tui]
			match := false
			for _, k := range ks {
				for _, ck := range res.CanonicalKeys {
					if k == ck {
						match = true
						break
					}
				}
				if match {
					break
				}
			}

			status := t.Success.Render("\u2713")
			if !match {
				status = t.Warning.Render("\u2190 differs")
			}
			shortTUI := strings.Split(tui, " ")[0]
			b.WriteString(fmt.Sprintf("    %-20s %-10s %s\n", shortTUI, strings.Join(ks, ", "), status))
		}

		for _, tui := range p.model.matrix.TUINames {
			if tui == "tmux-popup" {
				continue
			}
			if _, hasAction := res.TUIs[tui]; !hasAction {
				shortTUI := strings.Split(tui, " ")[0]
				b.WriteString(fmt.Sprintf("    %-20s %s %s\n", shortTUI, t.Muted.Render(fmt.Sprintf("%-10s", "-")), t.Muted.Render(theme.IconUnselect)))
			}
		}
		b.WriteString("\n")
	}

	if len(p.model.analysis.SemanticConflicts) > 0 {
		b.WriteString("\n" + t.Header.Render(" SEMANTIC CONFLICTS ") + "\n\n")
		for _, sc := range p.model.analysis.SemanticConflicts {
			if searchQuery != "" && !strings.Contains(strings.ToLower(sc.Key), searchQuery) {
				continue
			}
			b.WriteString(fmt.Sprintf("  Key [%s] has different meanings:\n", t.Error.Render(sc.Key)))
			meaningTUIs := make([]string, 0, len(sc.Meanings))
			for tui := range sc.Meanings {
				meaningTUIs = append(meaningTUIs, tui)
			}
			sort.Strings(meaningTUIs)
			for _, tui := range meaningTUIs {
				shortTUI := strings.Split(tui, " ")[0]
				b.WriteString(fmt.Sprintf("    %-20s %q\n", shortTUI, sc.Meanings[tui]))
			}
			b.WriteString("\n")
		}
	}

	p.vp.SetContent(b.String())
}

// ---------------------------------------------------------------------------
// MatrixPage
// ---------------------------------------------------------------------------

// MatrixPage shows the key x TUI matrix. It implements PageWithTextInput
// so that h/l keys are absorbed for horizontal scrolling and don't trigger
// pager tab cycling.
type MatrixPage struct {
	model  *Model
	vp     viewport.Model
	width  int
	height int
}

func newMatrixPage(m *Model) *MatrixPage {
	return &MatrixPage{
		model: m,
		vp:    viewport.New(80, 20),
	}
}

func (p *MatrixPage) Name() string  { return "Matrix" }
func (p *MatrixPage) Title() string { return theme.IconViewDashboard + " Key x TUI Matrix" }

func (p *MatrixPage) Init() tea.Cmd { return nil }

var _ pager.PageWithTitle = (*MatrixPage)(nil)

func (p *MatrixPage) Focus() tea.Cmd {
	p.refreshContent()
	return nil
}

func (p *MatrixPage) Blur() {}

func (p *MatrixPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.vp.Width = width
	p.vp.Height = height
	if p.vp.Height < 1 {
		p.vp.Height = 1
	}
}

// IsTextEntryActive is intentionally not implemented. The matrix page
// handles h/l scroll in its own Update before the pager sees them, but
// numeric 1-9 tab jumps and [/] cycling must still work.

func (p *MatrixPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Horizontal scroll with h/l or arrow keys.
		if key.Matches(msg, p.model.keys.ScrollTUIRight) || msg.String() == "right" {
			p.model.matrixScrollX++
			p.refreshContent()
			return p, nil
		}
		if key.Matches(msg, p.model.keys.ScrollTUILeft) || msg.String() == "left" {
			if p.model.matrixScrollX > 0 {
				p.model.matrixScrollX--
			}
			p.refreshContent()
			return p, nil
		}
	case tea.WindowSizeMsg:
		p.SetSize(msg.Width, msg.Height)
		p.refreshContent()
		return p, nil
	}

	p.vp, cmd = p.vp.Update(msg)
	return p, cmd
}

func (p *MatrixPage) View() string {
	p.refreshContent()
	return p.vp.View()
}

func (p *MatrixPage) refreshContent() {
	var b strings.Builder
	t := theme.DefaultTheme
	searchQuery := p.model.SearchQuery()

	b.WriteString("\n" + t.Header.Render(" KEY \u00d7 TUI MATRIX ") + "\n\n")

	if len(p.model.matrix.Rows) == 0 {
		b.WriteString("  " + t.Muted.Render("No keybindings found.") + "\n")
		p.vp.SetContent(b.String())
		return
	}

	// Compact TUI list at top.
	b.WriteString("  " + t.Bold.Render(fmt.Sprintf("TUIs (%d): ", len(p.model.matrix.TUINames))))
	var tuiList []string
	for _, tui := range p.model.matrix.TUINames {
		shortName := strings.Split(tui, " ")[0]
		tuiList = append(tuiList, shortName)
	}
	b.WriteString(t.Muted.Render(strings.Join(tuiList, ", ")) + "\n\n")

	keyColWidth := 10
	tuiColWidth := 14

	visibleTUIs := p.model.matrix.TUINames
	startTUI := p.model.matrixScrollX
	if startTUI >= len(visibleTUIs) {
		startTUI = 0
		p.model.matrixScrollX = 0
	}

	availableWidth := p.width - keyColWidth - 15
	maxTUIs := availableWidth / tuiColWidth
	if maxTUIs < 1 {
		maxTUIs = 1
	}
	endTUI := startTUI + maxTUIs
	if endTUI > len(visibleTUIs) {
		endTUI = len(visibleTUIs)
	}
	visibleTUIs = visibleTUIs[startTUI:endTUI]

	// Header row.
	header := fmt.Sprintf("  %-*s", keyColWidth, "KEY")
	for _, tui := range visibleTUIs {
		shortName := strings.Split(tui, " ")[0]
		if len(shortName) > tuiColWidth-2 {
			shortName = shortName[:tuiColWidth-3] + "\u2026"
		}
		header += fmt.Sprintf("%-*s", tuiColWidth, shortName)
	}
	header += "STATUS"
	b.WriteString(t.Bold.Render(header) + "\n")

	// Separator.
	sep := "  " + strings.Repeat("\u2500", keyColWidth)
	for range visibleTUIs {
		sep += strings.Repeat("\u2500", tuiColWidth)
	}
	sep += strings.Repeat("\u2500", 12)
	b.WriteString(t.Muted.Render(sep) + "\n")

	// Data rows.
	for _, row := range p.model.matrix.Rows {
		if searchQuery != "" {
			match := strings.Contains(strings.ToLower(row.Key), searchQuery)
			for _, action := range row.TUIs {
				if strings.Contains(strings.ToLower(action), searchQuery) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		keyStr := row.Key
		if len(keyStr) > keyColWidth-1 {
			keyStr = keyStr[:keyColWidth-2] + "\u2026"
		}
		line := fmt.Sprintf("  %s", t.Highlight.Render(fmt.Sprintf("%-*s", keyColWidth, keyStr)))

		for _, tui := range visibleTUIs {
			action := "-"
			if a, ok := row.TUIs[tui]; ok {
				action = a
				if len(action) > tuiColWidth-1 {
					action = action[:tuiColWidth-2] + "\u2026"
				}
			}
			line += fmt.Sprintf("%-*s", tuiColWidth, action)
		}

		var status string
		if !row.Consistent {
			status = t.Warning.Render("\u26a0 CONFLICT")
		} else if len(row.TUIs) == 1 {
			status = t.Muted.Render("TUI-only")
		} else {
			status = t.Success.Render("\u2713")
		}
		line += status
		b.WriteString(line + "\n")
	}

	// Scroll indicator.
	if len(p.model.matrix.TUINames) > maxTUIs {
		scrollInfo := fmt.Sprintf("\n  %s %s %d-%d of %d %s",
			t.Highlight.Render("\u25c0"),
			t.Bold.Render("Showing TUIs"),
			startTUI+1, endTUI, len(p.model.matrix.TUINames),
			t.Highlight.Render("\u25b6"))
		b.WriteString(scrollInfo + "\n")
		b.WriteString("  " + t.Muted.Render("Press h/l or \u2190/\u2192 to scroll through all TUIs") + "\n")
	}

	p.vp.SetContent(b.String())
}
