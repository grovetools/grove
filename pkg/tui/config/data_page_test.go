package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"

	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// writeGlobalAndProjectLayers builds a LayeredConfig with a global file and
// a project file carrying an orphan key (so config.AuditLayered reports a
// finding on the project layer).
func writeGlobalAndProjectLayers(t *testing.T) (*config.LayeredConfig, string, string) {
	t.Helper()
	dir := t.TempDir()

	globalPath := filepath.Join(dir, "global-grove.toml")
	if err := os.WriteFile(globalPath, []byte("[tui]\n"), 0o600); err != nil {
		t.Fatalf("failed to write global config: %v", err)
	}

	projPath := filepath.Join(dir, "grove.toml")
	content := "workspaces = [\"a\"]\n\n[stale_section]\nold_key = \"unused\"\n"
	if err := os.WriteFile(projPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write project config: %v", err)
	}

	layered := &config.LayeredConfig{
		Final:  &config.Config{},
		Global: &config.Config{},
		Project: &config.Config{
			Extensions: map[string]interface{}{
				"stale_section": map[string]interface{}{"old_key": "unused"},
			},
		},
		FilePaths: map[config.ConfigSource]string{
			config.SourceGlobal:  globalPath,
			config.SourceProject: projPath,
		},
	}
	return layered, globalPath, projPath
}

// newDataModel builds a Model over layered with the Data tab active.
func newDataModel(t *testing.T, layered *config.LayeredConfig, workspace string) Model {
	t.Helper()
	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil))
	m.workspacePath = workspace
	m.pager.SetActive(5) // Data tab (last)
	return m
}

func TestPagerPageOrder(t *testing.T) {
	m, _ := newTestModel(t)

	pages := m.pager.Pages()
	wantNames := []string{"Appearance", "Layout", "Keys", "Themes", "Notebook", "Data"}
	if len(pages) != len(wantNames) {
		t.Fatalf("expected %d pages, got %d", len(wantNames), len(pages))
	}
	for i, want := range wantNames {
		if got := pages[i].Name(); got != want {
			t.Errorf("page %d name = %q, want %q", i, got, want)
		}
	}
	if _, ok := pages[5].(*DataPage); !ok {
		t.Fatalf("expected last page to be *DataPage, got %T", pages[5])
	}
}

// TestDataPageCyclesLayers walks the cycle key through all four layers,
// asserting the title reflects each layer's name + file path and that audit
// rows (re)appear when the layer that owns the orphan key is shown.
func TestDataPageCyclesLayers(t *testing.T) {
	layered, globalPath, projPath := writeGlobalAndProjectLayers(t)
	m := newDataModel(t, layered, filepath.Dir(projPath))
	dp := m.dataPage

	pressL := func() {
		t.Helper()
		updated, _ := m.Update(runeKey('L'))
		m = updated.(Model)
	}

	// Starts on Global, title shows layer name + global file path.
	if dp.Layer() != config.SourceGlobal {
		t.Fatalf("initial layer = %s, want global", dp.Layer())
	}
	if title := dp.Title(); !strings.Contains(title, "Global") || !strings.Contains(title, setup.AbbreviatePath(globalPath)) {
		t.Errorf("Global title = %q, want layer name and %q", title, setup.AbbreviatePath(globalPath))
	}

	// → Ecosystem (no file in this fixture).
	pressL()
	if dp.Layer() != config.SourceEcosystem {
		t.Fatalf("after 1 cycle layer = %s, want ecosystem", dp.Layer())
	}
	if title := dp.Title(); !strings.Contains(title, "Ecosystem") || !strings.Contains(title, "no config file") {
		t.Errorf("Ecosystem title = %q, want layer name and no-file marker", title)
	}

	// → Notebook.
	pressL()
	if dp.Layer() != config.SourceProjectNotebook {
		t.Fatalf("after 2 cycles layer = %s, want notebook", dp.Layer())
	}
	if title := dp.Title(); !strings.Contains(title, "Notebook") {
		t.Errorf("Notebook title = %q, want layer name", title)
	}

	// → Project: file path shown and the orphan audit row appears.
	pressL()
	if dp.Layer() != config.SourceProject {
		t.Fatalf("after 3 cycles layer = %s, want project", dp.Layer())
	}
	if title := dp.Title(); !strings.Contains(title, "Project") || !strings.Contains(title, setup.AbbreviatePath(projPath)) {
		t.Errorf("Project title = %q, want layer name and %q", title, setup.AbbreviatePath(projPath))
	}
	node := findAuditNode(dp.inner)
	if node == nil {
		t.Fatal("expected the orphan audit row on the project layer")
	}
	if node.Audit.Key != "stale_section" {
		t.Errorf("audit key = %q, want %q", node.Audit.Key, "stale_section")
	}

	// Footer/filter-state line advertises the cycle key.
	if fs := dp.inner.renderFilterState(); !strings.Contains(fs, "[L: layer →]") {
		t.Errorf("filter state %q missing cycle hint", fs)
	}

	// → wraps back to Global; audit row from the project layer is gone.
	pressL()
	if dp.Layer() != config.SourceGlobal {
		t.Fatalf("after 4 cycles layer = %s, want global (wrap)", dp.Layer())
	}
	if n := findAuditNode(dp.inner); n != nil {
		t.Errorf("global layer unexpectedly shows project audit row %q", n.Audit.Key)
	}
}

// TestDataPageEditAndDeleteRouting proves the page-local messages still
// reach the outer Model through the DataPage wrapper: dd on an audit row
// opens the delete-confirm overlay, enter on a schema leaf opens the edit
// overlay.
func TestDataPageEditAndDeleteRouting(t *testing.T) {
	layered, path := writeProjectLayer(t)
	m := newDataModel(t, layered, filepath.Dir(path))

	// Cycle Global → Ecosystem → Notebook → Project.
	for i := 0; i < 3; i++ {
		updated, _ := m.Update(runeKey('L'))
		m = updated.(Model)
	}
	p := m.activeLayerPage()
	if p == nil || p.Layer() != config.SourceProject {
		t.Fatalf("expected active layer page on project layer, got %v", p)
	}

	// --- delete: dd on the orphan audit row.
	auditIdx := -1
	for i, n := range p.nodes {
		if n.Audit != nil && !n.IsAuditSection() {
			auditIdx = i
			break
		}
	}
	if auditIdx < 0 {
		t.Fatal("no audit row found on project layer")
	}
	p.cursor = auditIdx

	updated, _ := m.Update(runeKey('d'))
	m = updated.(Model)
	updated, cmd := m.Update(runeKey('d'))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("dd produced no command")
	}
	msg := cmd()
	if _, ok := msg.(deleteNodeMsg); !ok {
		t.Fatalf("dd emitted %T, want deleteNodeMsg", msg)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.state != viewConfirmDelete {
		t.Fatalf("state = %d, want viewConfirmDelete", m.state)
	}
	if m.deleteFile != path {
		t.Errorf("deleteFile = %q, want %q", m.deleteFile, path)
	}

	// Cancel back to the list.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != viewList {
		t.Fatalf("esc did not return to list view, state = %d", m.state)
	}

	// --- edit: enter on a schema leaf.
	p = m.activeLayerPage()
	configui.ExpandAll(p.treeRoots)
	p.rebuildNodeList()
	p.updateContent()

	leafIdx := -1
	for i, n := range p.nodes {
		if n.Audit == nil && !n.IsExpandable() && !n.IsContainer() {
			leafIdx = i
			break
		}
	}
	if leafIdx < 0 {
		t.Fatal("no editable schema leaf found on project layer")
	}
	p.cursor = leafIdx

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg = cmd()
	em, ok := msg.(editNodeMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want editNodeMsg", msg)
	}
	updated, _ = m.Update(em)
	m = updated.(Model)
	if m.state != viewEdit {
		t.Fatalf("state = %d, want viewEdit", m.state)
	}
}

// TestDataPageSurvivesPagerReassignment is the A3 regression: the pager
// reassigns m.pages[active] = updated on every message, and LayerPage.Update
// returns its bare receiver — so DataPage.Update must never leak the inner
// page as its return value or the layer cycler is permanently lost.
func TestDataPageSurvivesPagerReassignment(t *testing.T) {
	m, _ := newTestModel(t)
	m.pager.SetActive(5)

	// Direct contract: Update returns the DataPage itself.
	page, _ := m.dataPage.Update(runeKey('j'))
	if page != m.dataPage {
		t.Fatalf("DataPage.Update returned %T (%p), want the DataPage itself (%p)", page, page, m.dataPage)
	}

	// Through the pager: after a delegated keystroke, slot 5 must still
	// hold the *DataPage.
	updated, _ := m.Update(runeKey('j'))
	m = updated.(Model)
	if _, ok := m.pager.Pages()[5].(*DataPage); !ok {
		t.Fatalf("pager slot 5 holds %T after keystroke — DataPage was replaced by its inner page", m.pager.Pages()[5])
	}

	// And the cycler still works afterwards.
	updated, _ = m.Update(runeKey('L'))
	m = updated.(Model)
	if m.dataPage.Layer() != config.SourceEcosystem {
		t.Fatalf("cycle key inert after keystroke: layer = %s", m.dataPage.Layer())
	}
}
