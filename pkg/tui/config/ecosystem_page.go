package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// defaultEcosystemPath is the suggested first-ecosystem directory in the
// ~-abbreviated display dialect (the setup wizard's default, cmd/setup.go).
const defaultEcosystemPath = "~/Code"

// applyEcosystemMsg asks the outer Model to create-or-import and register the
// ecosystem at path — the themes page's applyThemeMsg pattern: the page owns
// the form, the Model owns persistence, reload, and status surfacing.
type applyEcosystemMsg struct{ path string }

// grovesConfig returns the merged groves map, or nil when absent.
func grovesConfig(lc *config.LayeredConfig) map[string]config.GroveSourceConfig {
	if lc != nil && lc.Final != nil {
		return lc.Final.Groves
	}
	return nil
}

// EcosystemPage is a bespoke form-shaped pager.Page (not a curated settings
// list — the themes page is the precedent for non-curated pages): one path
// input with a live derived-name preview, committing a create-or-import of a
// workspace directory plus its groves registration. Existing groves render
// read-only above the form.
type EcosystemPage struct {
	// pathValue is the form's current (uncommitted) path in the
	// ~-abbreviated display dialect; enter opens the inline editor over it.
	pathValue string
	editing   bool
	input     textinput.Model

	layered *config.LayeredConfig
	keys    grovekeymap.ConfigKeyMap
	width   int
	height  int
	active  bool
}

// Compile-time interface checks.
var (
	_ pager.Page              = (*EcosystemPage)(nil)
	_ pager.PageWithTitle     = (*EcosystemPage)(nil)
	_ pager.PageWithID        = (*EcosystemPage)(nil)
	_ pager.PageWithTextInput = (*EcosystemPage)(nil)
)

// NewEcosystemPage builds the Ecosystem page.
func NewEcosystemPage(layered *config.LayeredConfig, keys grovekeymap.ConfigKeyMap, width, height int) *EcosystemPage {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 200
	ti.Width = 50

	return &EcosystemPage{
		pathValue: defaultEcosystemPath,
		input:     ti,
		layered:   layered,
		keys:      keys,
		width:     width,
		height:    height,
	}
}

// Name implements pager.Page.
func (p *EcosystemPage) Name() string { return "Ecosystem" }

// TabID implements pager.PageWithID.
func (p *EcosystemPage) TabID() string { return "ecosystem" }

// Title implements pager.PageWithTitle.
func (p *EcosystemPage) Title() string {
	title := "  saves to " + setup.AbbreviatePath(globalConfigDisplayPath(p.layered))
	return theme.DefaultTheme.Muted.Render(title)
}

// Init implements pager.Page.
func (p *EcosystemPage) Init() tea.Cmd { return nil }

// Focus implements pager.Page.
func (p *EcosystemPage) Focus() tea.Cmd {
	p.active = true
	return nil
}

// Blur implements pager.Page. Leaving the page closes the inline editor
// without committing; the last shown path is kept for a return visit.
func (p *EcosystemPage) Blur() {
	p.active = false
	p.stopEditing()
}

// SetSize implements pager.Page.
func (p *EcosystemPage) SetSize(width, height int) {
	p.width = width
	p.height = height
}

// Refresh re-points the page at a reloaded layered config. When the shown
// path turns out to be registered now (the just-committed ecosystem coming
// back through the reload), the form resets to the suggested default so the
// page reads "add another" instead of re-offering a name that would collide.
func (p *EcosystemPage) Refresh(layered *config.LayeredConfig) {
	p.layered = layered
	if p.editing {
		return
	}
	expanded := setup.ExpandPath(strings.TrimSpace(p.pathValue))
	name := setup.DeriveEcosystemName(expanded)
	if g, ok := grovesConfig(layered)[name]; ok && g.Path == expanded {
		p.pathValue = defaultEcosystemPath
	}
}

// IsTextEntryActive implements pager.PageWithTextInput: while the path editor
// is open every keystroke belongs to it (embedding hosts — the config panel's
// quit/filter keys, the onboarding shell's step nav — forward wholesale).
func (p *EcosystemPage) IsTextEntryActive() bool { return p.editing }

// currentPath is the path the preview derives from: the live editor content
// while typing, else the last shown value.
func (p *EcosystemPage) currentPath() string {
	if p.editing {
		return p.input.Value()
	}
	return p.pathValue
}

// stopEditing closes the inline editor without committing.
func (p *EcosystemPage) stopEditing() {
	p.editing = false
	p.input.Blur()
}

// Update implements pager.Page.
func (p *EcosystemPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if !p.active {
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	if p.editing {
		switch {
		case key.Matches(keyMsg, p.keys.Cancel):
			p.stopEditing()
			return p, nil
		case key.Matches(keyMsg, p.keys.Confirm):
			value := p.input.Value()
			p.pathValue = value
			p.stopEditing()
			return p, func() tea.Msg { return applyEcosystemMsg{path: value} }
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(keyMsg)
		return p, cmd
	}

	switch {
	case key.Matches(keyMsg, p.keys.Edit), key.Matches(keyMsg, p.keys.Toggle):
		p.editing = true
		p.input.SetValue(p.pathValue)
		p.input.CursorEnd()
		return p, p.input.Focus()
	}
	return p, nil
}

// groveRow is one read-only row of the configured-ecosystems list.
type groveRow struct {
	name    string
	path    string
	enabled bool
}

// configuredGroves flattens the merged groves map into name-sorted rows
// (TOML maps carry no order; sorted is the stable presentation).
func configuredGroves(lc *config.LayeredConfig) []groveRow {
	groves := grovesConfig(lc)
	names := make([]string, 0, len(groves))
	for name := range groves {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]groveRow, 0, len(names))
	for _, name := range names {
		g := groves[name]
		enabled := g.Enabled == nil || *g.Enabled // unset defaults to enabled
		rows = append(rows, groveRow{name: name, path: g.Path, enabled: enabled})
	}
	return rows
}

// View implements pager.Page.
func (p *EcosystemPage) View() string {
	t := theme.DefaultTheme
	var lines []string

	rows := configuredGroves(p.layered)
	if len(rows) > 0 {
		lines = append(lines, t.Muted.Render("Configured ecosystems"))
		for _, r := range rows {
			state := "enabled"
			if !r.enabled {
				state = "disabled"
			}
			lines = append(lines,
				"  "+t.Normal.Render(r.name)+"  "+t.Path.Render(setup.AbbreviatePath(r.path))+"  "+t.Muted.Render(state))
		}
		lines = append(lines, "", t.Bold.Render("Add another"))
	} else {
		lines = append(lines,
			t.Normal.Render("An ecosystem is a directory of related projects that Grove"),
			t.Normal.Render("discovers and orchestrates — point it at where your code lives."),
			"",
			t.Bold.Render("Create or import your first ecosystem"))
	}
	lines = append(lines, "")

	// The form's one row: the path, editable inline.
	cursor := t.Highlight.Render(theme.IconArrowRightBold) + " "
	label := t.Bold.Render("Directory")
	if p.editing {
		lines = append(lines, cursor+label, "  "+p.input.View())
	} else {
		lines = append(lines, cursor+label+"  "+t.Normal.Render(p.pathValue))
	}

	lines = append(lines, "", p.renderEcosystemPreview())

	if len(rows) == 0 {
		lines = append(lines, "",
			t.Muted.Render("no projects yet? the demo (d) is a ready-made ecosystem to explore"))
	}

	body := strings.Join(lines, "\n")
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().MaxWidth(p.width).Render(body),
		p.renderEcosystemFooter())
}

// renderEcosystemPreview names what enter will do for the current path: the
// derived registry name, CREATE (scaffold) vs IMPORT (manifest present,
// register-only), or the collision refusal.
func (p *EcosystemPage) renderEcosystemPreview() string {
	t := theme.DefaultTheme
	raw := strings.TrimSpace(p.currentPath())
	if raw == "" {
		return t.Muted.Render("enter a directory path to continue")
	}
	expanded := setup.ExpandPath(raw)
	name := setup.DeriveEcosystemName(expanded)
	display := setup.AbbreviatePath(expanded)

	var lines []string
	switch {
	case hasGrove(p.layered, name):
		lines = append(lines,
			t.Warning.Render(fmt.Sprintf("an ecosystem named %q is already configured — choose a different directory", name)))
	case setup.HasEcosystemManifest(expanded):
		lines = append(lines,
			t.Normal.Render(fmt.Sprintf("enter will IMPORT %q — %s already is an ecosystem", name, display)),
			t.Muted.Render("registered as-is; nothing in the directory is touched"))
	default:
		lines = append(lines,
			t.Normal.Render(fmt.Sprintf("enter will CREATE %q at %s", name, display)),
			t.Muted.Render("scaffolds grove.toml, README.md, .gitignore and runs git init"))
	}
	return lipgloss.NewStyle().MaxWidth(p.width).Render(strings.Join(lines, "\n"))
}

// hasGrove reports whether name is already a configured grove.
func hasGrove(lc *config.LayeredConfig, name string) bool {
	_, ok := grovesConfig(lc)[name]
	return ok
}

// renderEcosystemFooter renders the key-hint footer.
func (p *EcosystemPage) renderEcosystemFooter() string {
	t := theme.DefaultTheme
	hints := "enter: edit the directory — its name becomes the ecosystem's name"
	if p.editing {
		hints = "enter: create/import & save • esc: cancel"
	}
	return lipgloss.NewStyle().PaddingTop(1).Render(t.Muted.Render(hints))
}

// commitEcosystem is the applyEcosystemMsg handler's work: refuse name
// collisions, scaffold (unless the directory already is an ecosystem —
// import mode), then write the groves registration. The global write runs
// LAST deliberately — a scaffold failure must leave no dangling groves entry
// pointing at a half-created directory (the write IS the registration; the
// reverse order could register garbage).
func (m *Model) commitEcosystem(raw string) (name string, imported bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("ecosystem path cannot be empty")
	}
	path := setup.ExpandPath(raw)
	name = setup.DeriveEcosystemName(path)
	if hasGrove(m.layered, name) {
		return "", false, fmt.Errorf("an ecosystem named %q is already configured — choose a different directory", name)
	}

	imported = setup.HasEcosystemManifest(path)
	if !imported {
		// The new page's manifest dialect is always TOML (the global config
		// write path here is TOML-first); the wizard keeps its YAML variant.
		if err := m.tomlHandler.Service().ScaffoldEcosystem(path, name, setup.ManifestTOML); err != nil {
			return "", false, err
		}
	}

	entry := map[string]interface{}{
		"path":        path,
		"enabled":     true,
		"description": "My projects",
		"notebook":    "nb",
	}
	if err := SaveGlobalSetting(m.tomlHandler, m.yamlHandler, m.layered, []string{"groves", name}, entry); err != nil {
		return "", false, err
	}
	return name, imported, nil
}
