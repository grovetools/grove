package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/notescope"
	"github.com/grovetools/grove/pkg/setup"
)

type noteCounts struct{ notes, plans, chats int }

// moveNotespaceMsg asks the outer Model to run `grove notespace move`. The page
// never runs it itself: the act belongs to the verb, and the Model owns the
// service handle that reaches it.
type moveNotespaceMsg struct{ notespace, to string }

// NotesPage is the recorded notebook inventory, at notebook grain and — when a
// notebook is expanded — at notespace grain.
//
// The sync column is DERIVED from containment and cannot be toggled here. The
// notebook is the only sync knob: a notespace is shared because the notebook
// containing it is shared, so a per-row toggle would be a second knob wearing
// a checkbox. What this page offers instead is `m` — a move, the act that
// actually changes a notespace's sync scope, run through the same verb the CLI
// runs and reporting the same evidence.
type NotesPage struct {
	layered       *coreconfig.LayeredConfig
	keys          grovekeymap.ConfigKeyMap
	width, height int
	scanned       []notescope.Notebook
	rows          []notescope.NotesRow
	counts        map[string]noteCounts
	expanded      map[string]bool
	cursor        int
	active        bool
	err           error

	// notice is the page's own one-line feedback for a keypress that could not
	// act (a move with nowhere to go, an unminted notespace). Refusals from the
	// verb itself travel back through the Model's status line instead.
	notice string

	// moving holds the destination prompt. It is armed by `m` and disarmed by
	// esc; nothing moves until enter.
	moving     bool
	moveSource notescope.NotesRow
	moveInput  textinput.Model
}

var (
	_ pager.Page              = (*NotesPage)(nil)
	_ pager.PageWithTitle     = (*NotesPage)(nil)
	_ pager.PageWithID        = (*NotesPage)(nil)
	_ pager.PageWithTextInput = (*NotesPage)(nil)
)

func NewNotesPage(layered *coreconfig.LayeredConfig, keys grovekeymap.ConfigKeyMap, w, h int) *NotesPage {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 200
	ti.Width = 40
	p := &NotesPage{layered: layered, keys: keys, width: w, height: h, expanded: map[string]bool{}, counts: map[string]noteCounts{}, moveInput: ti}
	p.Refresh(layered)
	return p
}
func (p *NotesPage) Name() string  { return "Notes" }
func (p *NotesPage) TabID() string { return "notes" }
func (p *NotesPage) Title() string {
	return theme.DefaultTheme.Muted.Render("  reads " + setup.AbbreviatePath(coderoot.NotebooksPath()))
}
func (p *NotesPage) Init() tea.Cmd  { return nil }
func (p *NotesPage) Focus() tea.Cmd { p.active = true; return nil }
func (p *NotesPage) Blur() {
	p.active = false
	p.moving = false
	p.moveInput.Blur()
}
func (p *NotesPage) SetSize(w, h int)        { p.width, p.height = w, h }
func (p *NotesPage) IsTextEntryActive() bool { return p.moving }

// Refresh re-reads the recorded pair and re-scans. It reads config and stamps
// and nothing else — no server, no daemon.
func (p *NotesPage) Refresh(layered *coreconfig.LayeredConfig) {
	p.layered = layered
	table, err := coderoot.Load()
	p.err = err
	p.scanned = nil
	if err == nil {
		// A machine with nothing recorded is the empty state, not an error:
		// Scan over a zero table yields no notebooks, which is the truth.
		p.scanned, p.err = notescope.Scan(table)
	}
	p.counts = map[string]noteCounts{}
	for _, nb := range p.scanned {
		if nb.Exists {
			p.counts[nb.Name] = countNotebook(nb.Root)
		}
	}
	p.rebuild()
}

func (p *NotesPage) rebuild() {
	p.rows = notescope.BuildNotesRows(p.scanned, p.expanded)
	if p.cursor >= len(p.rows) {
		p.cursor = max(0, len(p.rows)-1)
	}
}

func countNotebook(root string) (c noteCounts) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(rel, string(os.PathSeparator))
		for _, part := range parts {
			switch part {
			case "plans":
				c.plans++
				return nil
			case "chats", "transcripts":
				c.chats++
				return nil
			}
		}
		c.notes++
		return nil
	})
	return
}

func (p *NotesPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if !p.active {
		return p, nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	if p.moving {
		switch {
		case key.Matches(km, p.keys.Cancel):
			p.moving = false
			p.moveInput.Blur()
			p.notice = "move cancelled; nothing was written"
			return p, nil
		case key.Matches(km, p.keys.Confirm):
			destination := strings.TrimSpace(p.moveInput.Value())
			source := p.moveSource
			p.moving = false
			p.moveInput.Blur()
			if destination == "" {
				p.notice = "a move names its destination notebook"
				return p, nil
			}
			p.notice = ""
			// The id is what moves; it is also what the verb matches first.
			return p, func() tea.Msg { return moveNotespaceMsg{notespace: source.ID, to: destination} }
		}
		var cmd tea.Cmd
		p.moveInput, cmd = p.moveInput.Update(km)
		return p, cmd
	}

	switch km.String() {
	case "up", "k":
		p.notice = ""
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		p.notice = ""
		if p.cursor+1 < len(p.rows) {
			p.cursor++
		}
	case "enter", " ", "l", "right":
		p.notice = ""
		if row, ok := p.current(); ok && row.Kind == notescope.NotesRowNotebook {
			p.expanded[row.Key] = !p.expanded[row.Key]
			p.rebuild()
		}
	case "h", "left":
		if row, ok := p.current(); ok && row.Kind == notescope.NotesRowNotebook && p.expanded[row.Key] {
			p.expanded[row.Key] = false
			p.rebuild()
		}
	case "m":
		return p, p.armMove()
	}
	return p, nil
}

func (p *NotesPage) current() (notescope.NotesRow, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return notescope.NotesRow{}, false
	}
	return p.rows[p.cursor], true
}

// armMove opens the destination prompt. It never moves anything — that takes
// the second keypress, on enter, and even then the verb decides.
func (p *NotesPage) armMove() tea.Cmd {
	row, ok := p.current()
	if !ok {
		return nil
	}
	if row.Kind != notescope.NotesRowNotespace {
		p.notice = "moving is per notespace — expand this notebook (enter) and select one"
		return nil
	}
	if row.ID == "" {
		p.notice = fmt.Sprintf("%s carries no notespace id; `grove notebook share %s` mints one", row.Dir, row.Notebook)
		return nil
	}
	destinations := notescope.MoveDestinations(p.scanned, row.Notebook)
	if len(destinations) == 0 {
		p.notice = "no other recorded notebook with an existing root; a move never creates one"
		return nil
	}
	p.notice = ""
	p.moving = true
	p.moveSource = row
	p.moveInput.SetValue(destinations[0])
	p.moveInput.CursorEnd()
	return p.moveInput.Focus()
}

func (p *NotesPage) View() string {
	t := theme.DefaultTheme
	var lines []string
	if p.err != nil {
		lines = append(lines, t.Error.Render(p.err.Error()), "")
	}
	if len(p.rows) == 0 {
		lines = append(lines, t.Bold.Render("No notebooks recorded"), t.Muted.Render("A default notebook is recorded when you add your first code root."))
	}
	for i, row := range p.rows {
		cursor := "  "
		if i == p.cursor && p.active {
			cursor = t.Highlight.Render(theme.IconArrowRightBold) + " "
		}
		if row.Kind == notescope.NotesRowNotespace {
			id := row.ID
			style := t.Muted
			if id == "" {
				id = "(unminted)"
			}
			lines = append(lines, cursor+"      "+t.Normal.Render(row.Dir)+"  "+style.Render(id))
			continue
		}
		glyph := "  "
		if row.Expandable {
			glyph = "▸ "
			if row.Expanded {
				glyph = "▾ "
			}
		}
		sync := t.Muted.Render(row.Sync)
		if row.Sync == notescope.SyncLabelShared {
			sync = t.Success.Render(row.Sync)
		}
		// The derived sync state sits ahead of the root, not after it. It is
		// the answer this page exists to give, and a long root would push it
		// off the right edge exactly on the machines that have deep ones.
		head := glyph + row.Notebook + "  " + row.Sync + "  "
		path := setup.AbbreviatePath(row.Root)
		if room := p.width - lipgloss.Width(head) - 2; room > 0 {
			path = lipgloss.NewStyle().MaxWidth(room).Render(path)
		}
		lines = append(lines, cursor+t.Bold.Render(glyph+row.Notebook)+"  "+sync+"  "+t.Path.Render(path))

		c := p.counts[row.Notebook]
		detail := fmt.Sprintf("  %d notes  ·  %d plans  ·  %d chats", c.notes, c.plans, c.chats)
		if row.Notespaces > 0 {
			detail += fmt.Sprintf("  ·  %d notespaces", row.Notespaces)
		}
		lines = append(lines, "  "+t.Muted.Render(detail))
		if !row.Exists {
			lines = append(lines, "  "+t.Warning.Render("  recorded root does not exist"))
		}
		if row.Retention != "" {
			// The server's own retention sentence is long and is the point;
			// wrap it rather than clipping it into a half-statement.
			width := p.width - 6
			if width < 20 {
				width = 20
			}
			lines = append(lines, lipgloss.NewStyle().Width(width).MarginLeft(4).Render(t.Muted.Render(row.Retention)))
		}
	}
	if p.moving {
		lines = append(lines, "",
			t.Bold.Render(fmt.Sprintf("move %s out of %s into notebook:", p.moveSource.Dir, p.moveSource.Notebook)),
			"  "+p.moveInput.View())
	}
	if p.notice != "" {
		lines = append(lines, "", t.Muted.Render(p.notice))
	}
	return lipgloss.NewStyle().MaxWidth(p.width).Render(strings.Join(lines, "\n"))
}
