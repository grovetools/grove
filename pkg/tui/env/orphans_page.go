package env

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// orphansPage renders the Orphans tab. True orphan detection needs cloud
// API calls (list GCS state prefixes, compare against active worktrees),
// which is a v2 feature. For v1 we just list local .grove/env/state.json
// files that don't belong to any known worktree — cheap, filesystem-only,
// and catches the "worktree deleted but state lingers" case.
type orphansPage struct {
	ecosystemPath string
	worktrees     []WorktreeState
	localOrphans  []string

	width  int
	height int
}

func newOrphansPage() *orphansPage { return &orphansPage{} }

func (p *orphansPage) Name() string                            { return "Orphans" }
func (p *orphansPage) TabID() string                           { return "orphans" }
func (p *orphansPage) Init() tea.Cmd                           { return nil }
func (p *orphansPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *orphansPage) Focus() tea.Cmd                           { return nil }
func (p *orphansPage) Blur()                                    {}
func (p *orphansPage) SetSize(width, height int)                { p.width = width; p.height = height }

// setContext wires the ecosystem root + worktree list, then runs the cheap
// local scan. Called by EcosystemModel.updatePages whenever state reloads.
func (p *orphansPage) setContext(ecosystem *workspace.WorkspaceNode, worktrees []WorktreeState) {
	p.worktrees = worktrees
	p.localOrphans = nil
	if ecosystem == nil {
		p.ecosystemPath = ""
		return
	}
	p.ecosystemPath = ecosystem.Path
	p.localOrphans = DetectLocalOrphans(ecosystem.Path, worktrees)
}

func (p *orphansPage) View() string {
	th := theme.DefaultTheme

	var b strings.Builder
	b.WriteString("  " + th.Bold.Render("orphan detection (v2)") + "\n")
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 20))) + "\n")
	b.WriteString("  " + th.Muted.Render(
		"Cloud-backed orphan detection is not yet implemented. It will enumerate "+
			"state prefixes under each profile's state_bucket and flag any that "+
			"don't correspond to an active worktree or the shared-infra profile.") + "\n")

	b.WriteString("\n  " + th.Bold.Render("potential local orphans") + "\n")
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 20))) + "\n")
	if len(p.localOrphans) == 0 {
		b.WriteString("  " + th.Muted.Render("None found — every state.json under "+
			".grove-worktrees/ is mapped to a discovered worktree.") + "\n")
		return b.String()
	}
	for _, path := range p.localOrphans {
		b.WriteString(fmt.Sprintf("  %s %s\n",
			th.Warning.Render("⚠"),
			th.Info.Render(path),
		))
	}
	b.WriteString("\n  " + th.Muted.Render(
		"Tip: these are likely from deleted worktrees. Investigate and run `grove env down` "+
			"from the remnant path to clean up, or delete the directory manually.") + "\n")
	return b.String()
}

