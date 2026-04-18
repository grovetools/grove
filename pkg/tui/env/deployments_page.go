package env

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
)

// deploymentsPage renders the cross-worktree deployments matrix: one row
// per worktree, showing glyph, name/branch, profile, state, endpoints, and
// cached drift summary. Matches the "Deployments" tab in the mockup.
type deploymentsPage struct {
	worktrees []WorktreeState
	profiles  map[string]string // worktree name → profile last deployed (from env state)

	cursor          int
	driftingWT      string        // currently running drift, for spinner row
	driftQueueTotal int           // original length of the in-progress queue (for N/M display)
	driftQueueDone  int           // completed within the current queue
	youPath         string        // cached os.Getwd() for 'you' tag (may be "")
	lastComputedAt  time.Time

	width  int
	height int
}

// openRowOverlayMsg is emitted on Enter. The model picks it up and builds a
// rowActionItem overlay scoped to the selected worktree.
type openRowOverlayMsg struct {
	worktreeName string
}

var deploymentsKeys = struct {
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
}{
	Up:    key.NewBinding(key.WithKeys("up", "k")),
	Down:  key.NewBinding(key.WithKeys("down", "j")),
	Enter: key.NewBinding(key.WithKeys("enter")),
}

func newDeploymentsPage() *deploymentsPage {
	wd, _ := os.Getwd()
	return &deploymentsPage{youPath: wd, profiles: map[string]string{}}
}

func (p *deploymentsPage) Name() string  { return "Deployments" }
func (p *deploymentsPage) TabID() string { return "deployments" }
func (p *deploymentsPage) Init() tea.Cmd { return nil }
func (p *deploymentsPage) Focus() tea.Cmd {
	// Refresh cwd so 'you' reflects where the user came from each time they
	// land on this page — cheap and avoids staleness across SetWorkspaceMsg.
	if wd, err := os.Getwd(); err == nil {
		p.youPath = wd
	}
	return nil
}
func (p *deploymentsPage) Blur()                     {}
func (p *deploymentsPage) SetSize(width, height int) { p.width = width; p.height = height }

func (p *deploymentsPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(m, deploymentsKeys.Up):
			if len(p.worktrees) == 0 {
				return p, nil
			}
			p.cursor = (p.cursor - 1 + len(p.worktrees)) % len(p.worktrees)
		case key.Matches(m, deploymentsKeys.Down):
			if len(p.worktrees) == 0 {
				return p, nil
			}
			p.cursor = (p.cursor + 1) % len(p.worktrees)
		case key.Matches(m, deploymentsKeys.Enter):
			name := p.currentName()
			if name == "" {
				return p, nil
			}
			return p, func() tea.Msg { return openRowOverlayMsg{worktreeName: name} }
		}
	}
	return p, nil
}

func (p *deploymentsPage) currentName() string {
	if p.cursor < 0 || p.cursor >= len(p.worktrees) {
		return ""
	}
	return p.worktrees[p.cursor].Workspace.Name
}

func (p *deploymentsPage) View() string {
	th := theme.DefaultTheme
	if len(p.worktrees) == 0 {
		return "  " + th.Muted.Render("No worktrees discovered under this ecosystem yet.")
	}

	var b strings.Builder

	header := fmt.Sprintf("  %s %s %s %s %s %s",
		padRight("", 2),
		th.Muted.Render(padRight("WORKTREE · BRANCH", 26)),
		th.Muted.Render(padRight("PROFILE", 18)),
		th.Muted.Render(padRight("STATE", 18)),
		th.Muted.Render(padRight("ENDPOINTS", 32)),
		th.Muted.Render(padRight("DRIFT", 14)),
	)
	b.WriteString(header + "\n")
	b.WriteString("  " + th.Muted.Render(strings.Repeat("─", maxInt(p.width-4, 40))) + "\n")

	for i, w := range p.worktrees {
		glyph, glyphKey := worktreeGlyph(w)
		glyphStyle := th.Muted
		switch glyphKey {
		case "running":
			glyphStyle = th.Success
		case "cloud":
			glyphStyle = th.Info
		case "drift":
			glyphStyle = th.Warning
		}

		you := ""
		if p.isYou(w) {
			you = " " + th.Highlight.Render("[you]")
		}

		nameCell := padRight(w.Workspace.Name+you, 26)
		if i == p.cursor {
			nameCell = th.Bold.Render(nameCell)
		} else {
			nameCell = th.Normal.Render(nameCell)
		}

		profile := p.profiles[w.Workspace.Name]
		if profile == "" && w.EnvState != nil {
			profile = w.EnvState.Environment
		}
		if profile == "" {
			profile = "—"
		}
		profileCell := th.Info.Render(padRight(profile, 18))

		stateCell := padRight(worktreeStateSummary(w), 18)

		endpoint := firstEndpoint(w)
		endpointCell := padRight(endpoint, 32)

		drift := formatDriftCell(w, w.Workspace.Name == p.driftingWT)
		driftCell := padRight(drift.text, 14)
		driftCell = drift.style.Render(driftCell)

		cursor := "  "
		if i == p.cursor {
			cursor = th.Highlight.Render("> ")
		}

		line := fmt.Sprintf("%s%s %s %s %s %s %s",
			cursor,
			glyphStyle.Render(glyph),
			nameCell,
			profileCell,
			th.Muted.Render(stateCell),
			th.Info.Render(endpointCell),
			driftCell,
		)
		b.WriteString(line + "\n")
	}

	// Serial drift progress. Counter reads "drift-checking N/M: <name>".
	if p.driftingWT != "" && p.driftQueueTotal > 0 {
		progress := fmt.Sprintf("drift-checking %d/%d: %s",
			p.driftQueueDone+1, p.driftQueueTotal, p.driftingWT)
		b.WriteString("\n  " + th.Warning.Render(progress) + "\n")
	}

	// Legend matching the mockup. Kept muted so it doesn't compete with the
	// row glyphs.
	legend := fmt.Sprintf("%s local+cloud  %s cloud state  %s drifting/orphan  %s inactive",
		th.Success.Render("⚡"),
		th.Info.Render("☁"),
		th.Warning.Render("⚠"),
		th.Muted.Render("◯"),
	)
	b.WriteString("\n  " + th.Muted.Render(legend) + "\n")
	b.WriteString("  " + th.Muted.Render("↵ open row · j/k navigate · D drift · A drift all · W jump to worktree") + "\n")

	return b.String()
}

// isYou returns true when the worktree at row i matches the current TUI
// process's cwd (or one of its ancestors). We compare against the
// workspace Path prefix so being inside a sub-project still highlights
// the owning worktree.
func (p *deploymentsPage) isYou(state WorktreeState) bool {
	if p.youPath == "" || state.Workspace == nil || state.Workspace.Path == "" {
		return false
	}
	target := state.Workspace.Path
	if !strings.HasSuffix(target, string(os.PathSeparator)) {
		target += string(os.PathSeparator)
	}
	return strings.HasPrefix(p.youPath+string(os.PathSeparator), target) ||
		p.youPath == state.Workspace.Path
}

// firstEndpoint takes the first endpoint from state.json, truncating when
// too many are present ("api.dev, web.dev, …"). Empty state → "(local only)"
// matching the mockup's inactive-row convention.
func firstEndpoint(w WorktreeState) string {
	if w.EnvState == nil || len(w.EnvState.Endpoints) == 0 {
		return "(local only)"
	}
	eps := w.EnvState.Endpoints
	if len(eps) == 1 {
		return eps[0]
	}
	return fmt.Sprintf("%s, +%d", eps[0], len(eps)-1)
}

type driftCell struct {
	text  string
	style lipgloss.Style
}

// formatDriftCell turns the cached DriftSummary into the label shown in the
// drift column. Returns a styled pair so the caller doesn't re-derive it.
func formatDriftCell(w WorktreeState, running bool) driftCell {
	th := theme.DefaultTheme
	if running {
		return driftCell{text: "checking…", style: th.Warning}
	}
	if w.Drift == nil || w.DriftCheckedAt.IsZero() {
		// No cache: either non-TF profile or never run.
		if w.EnvState != nil && w.EnvState.Provider != "terraform" {
			return driftCell{text: "n/a", style: th.Muted}
		}
		return driftCell{text: "—", style: th.Muted}
	}
	age := humanAge(time.Since(w.DriftCheckedAt))
	if !w.Drift.HasDrift {
		return driftCell{text: "clean · " + age, style: th.Success}
	}
	label := fmt.Sprintf("+%d ~%d -%d · %s",
		w.Drift.Add, w.Drift.Change, w.Drift.Destroy, age)
	return driftCell{text: label, style: th.Warning}
}

// humanAge formats a duration in the compact "Nm"/"Nh"/"Nd" style the
// mockup uses. Durations under a minute round up to "1m" so the cell
// never reads "0s" for fresh cache hits.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
