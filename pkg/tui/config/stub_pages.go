package config

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// stubPage is a placeholder tab for the curated pages (Appearance, Layout,
// Keys) introduced by the pager restructure. Phase 2 of the curated-config
// plan replaces each stub with a real CuratedPage; only Name/TabID/tab
// wiring are load-bearing here.
type stubPage struct {
	name   string
	tabID  string
	body   string
	width  int
	height int
	active bool
}

// Compile-time interface checks.
var (
	_ pager.Page       = (*stubPage)(nil)
	_ pager.PageWithID = (*stubPage)(nil)
)

func newStubPage(name, tabID, body string, width, height int) *stubPage {
	return &stubPage{name: name, tabID: tabID, body: body, width: width, height: height}
}

func (p *stubPage) Name() string  { return p.name }
func (p *stubPage) TabID() string { return p.tabID }

func (p *stubPage) Init() tea.Cmd { return nil }

func (p *stubPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }

func (p *stubPage) View() string {
	msg := theme.DefaultTheme.Muted.Render(p.body)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.MutedText).
		Padding(1, 2).
		Align(lipgloss.Center).
		Render(msg)
	return lipgloss.Place(p.width, p.height-2, lipgloss.Center, lipgloss.Center, box)
}

func (p *stubPage) Focus() tea.Cmd {
	p.active = true
	return nil
}

func (p *stubPage) Blur() { p.active = false }

func (p *stubPage) SetSize(width, height int) {
	p.width = width
	p.height = height
}
