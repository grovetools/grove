package env

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// actionsPage shows the lifecycle keybindings scoped to the currently-
// selected profile, plus any user-defined commands from the profile's
// EnvironmentConfig.Commands. Invalid actions (Stop when not running,
// Drift on non-terraform profiles) render dimmed so the user sees the key
// exists but is currently a no-op for this profile.
type actionsPage struct {
	profile   string
	provider  string
	isRunning bool
	commands  map[string]string

	width  int
	height int
}

func newActionsPage() *actionsPage { return &actionsPage{} }

func (p *actionsPage) Name() string                            { return "Actions" }
func (p *actionsPage) TabID() string                           { return "actions" }
func (p *actionsPage) Init() tea.Cmd                           { return nil }
func (p *actionsPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *actionsPage) Focus() tea.Cmd                           { return nil }
func (p *actionsPage) Blur()                                    {}
func (p *actionsPage) SetSize(width, height int)                { p.width = width; p.height = height }

func (p *actionsPage) View() string {
	th := theme.DefaultTheme
	if p.profile == "" {
		return th.Muted.Render("  Select a profile on Overview (page 1) to see available actions.")
	}

	var b strings.Builder
	target := p.profile

	// lifecycle
	b.WriteString(th.Bold.Render("  lifecycle") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
	canUp, canStop, canRestart := !p.isRunning, p.isRunning, p.isRunning
	canDrift := p.provider == "terraform"
	p.renderAction(&b, "u", label(canUp, "Start", "Start (already running)"),
		fmt.Sprintf("grove env up %s", target), !canUp)
	p.renderAction(&b, "d", label(canStop, "Stop", "Stop (not running)"),
		fmt.Sprintf("grove env down %s", target), !canStop)
	p.renderAction(&b, "r", label(canRestart, "Restart", "Restart (not running)"),
		fmt.Sprintf("grove env restart %s", target), !canRestart)
	p.renderAction(&b, "D", label(canDrift, "Drift check", "Drift (non-terraform)"),
		fmt.Sprintf("grove env drift %s", target), !canDrift)

	// configured commands
	b.WriteString("\n" + th.Bold.Render("  configured commands") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
	if len(p.commands) == 0 {
		b.WriteString("  " + th.Muted.Render("no commands defined for this profile") + "\n")
	} else {
		names := make([]string, 0, len(p.commands))
		for k := range p.commands {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			p.renderAction(&b, n, "cmd: "+n, p.commands[n], false)
		}
	}

	// scope
	b.WriteString("\n" + th.Bold.Render("  scope") + "\n")
	b.WriteString(th.Muted.Render("  "+strings.Repeat("─", maxInt(p.width-6, 20))) + "\n")
	p.renderAction(&b, "P", "Switch profile", "opens quick-switcher overlay (5c)", false)
	p.renderAction(&b, "1", "Back to Overview", "", false)

	return b.String()
}

// renderAction formats one row: `<key>  <label>   <hint>`, dimmed when
// disabled so the user can still see the binding but knows it's inert for
// this profile. Uses theme.Muted for disabled styling so the row reads the
// same as the ecosystem-mode Actions page.
func (p *actionsPage) renderAction(b *strings.Builder, keyStr, labelStr, hint string, disabled bool) {
	th := theme.DefaultTheme
	keyChip := th.Info.Render(padRight(keyStr, 2))
	labelCol := th.Bold.Render(padRight(labelStr, 28))
	hintCol := th.Muted.Render(hint)
	if disabled {
		keyChip = th.Muted.Render(padRight(keyStr, 2))
		labelCol = th.Muted.Render(padRight(labelStr, 28))
		hintCol = th.Muted.Render(hint)
	}
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n", keyChip, labelCol, hintCol))
}

func label(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
