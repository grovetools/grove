package env

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// sharedInfraPage renders the detail view for the single shared-infra
// profile in an ecosystem (e.g., `terraform-infra`). The profile is chosen
// by config.IsSharedProfile; if none is found the page renders a
// placeholder instead of picking an arbitrary profile.
type sharedInfraPage struct {
	sharedName     string
	sharedResolved *config.EnvironmentConfig
	artifacts      []ArtifactGroup
	consumers      []string
	drift          *sharedDriftInfo

	width  int
	height int
}

// sharedDriftInfo is the cached drift summary for the shared profile plus
// the age of the cache. Drift data on shared infra lives in the ecosystem
// root's own .grove/env/drift.json, not in any worktree.
type sharedDriftInfo struct {
	Add, Change, Destroy int
	HasDrift             bool
	CheckedAt            time.Time
}

func newSharedInfraPage() *sharedInfraPage { return &sharedInfraPage{} }

func (p *sharedInfraPage) Name() string                            { return "Shared Infra" }
func (p *sharedInfraPage) TabID() string                           { return "shared" }
func (p *sharedInfraPage) Init() tea.Cmd                           { return nil }
func (p *sharedInfraPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *sharedInfraPage) Focus() tea.Cmd                           { return nil }
func (p *sharedInfraPage) Blur()                                    {}
func (p *sharedInfraPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *sharedInfraPage) View() string {
	th := theme.DefaultTheme
	if p.sharedName == "" {
		return "  " + th.Muted.Render(
			"No shared-infra profile defined in this ecosystem. "+
				"Mark a profile `shared = true` or point another profile's "+
				"`shared_env` at it to surface detail here.")
	}

	var b strings.Builder
	b.WriteString("  " + th.Bold.Render("profile") + "  " +
		th.Highlight.Render(p.sharedName) +
		"  " + th.Muted.Render("(shared across ecosystem)") + "\n")

	provider := "—"
	if p.sharedResolved != nil && p.sharedResolved.Provider != "" {
		provider = p.sharedResolved.Provider
	}
	b.WriteString("  " + th.Muted.Render(padRight("provider", 14)) + "  " + provider + "\n")

	status := "—"
	if p.drift != nil {
		if p.drift.HasDrift {
			status = th.Warning.Render("drift detected")
		} else {
			status = th.Success.Render("in sync")
		}
	}
	b.WriteString("  " + th.Muted.Render(padRight("status", 14)) + "  " + status + "\n")

	if p.sharedResolved != nil {
		if bucket, _ := p.sharedResolved.Config["state_bucket"].(string); bucket != "" {
			b.WriteString("  " + th.Muted.Render(padRight("state_bucket", 14)) + "  " +
				th.Info.Render(bucket) + "\n")
		}
		if len(p.sharedResolved.DisplayResources) > 0 {
			b.WriteString("  " + th.Muted.Render(padRight("resources", 14)) + "  " +
				strings.Join(p.sharedResolved.DisplayResources, ", ") + "\n")
		}
	}

	if len(p.consumers) > 0 {
		b.WriteString("  " + th.Muted.Render(padRight("consumed by", 14)) + "  " +
			strings.Join(p.consumers, ", ") + "\n")
	}

	// Last activity surfaces the drift-cache age — the only timestamp we
	// have out of the box for a shared profile. "—" when no cache exists
	// yet so the row isn't silently missing.
	activity := "—"
	if p.drift != nil && !p.drift.CheckedAt.IsZero() {
		activity = humanAge(time.Since(p.drift.CheckedAt)) + " ago (drift checked)"
	}
	b.WriteString("  " + th.Muted.Render(padRight("last activity", 14)) + "  " + activity + "\n")

	// Drift panel.
	b.WriteString("\n  " + th.Bold.Render("drift") + "\n")
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 20))) + "\n")
	if p.drift == nil || p.drift.CheckedAt.IsZero() {
		b.WriteString("  " + th.Muted.Render("not yet checked — press D to refresh") + "\n")
	} else {
		plusStyle := th.Muted
		changeStyle := th.Muted
		delStyle := th.Muted
		if p.drift.Add > 0 {
			plusStyle = th.Success
		}
		if p.drift.Change > 0 {
			changeStyle = th.Warning
		}
		if p.drift.Destroy > 0 {
			delStyle = th.Error
		}
		summary := fmt.Sprintf("%s %s %s   %s",
			plusStyle.Render(fmt.Sprintf("+ %d add", p.drift.Add)),
			changeStyle.Render(fmt.Sprintf("~ %d change", p.drift.Change)),
			delStyle.Render(fmt.Sprintf("- %d destroy", p.drift.Destroy)),
			th.Muted.Render("checked "+humanAge(time.Since(p.drift.CheckedAt))+" ago"),
		)
		b.WriteString("  " + summary + "\n")
	}

	// Sources & artifacts reuses the Summary tab helper so ecosystem-mode
	// and worktree-mode render identical path groups for the same profile.
	if len(p.artifacts) > 0 {
		b.WriteString("\n  " + th.Bold.Render("sources & artifacts") + "\n")
		b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 20))) + "\n")
		for _, g := range p.artifacts {
			b.WriteString(renderArtifactGroup(g, p.width))
		}
	}

	return b.String()
}
