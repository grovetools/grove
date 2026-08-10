package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	"github.com/grovetools/grove/pkg/setup"
)

const defaultCodeRoot = "~/Code"

type applyCodeRootMsg struct{ path string }
type toggleCodeExcludeMsg struct{ root, repo string }

type codeRow struct {
	root, repo, label, notebook string
	level                       int
	excluded, ecosystem         bool
}

// CodePage edits the recorded code-root table. Filesystem discovery is lazy:
// a fresh page performs no scan until the user submits a directory.
type CodePage struct {
	layered         *coreconfig.LayeredConfig
	keys            grovekeymap.ConfigKeyMap
	width, height   int
	active, editing bool
	input           textinput.Model
	pathValue       string
	cursor          int
	expanded        map[string]bool
	rows            []codeRow
	loadErr         error
}

var _ pager.Page = (*CodePage)(nil)
var _ pager.PageWithTitle = (*CodePage)(nil)
var _ pager.PageWithID = (*CodePage)(nil)
var _ pager.PageWithTextInput = (*CodePage)(nil)

func NewCodePage(layered *coreconfig.LayeredConfig, keys grovekeymap.ConfigKeyMap, width, height int) *CodePage {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 300
	ti.Width = 55
	p := &CodePage{layered: layered, keys: keys, width: width, height: height, input: ti, pathValue: defaultCodeRoot, expanded: map[string]bool{}}
	// Recorded roots are authoritative and may be displayed immediately. The
	// empty state deliberately does not touch the filesystem.
	if table, err := coderoot.Load(); err != nil {
		p.loadErr = err
	} else if len(table.Roots) > 0 {
		p.rebuild(table)
	}
	return p
}
func (p *CodePage) Name() string  { return "Code" }
func (p *CodePage) TabID() string { return "code" }
func (p *CodePage) Title() string {
	return theme.DefaultTheme.Muted.Render("  saves to " + setup.AbbreviatePath(coderoot.RootsPath()))
}
func (p *CodePage) Init() tea.Cmd           { return nil }
func (p *CodePage) Focus() tea.Cmd          { p.active = true; return nil }
func (p *CodePage) Blur()                   { p.active = false; p.editing = false; p.input.Blur() }
func (p *CodePage) SetSize(w, h int)        { p.width, p.height = w, h }
func (p *CodePage) IsTextEntryActive() bool { return p.editing }
func (p *CodePage) Refresh(layered *coreconfig.LayeredConfig) {
	p.layered = layered
	if t, e := coderoot.Load(); e != nil {
		p.loadErr = e
	} else {
		p.loadErr = nil
		p.rebuild(t)
	}
}

func (p *CodePage) rebuild(t coderoot.Table) {
	p.rows = nil
	for _, name := range t.SortedRootNames() {
		r := t.Roots[name]
		notebook := r.Notebook
		if notebook == "" {
			notebook = t.Default
		}
		p.rows = append(p.rows, codeRow{root: name, label: name + "  " + setup.AbbreviatePath(r.Path), notebook: notebook, ecosystem: setup.HasEcosystemManifest(setup.ExpandPath(r.Path))})
		if p.expanded[name] {
			children := discoverCodeRows(name, r, notebook)
			for _, child := range children {
				p.rows = append(p.rows, child)
				if child.ecosystem && p.expanded[name+"/"+child.repo] {
					p.rows = append(p.rows, discoverEcosystemMembers(name, filepath.Join(setup.ExpandPath(r.Path), child.repo), notebook)...)
				}
			}
		}
	}
	if p.cursor >= len(p.rows) {
		p.cursor = max(0, len(p.rows)-1)
	}
}

func discoverCodeRows(rootName string, r coderoot.Root, notebook string) []codeRow {
	base := setup.ExpandPath(r.Path)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var rows []codeRow
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(base, e.Name())
		isEco := setup.HasEcosystemManifest(path)
		rows = append(rows, codeRow{root: rootName, repo: e.Name(), label: e.Name(), notebook: notebook, level: 1, excluded: !r.IncludesRepo(e.Name()), ecosystem: isEco})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	return rows
}

func discoverEcosystemMembers(rootName, path, notebook string) []codeRow {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var rows []codeRow
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, e.Name(), ".git")); err != nil {
			continue
		}
		rows = append(rows, codeRow{root: rootName, repo: e.Name(), label: e.Name(), notebook: notebook, level: 2})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	return rows
}

func (p *CodePage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if !p.active {
		return p, nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	if p.editing {
		switch {
		case key.Matches(km, p.keys.Cancel):
			p.editing = false
			p.input.Blur()
			return p, nil
		case key.Matches(km, p.keys.Confirm):
			v := strings.TrimSpace(p.input.Value())
			p.pathValue = v
			p.editing = false
			p.input.Blur()
			return p, func() tea.Msg { return applyCodeRootMsg{v} }
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(km)
		return p, cmd
	}
	switch km.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor+1 < len(p.rows) {
			p.cursor++
		}
	case " ", "x":
		if len(p.rows) > 0 {
			r := p.rows[p.cursor]
			if r.repo != "" {
				return p, func() tea.Msg { return toggleCodeExcludeMsg{r.root, r.repo} }
			}
		}
	case "enter":
		if len(p.rows) > 0 {
			r := p.rows[p.cursor]
			if r.repo == "" {
				p.expanded[r.root] = !p.expanded[r.root]
				if t, e := coderoot.Load(); e == nil {
					p.rebuild(t)
				}
				return p, nil
			}
			if r.ecosystem {
				key := r.root + "/" + r.repo
				p.expanded[key] = !p.expanded[key]
				if t, e := coderoot.Load(); e == nil {
					p.rebuild(t)
				}
				return p, nil
			}
		}
		p.editing = true
		p.input.SetValue(p.pathValue)
		p.input.CursorEnd()
		return p, p.input.Focus()
	case "a":
		p.editing = true
		p.input.SetValue(p.pathValue)
		p.input.CursorEnd()
		return p, p.input.Focus()
	}
	return p, nil
}

func (p *CodePage) View() string {
	t := theme.DefaultTheme
	var lines []string
	if p.loadErr != nil {
		lines = append(lines, t.Error.Render(p.loadErr.Error()), "")
	}
	if len(p.rows) == 0 {
		lines = append(lines, t.Bold.Render("Where does your code live?"), t.Normal.Render("Record a directory. Grove will scan it only after you press enter."), "")
		if p.editing {
			lines = append(lines, "  "+p.input.View())
		} else {
			lines = append(lines, "  "+t.Highlight.Render(theme.IconArrowRightBold)+" "+t.Normal.Render(p.pathValue))
		}
	} else {
		lines = append(lines, t.Muted.Render("Code roots  (enter expand · space include/exclude · a add)"), "")
		for i, r := range p.rows {
			cur := "  "
			if i == p.cursor {
				cur = t.Highlight.Render(theme.IconArrowRightBold) + " "
			}
			route := ""
			if r.notebook != "" {
				route = "  " + t.Muted.Render("→ "+r.notebook)
			}
			if r.repo == "" {
				glyph := "▸"
				if p.expanded[r.root] {
					glyph = "▾"
				}
				if r.ecosystem {
					glyph += " " + theme.IconEcosystem
				}
				lines = append(lines, cur+t.Bold.Render(glyph+" "+r.label)+route)
				continue
			}
			box := "☑"
			style := t.Normal
			if r.excluded {
				box = "☐"
				style = t.Muted
			}
			glyph := ""
			if r.ecosystem {
				glyph = "▸ " + theme.IconEcosystem + " "
				if p.expanded[r.root+"/"+r.repo] {
					glyph = "▾ " + theme.IconEcosystem + " "
				}
			}
			indent := "  "
			if r.level > 1 {
				indent = "      "
			}
			lines = append(lines, cur+indent+style.Render(box+" "+glyph+r.label)+route)
		}
		if p.editing {
			lines = append(lines, "", p.input.View())
		}
	}
	return lipgloss.NewStyle().MaxWidth(p.width).Render(strings.Join(lines, "\n"))
}

func (m *Model) addCodeRoot(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("code root path cannot be empty")
	}
	path := setup.ExpandPath(raw)
	name := setup.DeriveEcosystemName(path)
	t, err := coderoot.Load()
	if err != nil {
		return err
	}
	notebook := t.Default
	if notebook == "" {
		notebook = "nb"
		if _, err = configWriteDefaultNotebook(notebook); err != nil {
			return err
		}
	}
	_, err = coreconfig.WriteCodeRoots(coderoot.RootsPath(), coreconfig.CodeRootEdits{Upserts: map[string]coderoot.Root{name: {Path: path, Scan: true, Notebook: notebook}}})
	return err
}
func configWriteDefaultNotebook(name string) (bool, error) {
	root := "~/notebooks/" + name
	return coreconfig.WriteNotebooks(coderoot.NotebooksPath(), coreconfig.NotebookEdits{Default: &name, Upserts: map[string]coderoot.Notebook{name: {Root: root}}})
}
func (m *Model) toggleCodeExclude(root, repo string) error {
	t, e := coderoot.Load()
	if e != nil {
		return e
	}
	r, ok := t.Roots[root]
	if !ok {
		return fmt.Errorf("code root %q no longer exists", root)
	}
	found := false
	for i, v := range r.Exclude {
		if v == repo {
			r.Exclude = append(r.Exclude[:i], r.Exclude[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		r.Exclude = append(r.Exclude, repo)
		sort.Strings(r.Exclude)
	}
	_, e = coreconfig.WriteCodeRoots(coderoot.RootsPath(), coreconfig.CodeRootEdits{Upserts: map[string]coderoot.Root{root: r}})
	return e
}
