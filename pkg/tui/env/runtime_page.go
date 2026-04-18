package env

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// runtimePage shows live runtime state (services + .env.local) for the
// profile currently scoped by the Overview cursor. If the scoped profile
// isn't the one actually running locally, a placeholder tells the user to
// press `u` instead — we do NOT show stale data from the wrong profile.
//
// Phase 5b: collapses the old Services and Variables pages into one.
type runtimePage struct {
	profile       string
	isRunning     bool
	workspaceRoot string

	state    *env.EnvStateFile
	response *env.EnvResponse
	loading  bool
	err      error

	width  int
	height int
}

func newRuntimePage() *runtimePage { return &runtimePage{} }

func (p *runtimePage) Name() string                            { return "Runtime" }
func (p *runtimePage) TabID() string                           { return "runtime" }
func (p *runtimePage) Init() tea.Cmd                           { return nil }
func (p *runtimePage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *runtimePage) Focus() tea.Cmd                           { return nil }
func (p *runtimePage) Blur()                                    {}
func (p *runtimePage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *runtimePage) View() string {
	th := theme.DefaultTheme

	if p.loading {
		return th.Muted.Render("  Loading runtime state...")
	}
	if p.err != nil {
		return th.Error.Render(fmt.Sprintf("  Error: %s", p.err))
	}
	if p.profile == "" {
		return th.Muted.Render("  Select a profile on Overview (page 1) to view runtime state.")
	}

	if !p.isRunning {
		return renderPlaceholder(fmt.Sprintf(
			"profile %s is not running — press u to start",
			th.Bold.Render(p.profile),
		), p.width)
	}

	var b strings.Builder

	// runtime state files block
	statePath := filepath.Join(safeRoot(p.workspaceRoot), ".grove/env/state.json")
	envPath := filepath.Join(safeRoot(p.workspaceRoot), ".env.local")
	b.WriteString(th.Bold.Render("  runtime state files") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
	b.WriteString(fmt.Sprintf("  %s %s  %s\n",
		th.Muted.Render(padRight("state.json", 14)),
		th.Info.Render(statePath),
		th.Muted.Render(fmt.Sprintf("env=%s", p.profile)),
	))
	b.WriteString(fmt.Sprintf("  %s %s\n",
		th.Muted.Render(padRight(".env.local", 14)),
		th.Info.Render(envPath),
	))

	// services table
	b.WriteString("\n" + th.Bold.Render("  services") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
	if p.state != nil && len(p.state.Services) > 0 {
		p.renderServices(&b)
	} else {
		b.WriteString("  " + th.Muted.Render("(no services reported)") + "\n")
	}

	// endpoints (if any)
	if p.response != nil && len(p.response.Endpoints) > 0 {
		b.WriteString("\n" + th.Bold.Render("  endpoints") + "\n")
		for _, ep := range p.response.Endpoints {
			b.WriteString(fmt.Sprintf("  %s %s\n", theme.IconEarth, th.Info.Render(ep)))
		}
	}

	// env.local variables
	vars := collectEnvVars(p.state, p.response)
	if len(vars) > 0 {
		b.WriteString("\n" + th.Bold.Render("  environment variables (.env.local)") + "\n")
		b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
		p.renderEnvVars(&b, vars)
	}

	// volumes
	if p.state != nil && len(p.state.Volumes) > 0 {
		b.WriteString("\n" + th.Bold.Render("  volumes") + "\n")
		for _, vol := range p.state.Volumes {
			suffix := ""
			if vol.Persist {
				suffix = th.Success.Render(" (persistent)")
			}
			b.WriteString(fmt.Sprintf("  %s %s%s\n",
				theme.IconFolder,
				th.Muted.Render(vol.Path),
				suffix,
			))
		}
	}

	return b.String()
}

func (p *runtimePage) renderServices(b *strings.Builder) {
	th := theme.DefaultTheme
	nameW := 10
	for _, svc := range p.state.Services {
		if len(svc.Name) > nameW {
			nameW = len(svc.Name)
		}
	}
	nameW += 2
	b.WriteString(fmt.Sprintf("  %s %s %s %s\n",
		th.Muted.Render(padRight("NAME", nameW)),
		th.Muted.Render(padRight("STATUS", 10)),
		th.Muted.Render(padRight("PORT", 8)),
		th.Muted.Render("COMMAND"),
	))
	for _, svc := range p.state.Services {
		icon, style := serviceStatusStyle(svc.Status)
		portStr := "—"
		if svc.Port > 0 {
			portStr = fmt.Sprintf("%d", svc.Port)
		}
		cmd := ""
		if p.state.ServiceCommands != nil {
			cmd = p.state.ServiceCommands[svc.Name]
		}
		statusCell := fmt.Sprintf("%s %s", icon, style.Render(svc.Status))
		b.WriteString(fmt.Sprintf("  %s %s %s %s\n",
			padRight(svc.Name, nameW),
			padRight(statusCell, 18),
			th.Muted.Render(padRight(portStr, 8)),
			th.Muted.Render(truncate(cmd, maxInt(p.width-nameW-30, 20))),
		))
	}
}

func (p *runtimePage) renderEnvVars(b *strings.Builder, vars map[string]string) {
	th := theme.DefaultTheme
	keys := make([]string, 0, len(vars))
	keyW := 0
	for k := range vars {
		keys = append(keys, k)
		if len(k) > keyW {
			keyW = len(k)
		}
	}
	sort.Strings(keys)
	keyW = maxInt(keyW+2, 16)
	maxVal := maxInt(p.width-keyW-6, 20)
	for _, k := range keys {
		v := vars[k]
		if v == "" {
			v = "<empty>"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n",
			th.Bold.Render(padRight(k, keyW)),
			th.Muted.Render(truncate(v, maxVal)),
		))
	}
}

// collectEnvVars merges vars from state (persisted) and response (live).
// response wins on conflict since it reflects the freshest call.
func collectEnvVars(state *env.EnvStateFile, resp *env.EnvResponse) map[string]string {
	out := make(map[string]string)
	if state != nil {
		for k, v := range state.EnvVars {
			out[k] = v
		}
	}
	if resp != nil {
		for k, v := range resp.EnvVars {
			out[k] = v
		}
	}
	return out
}

// renderPlaceholder draws a bordered hint the full width of the pane.
func renderPlaceholder(msg string, width int) string {
	th := theme.DefaultTheme
	bar := th.Muted.Render(strings.Repeat("─", maxInt(width-6, 20)))
	return fmt.Sprintf("\n  %s\n  %s\n  %s\n", bar, msg, bar)
}

// serviceStatusStyle picks the icon + lipgloss style for a service status.
func serviceStatusStyle(status string) (string, lipgloss.Style) {
	th := theme.DefaultTheme
	switch status {
	case "running":
		return theme.IconStatusRunning, th.Success
	case "stopped":
		return theme.IconStatusCompleted, th.Muted
	case "error":
		return theme.IconStatusFailed, th.Error
	default:
		return theme.IconPending, th.Muted
	}
}

func safeRoot(p string) string {
	if p == "" {
		return "."
	}
	return p
}
