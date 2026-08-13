package config

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/notescope"
)

// Messages the join-delta page asks the outer Model for. Each one is the
// product of exactly one keypress.
type (
	fetchJoinDeltaMsg struct{}
	shareNotebookMsg  struct{ notebook string }
	pullNotebookMsg   struct{ notebook string }
)

// JoinPage renders the join delta: what the recorded sync server holds that
// this machine does not, and the other way round, grouped by notebook and
// expandable to notespace grain.
//
// Two rules shape the whole page. Nothing moves without a keypress — `p` pulls,
// `s` shares, and neither runs on focus, refresh, or a timer. And the page does
// not fetch on its own either: the delta arrives only when the operator asks
// for it with `r`, because a config page that opened a device session to a
// server on tab-switch would be talking to something nobody asked about.
//
// The rows describe directions, never instructions. A row that names a key
// still hands the decision to the verb: pressing it runs `grove notebook
// share|pull`, refusals and all.
type JoinPage struct {
	layered       *coreconfig.LayeredConfig
	keys          grovekeymap.ConfigKeyMap
	width, height int
	active        bool

	rows     []notescope.JoinRow
	expanded map[string]bool
	cursor   int

	// inventory is the last answer the server gave, kept so a local-side
	// refresh (a share that just wrote notebooks.toml) re-derives the delta
	// without a second round trip. fetched distinguishes "the server said
	// nothing is there" from "nobody has asked yet".
	inventory syncproto.InventoryResponse
	fetched   bool
	loading   bool

	scanned []notescope.Notebook
	// err is the LOCAL half's error — the scan. fetchErr is the server half's.
	// They are separate because Refresh re-runs on every config reload and
	// clears what it re-derives: folded together, an unrelated config write
	// would erase the reason the last fetch failed while the page still shows
	// no comparison, leaving the operator with a blank page and no cause.
	err      error
	fetchErr error
	notice   string

	// acts reports whether this BINARY carries the verbs. Production reads the
	// registration; a test sets it to pin either host. It gates what the page
	// SAYS, never what it does — an act that got past this still goes through
	// the Model and gets ErrNoService, because the page is not the authority on
	// whether a verb can run.
	acts func() bool
}

var (
	_ pager.Page          = (*JoinPage)(nil)
	_ pager.PageWithTitle = (*JoinPage)(nil)
	_ pager.PageWithID    = (*JoinPage)(nil)
)

func NewJoinPage(layered *coreconfig.LayeredConfig, keys grovekeymap.ConfigKeyMap, w, h int) *JoinPage {
	p := &JoinPage{layered: layered, keys: keys, width: w, height: h, expanded: map[string]bool{}, acts: notescope.ServiceRegistered}
	p.Refresh(layered)
	return p
}

// hostCarriesTheActs reports whether this binary can run share/pull at all.
func (p *JoinPage) hostCarriesTheActs() bool { return p.acts == nil || p.acts() }

func (p *JoinPage) Name() string  { return "Join" }
func (p *JoinPage) TabID() string { return "join" }
func (p *JoinPage) Title() string {
	return theme.DefaultTheme.Muted.Render("  compares this machine against the recorded sync server")
}
func (p *JoinPage) Init() tea.Cmd     { return nil }
func (p *JoinPage) Focus() tea.Cmd    { p.active = true; return nil }
func (p *JoinPage) Blur()             { p.active = false }
func (p *JoinPage) SetSize(w, h int)  { p.width, p.height = w, h }
func (p *JoinPage) Loading() bool     { return p.loading }
func (p *JoinPage) Fetched() bool     { return p.fetched }
func (p *JoinPage) SetLoading(v bool) { p.loading = v }

// Refresh re-reads the local half only. The server half is never re-fetched
// implicitly — Refresh runs on every config reload, and a config reload is not
// a reason to talk to a server.
func (p *JoinPage) Refresh(layered *coreconfig.LayeredConfig) {
	p.layered = layered
	p.scanned, p.err = nil, nil
	table, err := coderoot.Load()
	if err != nil {
		p.err = err
	} else {
		p.scanned, p.err = notescope.Scan(table)
	}
	p.rebuild()
}

// SetInventory records a server answer and re-derives the delta.
func (p *JoinPage) SetInventory(inventory syncproto.InventoryResponse, scanned []notescope.Notebook) {
	p.inventory = inventory
	p.fetched = true
	p.loading = false
	p.fetchErr = nil
	if scanned != nil {
		p.scanned = scanned
	}
	p.rebuild()
}

// SetError records why the last fetch produced no comparison. It survives the
// config reloads that re-derive the local half.
func (p *JoinPage) SetError(err error) {
	p.loading = false
	p.fetchErr = err
}

func (p *JoinPage) rebuild() {
	if !p.fetched {
		p.rows = nil
		return
	}
	p.rows = notescope.BuildJoinRows(notescope.Delta(p.scanned, p.inventory), p.scanned, p.expanded)
	if p.cursor >= len(p.rows) {
		p.cursor = max(0, len(p.rows)-1)
	}
}

func (p *JoinPage) current() (notescope.JoinRow, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return notescope.JoinRow{}, false
	}
	return p.rows[p.cursor], true
}

func (p *JoinPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if !p.active {
		return p, nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	// Declared bindings only — see the note on NotesPage.Update. The two acting
	// keys are matched through the keymap AND checked against the row's own
	// Action, so a rebind moves the key without moving which rows it acts on.
	switch {
	case key.Matches(km, p.keys.FetchJoinDelta):
		if p.loading {
			p.notice = "already asking the server"
			return p, nil
		}
		p.notice = ""
		p.loading = true
		return p, func() tea.Msg { return fetchJoinDeltaMsg{} }
	case key.Matches(km, p.keys.Up):
		p.notice = ""
		if p.cursor > 0 {
			p.cursor--
		}
	case key.Matches(km, p.keys.Down):
		p.notice = ""
		if p.cursor+1 < len(p.rows) {
			p.cursor++
		}
	case key.Matches(km, p.keys.Toggle), key.Matches(km, p.keys.Expand), key.Matches(km, p.keys.Edit):
		p.notice = ""
		if row, ok := p.current(); ok && row.Expandable {
			p.expanded[row.Key] = !p.expanded[row.Key]
			p.rebuild()
		}
	case key.Matches(km, p.keys.Collapse):
		if row, ok := p.current(); ok && row.Expandable && p.expanded[row.Key] {
			p.expanded[row.Key] = false
			p.rebuild()
		}
	case key.Matches(km, p.keys.PullNotebook):
		return p, p.act(notescope.JoinActionPull, func(name string) tea.Msg { return pullNotebookMsg{notebook: name} })
	case key.Matches(km, p.keys.ShareNotebook):
		return p, p.act(notescope.JoinActionShare, func(name string) tea.Msg { return shareNotebookMsg{notebook: name} })
	}
	return p, nil
}

// act emits the intent for the row under the cursor, and only when that row
// says this key acts on it. A row that does not act says why instead.
func (p *JoinPage) act(action string, intent func(string) tea.Msg) tea.Cmd {
	row, ok := p.current()
	if !ok {
		return nil
	}
	if row.Action != action {
		switch {
		case row.Reason != "":
			p.notice = row.Reason
		case row.Kind != notescope.JoinRowNotebook:
			p.notice = "select a notebook row: share and pull act at notebook grain"
		default:
			p.notice = "nothing for this key to do on this row"
		}
		return nil
	}
	p.notice = ""
	name := row.Name
	return func() tea.Msg { return intent(name) }
}

func (p *JoinPage) View() string {
	t := theme.DefaultTheme
	var lines []string
	if p.err != nil {
		lines = append(lines, t.Error.Render(p.err.Error()), "")
	}
	if p.fetchErr != nil {
		lines = append(lines, t.Error.Render(p.fetchErr.Error()), "")
	}
	acts := p.hostCarriesTheActs()
	switch {
	case p.loading:
		lines = append(lines, t.Muted.Render("asking the server for its inventory…"))
	case !p.fetched && !acts:
		lines = append(lines,
			t.Bold.Render("No comparison yet"),
			t.Warning.Render("This build carries the pages but not the acts, so r, p and s cannot run here."),
			t.Muted.Render("Run them as `grove sync join`, `grove notebook pull <name>` and `grove notebook share <name>`,"),
			t.Muted.Render("or open the same page in the grove binary with `grove config`."))
	case !p.fetched:
		lines = append(lines,
			t.Bold.Render("No comparison yet"),
			t.Muted.Render("r  fetch the delta from the recorded sync server"),
			"",
			t.Muted.Render("Nothing here moves on its own: p pulls, s shares, and only on the keypress."))
	case len(p.rows) == 0:
		lines = append(lines, t.Muted.Render("neither side records a notebook this comparison can name."))
	}

	for i, row := range p.rows {
		cursor := "  "
		if i == p.cursor && p.active {
			cursor = t.Highlight.Render(theme.IconArrowRightBold) + " "
		}
		switch row.Kind {
		case notescope.JoinRowNotebook:
			glyph := "  "
			if row.Expandable {
				glyph = "▸ "
				if row.Expanded {
					glyph = "▾ "
				}
			}
			act := "  "
			if row.Action != "" {
				act = t.Highlight.Render(row.Action) + " "
			}
			lines = append(lines, cursor+act+t.Bold.Render(glyph+row.Name)+"  "+t.Muted.Render(row.ID))
			lines = append(lines, "      "+t.Muted.Render(fmt.Sprintf("here %s  ·  server %s", row.Here, row.Server)))
			lines = append(lines, "      "+t.Normal.Render(row.Summary))
			if row.Reason != "" {
				lines = append(lines, "      "+t.Warning.Render(row.Reason))
			}
		case notescope.JoinRowNotespace:
			lines = append(lines, cursor+"        "+t.Muted.Render(row.Text))
		case notescope.JoinRowUnparented:
			glyph := "▸ "
			if row.Expanded {
				glyph = "▾ "
			}
			lines = append(lines, cursor+"  "+t.Muted.Render(glyph+row.Text))
		case notescope.JoinRowNotice:
			lines = append(lines, cursor+"  "+t.Warning.Render("! "+row.Text))
		}
	}

	if p.fetched {
		footer := "Nothing above moved. p pulls · s shares · m moves a notespace on the Notes page."
		if !acts {
			footer = "Nothing above moved, and nothing here can: this build carries no acts. Run `grove notebook pull|share <name>`."
		}
		lines = append(lines, "", t.Muted.Render(footer))
	}
	if p.notice != "" {
		lines = append(lines, "", t.Muted.Render(p.notice))
	}
	return lipgloss.NewStyle().MaxWidth(p.width).Render(strings.Join(lines, "\n"))
}
