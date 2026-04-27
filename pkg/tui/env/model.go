// Package env renders the shrunk `grove env tui` — a single-screen,
// live-refreshing grid that mirrors the web dashboard served by the global
// grove daemon. Both surfaces consume the same `/api/dashboard/state`
// payload, so we only keep one data aggregator (the daemon's) and one
// rendering code path (this model + the browser SPA).
package env

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/workspace"
)

// Config is the TUI factory for worktree-mode callers. The fields match the
// pre-shrink envtui.Config so grove/cmd/env_tui.go keeps compiling; any of
// them may be nil without affecting behavior beyond the obvious (no ecosystem
// filter when InitialFocus is nil, etc).
type Config struct {
	DaemonClient daemon.Client
	InitialFocus *workspace.WorkspaceNode
	Cfg          *config.Config
	// Hosted flags that this model is embedded inside another TUI host
	// (treemux's EnvPanel). Currently a no-op — the shrunk model does not
	// attach a full-screen alt-buffer — kept on the Config surface so
	// treemux compiles unchanged.
	Hosted bool
}

// EcosystemConfig is the TUI factory for ecosystem-root callers. Fields
// mirror Config; kept as a distinct type so env_tui.go's dispatch stays
// self-documenting (and so we can diverge the two later without a breaking
// API change).
type EcosystemConfig struct {
	DaemonClient daemon.Client
	Root         *workspace.WorkspaceNode
	Cfg          *config.Config
}

// Model is exported so embedding hosts (treemux's EnvPanel) can keep a
// typed reference to the inner model rather than a bare tea.Model.
type Model = model

// New returns a tea.Model for worktree-mode launches. Implementation-wise
// there is no longer any difference vs ecosystem-mode — the model always
// renders the whole ecosystem grid — but we highlight the focused worktree
// if one was supplied.
func New(cfg Config) Model {
	focus := ""
	if cfg.InitialFocus != nil {
		focus = cfg.InitialFocus.Name
	}
	return newModel(focus)
}

// NewEcosystem returns a tea.Model for ecosystem-mode launches. Identical
// to New; both pivot on the same aggregated state.
func NewEcosystem(cfg EcosystemConfig) Model {
	focus := ""
	if cfg.Root != nil {
		focus = cfg.Root.Name
	}
	return newModel(focus)
}

// ---- internal model ----

type refreshMsg struct {
	state *dashboardState
	err   error
}

type tickMsg struct{}

type model struct {
	focus     string
	state     *dashboardState
	err       error
	lastFetch time.Time
	quitting  bool
	width     int
	height    int
}

func newModel(focus string) model {
	return model{focus: focus}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(), tick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, fetchCmd()
		case "d":
			return m, openDashboardCmd()
		}
	case refreshMsg:
		m.state = msg.state
		m.err = msg.err
		m.lastFetch = time.Now()
		return m, nil
	case tickMsg:
		return m, tea.Batch(fetchCmd(), tick())
	}
	return m, nil
}

// Close satisfies the embed host's teardown contract (treemux's EnvPanel).
// The shrunk model has no background goroutines or SSE streams, so this
// is a no-op.
func (m model) Close() error { return nil }

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.state == nil && m.err == nil {
		return "loading…  (q quit · r refresh · d open browser)"
	}
	if m.err != nil {
		return fmt.Sprintf("error: %v\n\n(q quit · r retry · d open browser)", m.err)
	}
	return m.renderGrid()
}

func (m model) renderGrid() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("grove env tui"))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("updated %s", m.lastFetch.Format("15:04:05"))))
	b.WriteString("\n\n")

	eco := m.selectedEcosystem()
	if eco == nil {
		b.WriteString("no ecosystems detected")
		return b.String()
	}

	running := 0
	for _, w := range eco.Worktrees {
		if w.State == "running" {
			running++
		}
	}
	b.WriteString(fmt.Sprintf("ecosystem: %s    %d/%d running\n",
		boldStyle.Render(eco.Name), running, len(eco.Worktrees)))
	b.WriteString(strings.Repeat("─", 64) + "\n")

	for _, wt := range eco.Worktrees {
		b.WriteString(m.renderWorktreeRow(wt))
		b.WriteString("\n")
	}

	if len(eco.Worktrees) == 0 {
		b.WriteString(mutedStyle.Render("  (no worktrees)") + "\n")
	}

	// Shared infra + orphans
	if eco.SharedInfra != nil {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("shared infra:"))
		b.WriteString(fmt.Sprintf(" %s (%s)\n", eco.SharedInfra.Profile, eco.SharedInfra.Provider))
	}
	if len(eco.Orphans) > 0 {
		b.WriteString("\n" + mutedStyle.Render(fmt.Sprintf("orphans (%d):", len(eco.Orphans))) + "\n")
		for _, o := range eco.Orphans {
			b.WriteString(fmt.Sprintf("  %s  %s\n", o.Name, mutedStyle.Render(o.Category)))
		}
	}

	b.WriteString("\n" + mutedStyle.Render("q quit · r refresh · d open browser dashboard"))
	return b.String()
}

func (m model) renderWorktreeRow(wt dashboardWorktree) string {
	name := wt.Name
	if wt.Name == m.focus {
		name = focusStyle.Render("▸ " + name)
	} else {
		name = "  " + name
	}
	dot := stateDot(wt.State)
	profile := wt.Profile
	if profile == "" {
		profile = "-"
	}
	svcs := ""
	for _, s := range wt.Services {
		svcs += serviceDot(s.Status)
	}
	eps := ""
	for i, e := range wt.Endpoints {
		if i > 0 {
			eps += " "
		}
		eps += e.Name
	}
	return fmt.Sprintf("%-32s  %s %-9s  %-16s  %-10s  %s",
		name, dot, wt.State, profile, svcs, mutedStyle.Render(eps))
}

func (m model) selectedEcosystem() *dashboardEcosystem {
	if m.state == nil || len(m.state.Ecosystems) == 0 {
		return nil
	}
	for i := range m.state.Ecosystems {
		if m.state.Ecosystems[i].Name == m.focus {
			return &m.state.Ecosystems[i]
		}
	}
	return &m.state.Ecosystems[0]
}

// ---- styling ----

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#58a6ff"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	boldStyle   = lipgloss.NewStyle().Bold(true)
	focusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff"))
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149"))
	greyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6e7681"))
)

func stateDot(state string) string {
	switch state {
	case "running":
		return greenStyle.Render("●")
	case "error":
		return redStyle.Render("●")
	default:
		return greyStyle.Render("●")
	}
}

func serviceDot(status string) string {
	switch status {
	case "running":
		return greenStyle.Render("●")
	case "error":
		return redStyle.Render("●")
	default:
		return greyStyle.Render("●")
	}
}

// ---- commands ----

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func fetchCmd() tea.Cmd {
	return func() tea.Msg {
		s, err := fetchDashboardState()
		return refreshMsg{state: s, err: err}
	}
}

func openDashboardCmd() tea.Cmd {
	return func() tea.Msg {
		// Fire-and-forget: the user just wants the browser to open, and we
		// deliberately don't block the TUI on the exec. Errors surface on
		// stderr once the TUI exits.
		cmd := exec.Command("grove", "env", "dashboard")
		_ = cmd.Start()
		return nil
	}
}

// ---- state fetch ----

type dashboardState struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Ecosystems  []dashboardEcosystem `json:"ecosystems"`
	Errors      []string             `json:"errors,omitempty"`
}

type dashboardEcosystem struct {
	Name        string              `json:"name"`
	Path        string              `json:"path"`
	Worktrees   []dashboardWorktree `json:"worktrees"`
	SharedInfra *dashboardShared    `json:"shared_infra,omitempty"`
	Orphans     []dashboardOrphan   `json:"orphans"`
}

type dashboardWorktree struct {
	Name      string              `json:"name"`
	Path      string              `json:"path"`
	Profile   string              `json:"profile,omitempty"`
	Provider  string              `json:"provider,omitempty"`
	State     string              `json:"state"`
	Endpoints []dashboardEndpoint `json:"endpoints"`
	Services  []dashboardService  `json:"services"`
}

type dashboardEndpoint struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	OK   bool   `json:"ok"`
}

type dashboardService struct {
	Name   string `json:"name"`
	Port   int    `json:"port,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Status string `json:"status"`
}

type dashboardShared struct {
	Profile  string `json:"profile"`
	Provider string `json:"provider,omitempty"`
}

type dashboardOrphan struct {
	Category string `json:"category"`
	Name     string `json:"name"`
}

// fetchDashboardState reads the dashboard port file written by the global
// daemon and does a GET against the aggregated state endpoint. Returns a
// helpful error when the port file is missing — typically means the global
// daemon is not running yet.
func fetchDashboardState() (*dashboardState, error) {
	port, err := readDashboardPort()
	if err != nil {
		return nil, fmt.Errorf("read dashboard port: %w (start the global grove daemon or run `grove env dashboard` once)", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/dashboard/state?probe=0", port)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dashboard state: HTTP %d", resp.StatusCode)
	}
	var out dashboardState
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func readDashboardPort() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".local", "state", "grove", "dashboard.port"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
