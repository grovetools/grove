package env

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	"github.com/grovetools/grove/pkg/envdrift"
)

// ecosystemHeaderRows is the vertical space the EcosystemModel claims for
// its header + subtitle lines before forwarding the remainder to the pager.
const ecosystemHeaderRows = 2

// driftChdirMu serializes any goroutine that chdirs to run drift against
// another worktree. Chdir is process-wide, so even though the ecosystem
// drift queue is logically serial, we still want a mutex to guard against
// the user pressing A twice in flight or a concurrent per-page drift call.
var driftChdirMu sync.Mutex

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

// EcosystemModel is the ecosystem-scoped env TUI: Deployments matrix,
// Shared Infra detail, Profiles catalog, Orphans, and ecosystem-wide
// actions. Selection / drift state lives on this model so the pages can
// stay plain renderers.
type EcosystemModel struct {
	pager pager.Model

	daemonClient daemon.Client
	workspace    *workspace.WorkspaceNode
	worktrees    []WorktreeState
	cfg          *config.Config
	hosted       bool
	focused      bool

	// Pages held as typed pointers so updatePages() can set fields
	// without type-asserting through the pager interface repeatedly.
	deployments *deploymentsPage
	shared      *sharedInfraPage
	profiles    *profilesPage
	orphans     *orphansPage
	actions     *ecosystemActionsPage

	// Overlay (worktree picker via W, row actions via Enter).
	overlay        *OverlayModel
	overlayKind    overlayKind // which overlay is currently on screen
	overlayTarget  string      // worktree name the row overlay is scoped to

	// Drift-all state. driftQueue is the FIFO of worktree names to drift;
	// driftingWorktree is the one in flight. driftQueueTotal/Done drive
	// the "N/M" progress label on the Deployments page.
	driftQueue       []string
	driftingWorktree string
	driftQueueTotal  int
	driftQueueDone   int

	// Cached lookup: worktree name → absolute path. Used to chdir in the
	// serial drift runner and to launch `grove env tui` via tea.ExecProcess.
	pathByName map[string]string

	width  int
	height int
}

// overlayKind tags which surface opened the overlay so overlaySelectedMsg
// dispatch can branch correctly. We keep it at the model level rather than
// on the overlay itself because OverlayModel is reused verbatim from 5c.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayWorktreePicker
	overlayRowActions
)

// driftNextMsg pops the next worktree off the drift queue and starts it.
type driftNextMsg struct{}

// ecosystemDriftDoneMsg carries the result of a serial drift check back to
// the model. The summary may be nil on error; checkedAt tracks when the
// cache was written so the Deployments column can render "Nm ago".
type ecosystemDriftDoneMsg struct {
	worktree  string
	summary   *envdrift.DriftSummary
	checkedAt time.Time
	err       error
}

// worktreeJumpMsg is emitted when the W overlay commits a selection. We
// translate it to tea.ExecProcess in Update so the model-level code stays
// focused on selection logic.
type worktreeJumpMsg struct {
	path string
}

// NewEcosystem constructs the ecosystem env panel.
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

	deployments := newDeploymentsPage()
	shared := newSharedInfraPage()
	profiles := newProfilesPage()
	orphans := newOrphansPage()
	actions := newEcosystemActionsPage()

	pages := []pager.Page{deployments, shared, profiles, orphans, actions}

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
		deployments:  deployments,
		shared:       shared,
		profiles:     profiles,
		orphans:      orphans,
		actions:      actions,
		pathByName:   map[string]string{},
	}
	m.reloadWorktrees()
	m.updatePages()
	return m
}

// Init wires up the pager.
func (m EcosystemModel) Init() tea.Cmd {
	return m.pager.Init()
}

// Update routes messages. Overlay intercept runs BEFORE the quit key
// check so Esc inside the overlay closes only the overlay.
func (m EcosystemModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.overlay != nil {
		switch typed := msg.(type) {
		case overlaySelectedMsg:
			return m.handleOverlaySelect(typed)
		case overlayClosedMsg:
			m.overlay = nil
			m.overlayKind = overlayNone
			m.overlayTarget = ""
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
		if msg.Node != nil {
			if loadedCfg, err := config.LoadFrom(msg.Node.Path); err == nil {
				m.cfg = loadedCfg
			}
		}
		m.reloadWorktrees()
		m.updatePages()
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
		// Reserve two rows for header + subtitle before handing the
		// remaining space to the pager.
		sub := msg
		sub.Height = msg.Height - ecosystemHeaderRows
		if sub.Height < 1 {
			sub.Height = 1
		}
		pm, pc := m.pager.Update(sub)
		m.pager = pm
		return m, pc

	case openRowOverlayMsg:
		return m.openRowOverlay(msg.worktreeName), nil

	case driftNextMsg:
		return m.dispatchNextDrift()

	case ecosystemDriftDoneMsg:
		return m.handleDriftDone(msg)

	case worktreeJumpMsg:
		if msg.path == "" {
			return m, nil
		}
		cmd := exec.Command("grove", "env", "tui")
		cmd.Dir = msg.path
		// tea.ExecProcess suspends the outer TUI, runs the child until it
		// exits (user hits q inside the worktree panel), then resumes.
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			// Swallow errors silently — a failed exec shouldn't crash
			// the outer TUI. A user-visible notice can come later.
			return nil
		})

	case tea.KeyMsg:
		km := keymap.Load(m.cfg, "grove.env")
		if key.Matches(msg, km.Quit) || key.Matches(msg, km.Back) {
			return m, func() tea.Msg { return embed.CloseRequestMsg{} }
		}
		switch msg.String() {
		case "W":
			return m.openWorktreePicker(), nil
		case "A":
			return m.startDriftAll()
		case "D":
			// Drift shared infra only when we actually have a shared
			// profile and it lives under the ecosystem root.
			if m.sharedProfileName() == "" || m.workspace == nil {
				return m, nil
			}
			return m.enqueueDriftForEcosystemRoot()
		}
	}

	pm, pc := m.pager.Update(msg)
	m.pager = pm
	// After a tab switch refresh the 'you' tag — Focus() is the page's hook.
	if active, ok := m.pager.Active().(*deploymentsPage); ok && active != nil {
		_ = active.Focus()
	}
	return m, pc
}

// handleOverlaySelect routes the overlay's commit message to the correct
// handler based on which surface opened it.
func (m EcosystemModel) handleOverlaySelect(msg overlaySelectedMsg) (tea.Model, tea.Cmd) {
	kind := m.overlayKind
	target := m.overlayTarget
	selected := msg.key
	m.overlay = nil
	m.overlayKind = overlayNone
	m.overlayTarget = ""

	switch kind {
	case overlayWorktreePicker:
		path := m.pathByName[selected]
		if path == "" {
			return m, nil
		}
		return m, func() tea.Msg { return worktreeJumpMsg{path: path} }
	case overlayRowActions:
		return m.applyRowAction(target, selected)
	}
	return m, nil
}

// openWorktreePicker builds the W overlay and stores its kind so
// handleOverlaySelect can route the commit correctly.
func (m EcosystemModel) openWorktreePicker() EcosystemModel {
	if len(m.worktrees) == 0 {
		return m
	}
	items := make([]OverlayItem, 0, len(m.worktrees))
	for _, w := range m.worktrees {
		profile := ""
		if w.EnvState != nil {
			profile = w.EnvState.Environment
		}
		items = append(items, newWorktreeItem(w, profile))
	}
	m.overlay = NewOverlay(
		"Jump to worktree · grove env tui",
		"Esc to cancel",
		items,
		"",
	)
	m.overlayKind = overlayWorktreePicker
	return m
}

// openRowOverlay builds the Enter-on-a-row overlay with four action items
// matching the mockup: jump, drift, open endpoint, view config diff. The
// last is a stub; the rest are wired.
func (m EcosystemModel) openRowOverlay(worktreeName string) EcosystemModel {
	if worktreeName == "" {
		return m
	}
	items := []OverlayItem{
		newRowActionItem("jump", "Jump to worktree's TUI",
			fmt.Sprintf("cd %s && grove env tui", worktreeName), 1),
		newRowActionItem("drift", "Run drift from here",
			fmt.Sprintf("grove env drift (in %s)", worktreeName), 2),
		newRowActionItem("endpoint", "Open endpoint in browser",
			firstEndpointByName(m.worktrees, worktreeName), 3),
		newRowActionItem("diff", "View config diff vs yours",
			"(coming in v2)", 4),
	}
	m.overlay = NewOverlay(
		m.rowOverlayTitle(worktreeName),
		"Esc to cancel",
		items,
		"jump",
	)
	m.overlayKind = overlayRowActions
	m.overlayTarget = worktreeName
	return m
}

// rowOverlayTitle returns a rich header string for the row-actions overlay:
// `<glyph> <worktree> (you) · <profile> · <state>` — the mockup uses this
// format so the user can confirm the row before committing to an action.
func (m EcosystemModel) rowOverlayTitle(worktreeName string) string {
	for _, w := range m.worktrees {
		if w.Workspace == nil || w.Workspace.Name != worktreeName {
			continue
		}
		glyph, _ := worktreeGlyph(w)
		profile := "—"
		if w.EnvState != nil && w.EnvState.Environment != "" {
			profile = w.EnvState.Environment
		}
		you := ""
		if wd, _ := os.Getwd(); wd != "" && strings.HasPrefix(wd, w.Workspace.Path) {
			you = " (you)"
		}
		return fmt.Sprintf("Actions · %s %s%s · %s · %s",
			glyph, worktreeName, you, profile, FormatWorktreeStateSummary(w))
	}
	return "Actions · " + worktreeName
}

// applyRowAction executes the commit message from the row-actions overlay.
// Only jump and drift do real work today; endpoint opens the browser when
// xdg-open / open is available; diff prints a stub.
func (m EcosystemModel) applyRowAction(worktree, action string) (tea.Model, tea.Cmd) {
	path := m.pathByName[worktree]
	switch action {
	case "jump":
		if path == "" {
			return m, nil
		}
		return m, func() tea.Msg { return worktreeJumpMsg{path: path} }
	case "drift":
		// Reuse the drift-all machinery for a single worktree so the same
		// chdir guard, spinner, and cache-write path cover both entry points.
		m.driftQueue = []string{worktree}
		m.driftQueueTotal = 1
		m.driftQueueDone = 0
		return m, func() tea.Msg { return driftNextMsg{} }
	case "endpoint":
		endpoint := firstEndpointByName(m.worktrees, worktree)
		if endpoint == "" || endpoint == "(local only)" {
			return m, nil
		}
		return m, openURLCmd(endpoint)
	case "diff":
		// v2 stub.
		return m, nil
	}
	return m, nil
}

// startDriftAll populates the drift queue with every TF-backed worktree
// that currently deploys a terraform profile, then kicks the queue. Press
// on a non-TF-only ecosystem becomes a no-op with no state mutation.
func (m EcosystemModel) startDriftAll() (tea.Model, tea.Cmd) {
	if m.driftingWorktree != "" || len(m.driftQueue) > 0 {
		return m, nil // already running
	}
	var queue []string
	for _, w := range m.worktrees {
		if w.Workspace == nil {
			continue
		}
		if !m.isTerraformDeployable(w) {
			continue
		}
		queue = append(queue, w.Workspace.Name)
	}
	if len(queue) == 0 {
		return m, nil
	}
	m.driftQueue = queue
	m.driftQueueTotal = len(queue)
	m.driftQueueDone = 0
	return m, func() tea.Msg { return driftNextMsg{} }
}

// enqueueDriftForEcosystemRoot starts a one-element drift queue that
// targets the ecosystem root, which is where the shared-infra profile's
// state lives. The name used for progress display is the workspace name.
func (m EcosystemModel) enqueueDriftForEcosystemRoot() (tea.Model, tea.Cmd) {
	if m.driftingWorktree != "" || len(m.driftQueue) > 0 {
		return m, nil
	}
	name := m.workspace.Name
	m.pathByName[name] = m.workspace.Path
	m.driftQueue = []string{name}
	m.driftQueueTotal = 1
	m.driftQueueDone = 0
	return m, func() tea.Msg { return driftNextMsg{} }
}

// dispatchNextDrift pops the head of the queue and kicks a tea.Cmd that
// runs envdrift in the target worktree. chdir is guarded by driftChdirMu
// because Go has no per-goroutine cwd.
func (m EcosystemModel) dispatchNextDrift() (tea.Model, tea.Cmd) {
	if len(m.driftQueue) == 0 {
		m.driftingWorktree = ""
		m.driftQueueTotal = 0
		m.driftQueueDone = 0
		m.updatePages()
		return m, nil
	}
	name := m.driftQueue[0]
	m.driftQueue = m.driftQueue[1:]
	m.driftingWorktree = name
	m.updatePages()

	path := m.pathByName[name]
	profile := m.profileForWorktree(name)
	return m, ecosystemDriftCmd(name, path, profile)
}

// handleDriftDone writes the summary back into m.worktrees, bumps the
// progress counter, and either emits driftNextMsg or clears drift state.
func (m EcosystemModel) handleDriftDone(msg ecosystemDriftDoneMsg) (tea.Model, tea.Cmd) {
	for i := range m.worktrees {
		if m.worktrees[i].Workspace == nil {
			continue
		}
		if m.worktrees[i].Workspace.Name == msg.worktree {
			if msg.err == nil && msg.summary != nil {
				m.worktrees[i].Drift = msg.summary
				m.worktrees[i].DriftCheckedAt = msg.checkedAt
			}
			break
		}
	}
	if m.driftingWorktree == msg.worktree {
		m.driftingWorktree = ""
		m.driftQueueDone++
	}
	m.updatePages()
	if len(m.driftQueue) > 0 {
		return m, func() tea.Msg { return driftNextMsg{} }
	}
	// Queue drained: clear progress counters.
	m.driftQueueTotal = 0
	m.driftQueueDone = 0
	m.updatePages()
	return m, nil
}

// profileForWorktree returns the profile name to pass to envdrift.RunEnvDrift
// for a given worktree. We prefer the profile the worktree last deployed
// (recorded in its state.json). If nothing is recorded we fall back to the
// empty string, which RunEnvDrift resolves as the sticky default.
func (m EcosystemModel) profileForWorktree(name string) string {
	if name == m.workspace.Name {
		// Ecosystem root drift targets the shared profile explicitly.
		return m.sharedProfileName()
	}
	for _, w := range m.worktrees {
		if w.Workspace != nil && w.Workspace.Name == name {
			if w.EnvState != nil {
				return w.EnvState.Environment
			}
			return ""
		}
	}
	return ""
}

// isTerraformDeployable reports whether a worktree has a terraform-backed
// profile last deployed. Non-terraform profiles can't drift, so they're
// filtered out of the "drift all" queue.
func (m EcosystemModel) isTerraformDeployable(w WorktreeState) bool {
	if w.EnvState == nil {
		return false
	}
	return w.EnvState.Provider == "terraform"
}

// ecosystemDriftCmd returns a tea.Cmd that runs envdrift.RunEnvDrift after
// chdir-ing into the worktree. The chdir is restored in a deferred call so
// a panic in RunEnvDrift doesn't leave the process cwd pointing elsewhere.
func ecosystemDriftCmd(name, path, profile string) tea.Cmd {
	return func() tea.Msg {
		driftChdirMu.Lock()
		defer driftChdirMu.Unlock()

		prev, _ := os.Getwd()
		if path != "" {
			if err := os.Chdir(path); err != nil {
				return ecosystemDriftDoneMsg{worktree: name, err: err}
			}
		}
		defer func() {
			if prev != "" {
				_ = os.Chdir(prev)
			}
		}()

		summary, err := envdrift.RunEnvDrift(context.Background(), profile)
		return ecosystemDriftDoneMsg{
			worktree:  name,
			summary:   summary,
			checkedAt: time.Now().UTC(),
			err:       err,
		}
	}
}

// openURLCmd opens a URL in the default browser using the platform-native
// helper. Silent failure is fine here — the mockup treats endpoint-open as
// a best-effort convenience.
func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch {
		case hasCommand("open"):
			cmd = exec.Command("open", url)
		case hasCommand("xdg-open"):
			cmd = exec.Command("xdg-open", url)
		default:
			return nil
		}
		_ = cmd.Start()
		return nil
	}
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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

	// Status line matching the mockup: worktree counts, deploy summary,
	// shared-infra status. Cheap to recompute on every View.
	subtitle := "  " + th.Muted.Render(m.renderStatusLine())

	body := m.pager.View()
	out := header + "\n" + subtitle + "\n" + body
	base := lipgloss.NewStyle().MaxWidth(m.width).Render(out)

	if m.overlay == nil {
		return base
	}
	overlayView := m.overlay.View()
	x := (m.width - lipgloss.Width(overlayView)) / 2
	y := (m.height - lipgloss.Height(overlayView)) / 2
	return placeOverlay(x, y, overlayView, base)
}

// renderStatusLine summarises ecosystem health in one row under the
// header — mirrors the mockup's `4 worktrees · 2 deployed · 1 drifting ·
// N orphan · shared infra clean · applied 3m ago`.
func (m EcosystemModel) renderStatusLine() string {
	total := 0
	deployed := 0
	drifting := 0
	orphans := 0
	for _, w := range m.worktrees {
		if w.Workspace == nil {
			orphans++
			continue
		}
		total++
		if w.EnvState != nil && (len(w.EnvState.Services) > 0 || len(w.EnvState.Endpoints) > 0) {
			deployed++
		}
		if w.Drift != nil && w.Drift.HasDrift {
			drifting++
		}
	}
	shared := m.sharedProfileName()
	sharedSeg := "no shared profile"
	if shared != "" {
		sharedSeg = "shared: " + shared
		if d := m.sharedDrift(); d != nil {
			if d.HasDrift {
				sharedSeg += fmt.Sprintf(" · %s (+%d ~%d -%d)",
					"drifting", d.Add, d.Change, d.Destroy)
			} else {
				sharedSeg += " · clean"
			}
			if !d.CheckedAt.IsZero() {
				sharedSeg += " · checked " + humanAge(time.Since(d.CheckedAt)) + " ago"
			}
		}
	}
	base := fmt.Sprintf("%d worktree(s) · %d deployed · %d drifting",
		total, deployed, drifting)
	if orphans > 0 {
		base += fmt.Sprintf(" · %d orphan", orphans)
	}
	return base + " · " + sharedSeg
}

// sharedProfileName returns the single shared profile in this ecosystem, or
// "" when none is defined. The resolution logic is centralised in
// config.IsSharedProfile so both this page and the catalog agree.
func (m EcosystemModel) sharedProfileName() string {
	if m.cfg == nil {
		return ""
	}
	for name := range m.cfg.Environments {
		if config.IsSharedProfile(m.cfg, name) {
			return name
		}
	}
	return ""
}

// reloadWorktrees re-enumerates the worktrees under the current ecosystem
// root and rebuilds the name→path lookup used by W and the drift queue.
func (m *EcosystemModel) reloadWorktrees() {
	m.pathByName = map[string]string{}
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
	for _, w := range states {
		if w.Workspace == nil {
			continue
		}
		m.pathByName[w.Workspace.Name] = w.Workspace.Path
	}
	// Ecosystem root is a valid jump/drift target even when it isn't a
	// worktree itself — users press W while at the root to go elsewhere,
	// but the reverse direction (jump back) and the D key both need its
	// path to stay addressable.
	if m.workspace != nil && m.workspace.Name != "" {
		m.pathByName[m.workspace.Name] = m.workspace.Path
	}
}

// updatePages pushes current state into each page. Called after every
// mutation so pages render a consistent view without poking into the
// model themselves.
func (m *EcosystemModel) updatePages() {
	if m.deployments != nil {
		m.deployments.worktrees = m.worktrees
		m.deployments.driftingWT = m.driftingWorktree
		m.deployments.driftQueueTotal = m.driftQueueTotal
		m.deployments.driftQueueDone = m.driftQueueDone
		// Populate profile-by-worktree for the matrix cell.
		for _, w := range m.worktrees {
			if w.Workspace == nil {
				continue
			}
			if w.EnvState != nil && w.EnvState.Environment != "" {
				m.deployments.profiles[w.Workspace.Name] = w.EnvState.Environment
			}
		}
		if m.deployments.cursor >= len(m.worktrees) && len(m.worktrees) > 0 {
			m.deployments.cursor = len(m.worktrees) - 1
		}
	}

	shared := m.sharedProfileName()
	if m.shared != nil {
		m.shared.sharedName = shared
		m.shared.sharedResolved = m.resolvedFor(shared)
		m.shared.consumers = m.consumersOf(shared)
		m.shared.drift = m.sharedDrift()
		m.shared.artifacts = m.sharedArtifacts(shared)
	}

	if m.profiles != nil {
		m.profiles.cfg = m.cfg
		m.profiles.worktrees = m.worktrees
	}

	if m.orphans != nil {
		m.orphans.setContext(m.workspace, m.worktrees)
	}

	if m.actions != nil {
		m.actions.sharedProfile = shared
		m.actions.worktreeCount = len(m.worktrees)
		m.actions.driftingWT = m.driftingWorktree
	}
}

// resolvedFor returns the raw EnvironmentConfig for a named profile from
// the ecosystem's grove.toml. "" and "default" both resolve to the default
// environment block.
func (m *EcosystemModel) resolvedFor(name string) *config.EnvironmentConfig {
	if m.cfg == nil || name == "" {
		return nil
	}
	if name == "default" {
		return m.cfg.Environment
	}
	if m.cfg.Environments != nil {
		return m.cfg.Environments[name]
	}
	return nil
}

// consumersOf returns the list of profile names that reference `name` via
// `shared_env`. Used on the Shared Infra page for the "consumed by" line.
func (m *EcosystemModel) consumersOf(name string) []string {
	if m.cfg == nil || name == "" {
		return nil
	}
	var out []string
	for other, ec := range m.cfg.Environments {
		if ec == nil || other == name {
			continue
		}
		if ref, ok := ec.Config["shared_env"].(string); ok && ref == name {
			out = append(out, other)
		}
	}
	return out
}

// sharedDrift locates the drift cache for the shared profile. The cache
// lives at the ecosystem root's .grove/env/drift.json because that's where
// `grove env drift <shared>` writes it when run at the ecosystem level.
func (m *EcosystemModel) sharedDrift() *sharedDriftInfo {
	if m.workspace == nil {
		return nil
	}
	stateDir := fmt.Sprintf("%s/.grove/env", m.workspace.Path)
	summary, checkedAt, err := envdrift.LoadCache(stateDir)
	if err != nil || summary == nil {
		return nil
	}
	return &sharedDriftInfo{
		Add:       summary.Add,
		Change:    summary.Change,
		Destroy:   summary.Destroy,
		HasDrift:  summary.HasDrift,
		CheckedAt: checkedAt,
	}
}

// sharedArtifacts reuses deriveArtifacts from Phase 5b so the Shared Infra
// page renders the same sources-and-artifacts block the worktree Summary
// page does, keeping the two views visually consistent.
func (m *EcosystemModel) sharedArtifacts(name string) []ArtifactGroup {
	if m.cfg == nil || name == "" || m.workspace == nil {
		return nil
	}
	resolved := m.resolvedFor(name)
	if resolved == nil {
		return nil
	}
	all := map[string]*config.EnvironmentConfig{}
	if m.cfg.Environment != nil {
		all["default"] = m.cfg.Environment
	}
	for n, ec := range m.cfg.Environments {
		all[n] = ec
	}
	return deriveArtifacts(name, resolved.Provider, resolved, nil,
		m.workspace.Path, false, config.IsSharedProfile(m.cfg, name), all)
}

// firstEndpointByName is the Deployments-agnostic lookup used by the row
// action overlay — finds the named worktree and returns its first endpoint,
// or "(local only)" when there isn't one.
func firstEndpointByName(states []WorktreeState, name string) string {
	for _, w := range states {
		if w.Workspace != nil && w.Workspace.Name == name {
			return FirstEndpoint(w)
		}
	}
	return ""
}
