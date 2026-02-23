package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
)

// keysTUIKeyMap defines the keybindings for the keys browser TUI.
type keysTUIKeyMap struct {
	keymap.Base
	EditConfig    key.Binding
	ToggleView    key.Binding
	ScrollTUILeft key.Binding
	ScrollTUIRight key.Binding
}

func (k keysTUIKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Search, k.NextTab, k.PrevTab, k.ToggleView, k.Quit}
}

func (k keysTUIKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Search, k.NextTab, k.PrevTab, k.ToggleView, k.EditConfig},
		{k.ScrollTUILeft, k.ScrollTUIRight},
		{k.Help, k.Quit},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
func (k keysTUIKeyMap) Sections() []keymap.Section {
	return []keymap.Section{
		keymap.NavigationSection(k.Up, k.Down, k.PageUp, k.PageDown, k.NextTab, k.PrevTab),
		keymap.ActionsSection(k.Search, k.ToggleView, k.EditConfig),
		keymap.NewSectionWithIcon("Matrix View", theme.IconViewDashboard,
			k.ScrollTUILeft, k.ScrollTUIRight,
		),
		k.Base.SystemSection(),
	}
}

// newKeysTUIKeyMap creates a new keysTUIKeyMap with user configuration applied.
// Base bindings (navigation, actions, search, selection) come from keymap.Load().
// Only TUI-specific bindings are defined here.
func newKeysTUIKeyMap(cfg *config.Config) keysTUIKeyMap {
	km := keysTUIKeyMap{
		Base: keymap.Load(cfg, "grove.keys"),
		EditConfig: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit config"),
		),
		ToggleView: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("v", "toggle view"),
		),
		ScrollTUILeft: key.NewBinding(
			key.WithKeys("h"),
			key.WithHelp("h", "scroll TUIs left"),
		),
		ScrollTUIRight: key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("l", "scroll TUIs right"),
		),
	}

	// Apply TUI-specific overrides from config
	keymap.ApplyTUIOverrides(cfg, "grove", "keys", &km)

	return km
}

// keysModel holds the state for the keys TUI browser.
type keysModel struct {
	cfg       *config.Config
	keys      keysTUIKeyMap
	bindings  []keys.KeyBinding
	conflicts []keys.Conflict
	analysis  keys.AnalysisReport
	matrix    keys.MatrixReport

	domains   []keys.KeyDomain
	activeTab int

	viewMode int // 0 = By Domain/Section, 1 = By Canonical Action, 2 = Matrix

	// Matrix view state
	matrixScrollX int // Horizontal scroll offset for matrix

	searchInput  textinput.Model
	searchActive bool

	vp     viewport.Model
	help   help.Model
	width  int
	height int

	stream <-chan daemon.StateUpdate
}

// configReloadMsg is sent when the config file changes via daemon streaming.
type configReloadMsg struct {
	File string
}

// listenForConfig listens for config reload events from the daemon stream.
func listenForConfig(sub <-chan daemon.StateUpdate) tea.Cmd {
	return func() tea.Msg {
		if sub == nil {
			return nil
		}
		for {
			update, ok := <-sub
			if !ok {
				return nil // Stream closed
			}
			if update.UpdateType == "config_reload" {
				return configReloadMsg{File: update.ConfigFile}
			}
		}
	}
}

// runKeysTUI launches the interactive keybindings browser.
func runKeysTUI() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	bindings, _ := keys.Aggregate(cfg)
	conflicts := keys.DetectConflicts(bindings)
	analysis := keys.Analyze(bindings)
	matrix := keys.BuildMatrix(bindings)

	ti := textinput.New()
	ti.Placeholder = "Search bindings or actions..."
	ti.Prompt = " / "

	// Connect to daemon for config reload events
	client := daemon.New()
	var stream <-chan daemon.StateUpdate
	if client.IsRunning() {
		// Use a detached context for the stream since it lives for the app lifecycle
		stream, _ = client.StreamState(context.Background())
	}

	km := newKeysTUIKeyMap(cfg)
	helpModel := help.New(km)
	helpModel.Title = "Keybindings Browser Help"

	m := keysModel{
		cfg:           cfg,
		keys:          km,
		bindings:      bindings,
		conflicts:     conflicts,
		analysis:      analysis,
		matrix:        matrix,
		domains:       keys.AllDomains(),
		activeTab:     0,
		viewMode:      0,
		matrixScrollX: 0,
		searchInput:   ti,
		vp:            viewport.New(80, 20),
		help:          helpModel,
		stream:        stream,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func (m keysModel) Init() tea.Cmd {
	if m.stream != nil {
		return listenForConfig(m.stream)
	}
	return nil
}

func (m keysModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case configReloadMsg:
		// Reload configuration and refresh data
		if newCfg, err := config.LoadDefault(); err == nil {
			m.cfg = newCfg
			m.bindings, _ = keys.Aggregate(m.cfg)
			m.conflicts = keys.DetectConflicts(m.bindings)
			m.analysis = keys.Analyze(m.bindings)
			m.matrix = keys.BuildMatrix(m.bindings)
			m.updateViewport()
		}
		// Re-subscribe to the next event
		return m, listenForConfig(m.stream)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp.Width = msg.Width
		m.vp.Height = msg.Height - 6 // Reserve room for tabs, search, and footer
		m.help.SetSize(msg.Width, msg.Height)
		m.updateViewport()
		return m, nil

	case tea.KeyMsg:
		// Handle help view first
		if m.help.ShowAll {
			if key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
				m.help.Toggle()
				return m, nil
			}
			// Let help handle scrolling
			m.help, cmd = m.help.Update(msg)
			return m, cmd
		}

		if m.searchActive {
			if key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Back) {
				m.searchActive = false
				m.searchInput.Blur()
				m.updateViewport()
			} else {
				m.searchInput, cmd = m.searchInput.Update(msg)
				m.updateViewport()
			}
			return m, cmd
		}

		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}

		if key.Matches(msg, m.keys.Help) {
			m.help.Toggle()
			return m, nil
		}

		if key.Matches(msg, m.keys.Search) {
			m.searchActive = true
			m.searchInput.Focus()
			return m, textinput.Blink
		}

		// Toggle view mode with 'v'
		if key.Matches(msg, m.keys.ToggleView) {
			m.viewMode = (m.viewMode + 1) % 3
			m.matrixScrollX = 0 // Reset horizontal scroll when switching views
			m.vp.GotoTop()
			m.updateViewport()
			return m, nil
		}

		// Horizontal scroll for matrix view with h/l (check BEFORE tab navigation)
		if m.viewMode == 2 {
			if key.Matches(msg, m.keys.ScrollTUIRight) || msg.String() == "right" {
				m.matrixScrollX++
				m.updateViewport()
				return m, nil
			}
			if key.Matches(msg, m.keys.ScrollTUILeft) || msg.String() == "left" {
				if m.matrixScrollX > 0 {
					m.matrixScrollX--
				}
				m.updateViewport()
				return m, nil
			}
		}

		// Tab navigation (not in matrix view where h/l scroll TUIs)
		if key.Matches(msg, m.keys.NextTab) || key.Matches(msg, m.keys.Right) {
			m.activeTab = (m.activeTab + 1) % len(m.domains)
			m.vp.GotoTop()
			m.updateViewport()
			return m, nil
		}

		if key.Matches(msg, m.keys.PrevTab) || key.Matches(msg, m.keys.Left) {
			m.activeTab--
			if m.activeTab < 0 {
				m.activeTab = len(m.domains) - 1
			}
			m.vp.GotoTop()
			m.updateViewport()
			return m, nil
		}

		// Edit configuration with 'e' when on TMUX tab
		if key.Matches(msg, m.keys.EditConfig) && m.domains[m.activeTab] == keys.DomainTmux {
			home, _ := os.UserHomeDir()
			configPath := filepath.Join(home, ".config", "grove", "grove.toml")

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vim"
			}

			cmd := exec.Command(editor, configPath)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				// If daemon is running, ConfigWatcher handles regeneration automatically.
				// Otherwise, fall back to manual regeneration.
				if err == nil && m.stream == nil {
					_ = exec.Command("grove", "keys", "generate", "tmux").Run()
					// Emit manual reload message since daemon isn't running
					return configReloadMsg{File: "grove.toml"}
				}
				return nil
			})
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
	searchQuery := strings.ToLower(m.searchInput.Value())

	switch m.viewMode {
	case 1:
		m.renderCanonicalView(&b, searchQuery, t)
	case 2:
		m.renderMatrixView(&b, searchQuery, t)
	default:
		m.renderDomainView(&b, searchQuery, t)
	}

	m.vp.SetContent(b.String())
}

func (m *keysModel) renderCanonicalView(b *strings.Builder, searchQuery string, t *theme.Theme) {
	b.WriteString("\n" + t.Header.Render(" CANONICAL ACTIONS & CONSISTENCY ") + "\n\n")

	for _, canon := range keys.StandardActions {
		if searchQuery != "" && !strings.Contains(strings.ToLower(canon.Name), searchQuery) {
			continue
		}

		res, exists := m.analysis.Consistency[canon.Name]
		if !exists {
			continue
		}

		b.WriteString(fmt.Sprintf("  %s (Standard: %s)\n",
			t.Bold.Render(canon.Name),
			t.Highlight.Render(strings.Join(res.CanonicalKeys, ", "))))

		// Show TUIs that have this action
		for tui, ks := range res.TUIs {
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

			status := t.Success.Render("✓")
			if !match {
				status = t.Warning.Render("← differs")
			}
			// Extract short TUI name
			shortTUI := strings.Split(tui, " ")[0]
			b.WriteString(fmt.Sprintf("    %-20s %-10s %s\n", shortTUI, strings.Join(ks, ", "), status))
		}

		// Show TUIs that don't have this action (subtle indicator)
		for _, tui := range m.matrix.TUINames {
			if tui == "tmux-popup" {
				continue // Skip tmux bindings
			}
			if _, hasAction := res.TUIs[tui]; !hasAction {
				shortTUI := strings.Split(tui, " ")[0]
				// Pad before styling to maintain alignment
				b.WriteString(fmt.Sprintf("    %-20s %s %s\n", shortTUI, t.Muted.Render(fmt.Sprintf("%-10s", "-")), t.Muted.Render(theme.IconUnselect)))
			}
		}
		b.WriteString("\n")
	}

	if len(m.analysis.SemanticConflicts) > 0 {
		b.WriteString("\n" + t.Header.Render(" SEMANTIC CONFLICTS ") + "\n\n")
		for _, sc := range m.analysis.SemanticConflicts {
			if searchQuery != "" && !strings.Contains(strings.ToLower(sc.Key), searchQuery) {
				continue
			}
			b.WriteString(fmt.Sprintf("  Key [%s] has different meanings:\n", t.Error.Render(sc.Key)))
			for tui, meaning := range sc.Meanings {
				shortTUI := strings.Split(tui, " ")[0]
				b.WriteString(fmt.Sprintf("    %-20s %q\n", shortTUI, meaning))
			}
			b.WriteString("\n")
		}
	}
}

func (m *keysModel) renderMatrixView(b *strings.Builder, searchQuery string, t *theme.Theme) {
	b.WriteString("\n" + t.Header.Render(" KEY × TUI MATRIX ") + "\n\n")

	if len(m.matrix.Rows) == 0 {
		b.WriteString("  " + t.Muted.Render("No keybindings found.") + "\n")
		return
	}

	// Show all TUI names in a compact list at the top
	b.WriteString("  " + t.Bold.Render(fmt.Sprintf("TUIs (%d): ", len(m.matrix.TUINames))))
	var tuiList []string
	for _, tui := range m.matrix.TUINames {
		shortName := strings.Split(tui, " ")[0]
		tuiList = append(tuiList, shortName)
	}
	b.WriteString(t.Muted.Render(strings.Join(tuiList, ", ")) + "\n\n")

	// Column widths - narrower to fit more TUIs
	keyColWidth := 10
	tuiColWidth := 14

	// Visible TUIs based on scroll position
	visibleTUIs := m.matrix.TUINames
	startTUI := m.matrixScrollX
	if startTUI >= len(visibleTUIs) {
		startTUI = 0
		m.matrixScrollX = 0
	}

	// Calculate how many TUIs we can show
	availableWidth := m.width - keyColWidth - 15 // Reserve space for key column and status
	maxTUIs := availableWidth / tuiColWidth
	if maxTUIs < 1 {
		maxTUIs = 1
	}
	endTUI := startTUI + maxTUIs
	if endTUI > len(visibleTUIs) {
		endTUI = len(visibleTUIs)
	}
	visibleTUIs = visibleTUIs[startTUI:endTUI]

	// Header row
	header := fmt.Sprintf("  %-*s", keyColWidth, "KEY")
	for _, tui := range visibleTUIs {
		shortName := strings.Split(tui, " ")[0]
		if len(shortName) > tuiColWidth-2 {
			shortName = shortName[:tuiColWidth-3] + "…"
		}
		header += fmt.Sprintf("%-*s", tuiColWidth, shortName)
	}
	header += "STATUS"
	b.WriteString(t.Bold.Render(header) + "\n")

	// Separator
	sep := "  " + strings.Repeat("─", keyColWidth)
	for range visibleTUIs {
		sep += strings.Repeat("─", tuiColWidth)
	}
	sep += strings.Repeat("─", 12)
	b.WriteString(t.Muted.Render(sep) + "\n")

	// Data rows
	for _, row := range m.matrix.Rows {
		// Filter by search
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

		// Key column
		keyStr := row.Key
		if len(keyStr) > keyColWidth-1 {
			keyStr = keyStr[:keyColWidth-2] + "…"
		}
		line := fmt.Sprintf("  %s", t.Highlight.Render(fmt.Sprintf("%-*s", keyColWidth, keyStr)))

		// TUI columns
		for _, tui := range visibleTUIs {
			action := "-"
			if a, ok := row.TUIs[tui]; ok {
				action = a
				if len(action) > tuiColWidth-1 {
					action = action[:tuiColWidth-2] + "…"
				}
			}
			line += fmt.Sprintf("%-*s", tuiColWidth, action)
		}

		// Status column
		var status string
		if !row.Consistent {
			status = t.Warning.Render("⚠ CONFLICT")
		} else if len(row.TUIs) == 1 {
			status = t.Muted.Render("TUI-only")
		} else {
			status = t.Success.Render("✓")
		}
		line += status

		b.WriteString(line + "\n")
	}

	// Scroll indicator
	if len(m.matrix.TUINames) > maxTUIs {
		scrollInfo := fmt.Sprintf("\n  %s %s %d-%d of %d %s",
			t.Highlight.Render("◀"),
			t.Bold.Render("Showing TUIs"),
			startTUI+1, endTUI, len(m.matrix.TUINames),
			t.Highlight.Render("▶"))
		b.WriteString(scrollInfo + "\n")
		b.WriteString("  " + t.Muted.Render("Press h/l or ←/→ to scroll through all TUIs") + "\n")
	}
}

func (m *keysModel) renderDomainView(b *strings.Builder, searchQuery string, t *theme.Theme) {
	domain := m.domains[m.activeTab]

	if domain == keys.DomainTmux {
		var keysExt keys.KeysExtension
		if m.cfg != nil {
			_ = m.cfg.UnmarshalExtension("keys", &keysExt)
		}
		if keysExt.Tmux.Prefix != "" {
			b.WriteString("\n  " + t.Muted.Render(fmt.Sprintf("Active Prefix: %s (Table: grove-popups)", keysExt.Tmux.Prefix)) + "\n")
		}
	}

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
}

func (m keysModel) View() string {
	// Show help overlay if active
	if m.help.ShowAll {
		return m.help.View()
	}

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
		var viewLabel string
		switch m.viewMode {
		case 1:
			viewLabel = "Canonical"
		case 2:
			viewLabel = "Matrix"
		default:
			viewLabel = "Domain"
		}

		var helpText string
		if m.viewMode == 2 {
			helpText = fmt.Sprintf(" / to search • h/l to scroll TUIs • v to toggle view (%s) • q to quit", viewLabel)
		} else if m.domains[m.activeTab] == keys.DomainTmux {
			helpText = fmt.Sprintf(" / to search • ]/[ to switch domains • e to edit • v to toggle view (%s) • q to quit", viewLabel)
		} else {
			helpText = fmt.Sprintf(" / to search • ]/[ to switch domains • v to toggle view (%s) • q to quit", viewLabel)
		}
		s.WriteString(t.Muted.Render(helpText + "\n"))
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
