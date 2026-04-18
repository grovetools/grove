package env

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// summaryPage renders the "Summary" tab for the profile currently scoped
// by the Overview cursor: provider/status k/v, a one-liner preview of
// what `u` would do, and the sources & artifacts path groups.
type summaryPage struct {
	profile   string
	provider  string
	isRunning bool
	resolved  *config.EnvironmentConfig
	artifacts []ArtifactGroup

	width  int
	height int
}

func newSummaryPage() *summaryPage { return &summaryPage{} }

func (p *summaryPage) Name() string                            { return "Summary" }
func (p *summaryPage) TabID() string                           { return "summary" }
func (p *summaryPage) Init() tea.Cmd                           { return nil }
func (p *summaryPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *summaryPage) Focus() tea.Cmd                           { return nil }
func (p *summaryPage) Blur()                                    {}
func (p *summaryPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *summaryPage) View() string {
	th := theme.DefaultTheme
	if p.profile == "" {
		return th.Muted.Render("  Select a profile on Overview (page 1) to view its summary.")
	}

	var b strings.Builder

	// key/value block
	status := "inactive"
	if p.isRunning {
		status = "running"
	}
	p.kv(&b, "provider", nonEmpty(p.provider, "(unknown)"))
	p.kv(&b, "status", status)
	p.kv(&b, "services", summariseServices(p.resolved))
	p.kv(&b, "managed by", managedBy(p.resolved))

	// preview of `u`
	b.WriteString("\n" + th.Bold.Render("  what happens on u") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
	b.WriteString("  " + previewUp(p.profile, p.provider, p.resolved) + "\n")

	// sources & artifacts
	if len(p.artifacts) > 0 {
		b.WriteString("\n" + th.Bold.Render("  sources & artifacts") + "\n")
		b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
		for _, g := range p.artifacts {
			b.WriteString(renderArtifactGroup(g, p.width))
		}
	}

	return b.String()
}

// kv writes a single label/value row with a muted label.
func (p *summaryPage) kv(b *strings.Builder, label, value string) {
	th := theme.DefaultTheme
	b.WriteString(fmt.Sprintf("  %s %s\n",
		th.Muted.Render(padRight(label, 14)),
		value,
	))
}

// renderArtifactGroup renders one ArtifactGroup: a group label followed by
// "  <label-col> <path> <anno>" rows. Path color varies with Kind.
func renderArtifactGroup(g ArtifactGroup, width int) string {
	th := theme.DefaultTheme
	var b strings.Builder
	b.WriteString("  " + th.Muted.Render(strings.ToUpper(g.Group)) + "\n")
	for _, r := range g.Rows {
		pathStyle := th.Info
		switch r.Kind {
		case "remote":
			pathStyle = th.Highlight
		case "generated":
			pathStyle = th.Warning
		case "missing":
			pathStyle = lipgloss.NewStyle().Strikethrough(true).Foreground(th.Muted.GetForeground())
		}
		line := fmt.Sprintf("  %s %s",
			th.Muted.Render(padRight(r.Label, 14)),
			pathStyle.Render(r.Path),
		)
		if r.Anno != "" {
			line += "  " + th.Muted.Render(r.Anno)
		}
		b.WriteString(truncate(line, maxInt(width*2, 80)) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// summariseServices joins the service names declared under
// `resolved.Config["services"]` for a quick overview.
func summariseServices(resolved *config.EnvironmentConfig) string {
	if resolved == nil {
		return "(none)"
	}
	svcs, ok := resolved.Config["services"].(map[string]interface{})
	if !ok || len(svcs) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(svcs))
	for k := range svcs {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

// managedBy honors the Shared flag in EnvironmentConfig; otherwise falls
// back to "—".
func managedBy(resolved *config.EnvironmentConfig) string {
	if resolved == nil {
		return "—"
	}
	if resolved.Shared != nil && *resolved.Shared {
		return "shared infra"
	}
	return "—"
}

// previewUp crafts a short, human-readable "what happens on u" sentence.
// It leans on the provider; unknown providers get a generic line.
func previewUp(profile, provider string, resolved *config.EnvironmentConfig) string {
	switch provider {
	case "native":
		return "Start the configured services as local processes and write .env.local + state.json."
	case "docker":
		compose := "docker-compose.yml"
		if resolved != nil {
			if c, ok := resolved.Config["compose_file"].(string); ok && c != "" {
				compose = c
			}
		}
		return fmt.Sprintf("Bring up %s in containers (docker compose up).", compose)
	case "terraform":
		if resolved != nil {
			if _, ok := resolved.Config["shared_env"].(string); ok {
				return "Read shared TF outputs, then apply this profile's TF and start any local services."
			}
		}
		return "Apply this profile's TF state and start any local services backed by it."
	default:
		return fmt.Sprintf("Bring up profile %q (provider %s).", profile, nonEmpty(provider, "unknown"))
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
