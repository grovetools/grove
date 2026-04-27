// Package keys provides an embeddable TUI for browsing Grove keybindings.
// It renders three pager.Page tabs (Domain, Canonical, Matrix) and handles
// search, help, and live config-reload via the daemon SSE stream.
package keys

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"

	pkgkeys "github.com/grovetools/grove/pkg/keys"
)

// keyMap defines the keybindings for the keys browser TUI.
type keyMap struct {
	keymap.Base
	EditConfig     key.Binding
	ScrollTUILeft  key.Binding
	ScrollTUIRight key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Search, k.NextTab, k.PrevTab, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Search, k.NextTab, k.PrevTab, k.EditConfig},
		{k.ScrollTUILeft, k.ScrollTUIRight},
		{k.Help, k.Quit},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
func (k keyMap) Sections() []keymap.Section {
	return []keymap.Section{
		keymap.NavigationSection(k.Up, k.Down, k.PageUp, k.PageDown, k.NextTab, k.PrevTab),
		keymap.ActionsSection(k.Search, k.EditConfig),
		keymap.NewSectionWithIcon("Matrix View", theme.IconViewDashboard,
			k.ScrollTUILeft, k.ScrollTUIRight,
		),
		k.Base.SystemSection(),
	}
}

// NewKeyMap creates a keyMap for the keys TUI with user config applied.
// Exported so the keymap registry generator can introspect bindings.
func NewKeyMap(cfg *config.Config) keyMap {
	return newKeyMap(cfg)
}

func newKeyMap(cfg *config.Config) keyMap {
	km := keyMap{
		Base: keymap.Load(cfg, "grove.keys"),
		EditConfig: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit config"),
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

	// Apply TUI-specific overrides from config.
	keymap.ApplyTUIOverrides(cfg, "grove", "keys", &km)

	return km
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
				return nil // Stream closed.
			}
			if update.UpdateType == "config_reload" {
				return configReloadMsg{File: update.ConfigFile}
			}
		}
	}
}

// Model is the top-level embeddable TUI model for the keybindings browser.
type Model struct {
	pager pager.Model
	cfg   *config.Config
	keys  keyMap

	// Shared data accessed by pages via pointer.
	bindings  []pkgkeys.KeyBinding
	conflicts []pkgkeys.Conflict
	analysis  pkgkeys.AnalysisReport
	matrix    pkgkeys.MatrixReport

	// Domain view state (shared with DomainPage).
	domains   []pkgkeys.KeyDomain
	activeTab int // domain sub-tab

	// Matrix view state (shared with MatrixPage).
	matrixScrollX int

	// Search (model-level, shared across all pages).
	searchInput  textinput.Model
	searchActive bool

	// Help overlay.
	help help.Model

	// Daemon stream for config reload.
	stream <-chan daemon.StateUpdate

	width, height int
}

// New creates a new keys browser Model. The caller should pass the loaded
// config and an optional daemon state stream (nil is fine when the daemon
// is not running).
func New(cfg *config.Config, bindings []pkgkeys.KeyBinding, stream <-chan daemon.StateUpdate) Model {
	conflicts := pkgkeys.DetectConflicts(bindings)
	analysis := pkgkeys.Analyze(bindings)
	matrix := pkgkeys.BuildMatrix(bindings)

	ti := textinput.New()
	ti.Placeholder = "Search bindings or actions..."
	ti.Prompt = " / "

	km := newKeyMap(cfg)
	helpModel := help.New(km)
	helpModel.Title = "Keybindings Browser Help"

	m := Model{
		cfg:           cfg,
		keys:          km,
		bindings:      bindings,
		conflicts:     conflicts,
		analysis:      analysis,
		matrix:        matrix,
		domains:       pkgkeys.AllDomains(),
		activeTab:     0,
		matrixScrollX: 0,
		searchInput:   ti,
		help:          helpModel,
		stream:        stream,
	}

	// Build the three pager pages. Each page holds a pointer to the model
	// so it can read shared state (bindings, search query, etc.).
	pages := []pager.Page{
		newDomainPage(&m),
		newCanonicalPage(&m),
		newMatrixPage(&m),
	}

	pagerKeys := pager.KeyMapFromBase(km.Base)
	pgr := pager.NewWith(pages, pagerKeys, pager.Config{
		OuterPadding: [4]int{1, 2, 0, 2},
		ShowTitleRow: true,
		FooterHeight: 2, // search/hint line + conflict summary
	})
	m.pager = pgr

	return m
}

// SearchQuery returns the current lowercased search string.
func (m *Model) SearchQuery() string {
	return strings.ToLower(m.searchInput.Value())
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.pager.Init()}
	if m.stream != nil {
		cmds = append(cmds, listenForConfig(m.stream))
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case configReloadMsg:
		// Reload configuration and refresh data.
		if newCfg, err := config.LoadDefault(); err == nil {
			m.cfg = newCfg
			m.bindings, _ = pkgkeys.Aggregate(m.cfg)
			m.conflicts = pkgkeys.DetectConflicts(m.bindings)
			m.analysis = pkgkeys.Analyze(m.bindings)
			m.matrix = pkgkeys.BuildMatrix(m.bindings)
		}
		return m, listenForConfig(m.stream)

	case embed.EditFinishedMsg:
		// After editor closes, if daemon isn't running, reload manually.
		if msg.Err == nil && m.stream == nil {
			if newCfg, err := config.LoadDefault(); err == nil {
				m.cfg = newCfg
				m.bindings, _ = pkgkeys.Aggregate(m.cfg)
				m.conflicts = pkgkeys.DetectConflicts(m.bindings)
				m.analysis = pkgkeys.Analyze(m.bindings)
				m.matrix = pkgkeys.BuildMatrix(m.bindings)
			}
		}
		return m, nil

	case embed.FocusMsg, embed.BlurMsg:
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd

	case embed.SetWorkspaceMsg:
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetSize(msg.Width, msg.Height)

		// Let the pager handle sizing for all pages.
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		// Help overlay intercepts all keys when active.
		if m.help.ShowAll {
			if key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
				m.help.Toggle()
				return m, nil
			}
			m.help, cmd = m.help.Update(msg)
			return m, cmd
		}

		// Search mode intercepts all keys when active.
		if m.searchActive {
			if key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Back) {
				m.searchActive = false
				m.searchInput.Blur()
			} else {
				m.searchInput, cmd = m.searchInput.Update(msg)
			}
			return m, cmd
		}

		// Quit -> embed.CloseRequestMsg.
		if key.Matches(msg, m.keys.Quit) {
			return m, func() tea.Msg { return embed.CloseRequestMsg{} }
		}

		// Help toggle.
		if key.Matches(msg, m.keys.Help) {
			m.help.Toggle()
			return m, nil
		}

		// Search activation.
		if key.Matches(msg, m.keys.Search) {
			m.searchActive = true
			m.searchInput.Focus()
			return m, textinput.Blink
		}

		// Edit config when on domain page / tmux tab.
		if key.Matches(msg, m.keys.EditConfig) {
			if m.pager.ActiveIndex() == 0 && m.domains[m.activeTab] == pkgkeys.DomainTmux {
				home, _ := os.UserHomeDir()
				configPath := filepath.Join(home, ".config", "grove", "grove.toml")
				return m, func() tea.Msg { return embed.EditRequestMsg{Path: configPath} }
			}
		}

		// Forward to pager (handles tab switching, then forwards to active page).
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd
	}

	// Forward non-key messages to pager.
	m.pager, cmd = m.pager.Update(msg)
	return m, cmd
}

// View implements tea.Model.
func (m Model) View() string {
	// Help overlay replaces everything.
	if m.help.ShowAll {
		return m.help.View()
	}

	t := theme.DefaultTheme

	// Build footer: search bar or hint line + conflict summary.
	var footerParts []string

	if m.searchActive || m.searchInput.Value() != "" {
		footerParts = append(footerParts, m.searchInput.View())
	} else {
		var helpParts []string
		helpParts = append(helpParts, "/ search")

		// Context-specific hints.
		if m.pager.ActiveIndex() == 2 {
			helpParts = append(helpParts, "h/l scroll TUIs")
		}
		if m.pager.ActiveIndex() == 0 && m.domains[m.activeTab] == pkgkeys.DomainTmux {
			helpParts = append(helpParts, "e edit config")
		}

		footerParts = append(footerParts, t.Muted.Render(strings.Join(helpParts, " "+theme.IconBullet+" ")))
	}

	// Conflict summary.
	totalConflicts := len(m.conflicts)
	if totalConflicts > 0 {
		footerParts = append(footerParts, t.Error.Render(fmt.Sprintf("%d conflict(s) detected", totalConflicts)))
	} else {
		footerParts = append(footerParts, t.Success.Render("No conflicts"))
	}

	m.pager.SetFooter(strings.Join(footerParts, "\n"))

	return m.pager.View()
}
