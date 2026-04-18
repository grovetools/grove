package env

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/envdrift"
)

// driftPage renders a per-profile drift summary. It has no Update logic —
// `D` still fires on the Overview page (that's where the action makes sense
// because the spinner inlines beside the profile). This page simply shows
// whatever the latest driftResults[profile] holds.
//
// For non-terraform profiles it shows a placeholder explaining drift only
// applies to terraform. Avoids spurious "not checked yet" confusion on
// docker/native profiles where drift is conceptually N/A.
type driftPage struct {
	profile  string
	provider string

	summary *envdrift.DriftSummary
	err     error
	pending bool // true when a drift check is in flight for this profile

	width  int
	height int
}

func newDriftPage() *driftPage { return &driftPage{} }

func (p *driftPage) Name() string                            { return "Drift" }
func (p *driftPage) TabID() string                           { return "drift" }
func (p *driftPage) Init() tea.Cmd                           { return nil }
func (p *driftPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *driftPage) Focus() tea.Cmd                           { return nil }
func (p *driftPage) Blur()                                    {}
func (p *driftPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *driftPage) View() string {
	th := theme.DefaultTheme

	if p.profile == "" {
		return th.Muted.Render("  Select a profile on Overview (page 1) to view drift.")
	}
	if p.provider != "terraform" {
		return renderPlaceholder(fmt.Sprintf(
			"%s is %s-backed — drift check only applies to terraform profiles",
			th.Bold.Render(p.profile), nonEmpty(p.provider, "unknown"),
		), p.width)
	}

	var b strings.Builder
	b.WriteString(th.Bold.Render(fmt.Sprintf("  Drift — %s", p.profile)) + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")

	if p.pending {
		b.WriteString("  " + th.Muted.Render("checking drift — this can take 10–30s (runs terraform plan)") + "\n")
		return b.String()
	}
	if p.err != nil {
		b.WriteString("  " + th.Error.Render(p.err.Error()) + "\n")
		b.WriteString("\n  " + th.Muted.Render("Press D on Overview to retry.") + "\n")
		return b.String()
	}
	if p.summary == nil {
		b.WriteString("  " + th.Muted.Render("no drift check run yet — press D on Overview to check") + "\n")
		return b.String()
	}

	// summary bar
	b.WriteString(p.renderSummaryBar() + "\n")

	// resources
	if len(p.summary.Resources) == 0 {
		if p.summary.HasDrift {
			b.WriteString("  " + th.Muted.Render("(no resource list returned)") + "\n")
		} else {
			b.WriteString("  " + th.Success.Render("No drift — cloud state matches config.") + "\n")
		}
	} else {
		b.WriteString("\n  " + th.Muted.Render(fmt.Sprintf("%-10s %s", "ACTION", "RESOURCE")) + "\n")
		for _, r := range p.summary.Resources {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				th.Muted.Render(padRight(r.Action, 10)),
				r.Address,
			))
		}
	}

	b.WriteString("\n  " + th.Muted.Render("Press D on Overview to refresh · exits 0/1/2 match terraform plan -detailed-exitcode") + "\n")
	return b.String()
}

func (p *driftPage) renderSummaryBar() string {
	th := theme.DefaultTheme
	addStyle, changeStyle, delStyle := th.Success, th.Warning, th.Error
	if p.summary.Add == 0 {
		addStyle = th.Muted
	}
	if p.summary.Change == 0 {
		changeStyle = th.Muted
	}
	if p.summary.Destroy == 0 {
		delStyle = th.Muted
	}
	return fmt.Sprintf("  %s  %s  %s",
		addStyle.Render(fmt.Sprintf("+ %d add", p.summary.Add)),
		changeStyle.Render(fmt.Sprintf("~ %d change", p.summary.Change)),
		delStyle.Render(fmt.Sprintf("- %d destroy", p.summary.Destroy)),
	)
}
