// Package env provides an embeddable TUI panel for monitoring and controlling
// grove environments. It uses a pager with three tabs (Services, Variables,
// Actions) and streams real-time state from the daemon via SSE.
//
// The panel follows the embed contract (FocusMsg, BlurMsg, SetWorkspaceMsg)
// and the streamLifecycle pattern from hooks/pkg/tui/view/io.go.
package env

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
)

// Config holds construction parameters for the env panel.
type Config struct {
	DaemonClient daemon.Client
	InitialFocus *workspace.WorkspaceNode
	Cfg          *config.Config
	Hosted       bool
}

// Model is the embeddable env panel.
type Model struct {
	pager  pager.Model
	stream *streamLifecycle

	daemonClient daemon.Client
	workspace    *workspace.WorkspaceNode
	hosted       bool
	focused      bool
	cfg          *config.Config

	// env state
	envState    *env.EnvStateFile
	envResponse *env.EnvResponse
	envProfiles []string // available profiles from config
	statusErr   error
	loading     bool
	actionMsg   string // transient feedback message

	width  int
	height int
}

// New constructs an env panel Model from a Config.
func New(cfg Config) Model {
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

	svcPage := &servicesPage{}
	varsPage := &variablesPage{}
	actPage := &actionsPage{}

	p := pager.NewWith([]pager.Page{svcPage, varsPage, actPage}, keys, pager.Config{
		OuterPadding: [4]int{1, 2, 0, 2},
		FooterHeight: 1, // help hint line
	})

	m := Model{
		pager:        p,
		stream:       newStreamLifecycle(),
		daemonClient: cfg.DaemonClient,
		workspace:    cfg.InitialFocus,
		hosted:       cfg.Hosted,
		cfg:          cfg.Cfg,
		loading:      true,
	}

	// Discover available env profiles from config
	m.envProfiles = discoverProfiles(cfg.Cfg)

	return m
}

func discoverProfiles(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var profiles []string
	if cfg.Environment != nil {
		profiles = append(profiles, "default")
	}
	if cfg.Environments != nil {
		for name := range cfg.Environments {
			profiles = append(profiles, name)
		}
		sort.Strings(profiles)
	}
	return profiles
}

// Init starts the SSE subscription and initial env status fetch.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		subscribeToDaemonCmd(m.daemonClient),
	}
	if m.workspace != nil {
		cmds = append(cmds, fetchEnvStatusCmd(m.daemonClient, m.workspace.Name))
	}
	cmds = append(cmds, m.pager.Init())
	return tea.Batch(cmds...)
}

// Update handles messages for the env panel.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case embed.SetWorkspaceMsg:
		m.workspace = msg.Node
		m.hosted = true
		m.envState = nil
		m.envResponse = nil
		m.statusErr = nil
		m.loading = true
		m.actionMsg = ""
		// Reload config for the new workspace
		if msg.Node != nil {
			if loadedCfg, err := config.LoadFrom(msg.Node.Path); err == nil {
				m.cfg = loadedCfg
				m.envProfiles = discoverProfiles(loadedCfg)
			}
		}
		m.updatePages()
		if m.workspace != nil {
			return m, fetchEnvStatusCmd(m.daemonClient, m.workspace.Name)
		}
		return m, nil

	case embed.FocusMsg:
		m.focused = true
		var cmds []tea.Cmd
		if m.workspace != nil {
			cmds = append(cmds, fetchEnvStatusCmd(m.daemonClient, m.workspace.Name))
		}
		pm, pc := m.pager.Update(msg)
		m.pager = pm
		if pc != nil {
			cmds = append(cmds, pc)
		}
		return m, tea.Batch(cmds...)

	case embed.BlurMsg:
		m.focused = false
		pm, pc := m.pager.Update(msg)
		m.pager = pm
		return m, pc

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		pm, pc := m.pager.Update(msg)
		m.pager = pm
		return m, pc

	case daemonStreamConnectedMsg:
		m.stream.store(msg.ch, msg.cancel)
		return m, m.stream.readDaemonStreamCmd(msg.ch)

	case daemonStreamErrorMsg:
		// Non-fatal: panel continues with fetched data
		return m, nil

	case daemonStateUpdateMsg:
		var cmds []tea.Cmd
		// On env-related updates, refetch status
		if m.workspace != nil {
			cmds = append(cmds, fetchEnvStatusCmd(m.daemonClient, m.workspace.Name))
		}
		cmds = append(cmds, m.stream.readDaemonStreamCmd(m.stream.ch))
		return m, tea.Batch(cmds...)

	case envStatusFetchedMsg:
		m.loading = false
		if msg.err != nil {
			m.statusErr = msg.err
			m.envState = nil
			m.envResponse = msg.response
		} else {
			m.statusErr = nil
			m.envResponse = msg.response
			// Parse state from response
			if msg.response != nil && msg.response.Status != "" {
				m.envState = responseToState(msg.response)
			}
		}
		m.updatePages()
		return m, nil

	case envActionResultMsg:
		m.loading = false
		if msg.err != nil {
			m.actionMsg = fmt.Sprintf("%s failed: %s", msg.action, msg.err)
		} else {
			m.actionMsg = fmt.Sprintf("%s completed", msg.action)
			m.envResponse = msg.response
			if msg.response != nil {
				m.envState = responseToState(msg.response)
			}
		}
		m.updatePages()
		// Refetch status after action
		if m.workspace != nil {
			return m, fetchEnvStatusCmd(m.daemonClient, m.workspace.Name)
		}
		return m, nil

	case tea.KeyMsg:
		// Intercept quit/back at the top so the panel reliably closes when
		// embedded in a host. Without this, q/Esc fall through to the pager
		// (which doesn't handle them), leaving the user stuck.
		km := keymap.Load(m.cfg, "grove.env")
		if key.Matches(msg, km.Quit) || key.Matches(msg, km.Back) {
			return m, func() tea.Msg { return embed.CloseRequestMsg{} }
		}
		// Global action keys work regardless of active tab
		switch {
		case key.Matches(msg, actionKeys.Up):
			return m, m.envAction("up")
		case key.Matches(msg, actionKeys.Down):
			return m, m.envAction("down")
		case key.Matches(msg, actionKeys.Restart):
			return m, m.envAction("restart")
		}
	}

	// Forward to pager for tab navigation
	pm, pc := m.pager.Update(msg)
	m.pager = pm
	return m, pc
}

// View renders the env panel.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	th := theme.DefaultTheme

	// Header: workspace name + env status
	header := m.renderHeader()

	// Action feedback
	feedback := ""
	if m.actionMsg != "" {
		feedback = "\n" + th.Muted.Render("  "+m.actionMsg)
	}

	// Build footer: action hints + optional feedback.
	var footerParts []string
	sep := " " + theme.IconBullet + " "
	footerParts = append(footerParts, th.Muted.Render(
		"u env up"+sep+"d env down"+sep+"r restart",
	))
	m.pager.SetFooter(strings.Join(footerParts, "\n"))

	// Pager body (tabs)
	body := m.pager.View()

	out := header + feedback + "\n" + body
	return lipgloss.NewStyle().MaxWidth(m.width).Render(out)
}

func (m Model) renderHeader() string {
	th := theme.DefaultTheme

	wsName := "No workspace"
	if m.workspace != nil {
		wsName = m.workspace.Name
	}

	status := "unknown"
	statusStyle := th.Muted
	statusIcon := theme.IconPending

	if m.loading {
		status = "loading"
		statusIcon = theme.IconStatusRunning
	} else if m.statusErr != nil {
		status = "error"
		statusStyle = th.Error
		statusIcon = theme.IconStatusFailed
	} else if m.envResponse != nil {
		status = m.envResponse.Status
		switch status {
		case "running":
			statusStyle = th.Success
			statusIcon = theme.IconStatusRunning
		case "stopped":
			statusStyle = th.Muted
			statusIcon = theme.IconStatusCompleted
		case "failed":
			statusStyle = th.Error
			statusIcon = theme.IconStatusFailed
		}
	} else {
		status = "no environment"
	}

	profile := ""
	if m.envState != nil && m.envState.Environment != "" {
		profile = fmt.Sprintf(" (%s)", m.envState.Environment)
	}

	provider := ""
	if m.envState != nil && m.envState.Provider != "" {
		provider = fmt.Sprintf(" [%s]", m.envState.Provider)
	}

	headerStyle := lipgloss.NewStyle().PaddingLeft(2).Bold(true)
	return headerStyle.Render(
		fmt.Sprintf("%s %s  %s%s%s",
			statusIcon,
			statusStyle.Render(status),
			th.Bold.Render(wsName),
			th.Muted.Render(profile),
			th.Muted.Render(provider),
		),
	)
}

// updatePages pushes current state into each page.
func (m *Model) updatePages() {
	pages := m.pager.Pages()
	for _, p := range pages {
		switch page := p.(type) {
		case *servicesPage:
			page.state = m.envState
			page.response = m.envResponse
			page.loading = m.loading
			page.err = m.statusErr
		case *variablesPage:
			page.state = m.envState
			page.response = m.envResponse
			page.loading = m.loading
		case *actionsPage:
			page.state = m.envState
			page.profiles = m.envProfiles
			page.commands = m.envCommands()
			page.loading = m.loading
		}
	}
}

func (m Model) envCommands() map[string]string {
	if m.cfg == nil {
		return nil
	}
	// Check active profile first, then default
	if m.envState != nil && m.envState.Environment != "" {
		if m.cfg.Environments != nil {
			if ec, ok := m.cfg.Environments[m.envState.Environment]; ok && ec.Commands != nil {
				return ec.Commands
			}
		}
	}
	if m.cfg.Environment != nil && m.cfg.Environment.Commands != nil {
		return m.cfg.Environment.Commands
	}
	return nil
}

// envAction dispatches an env up/down/restart command to the daemon.
func (m *Model) envAction(action string) tea.Cmd {
	if m.workspace == nil || m.daemonClient == nil {
		return nil
	}
	m.loading = true
	m.actionMsg = fmt.Sprintf("%s in progress...", action)
	m.updatePages()

	ws := m.workspace
	client := m.daemonClient
	return func() tea.Msg {
		req := env.EnvRequest{
			Action:    action,
			Workspace: ws,
			StateDir:  ".grove/env/",
		}
		var resp *env.EnvResponse
		var err error
		switch action {
		case "up":
			resp, err = client.EnvUp(context.Background(), req)
		case "down":
			resp, err = client.EnvDown(context.Background(), req)
		case "restart":
			req.Action = "down"
			req.Force = true
			_, _ = client.EnvDown(context.Background(), req)
			req.Action = "up"
			resp, err = client.EnvUp(context.Background(), req)
		}
		return envActionResultMsg{action: action, response: resp, err: err}
	}
}

// Close tears down the SSE stream.
func (m Model) Close() error {
	m.stream.close()
	return nil
}

// responseToState converts an EnvResponse into an EnvStateFile for display.
func responseToState(resp *env.EnvResponse) *env.EnvStateFile {
	if resp == nil {
		return nil
	}
	state := &env.EnvStateFile{
		EnvVars: resp.EnvVars,
		Volumes: resp.Volumes,
	}
	// Extract state from response.State map if present
	if v, ok := resp.State["provider"]; ok {
		state.Provider = v
	}
	if v, ok := resp.State["environment"]; ok {
		state.Environment = v
	}
	if v, ok := resp.State["managed_by"]; ok {
		state.ManagedBy = v
	}
	return state
}

// actionKeyMap defines global action keybindings.
type actionKeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Restart key.Binding
}

var actionKeys = actionKeyMap{
	Up:      key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "env up")),
	Down:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "env down")),
	Restart: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
}

// helpKeys returns the help bindings for the env panel.
func helpKeys() []key.Binding {
	return []key.Binding{
		actionKeys.Up,
		actionKeys.Down,
		actionKeys.Restart,
	}
}

// --- Helpers ---

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func padRight(s string, length int) string {
	if len(s) >= length {
		return s[:length]
	}
	return s + strings.Repeat(" ", length-len(s))
}
