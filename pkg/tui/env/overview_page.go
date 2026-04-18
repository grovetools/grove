package env

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	envconfig "github.com/grovetools/core/config"
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
	// resolved is the per-profile config keyed by name. Used to derive the
	// per-row subline (description / synthesised summary).
	resolved map[string]*envconfig.EnvironmentConfig

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

// jumpToSummaryMsg is emitted when Enter is pressed on an overview row. The
// parent flips the pager to Summary (page 2) so the user sees the detail for
// the profile they just highlighted.
type jumpToSummaryMsg struct{}

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
	Enter key.Binding
}{
	Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Drift: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "drift check")),
	Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "jump to Summary")),
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
		case key.Matches(msg, overviewKeys.Enter):
			if len(p.profiles) == 0 {
				return p, nil
			}
			// Fire both: emit selection (in case the cursor moved without
			// publishing yet) and the jump message in one tick.
			sel := p.emitSelected()
			jump := func() tea.Msg { return jumpToSummaryMsg{} }
			return p, tea.Batch(sel, jump)
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
		provider := p.providers[profile]
		glyph, glyphStyle := p.rowGlyph(profile, provider)

		// Tags: [Sticky Default] / [Config Default] / [Active (Local)]
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

		cursor := "  "
		nameStyle := th.Muted
		if i == p.cursor {
			cursor = "> "
			nameStyle = th.Bold
		}
		if p.activeLocal != "" && profile == p.activeLocal {
			nameStyle = th.Success
		}

		// Row: glyph  name  (provider)  · <state string>  <tags>
		line := fmt.Sprintf("  %s%s %s", cursor, glyphStyle.Render(glyph), nameStyle.Render(profile))
		if provider != "" {
			line += th.Muted.Render(fmt.Sprintf("  (%s)", provider))
		}
		line += "  " + p.rowStateString(profile, provider)
		if len(tags) > 0 {
			line += "  " + th.Muted.Render(strings.Join(tags, " "))
		}

		// Drift / spinner / error suffix preserved as a right-side addendum.
		if profile == p.driftingProfile {
			line += "  " + p.spinner.View() + th.Muted.Render(" checking drift…")
		} else if err, ok := p.driftErrors[profile]; ok && err != nil {
			line += "  " + th.Error.Render(truncate(err.Error(), maxInt(p.width-40, 20)))
		}
		b.WriteString(line + "\n")

		// Subline with an overview-shaped one-liner (description / derived
		// summary). Indented under the glyph column so rows read as stacked
		// pairs rather than a wall of text.
		if sub := p.rowSubline(profile, provider); sub != "" {
			b.WriteString("      " + th.Muted.Render(sub) + "\n")
		}
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

	// Legend + key-hint footer matching the mockup. Legend lives above the
	// key hints so the glyphs it explains sit visually next to the rows.
	legend := fmt.Sprintf("%s local+cloud  %s cloud state  %s drifting  %s inactive",
		th.Success.Render("⚡"),
		th.Info.Render("☁"),
		th.Warning.Render("⚠"),
		th.Muted.Render("◯"),
	)
	b.WriteString("\n  " + th.Muted.Render(legend) + "\n")
	b.WriteString("  " + th.Muted.Render("↵ jump to Summary · D run drift · j/k navigate"))
	return b.String()
}

// rowGlyph maps a profile to its single-cell state glyph. Rule matches the
// worktree-mode audit design: ⚡ for the active local (plus cloud) profile,
// ⚠ when the drift cache reports drift, ☁ for cloud-state only, ◯ otherwise.
func (p *overviewPage) rowGlyph(profile, provider string) (string, lipgloss.Style) {
	th := theme.DefaultTheme
	if profile != "" && profile == p.activeLocal {
		return "⚡", th.Success
	}
	if provider == "terraform" {
		if sum, ok := p.driftResults[profile]; ok && sum != nil && sum.HasDrift {
			return "⚠", th.Warning
		}
		if sum, ok := p.driftResults[profile]; ok && sum != nil {
			return "☁", th.Info
		}
	}
	return "◯", th.Muted
}

// rowStateString mirrors the glyph with a short word-form the mockup uses
// so users can skim either column.
func (p *overviewPage) rowStateString(profile, provider string) string {
	th := theme.DefaultTheme
	if profile != "" && profile == p.activeLocal {
		return th.Success.Render("● running") + "  " + th.Info.Render("· ☁ applied")
	}
	if provider == "terraform" {
		if sum, ok := p.driftResults[profile]; ok && sum != nil && sum.HasDrift {
			return th.Warning.Render(fmt.Sprintf("drifting (+%d ~%d -%d)",
				sum.Add, sum.Change, sum.Destroy))
		}
		if _, ok := p.driftResults[profile]; ok {
			return th.Info.Render("☁ applied")
		}
	}
	return th.Muted.Render("inactive")
}

// rowSubline returns a short semantic descriptor for a profile. Prefers an
// explicit `description` key in the profile config; otherwise derives a
// terse phrase from the provider so the user gets at least one extra signal
// beyond the state word.
func (p *overviewPage) rowSubline(profile, provider string) string {
	ec := p.resolved[profile]
	if ec != nil {
		if d, ok := ec.Config["description"].(string); ok && d != "" {
			return d
		}
	}
	switch provider {
	case "docker":
		if ec != nil {
			if c, ok := ec.Config["compose_file"].(string); ok && c != "" {
				return "compose stack · " + c
			}
		}
		return "compose stack"
	case "native":
		return "native processes"
	case "terraform":
		if ec != nil {
			if s, ok := ec.Config["shared_env"].(string); ok && s != "" {
				return "terraform · reads " + s
			}
		}
		return "terraform"
	}
	return ""
}

func (p *overviewPage) Focus() tea.Cmd            { return nil }
func (p *overviewPage) Blur()                     {}
func (p *overviewPage) SetSize(width, height int) { p.width = width; p.height = height }
