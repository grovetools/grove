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
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/state"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/envdrift"
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
	layered      *config.LayeredConfig // raw layers for provenance resolution

	// env state
	envState    *env.EnvStateFile
	envResponse *env.EnvResponse
	envProfiles []string // available profiles from config
	statusErr   error
	loading     bool
	actionMsg   string // transient feedback message

	// overview / drift state
	stickyDefault    string
	configDefault    string
	profileProviders map[string]string
	selectedProfile  string
	driftingProfile  string
	driftResults     map[string]*envdrift.DriftSummary
	driftErrors      map[string]error

	// resolved config & provenance for the selected profile
	configResolved   *config.EnvironmentConfig
	configProvenance map[string]string
	configDeleted    map[string]string
	configErr        error

	// overlay, when non-nil, intercepts all keystrokes and renders on top
	// of the base view. Used by the P quick-switcher (Phase 5c) and — in
	// 5e — the W worktree picker.
	overlay *OverlayModel

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

	overview := newOverviewPage()
	summaryPg := newSummaryPage()
	configPg := newConfigPage()
	runtimePg := newRuntimePage()
	driftPg := newDriftPage()
	actPage := newActionsPage()

	// Phase 5b page order: Overview, Summary, Config, Runtime, Drift, Actions.
	// Pages 2-6 all scope to m.selectedProfile (driven by Overview's cursor).
	p := pager.NewWith(
		[]pager.Page{overview, summaryPg, configPg, runtimePg, driftPg, actPage},
		keys,
		pager.Config{
			OuterPadding: [4]int{1, 2, 0, 2},
			FooterHeight: 1, // help hint line
		},
	)

	m := Model{
		pager:            p,
		stream:           newStreamLifecycle(),
		daemonClient:     cfg.DaemonClient,
		workspace:        cfg.InitialFocus,
		hosted:           cfg.Hosted,
		cfg:              cfg.Cfg,
		loading:          true,
		driftResults:     make(map[string]*envdrift.DriftSummary),
		driftErrors:      make(map[string]error),
		profileProviders: make(map[string]string),
	}

	m.envProfiles = discoverProfiles(cfg.Cfg)
	m.loadOverviewContext()
	m.resolveSelectedConfig()
	m.updatePages()

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
	// Overlay intercept block — must run BEFORE the quit/back key check
	// below so Esc inside the overlay closes only the overlay rather than
	// propagating out and closing the whole embed host.
	if m.overlay != nil {
		switch typed := msg.(type) {
		case overlaySelectedMsg:
			m.selectedProfile = typed.key
			m.overlay = nil
			m.resolveSelectedConfig()
			m.updatePages()
			return m, nil
		case overlayClosedMsg:
			m.overlay = nil
			return m, nil
		case tea.KeyMsg:
			var cmd tea.Cmd
			m.overlay, cmd = m.overlay.Update(typed)
			return m, cmd
		}
	}

	switch msg := msg.(type) {

	case embed.SetWorkspaceMsg:
		m.workspace = msg.Node
		m.hosted = true
		m.envState = nil
		m.envResponse = nil
		m.statusErr = nil
		m.loading = true
		m.actionMsg = ""
		m.selectedProfile = ""
		m.driftResults = make(map[string]*envdrift.DriftSummary)
		m.driftErrors = make(map[string]error)
		m.driftingProfile = ""
		// Reload config for the new workspace
		if msg.Node != nil {
			if loadedCfg, err := config.LoadFrom(msg.Node.Path); err == nil {
				m.cfg = loadedCfg
				m.envProfiles = discoverProfiles(loadedCfg)
			}
		}
		m.loadOverviewContext()
		m.resolveSelectedConfig()
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

	case profileSelectedMsg:
		if msg.profile != m.selectedProfile {
			m.selectedProfile = msg.profile
			m.resolveSelectedConfig()
			m.updatePages()
		}
		return m, nil

	case driftCheckFinishedMsg:
		if m.driftingProfile == msg.profile {
			m.driftingProfile = ""
		}
		if msg.err != nil {
			m.driftErrors[msg.profile] = msg.err
			delete(m.driftResults, msg.profile)
		} else {
			m.driftResults[msg.profile] = msg.summary
			delete(m.driftErrors, msg.profile)
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
		// Global action keys work regardless of active tab. All lifecycle
		// actions now scope to the Overview cursor (m.selectedProfile).
		switch {
		case key.Matches(msg, actionKeys.Up):
			return m, m.envAction("up")
		case key.Matches(msg, actionKeys.Down):
			return m, m.envAction("down")
		case key.Matches(msg, actionKeys.Restart):
			return m, m.envAction("restart")
		}
		switch msg.String() {
		case "P":
			// Overview IS the profile picker — P is only meaningful on
			// pages 2-6 where the user is already scoped to a profile
			// and wants to re-scope without leaving the current page.
			if m.pager.ActiveIndex() == 0 || len(m.envProfiles) == 0 {
				return m, nil
			}
			items := make([]OverlayItem, 0, len(m.envProfiles))
			activeLocal := ""
			if m.envState != nil {
				activeLocal = m.envState.Environment
			}
			for _, name := range m.envProfiles {
				items = append(items, newProfileItem(
					name,
					m.profileProviders[name],
					name == activeLocal,
					name == m.stickyDefault,
					name == m.configDefault,
				))
			}
			m.overlay = NewOverlay(
				"Switch profile · scoped to current page",
				"Esc to cancel",
				items,
				displaySelectedProfile(m.selectedProfile, m.envProfiles),
			)
			return m, nil
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
		"u env up"+sep+"d env down"+sep+"r restart"+sep+"D drift"+sep+"P switch profile",
	))
	m.pager.SetFooter(strings.Join(footerParts, "\n"))

	// Scope breadcrumb band. Constant-height on all pages (including
	// Overview) so switching tabs doesn't shift the pager body up/down —
	// the mockup uses the same band on page 1 as an instruction line.
	breadcrumb := m.renderBreadcrumb()

	// Pager body (tabs)
	body := m.pager.View()

	out := header + feedback + "\n" + breadcrumb + "\n" + body
	base := lipgloss.NewStyle().MaxWidth(m.width).Render(out)

	if m.overlay == nil {
		return base
	}
	// Center the overlay over the base view. lipgloss v1.1 doesn't have
	// PlaceOverlay; placeOverlay() is the minimal substitute we ship in
	// overlay.go. Negative offsets get clamped inside placeOverlay.
	overlayView := m.overlay.View()
	x := (m.width - lipgloss.Width(overlayView)) / 2
	y := (m.height - lipgloss.Height(overlayView)) / 2
	return placeOverlay(x, y, overlayView, base)
}

// profileItem adapts a profile name + metadata into the OverlayItem
// interface so the reusable overlay component can render it. Kept in
// model.go (rather than overlay.go) because the overlay component itself
// is deliberately profile-agnostic — Phase 5e will add a worktreeItem
// next to this one.
type profileItem struct {
	name     string
	provider string
	running  bool
	sticky   bool
	dflt     bool
}

func newProfileItem(name, provider string, running, sticky, dflt bool) profileItem {
	return profileItem{name: name, provider: provider, running: running, sticky: sticky, dflt: dflt}
}

func (p profileItem) Key() string   { return p.name }
func (p profileItem) Label() string { return p.name }

func (p profileItem) Glyph() string {
	if p.running {
		return theme.IconStatusRunning
	}
	return theme.IconBullet
}

func (p profileItem) GlyphStyle() lipgloss.Style {
	th := theme.DefaultTheme
	if p.running {
		return th.Success
	}
	return th.Muted
}

func (p profileItem) Subtitle() string {
	// Match the mockup: running rows get "● running"; inactive rows get
	// a short state-tag summary if one of the default flags applies.
	if p.running {
		return "● running"
	}
	switch {
	case p.sticky:
		return "sticky default"
	case p.dflt:
		return "config default"
	default:
		return "inactive"
	}
}

func (p profileItem) Provider() string {
	if p.provider == "" {
		return "—"
	}
	return p.provider
}

// renderBreadcrumb returns the scope line shown between the header and
// the pager body. On page 1 it explains that the cursor drives the scope;
// on pages 2-6 it shows `<glyph> <profile> › <page-name>` so the user
// always knows what selection they're acting on.
func (m Model) renderBreadcrumb() string {
	th := theme.DefaultTheme
	if m.pager.ActiveIndex() == 0 {
		return "  " + th.Muted.Render(
			"Pick a profile below · the selection scopes pages 2-6",
		)
	}
	profile := displaySelectedProfile(m.selectedProfile, m.envProfiles)
	if profile == "" {
		profile = "(none)"
	}
	page := ""
	if active := m.pager.Active(); active != nil {
		page = active.Name()
	}
	glyph := scopeGlyph(m.profileProviders[profile])
	return fmt.Sprintf("  %s %s %s %s",
		th.Highlight.Render(glyph),
		th.Bold.Render(profile),
		th.Muted.Render("›"),
		th.Muted.Render(page),
	)
}

// scopeGlyph picks a single glyph keyed on the provider, matching the
// symbol set used on the Overview page so the breadcrumb reads as a
// continuation rather than a separate legend.
func scopeGlyph(provider string) string {
	switch provider {
	case "terraform":
		return theme.IconEarth
	case "docker":
		return theme.IconRunning
	case "native":
		return theme.IconBullet
	default:
		return theme.IconPending
	}
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

// updatePages pushes current state into each page. Phase 5b: all detail
// pages (2-6) scope to m.selectedProfile; only Overview reads live global
// state directly. Runtime only shows data when the selected profile is
// the one actually running locally.
func (m *Model) updatePages() {
	pages := m.pager.Pages()
	activeLocal := ""
	if m.envState != nil {
		activeLocal = m.envState.Environment
	}
	selected := displaySelectedProfile(m.selectedProfile, m.envProfiles)
	isRunning := selected != "" && selected == activeLocal

	provider := ""
	if m.configResolved != nil && m.configResolved.Provider != "" {
		provider = m.configResolved.Provider
	} else if p, ok := m.profileProviders[selected]; ok {
		provider = p
	}

	workspaceRoot := ""
	if m.workspace != nil {
		workspaceRoot = m.workspace.Path
	}

	for _, p := range pages {
		switch page := p.(type) {
		case *overviewPage:
			page.profiles = m.envProfiles
			page.configDefault = m.configDefault
			page.stickyDefault = m.stickyDefault
			page.activeLocal = activeLocal
			page.providers = m.profileProviders
			page.state = m.envState
			page.driftingProfile = m.driftingProfile
			page.driftResults = m.driftResults
			page.driftErrors = m.driftErrors
			// Keep the cursor in sync with the model's scope so that
			// arrow-key moves on Overview and page jumps via numbers
			// both converge on the same selection.
			if m.selectedProfile != "" {
				for i, name := range m.envProfiles {
					if name == m.selectedProfile {
						page.cursor = i
						break
					}
				}
			}
		case *summaryPage:
			page.profile = selected
			page.provider = provider
			page.isRunning = isRunning
			page.resolved = m.configResolved
			page.artifacts = deriveArtifacts(
				selected, provider, m.configResolved, m.envState,
				workspaceRoot, isRunning, m.allResolvedConfigs(),
			)
		case *configPage:
			page.profile = selected
			page.resolved = m.configResolved
			page.provenance = m.configProvenance
			page.deleted = m.configDeleted
			page.err = m.configErr
		case *runtimePage:
			page.profile = selected
			page.isRunning = isRunning
			page.workspaceRoot = workspaceRoot
			page.loading = m.loading
			page.err = m.statusErr
			if isRunning {
				page.state = m.envState
				page.response = m.envResponse
			} else {
				page.state = nil
				page.response = nil
			}
		case *driftPage:
			page.profile = selected
			page.provider = provider
			page.summary = m.driftResults[selected]
			page.err = m.driftErrors[selected]
			page.pending = m.driftingProfile == selected && selected != ""
		case *actionsPage:
			page.profile = selected
			page.provider = provider
			page.isRunning = isRunning
			page.commands = m.commandsForProfile(selected)
		}
	}
}

// allResolvedConfigs flattens cfg.Environment + cfg.Environments into a
// name->config map so deriveArtifacts can look up cross-profile
// dependencies (e.g. a shared_env reference to a sibling profile).
func (m *Model) allResolvedConfigs() map[string]*config.EnvironmentConfig {
	out := make(map[string]*config.EnvironmentConfig)
	if m.cfg == nil {
		return out
	}
	if m.cfg.Environment != nil {
		out["default"] = m.cfg.Environment
	}
	for name, ec := range m.cfg.Environments {
		out[name] = ec
	}
	return out
}

// commandsForProfile returns the EnvironmentConfig.Commands for the given
// profile name — prefers the named profile but falls back to the default
// block when asked for "default".
func (m *Model) commandsForProfile(profile string) map[string]string {
	if m.cfg == nil || profile == "" {
		return nil
	}
	if profile == "default" && m.cfg.Environment != nil {
		return m.cfg.Environment.Commands
	}
	if m.cfg.Environments != nil {
		if ec, ok := m.cfg.Environments[profile]; ok && ec != nil {
			return ec.Commands
		}
	}
	return nil
}

// loadOverviewContext refreshes the sticky-default, config-default, and
// per-profile provider map used by the Overview page. It must be called
// whenever m.cfg or the workspace root changes. Also attempts to load the
// LayeredConfig so the Config page has provenance data.
func (m *Model) loadOverviewContext() {
	m.stickyDefault, _ = state.GetString("environment")
	m.configDefault = ""
	m.profileProviders = make(map[string]string)
	if m.cfg != nil {
		if m.cfg.Environment != nil {
			m.configDefault = "default"
			m.profileProviders["default"] = m.cfg.Environment.Provider
		}
		for name, ec := range m.cfg.Environments {
			if ec != nil {
				m.profileProviders[name] = ec.Provider
			}
		}
	}

	// Layered config is best-effort; if it fails the Config page simply
	// renders an error and the rest of the TUI still works.
	cwd, err := os.Getwd()
	if err == nil {
		if layered, err := config.LoadLayered(cwd); err == nil {
			m.layered = layered
		} else {
			m.layered = nil
		}
	}

	// Default the selected profile to whichever makes sense given what we
	// know. Preference order: sticky default → config default → first
	// discovered profile.
	if m.selectedProfile == "" {
		switch {
		case m.stickyDefault != "":
			m.selectedProfile = m.stickyDefault
		case m.configDefault != "":
			m.selectedProfile = m.configDefault
		case len(m.envProfiles) > 0:
			m.selectedProfile = m.envProfiles[0]
		}
	}
}

// resolveSelectedConfig re-runs ResolveEnvironmentWithProvenance for the
// currently selected profile so the Config page can render a tree of the
// merged config annotated with per-key layer labels. Safe to call with a
// missing or nil LayeredConfig — the page simply shows an error.
func (m *Model) resolveSelectedConfig() {
	m.configResolved = nil
	m.configProvenance = nil
	m.configDeleted = nil
	m.configErr = nil

	if m.layered == nil {
		return
	}
	profile := m.selectedProfile
	if profile == "default" {
		profile = ""
	}
	resolved, prov, deleted, err := config.ResolveEnvironmentWithProvenance(m.layered, profile)
	if err != nil {
		m.configErr = err
		return
	}
	m.configResolved = resolved
	m.configProvenance = prov
	m.configDeleted = deleted
}

// displaySelectedProfile normalizes the empty-sentinel profile name for the
// Config page header. Returns "" when there's no meaningful selection yet.
func displaySelectedProfile(selected string, profiles []string) string {
	if selected != "" {
		return selected
	}
	if len(profiles) > 0 {
		return profiles[0]
	}
	return ""
}

// envAction dispatches an env up/down/restart command to the daemon,
// targeting the currently-selected profile rather than whatever the
// daemon thinks is running. This is the core of the Phase 5b single-scope
// model — the user's cursor determines what we act on, not the daemon's
// global state.
func (m *Model) envAction(action string) tea.Cmd {
	if m.workspace == nil || m.daemonClient == nil {
		return nil
	}
	profile := displaySelectedProfile(m.selectedProfile, m.envProfiles)

	m.loading = true
	m.actionMsg = fmt.Sprintf("%s %s in progress...", action, profile)
	m.updatePages()

	ws := m.workspace
	client := m.daemonClient
	return func() tea.Msg {
		req := env.EnvRequest{
			Action:    action,
			Workspace: ws,
			Profile:   profile,
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
