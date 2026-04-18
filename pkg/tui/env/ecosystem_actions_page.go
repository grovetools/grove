package env

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// ecosystemActionsPage shows the ecosystem-scoped hotkeys: u (apply shared
// infra), D (drift shared infra), A (drift all worktrees), W (worktree
// picker). Kept separate from the worktree-mode actionsPage so the two
// action sets don't compete for key bindings inside one file.
type ecosystemActionsPage struct {
	sharedProfile string
	worktreeCount int
	driftingWT    string

	width  int
	height int
}

func newEcosystemActionsPage() *ecosystemActionsPage { return &ecosystemActionsPage{} }

func (p *ecosystemActionsPage) Name() string                            { return "Actions" }
func (p *ecosystemActionsPage) TabID() string                           { return "actions" }
func (p *ecosystemActionsPage) Init() tea.Cmd                           { return nil }
func (p *ecosystemActionsPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *ecosystemActionsPage) Focus() tea.Cmd                           { return nil }
func (p *ecosystemActionsPage) Blur()                                    {}
func (p *ecosystemActionsPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *ecosystemActionsPage) View() string {
	th := theme.DefaultTheme
	var b strings.Builder

	b.WriteString("  " + th.Bold.Render("ecosystem-wide") + "\n")
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 20))) + "\n")

	sharedTarget := p.sharedProfile
	sharedDisabled := sharedTarget == ""
	if sharedTarget == "" {
		sharedTarget = "(no shared profile)"
	}
	p.renderAction(&b, "u", "Apply shared infra",
		fmt.Sprintf("grove env up %s (at ecosystem root)", sharedTarget), sharedDisabled)
	p.renderAction(&b, "D", "Drift: shared infra",
		fmt.Sprintf("grove env drift %s", sharedTarget), sharedDisabled)
	p.renderAction(&b, "A", "Drift all worktrees",
		fmt.Sprintf("iterate %d worktree(s), cache results · shown in Deployments", p.worktreeCount),
		p.worktreeCount == 0 || p.driftingWT != "")

	b.WriteString("\n  " + th.Bold.Render("scope") + "\n")
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 20))) + "\n")
	p.renderAction(&b, "W", "Jump to worktree", "opens worktree picker overlay", p.worktreeCount == 0)
	p.renderAction(&b, "1", "Back to Deployments", "", false)

	return b.String()
}

// renderAction mirrors the worktree-mode actionsPage layout so the two
// pages look uniform — same key chip (6-cell pad), label column, hint
// column, dim styling when disabled.
func (p *ecosystemActionsPage) renderAction(b *strings.Builder, keyStr, labelStr, hint string, disabled bool) {
	th := theme.DefaultTheme
	keyChip := th.Info.Render(padRight(keyStr, 6))
	labelCol := th.Bold.Render(padRight(labelStr, 28))
	hintCol := th.Muted.Render(hint)
	if disabled {
		keyChip = th.Muted.Render(padRight(keyStr, 6))
		labelCol = th.Muted.Render(padRight(labelStr, 28))
		hintCol = th.Muted.Render(hint)
	}
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n", keyChip, labelCol, hintCol))
}
