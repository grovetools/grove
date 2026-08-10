package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
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
	"gopkg.in/yaml.v3"
)

const defaultCodeRoot = "~/Code"

type applyCodeRootMsg struct{ path string }
type toggleCodeExcludeMsg struct{ root, repo string }

type codeRow struct {
	root, repo, label, notebook string
	level                       int
	excluded, ecosystem, member bool
} // level-2 rows retain their direct ecosystem parent in repo and their manifest-relative member path in label.

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
		rootPath := setup.ExpandPath(r.Path)
		isEcosystem := setup.HasEcosystemManifest(rootPath)
		p.rows = append(p.rows, codeRow{root: name, label: name + "  " + setup.AbbreviatePath(r.Path), notebook: notebook, ecosystem: isEcosystem})
		if !p.expanded[name] {
			continue
		}
		if isEcosystem {
			p.rows = append(p.rows, discoverEcosystemMembers(name, rootPath, notebook)...)
			continue
		}
		for _, child := range discoverCodeRows(name, r, notebook) {
			p.rows = append(p.rows, child)
			if child.ecosystem && p.expanded[name+"/"+child.repo] {
				p.rows = append(p.rows, discoverEcosystemMembers(name, filepath.Join(rootPath, child.repo), notebook)...)
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
	manifest := coreconfig.FindEcosystemManifest(path)
	if manifest == "" {
		return nil
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil
	}
	var declared struct {
		Workspaces []string `toml:"workspaces" yaml:"workspaces"`
	}
	if filepath.Ext(manifest) == ".toml" {
		_, err = toml.Decode(string(data), &declared)
	} else {
		err = yaml.Unmarshal(data, &declared)
	}
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var rows []codeRow
	for _, pattern := range declared.Workspaces {
		pattern = filepath.Clean(strings.TrimSpace(pattern))
		if pattern == "." || filepath.IsAbs(pattern) || pattern == ".." || strings.HasPrefix(pattern, ".."+string(filepath.Separator)) {
			continue
		}
		matches, globErr := filepath.Glob(filepath.Join(path, pattern))
		if globErr != nil {
			continue
		}
		for _, match := range matches {
			info, statErr := os.Stat(match)
			if statErr != nil || !info.IsDir() {
				continue
			}
			// A workspace is a repository, possibly with .git as a worktree file.
			if _, gitErr := os.Stat(filepath.Join(match, ".git")); gitErr != nil {
				continue
			}
			rel, relErr := filepath.Rel(path, match)
			if relErr != nil || rel == "." || seen[rel] {
				continue
			}
			seen[rel] = true
			rows = append(rows, codeRow{root: rootName, repo: filepath.Base(path), label: filepath.ToSlash(rel), notebook: notebook, level: 2, member: true})
		}
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
			// Only direct children are representable by roots.<name>.exclude.
			// Manifest members inherit the containing ecosystem's checkbox.
			if r.level == 1 && r.repo != "" {
				return p, func() tea.Msg { return toggleCodeExcludeMsg{r.root, r.repo} }
			}
		}
	case "enter":
		if len(p.rows) > 0 {
			r := p.rows[p.cursor]
			if r.member {
				return p, nil
			}
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
		lines = append(lines, t.Bold.Render("Where does your code live?"), "")
		if p.editing {
			lines = append(lines, "  "+p.input.View())
		} else {
			lines = append(lines, "  "+t.Highlight.Render(theme.IconArrowRightBold)+" "+t.Normal.Render(p.pathValue))
		}
	} else {
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
			style := t.Normal
			if r.excluded {
				style = t.Muted
			}
			prefix := "☑ "
			if r.excluded {
				prefix = "☐ "
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
				prefix = theme.IconRepo + " "
			}
			lines = append(lines, cur+indent+style.Render(prefix+glyph+r.label)+route)
		}
		if p.editing {
			lines = append(lines, "", p.input.View())
		}
	}
	return lipgloss.NewStyle().MaxWidth(p.width).Render(strings.Join(lines, "\n"))
}

func (m *Model) addCodeRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("code root path cannot be empty")
	}
	path := filepath.Clean(setup.ExpandPath(raw))
	base := setup.DeriveEcosystemName(path)
	t, err := coderoot.Load()
	if err != nil {
		return "", err
	}
	// Re-confirming an existing path selects it; a distinct same-basename path
	// receives a stable suffix instead of overwriting the first binding.
	for _, name := range t.SortedRootNames() {
		if filepath.Clean(setup.ExpandPath(t.Roots[name].Path)) == path {
			return name, nil
		}
	}
	name := base
	for suffix := 2; ; suffix++ {
		if _, occupied := t.Roots[name]; !occupied {
			break
		}
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	notebook := t.Default
	if notebook == "" {
		notebook = "nb"
		if _, err = configWriteDefaultNotebook(notebook); err != nil {
			return "", err
		}
	}
	_, err = coreconfig.WriteCodeRoots(coderoot.RootsPath(), coreconfig.CodeRootEdits{Upserts: map[string]coderoot.Root{name: {Path: path, Scan: true, Notebook: notebook}}})
	return name, err
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
