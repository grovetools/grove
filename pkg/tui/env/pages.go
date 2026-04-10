package env

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// ---------------------------------------------------------------------------
// Services page
// ---------------------------------------------------------------------------

type servicesPage struct {
	state    *env.EnvStateFile
	response *env.EnvResponse
	loading  bool
	err      error
	width    int
	height   int
}

func (p *servicesPage) Name() string  { return "Services" }
func (p *servicesPage) TabID() string { return "services" }

func (p *servicesPage) Init() tea.Cmd { return nil }

func (p *servicesPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	return p, nil
}

func (p *servicesPage) View() string {
	th := theme.DefaultTheme

	if p.loading {
		return th.Muted.Render("  Loading environment status...")
	}
	if p.err != nil {
		return th.Error.Render(fmt.Sprintf("  Error: %s", p.err))
	}
	if p.state == nil && p.response == nil {
		return th.Muted.Render("  No active environment")
	}

	var b strings.Builder

	// Services table
	if p.state != nil && len(p.state.Services) > 0 {
		b.WriteString(th.Bold.Render("  Services") + "\n")
		b.WriteString(th.Muted.Render("  " + strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

		nameWidth := 12
		for _, svc := range p.state.Services {
			if len(svc.Name) > nameWidth {
				nameWidth = len(svc.Name)
			}
		}
		nameWidth = maxInt(nameWidth+2, 14)

		// Header
		b.WriteString(fmt.Sprintf("  %s %s %s\n",
			th.Muted.Render(padRight("NAME", nameWidth)),
			th.Muted.Render(padRight("PORT", 8)),
			th.Muted.Render("STATUS"),
		))

		for _, svc := range p.state.Services {
			icon, style := serviceStatusStyle(svc.Status)
			portStr := "—"
			if svc.Port > 0 {
				portStr = fmt.Sprintf("%d", svc.Port)
			}
			b.WriteString(fmt.Sprintf("  %s %s %s %s\n",
				padRight(svc.Name, nameWidth),
				th.Muted.Render(padRight(portStr, 8)),
				icon,
				style.Render(svc.Status),
			))
		}
	} else if p.state != nil && len(p.state.Ports) > 0 {
		// Fallback: show ports if no structured services
		b.WriteString(th.Bold.Render("  Ports") + "\n")
		b.WriteString(th.Muted.Render("  " + strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

		nameWidth := 12
		names := make([]string, 0, len(p.state.Ports))
		for name := range p.state.Ports {
			names = append(names, name)
			if len(name) > nameWidth {
				nameWidth = len(name)
			}
		}
		sort.Strings(names)
		nameWidth = maxInt(nameWidth+2, 14)

		for _, name := range names {
			port := p.state.Ports[name]
			b.WriteString(fmt.Sprintf("  %s %s\n",
				padRight(name, nameWidth),
				th.Muted.Render(fmt.Sprintf(":%d", port)),
			))
		}
	}

	// Endpoints
	if p.response != nil && len(p.response.Endpoints) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(th.Bold.Render("  Endpoints") + "\n")
		for _, ep := range p.response.Endpoints {
			b.WriteString(fmt.Sprintf("  %s %s\n", theme.IconEarth, th.Info.Render(ep)))
		}
	}

	// Volumes
	if p.state != nil && len(p.state.Volumes) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(th.Bold.Render("  Volumes") + "\n")
		for _, vol := range p.state.Volumes {
			persist := ""
			if vol.Persist {
				persist = th.Success.Render(" (persistent)")
			}
			b.WriteString(fmt.Sprintf("  %s %s%s\n",
				theme.IconFolder,
				th.Muted.Render(vol.Path),
				persist,
			))
		}
	}

	if b.Len() == 0 {
		status := "stopped"
		if p.response != nil {
			status = p.response.Status
		}
		return th.Muted.Render(fmt.Sprintf("  Environment is %s", status))
	}

	return b.String()
}

func (p *servicesPage) Focus() tea.Cmd             { return nil }
func (p *servicesPage) Blur()                       {}
func (p *servicesPage) SetSize(width, height int)   { p.width = width; p.height = height }

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

// ---------------------------------------------------------------------------
// Variables page
// ---------------------------------------------------------------------------

type variablesPage struct {
	state    *env.EnvStateFile
	response *env.EnvResponse
	loading  bool
	width    int
	height   int
}

func (p *variablesPage) Name() string  { return "Variables" }
func (p *variablesPage) TabID() string { return "variables" }

func (p *variablesPage) Init() tea.Cmd { return nil }

func (p *variablesPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	return p, nil
}

func (p *variablesPage) View() string {
	th := theme.DefaultTheme

	if p.loading {
		return th.Muted.Render("  Loading...")
	}

	// Collect env vars from state or response
	vars := make(map[string]string)
	if p.state != nil && p.state.EnvVars != nil {
		for k, v := range p.state.EnvVars {
			vars[k] = v
		}
	}
	if p.response != nil && p.response.EnvVars != nil {
		for k, v := range p.response.EnvVars {
			vars[k] = v
		}
	}

	if len(vars) == 0 {
		return th.Muted.Render("  No environment variables")
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(th.Bold.Render("  Environment Variables") + "\n")
	b.WriteString(th.Muted.Render("  " + strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

	keyWidth := 0
	for _, k := range keys {
		if len(k) > keyWidth {
			keyWidth = len(k)
		}
	}
	keyWidth = maxInt(keyWidth+2, 16)

	maxValWidth := maxInt(p.width-keyWidth-6, 20)

	for _, k := range keys {
		v := vars[k]
		displayVal := truncate(v, maxValWidth)
		b.WriteString(fmt.Sprintf("  %s %s\n",
			th.Bold.Render(padRight(k, keyWidth)),
			th.Muted.Render(displayVal),
		))
	}

	return b.String()
}

func (p *variablesPage) Focus() tea.Cmd           { return nil }
func (p *variablesPage) Blur()                     {}
func (p *variablesPage) SetSize(width, height int) { p.width = width; p.height = height }

// ---------------------------------------------------------------------------
// Actions page
// ---------------------------------------------------------------------------

type actionsPage struct {
	state    *env.EnvStateFile
	profiles []string
	commands map[string]string
	loading  bool
	width    int
	height   int
}

func (p *actionsPage) Name() string  { return "Actions" }
func (p *actionsPage) TabID() string { return "actions" }

func (p *actionsPage) Init() tea.Cmd { return nil }

func (p *actionsPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	return p, nil
}

func (p *actionsPage) View() string {
	th := theme.DefaultTheme

	if p.loading {
		return th.Muted.Render("  Loading...")
	}

	var b strings.Builder

	// Action keys
	b.WriteString(th.Bold.Render("  Actions") + "\n")
	b.WriteString(th.Muted.Render("  " + strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

	actions := []struct {
		key  string
		desc string
	}{
		{"u", "Start environment (env up)"},
		{"d", "Stop environment (env down)"},
		{"r", "Restart environment"},
	}

	for _, a := range actions {
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			th.Info.Render(padRight(a.key, 4)),
			a.desc,
		))
	}

	// Custom commands
	if len(p.commands) > 0 {
		b.WriteString("\n" + th.Bold.Render("  Commands") + "\n")
		b.WriteString(th.Muted.Render("  " + strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

		names := make([]string, 0, len(p.commands))
		for name := range p.commands {
			names = append(names, name)
		}
		sort.Strings(names)

		nameWidth := 0
		for _, name := range names {
			if len(name) > nameWidth {
				nameWidth = len(name)
			}
		}
		nameWidth = maxInt(nameWidth+2, 12)

		for _, name := range names {
			cmd := p.commands[name]
			b.WriteString(fmt.Sprintf("  %s %s\n",
				th.Bold.Render(padRight(name, nameWidth)),
				th.Muted.Render(truncate(cmd, maxInt(p.width-nameWidth-6, 20))),
			))
		}
	}

	// Profiles
	if len(p.profiles) > 0 {
		b.WriteString("\n" + th.Bold.Render("  Profiles") + "\n")
		b.WriteString(th.Muted.Render("  " + strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

		activeProfile := ""
		if p.state != nil {
			activeProfile = p.state.Environment
		}

		for _, profile := range p.profiles {
			marker := "  "
			style := th.Muted
			if profile == activeProfile {
				marker = theme.IconStatusRunning + " "
				style = th.Success
			}
			b.WriteString(fmt.Sprintf("  %s%s\n", marker, style.Render(profile)))
		}
	}

	return b.String()
}

func (p *actionsPage) Focus() tea.Cmd           { return nil }
func (p *actionsPage) Blur()                     {}
func (p *actionsPage) SetSize(width, height int) { p.width = width; p.height = height }
