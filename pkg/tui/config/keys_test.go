package config

import (
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// runeKey builds a KeyMsg for a single printable rune (e.g. 'z', 'M', 'a').
func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// newConfigModel builds a config Model with the given keymap over a temp
// project layer. The Global layer page (schema-driven, index 0) is active.
func newConfigModel(t *testing.T, keys grovekeymap.ConfigKeyMap) Model {
	t.Helper()
	layered, path := writeProjectLayer(t)
	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), keys)
	m.workspacePath = filepath.Dir(path)
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
