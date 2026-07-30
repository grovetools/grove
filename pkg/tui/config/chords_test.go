package config

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
)

// newChordModel builds a config Model with the Data tab active and a realistic
// window size, so View() renders a full frame the which-key popup can dock to.
// It goes through newConfigModel so it inherits the filter pin — these tests
// fire the very toggles that persist UI state, so an unpinned model would make
// every later test in the package depend on this one's leftovers.
func newChordModel(t *testing.T) Model {
	t.Helper()
	m := newConfigModel(t, grovekeymap.NewConfigKeyMap(nil))
	m.width, m.height = 100, 40
	// The Data page builds its node list lazily; expand it so row-scoped
	// actions (field info) have a node under the cursor.
	if p := m.activeLayerPage(); p != nil {
		configui.ExpandAll(p.treeRoots)
		p.rebuildNodeList()
		p.updateContent()
	}
	return m
}

// TestViewChordsDispatch covers the v… namespace: each member must fire the
// action the flat key used to.
func TestViewChordsDispatch(t *testing.T) {
	// vm — toggle view mode (was flat `v`, the reserved-prefix squatter).
	m := newChordModel(t)
	before := m.filters.ViewMode
	if m = pressChord(t, m, "vm"); m.filters.ViewMode == before {
		t.Error("vm did not cycle the view mode")
	}

	// vs — config sources (was flat `c`, the reserved-prefix squatter).
	m = newChordModel(t)
	if m = pressChord(t, m, "vs"); m.state != viewSources {
		t.Errorf("vs did not open the sources overlay (state=%v)", m.state)
	}

	// vp — toggle preview (was flat `p`).
	m = newChordModel(t)
	preview := m.filters.ShowPreview
	if m = pressChord(t, m, "vp"); m.filters.ShowPreview == preview {
		t.Error("vp did not toggle the preview pane")
	}

	// vi — field info (was flat `i`, now freed for Ring-1 insert). The layer
	// page answers with an async infoNodeMsg rather than a state change.
	m = newChordModel(t)
	m = pressChord(t, m, "v")
	updated, cmd := m.Update(runeKey('i'))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("vi produced no command")
	}
	if _, ok := cmd().(infoNodeMsg); !ok {
		t.Errorf("vi emitted %T, want infoNodeMsg", cmd())
	}
}

// TestToggleChordsDispatch covers the t… namespace (RULE T), including the
// uppercase-in-chord reverse cycles.
func TestToggleChordsDispatch(t *testing.T) {
	// ts / tS — cycle sort forward, then back to where it started.
	m := newChordModel(t)
	sort := m.filters.SortMode
	if m = pressChord(t, m, "ts"); m.filters.SortMode == sort {
		t.Error("ts did not cycle the sort mode")
	}
	if m = pressChord(t, m, "tS"); m.filters.SortMode != sort {
		t.Errorf("tS did not cycle sort back: %v, want %v", m.filters.SortMode, sort)
	}

	// tm — cycle maturity (was flat `m`).
	m = newChordModel(t)
	maturity := m.filters.MaturityFilter
	if m = pressChord(t, m, "tm"); m.filters.MaturityFilter == maturity {
		t.Error("tm did not cycle the maturity filter")
	}
}

// TestChordSeamConsumesEscAndStrayKeys locks in the two which-key idioms the
// host provides: esc dismisses an armed namespace, and a stray key closes the
// menu instead of firing its own flat action ("t" then "q" must not quit).
func TestChordSeamConsumesEscAndStrayKeys(t *testing.T) {
	m := pressChord(t, newChordModel(t), "v")
	if !m.whichKey.Armed() {
		t.Fatal("`v` did not arm the View namespace")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = updated.(Model); m.whichKey.IsPending() {
		t.Error("esc did not clear the armed chord")
	}

	m = pressChord(t, newChordModel(t), "t")
	if _, cmd := m.Update(runeKey('q')); cmd != nil {
		t.Error("a stray `q` while armed leaked a command — it would have quit the editor")
	}
}

// TestWhichKeyPopupRendersBottomAnchored asserts View() docks the popup at the
// bottom of the frame once the show-delay has elapsed.
func TestWhichKeyPopupRendersBottomAnchored(t *testing.T) {
	m := newChordModel(t)
	m.whichKey.Delay = 0
	m = pressChord(t, m, "t")

	out := m.View()
	if !strings.Contains(out, "Toggle (t…)") {
		t.Fatalf("which-key popup not rendered; view:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	tail := strings.Join(lines[len(lines)-12:], "\n")
	if !strings.Contains(tail, "cycle sort") {
		t.Errorf("popup is not bottom-anchored; last 12 lines:\n%s", tail)
	}
}
