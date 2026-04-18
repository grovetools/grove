package env

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
)

// ecosystemHeaderRows is the vertical space the EcosystemModel claims for
// its header + subtitle lines before forwarding the remainder to the pager.
const ecosystemHeaderRows = 2

// EcosystemConfig mirrors Config for the ecosystem-scoped panel. Passing a
// different type keeps the two construction paths explicit — the TUI entry
// in grove/cmd/env_tui.go picks between New and NewEcosystem based on the
// workspace kind, and we want the compiler to confirm each site passed the
// right shape of data.
type EcosystemConfig struct {
	DaemonClient daemon.Client
	Root         *workspace.WorkspaceNode
	Cfg          *config.Config
	Hosted       bool
}

// EcosystemModel is the scaffold for the ecosystem-scoped env TUI introduced
// in Phase 5d. The real Deployments / Shared Infra / Profiles / Orphans /
// Actions pages are deferred to 5e; this model wires up the pager, workspace
// repoint messaging, and keyboard quit path so 5e can focus on rendering.
type EcosystemModel struct {
	pager pager.Model

	daemonClient daemon.Client
	workspace    *workspace.WorkspaceNode
	worktrees    []WorktreeState
	cfg          *config.Config
	hosted       bool
	focused      bool

	width  int
	height int
}

// NewEcosystem constructs the ecosystem env panel. Worktree enumeration runs
// synchronously because file I/O for a handful of state.json files is <5ms
// on local disk; if this becomes a problem with large ecosystems we'll move
// the seed read into a tea.Cmd.
func NewEcosystem(cfg EcosystemConfig) EcosystemModel {
	if cfg.DaemonClient == nil {
		cfg.DaemonClient = daemon.NewWithAutoStart()
	}
	if cfg.Cfg == nil {
		cfg.Cfg, _ = config.LoadDefault()
	}

	base := keymap.NewBase()
	if cfg.Cfg != nil {
		base = keymap.Load(cfg.Cfg, "env")
	}
	keys := pager.KeyMapFromBase(base)

	pages := []pager.Page{
		newEcosystemPlaceholder("Deployments", "deployments", "Deployments matrix · coming in 5e"),
		newEcosystemPlaceholder("Shared Infra", "shared", "Shared-infra profile detail · coming in 5e"),
		newEcosystemPlaceholder("Profiles", "profiles", "Per-profile catalog · coming in 5e"),
		newEcosystemPlaceholder("Orphans", "orphans", "Drifted / orphaned workspaces · coming in 5e"),
		newEcosystemPlaceholder("Actions", "actions", "Bulk ecosystem actions · coming in 5e"),
	}

	p := pager.NewWith(pages, keys, pager.Config{
		OuterPadding: [4]int{1, 2, 0, 2},
		FooterHeight: 1,
	})

	m := EcosystemModel{
		pager:        p,
		daemonClient: cfg.DaemonClient,
		workspace:    cfg.Root,
		cfg:          cfg.Cfg,
		hosted:       cfg.Hosted,
	}
	m.reloadWorktrees()
	return m
}

// Init wires up the pager and returns any commands its active page emits.
// The ecosystem panel has no live streams of its own yet — 5e will add
// daemon EnvStatus fan-out — so this is just pager bootstrapping.
func (m EcosystemModel) Init() tea.Cmd {
	return m.pager.Init()
}

// Update routes messages to the pager and intercepts the standard embed
// contract (focus/blur/workspace repoint) plus q/Esc → CloseRequestMsg so
// the standalone host can quit cleanly.
func (m EcosystemModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case embed.SetWorkspaceMsg:
		m.workspace = msg.Node
		m.hosted = true
		if msg.Node != nil {
			if loadedCfg, err := config.LoadFrom(msg.Node.Path); err == nil {
				m.cfg = loadedCfg
			}
		}
		m.reloadWorktrees()
		return m, nil

	case embed.FocusMsg:
		m.focused = true
		pm, pc := m.pager.Update(msg)
		m.pager = pm
		return m, pc

	case embed.BlurMsg:
		m.focused = false
		pm, pc := m.pager.Update(msg)
		m.pager = pm
		return m, pc

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve two rows for our header + subtitle before handing the
		// remaining space to the pager. Without this the pager claims the
		// full terminal height and our header gets scrolled off the top.
		sub := msg
		sub.Height = msg.Height - ecosystemHeaderRows
		if sub.Height < 1 {
			sub.Height = 1
		}
		pm, pc := m.pager.Update(sub)
		m.pager = pm
		return m, pc

	case tea.KeyMsg:
		km := keymap.Load(m.cfg, "grove.env")
		if key.Matches(msg, km.Quit) || key.Matches(msg, km.Back) {
			return m, func() tea.Msg { return embed.CloseRequestMsg{} }
		}
	}

	pm, pc := m.pager.Update(msg)
	m.pager = pm
	return m, pc
}

// View renders the ecosystem env panel. Layout mirrors the worktree-mode
// Model so a user switching between the two doesn't see the header jump:
// header line, spacer, pager body.
func (m EcosystemModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	th := theme.DefaultTheme

	name := "No ecosystem"
	if m.workspace != nil {
		name = m.workspace.Name
	}
	headerStyle := lipgloss.NewStyle().PaddingLeft(2).Bold(true)
	header := headerStyle.Render(
		th.Highlight.Render("ECOSYSTEM") + "  " + th.Bold.Render(name),
	)

	subtitle := "  " + th.Muted.Render(
		"Ecosystem-wide env dashboard · scaffolded in 5d, pages land in 5e",
	)

	body := m.pager.View()
	out := header + "\n" + subtitle + "\n" + body
	return lipgloss.NewStyle().MaxWidth(m.width).Render(out)
}

// reloadWorktrees re-enumerates the worktrees under the current ecosystem
// root. 5e will consume m.worktrees in the Deployments page; today the data
// is fetched eagerly so tests and smoke checks can confirm the pipeline.
func (m *EcosystemModel) reloadWorktrees() {
	if m.workspace == nil {
		m.worktrees = nil
		return
	}
	states, err := EnumerateWorktreeStates(m.workspace)
	if err != nil {
		m.worktrees = nil
		return
	}
	m.worktrees = states
}

// ecosystemPlaceholderPage is the stub page used for every tab in 5d. It
// renders a centered "coming soon" message so a user opening the panel sees
// the five-tab structure is already in place even if the bodies are empty.
type ecosystemPlaceholderPage struct {
	name    string
	tabID   string
	message string
	width   int
	height  int
}

func newEcosystemPlaceholder(name, tabID, message string) *ecosystemPlaceholderPage {
	return &ecosystemPlaceholderPage{name: name, tabID: tabID, message: message}
}

func (p *ecosystemPlaceholderPage) Name() string                           { return p.name }
func (p *ecosystemPlaceholderPage) TabID() string                          { return p.tabID }
func (p *ecosystemPlaceholderPage) Init() tea.Cmd                          { return nil }
func (p *ecosystemPlaceholderPage) Update(tea.Msg) (pager.Page, tea.Cmd)   { return p, nil }
func (p *ecosystemPlaceholderPage) Focus() tea.Cmd                         { return nil }
func (p *ecosystemPlaceholderPage) Blur()                                  {}
func (p *ecosystemPlaceholderPage) SetSize(width, height int) {
	p.width = width
	p.height = height
}

func (p *ecosystemPlaceholderPage) View() string {
	th := theme.DefaultTheme
	return lipgloss.NewStyle().
		Padding(2, 4).
		Render(th.Muted.Render(p.message))
}
