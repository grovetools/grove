package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

type (
	noteCounts  struct{ notes, plans, chats int }
	notebookRow struct {
		name, root string
		counts     noteCounts
	}
)

// NotesPage is the deliberately small P1 notebook inventory. Sync state and
// actions arrive in P3; today each notebook gets exactly two lines.
type NotesPage struct {
	layered       *coreconfig.LayeredConfig
	keys          grovekeymap.ConfigKeyMap
	width, height int
	rows          []notebookRow
	err           error
}

var (
	_ pager.Page          = (*NotesPage)(nil)
	_ pager.PageWithTitle = (*NotesPage)(nil)
	_ pager.PageWithID    = (*NotesPage)(nil)
)

func NewNotesPage(layered *coreconfig.LayeredConfig, keys grovekeymap.ConfigKeyMap, w, h int) *NotesPage {
	p := &NotesPage{layered: layered, keys: keys, width: w, height: h}
	p.Refresh(layered)
	return p
}
func (p *NotesPage) Name() string  { return "Notes" }
func (p *NotesPage) TabID() string { return "notes" }
func (p *NotesPage) Title() string {
	return theme.DefaultTheme.Muted.Render("  reads " + setup.AbbreviatePath(coderoot.NotebooksPath()))
}
func (p *NotesPage) Init() tea.Cmd                            { return nil }
func (p *NotesPage) Focus() tea.Cmd                           { return nil }
func (p *NotesPage) Blur()                                    {}
func (p *NotesPage) SetSize(w, h int)                         { p.width, p.height = w, h }
func (p *NotesPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) { return p, nil }
func (p *NotesPage) Refresh(layered *coreconfig.LayeredConfig) {
	p.layered = layered
	p.rows = nil
	t, e := coderoot.Load()
	p.err = e
	if e != nil {
		return
	}
	for _, name := range t.SortedNotebookNames() {
		nb := t.Notebooks[name]
		p.rows = append(p.rows, notebookRow{name: name, root: nb.Root, counts: countNotebook(setup.ExpandPath(nb.Root))})
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

func (p *NotesPage) View() string {
	t := theme.DefaultTheme
	var lines []string
	if p.err != nil {
		lines = append(lines, t.Error.Render(p.err.Error()), "")
	}
	if len(p.rows) == 0 {
		lines = append(lines, t.Bold.Render("No notebooks recorded"), t.Muted.Render("A default notebook is recorded when you add your first code root."))
	}
	for _, r := range p.rows {
		lines = append(lines, t.Bold.Render(r.name)+"  "+t.Path.Render(setup.AbbreviatePath(r.root)), t.Muted.Render(fmt.Sprintf("  %d notes  ·  %d plans  ·  %d chats", r.counts.notes, r.counts.plans, r.counts.chats)))
	}
	return lipgloss.NewStyle().MaxWidth(p.width).Render(strings.Join(lines, "\n"))
}
