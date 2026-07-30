package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// TestMain sandboxes the XDG state dir for the whole package. Model.saveUIState
// persists the preview/view-mode/maturity/sort filters to
// $XDG_STATE_HOME/grove/config-ui.json, so any test that fires one of those
// toggles otherwise writes the DEVELOPER'S real UI state — and, because
// loadUIStateFromDisk seeds the next run's filters from that same file, leaks
// across runs: a flipped maturity filter empties the Data page's node list and
// the z-chord tests fail intermittently on the following invocation.
// GROVE_HOME wins over XDG_STATE_HOME in paths.StateDir, so it is cleared too;
// the tests that genuinely need a grove home set their own with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "grove-config-ui-state")
	if err != nil {
		panic(err)
	}
	os.Setenv("GROVE_HOME", "")
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// runeKey builds a KeyMsg for a single printable rune (e.g. 'z', 'M', 'a').
func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// pressChord types a which-key chord one rune at a time (e.g. "tl" = cycle
// layer, "vs" = config sources), returning the Model after the last keystroke.
// The intermediate presses arm the namespace and return a popup tick, which
// tests do not need.
func pressChord(t *testing.T, m Model, chord string) Model {
	t.Helper()
	for _, r := range chord {
		updated, _ := m.Update(runeKey(r))
		m = updated.(Model)
	}
	return m
}

// newConfigModel builds a config Model with the given keymap over a temp
// project layer, with the Data tab active on its Global layer (the
// schema-driven tree the layer-page key tests exercise).
func newConfigModel(t *testing.T, keys grovekeymap.ConfigKeyMap) Model {
	t.Helper()
	layered, path := writeProjectLayer(t)
	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), keys)
	m.workspacePath = filepath.Dir(path)
	m.pager.SetActive(6) // Data tab

	// New() seeds the filters from the on-disk UI state, and any sibling test
	// that fires a preview/view-mode/sort/maturity toggle writes that file back
	// — so without an explicit pin the tree a test walks depends on which tests
	// ran before it (a persisted ViewConfigured empties the Data page outright).
	// Re-assert the shipped defaults so every model starts from the same tree.
	m.filters.ShowPreview = true
	m.filters.ViewMode = configui.ViewAll
	m.filters.MaturityFilter = configui.MaturityStable
	m.filters.SortMode = configui.SortConfiguredFirst
	m.refreshAllPages()
	return m
}

// TestMaturityFilterRoutesThroughKeymap proves the maturity-filter cycle is
// matched via key.Matches against the ConfigKeyMap (honoring overrides), not a
// raw "m" compare: rebinding the field to another key makes the old key inert
// and the new key live.
func TestMaturityFilterRoutesThroughKeymap(t *testing.T) {
	keys := grovekeymap.NewConfigKeyMap(nil)
	keys.MaturityFilter = key.NewBinding(
		key.WithKeys("9"),
		key.WithHelp("9", "cycle maturity"),
	)
	m := newConfigModel(t, keys)

	before := m.filters.MaturityFilter

	// The old default key "m" must no longer cycle the filter.
	updated, _ := m.Update(runeKey('m'))
	m = updated.(Model)
	if m.filters.MaturityFilter != before {
		t.Fatalf("old key 'm' still fired maturity cycle — raw compare regression")
	}

	// The rebound key "9" must cycle it.
	updated, _ = m.Update(runeKey('9'))
	m = updated.(Model)
	if m.filters.MaturityFilter == before {
		t.Fatalf("rebound key '9' did not cycle maturity filter")
	}
}

// TestZMCollapseAllNotShadowed locks in the dead-zM fix: with a z-chord
// pending, the raw "M" (maturity-back) handler must defer to the pager so the
// page receives "zM" as collapse-all, and the maturity filter stays put.
func TestZMCollapseAllNotShadowed(t *testing.T) {
	m := newConfigModel(t, grovekeymap.NewConfigKeyMap(nil))

	p := m.activeLayerPage()
	if p == nil {
		t.Fatal("expected an active layer page")
	}
	configui.ExpandAll(p.treeRoots)
	p.rebuildNodeList()
	p.updateContent()

	before := len(p.nodes)
	if before == 0 {
		t.Fatal("expected schema nodes on the Global layer page")
	}
	maturityBefore := m.filters.MaturityFilter

	// z then M -> collapse-all (not maturity-back).
	updated, _ := m.Update(runeKey('z'))
	m = updated.(Model)
	updated, _ = m.Update(runeKey('M'))
	m = updated.(Model)

	p = m.activeLayerPage()
	if len(p.nodes) >= before {
		t.Errorf("zM collapse-all did not fire: nodes before=%d after=%d", before, len(p.nodes))
	}
	if m.filters.MaturityFilter != maturityBefore {
		t.Errorf("zM was shadowed by maturity-back: filter changed %v -> %v", maturityBefore, m.filters.MaturityFilter)
	}
}

// TestZAToggleNode proves za is wired to ToggleNode on the focused node.
func TestZAToggleNode(t *testing.T) {
	m := newConfigModel(t, grovekeymap.NewConfigKeyMap(nil))

	p := m.activeLayerPage()
	if p == nil {
		t.Fatal("expected an active layer page")
	}

	idx := -1
	for i, n := range p.nodes {
		if n.IsExpandable() {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("expected at least one expandable node on the Global layer page")
	}
	p.cursor = idx
	node := p.nodes[idx]
	before := node.Collapsed

	// z then a -> toggle fold on the focused node.
	updated, _ := m.Update(runeKey('z'))
	m = updated.(Model)
	updated, _ = m.Update(runeKey('a'))
	m = updated.(Model)

	if node.Collapsed == before {
		t.Errorf("za did not toggle node collapse state (still %v)", before)
	}
}
