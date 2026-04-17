package env

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/envdrift"
)

// overviewPage lists every discovered env profile, annotates each with its
// default / sticky / active / cloud markers, and lets the user fire a drift
// check against the currently highlighted profile. It is the landing tab.
type overviewPage struct {
	profiles      []string
	configDefault string            // e.g. "default" when grove.toml has an [environment] block
	stickyDefault string            // from .grove/state.yml "environment"
	activeLocal   string            // profile currently running in this worktree
	providers     map[string]string // profile -> provider ("terraform", "docker", …)

	state *env.EnvStateFile

	cursor          int
	spinner         spinner.Model
	driftingProfile string
	driftResults    map[string]*envdrift.DriftSummary
	driftErrors     map[string]error

	width  int
	height int
}

// profileSelectedMsg is emitted by the overview page when the cursor moves,
// so the parent model can re-resolve the Config & Provenance page for the
// newly-highlighted profile.
type profileSelectedMsg struct {
	profile string
}

// driftCheckFinishedMsg carries the result of an async drift run back into
// the model. summary is populated on success (including the no-drift case);
// err is populated when the drift engine itself failed.
type driftCheckFinishedMsg struct {
	profile string
	summary *envdrift.DriftSummary
	err     error
}

var overviewKeys = struct {
	Up    key.Binding
	Down  key.Binding
	Drift key.Binding
}{
	Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Drift: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "drift check")),
}

func newOverviewPage() *overviewPage {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = theme.DefaultTheme.Highlight
	return &overviewPage{
		spinner:      s,
		driftResults: make(map[string]*envdrift.DriftSummary),
		driftErrors:  make(map[string]error),
	}
}

func (p *overviewPage) Name() string  { return "Overview" }
func (p *overviewPage) TabID() string { return "overview" }

func (p *overviewPage) Init() tea.Cmd { return nil }

func (p *overviewPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, overviewKeys.Up):
			if len(p.profiles) == 0 {
				return p, nil
			}
			p.cursor = (p.cursor - 1 + len(p.profiles)) % len(p.profiles)
			return p, p.emitSelected()
		case key.Matches(msg, overviewKeys.Down):
			if len(p.profiles) == 0 {
				return p, nil
			}
			p.cursor = (p.cursor + 1) % len(p.profiles)
			return p, p.emitSelected()
		case key.Matches(msg, overviewKeys.Drift):
			profile := p.currentProfile()
			if profile == "" || p.driftingProfile != "" {
				return p, nil
			}
			// Only sensible for terraform-backed profiles; other providers
			// report an error immediately which we surface in the list.
			p.driftingProfile = profile
			return p, tea.Batch(
				p.spinner.Tick,
				runDriftCmd(context.Background(), profile),
			)
		}
	case spinner.TickMsg:
		// Keep ticking only while a drift is in flight; otherwise we leak
		// wake-ups that do no work.
		if p.driftingProfile == "" {
			return p, nil
		}
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(msg)
		return p, cmd
	}
	return p, nil
}

func (p *overviewPage) currentProfile() string {
	if p.cursor < 0 || p.cursor >= len(p.profiles) {
		return ""
	}
	return p.profiles[p.cursor]
}

func (p *overviewPage) emitSelected() tea.Cmd {
	profile := p.currentProfile()
	return func() tea.Msg {
		return profileSelectedMsg{profile: profile}
	}
}

func (p *overviewPage) View() string {
	th := theme.DefaultTheme
	if len(p.profiles) == 0 {
		return th.Muted.Render("  No environment profiles configured")
	}
	// Clamp cursor if profiles shrank.
	if p.cursor >= len(p.profiles) {
		p.cursor = len(p.profiles) - 1
	}

	var b strings.Builder
	b.WriteString(th.Bold.Render("  Profiles") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

	for i, profile := range p.profiles {
		var tags []string
		if p.configDefault != "" && profile == p.configDefault {
			tags = append(tags, "[Config Default]")
		}
		if p.stickyDefault != "" && profile == p.stickyDefault {
			tags = append(tags, "[Sticky Default]")
		}
		if p.activeLocal != "" && profile == p.activeLocal {
			tags = append(tags, "[Active (Local)]")
		}
		if prov := p.providers[profile]; prov == "terraform" {
			tags = append(tags, theme.IconEarth+" Cloud")
		}

		cursor := "  "
		nameStyle := th.Muted
		if i == p.cursor {
			cursor = "> "
			nameStyle = th.Bold
		}
		if p.activeLocal != "" && profile == p.activeLocal {
			nameStyle = th.Success
		}

		line := fmt.Sprintf("  %s%s", cursor, nameStyle.Render(profile))
		if prov := p.providers[profile]; prov != "" {
			line += th.Muted.Render(fmt.Sprintf("  (%s)", prov))
		}
		if len(tags) > 0 {
			line += "  " + th.Muted.Render(strings.Join(tags, " "))
		}

		if profile == p.driftingProfile {
			line += "  " + p.spinner.View() + th.Muted.Render(" checking drift…")
		} else if sum, ok := p.driftResults[profile]; ok && sum != nil {
			if sum.HasDrift {
				line += "  " + th.Warning.Render(fmt.Sprintf("Δ +%d ~%d -%d", sum.Add, sum.Change, sum.Destroy))
			} else {
				line += "  " + th.Success.Render("✓ in sync")
			}
		} else if err, ok := p.driftErrors[profile]; ok && err != nil {
			line += "  " + th.Error.Render(truncate(err.Error(), maxInt(p.width-40, 20)))
		}
		b.WriteString(line + "\n")
	}

	// Expand drift detail for the currently highlighted profile so the user
	// can skim the resource list without pressing another key.
	current := p.currentProfile()
	if sum, ok := p.driftResults[current]; ok && sum != nil && sum.HasDrift && len(sum.Resources) > 0 {
		b.WriteString("\n" + th.Bold.Render("  Drift details") + "\n")
		const maxShown = 10
		for i, r := range sum.Resources {
			if i >= maxShown {
				b.WriteString(th.Muted.Render(fmt.Sprintf("  … %d more\n", len(sum.Resources)-maxShown)))
				break
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", th.Muted.Render(padRight(r.Action, 10)), r.Address))
		}
	}

	b.WriteString("\n" + th.Muted.Render("  D run drift · j/k navigate"))
	return b.String()
}

func (p *overviewPage) Focus() tea.Cmd            { return nil }
func (p *overviewPage) Blur()                     {}
func (p *overviewPage) SetSize(width, height int) { p.width = width; p.height = height }
