package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
)

// keysModel holds the state for the keys TUI browser.
type keysModel struct {
	cfg       *config.Config
	baseKeys  keymap.Base
	bindings  []keys.KeyBinding
	conflicts []keys.Conflict

	domains   []keys.KeyDomain
	activeTab int

	searchInput  textinput.Model
	searchActive bool

	vp     viewport.Model
	width  int
	height int
}

// runKeysTUI launches the interactive keybindings browser.
func runKeysTUI() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	bindings, _ := keys.Aggregate(cfg)
	conflicts := keys.DetectConflicts(bindings)

	ti := textinput.New()
	ti.Placeholder = "Search bindings or actions..."
	ti.Prompt = " / "

	m := keysModel{
		cfg:         cfg,
		baseKeys:    keymap.Load(cfg, ""),
		bindings:    bindings,
		conflicts:   conflicts,
		domains:     keys.AllDomains(),
		activeTab:   0,
		searchInput: ti,
		vp:          viewport.New(80, 20),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func (m keysModel) Init() tea.Cmd {
	return nil
}

func (m keysModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp.Width = msg.Width
		m.vp.Height = msg.Height - 6 // Reserve room for tabs, search, and footer
		m.updateViewport()
		return m, nil

	case tea.KeyMsg:
		if m.searchActive {
			if key.Matches(msg, m.baseKeys.Confirm) || key.Matches(msg, m.baseKeys.Back) {
				m.searchActive = false
				m.searchInput.Blur()
				m.updateViewport()
			} else {
				m.searchInput, cmd = m.searchInput.Update(msg)
				m.updateViewport()
			}
			return m, cmd
		}

		if key.Matches(msg, m.baseKeys.Quit) {
			return m, tea.Quit
		}

		if key.Matches(msg, m.baseKeys.Search) {
			m.searchActive = true
			m.searchInput.Focus()
			return m, textinput.Blink
		}

		if key.Matches(msg, m.baseKeys.NextTab) || key.Matches(msg, m.baseKeys.Right) {
			m.activeTab = (m.activeTab + 1) % len(m.domains)
			m.vp.GotoTop()
			m.updateViewport()
			return m, nil
		}

		if key.Matches(msg, m.baseKeys.PrevTab) || key.Matches(msg, m.baseKeys.Left) {
			m.activeTab--
			if m.activeTab < 0 {
				m.activeTab = len(m.domains) - 1
			}
			m.vp.GotoTop()
			m.updateViewport()
			return m, nil
		}

		// Let viewport handle scrolling
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *keysModel) updateViewport() {
	var b strings.Builder
	t := theme.DefaultTheme

	domain := m.domains[m.activeTab]
	searchQuery := strings.ToLower(m.searchInput.Value())

	// Group by section
	sections := make(map[string][]keys.KeyBinding)
	var orderedSections []string

	for _, bind := range m.bindings {
		if bind.Domain != domain {
			continue
		}

		// Filter by search query
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

				// Determine if this key conflicts
				isConflict := false
				for _, c := range m.conflicts {
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

				// Format key display
				keyStyle := t.Highlight
				keyStr := keyStyle.Render(keyCombo)
				if isConflict {
					keyStr = t.Error.Bold(true).Render(keyCombo + " (CONFLICT)")
				}

				// Format action/description
				actionStr := bind.Action
				if bind.Description != "" && bind.Description != bind.Action {
					actionStr = bind.Description
				}

				b.WriteString(fmt.Sprintf("   %-25s  %s\n", keyStr, t.Muted.Render(actionStr)))
			}
		}
	}

	m.vp.SetContent(b.String())
}

func (m keysModel) View() string {
	t := theme.DefaultTheme
	var s strings.Builder

	// Header
	s.WriteString("\n " + t.Header.Render(theme.IconGear+" Grove Keybindings Browser") + "\n\n")

	// Tabs
	var tabs []string
	for i, dom := range m.domains {
		name := strings.ToUpper(dom.String())

		// Add conflict indicator
		conflictCount := keys.CountConflicts(m.conflicts, dom)
		if conflictCount > 0 {
			name = fmt.Sprintf("%s (%d)", name, conflictCount)
		}

		if i == m.activeTab {
			tabs = append(tabs, t.Selected.Render(fmt.Sprintf(" %s ", name)))
		} else {
			tabs = append(tabs, t.Muted.Render(fmt.Sprintf(" %s ", name)))
		}
	}
	s.WriteString(" " + strings.Join(tabs, " │ ") + "\n")

	// Search bar
	if m.searchActive || m.searchInput.Value() != "" {
		s.WriteString(m.searchInput.View() + "\n")
	} else {
		s.WriteString(t.Muted.Render(" / to search • ]/[ or h/l to switch domains • j/k to scroll • q to quit\n"))
	}

	// Border
	borderWidth := m.width
	if borderWidth <= 0 {
		borderWidth = 80
	}
	s.WriteString(t.Muted.Render(strings.Repeat("─", borderWidth)) + "\n")

	// Viewport
	s.WriteString(m.vp.View())

	// Footer with conflict summary
	conflictSummary := ""
	totalConflicts := len(m.conflicts)
	if totalConflicts > 0 {
		conflictSummary = t.Error.Render(fmt.Sprintf(" %d conflict(s) detected", totalConflicts))
	} else {
		conflictSummary = t.Success.Render(" No conflicts")
	}

	// Add padding style
	marginStyle := lipgloss.NewStyle().Padding(0, 1)

	return marginStyle.Render(s.String() + "\n" + conflictSummary)
}
