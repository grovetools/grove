package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/grovetools/grove/pkg/release"
)

// pressReleaseChord types a which-key chord one keystroke at a time.
func pressReleaseChord(t *testing.T, m releaseTuiModel, chord string) releaseTuiModel {
	t.Helper()
	for _, r := range chord {
		updated, _ := m.Update(keyMsg(r))
		m = updated.(releaseTuiModel)
	}
	return m
}

func newChordReleaseModel(t *testing.T) releaseTuiModel {
	t.Helper()
	return newTestReleaseModel(t, &release.RepoReleasePlan{
		CurrentVersion: "v1.0.0",
		NextVersion:    "v1.1.0",
		DocsGenerated:  true,
		DocsSections:   []string{"overview"},
	})
}

// TestReleaseChordsDispatch walks every v…/t…/c… member and asserts it fires
// the action its retired flat key used to.
func TestReleaseChordsDispatch(t *testing.T) {
	// v… — the view namespace.
	if m := pressReleaseChord(t, newChordReleaseModel(t), "vc"); m.currentView != viewChangelog {
		t.Errorf("vc: view=%q, want changelog", m.currentView)
	}
	if m := pressReleaseChord(t, newChordReleaseModel(t), "vs"); m.currentView != viewDocs {
		t.Errorf("vs: view=%q, want docs", m.currentView)
	}
	if m := pressReleaseChord(t, newChordReleaseModel(t), "vd"); m.currentView != viewDiff {
		t.Errorf("vd: view=%q, want diff", m.currentView)
	}

	// c… — the change namespace. cd is the rebind that retires the fleet's
	// only real reserved-key violation (`G` is reserved for bottom).
	if m := pressReleaseChord(t, newChordReleaseModel(t), "cd"); !m.generating {
		t.Error("cd did not start a docs regeneration")
	}
	if m := pressReleaseChord(t, newChordReleaseModel(t), "cg"); !m.generating {
		t.Error("cg did not start a changelog generation")
	}

	// t… — the toggle namespace, reachable from the settings view.
	m := newChordReleaseModel(t)
	m.currentView = viewSettings
	if got := pressReleaseChord(t, m, "td"); got.dryRun == m.dryRun {
		t.Error("td did not toggle dry-run")
	}
	if got := pressReleaseChord(t, m, "tp"); got.push == m.push {
		t.Error("tp did not toggle push")
	}
}

// TestReleaseRetiredFlatKeysAreInert proves the migration is chord-ONLY (E4):
// the old flat keys must no longer fire.
func TestReleaseRetiredFlatKeysAreInert(t *testing.T) {
	// `G` is reserved for bottom and must not regenerate docs any more.
	m := newChordReleaseModel(t)
	updated, _ := m.Update(keyMsg('G'))
	if got := updated.(releaseTuiModel); got.generating || got.currentView != viewTable {
		t.Error("flat G still triggered regen_docs")
	}

	// `V` no longer opens the docs panel (it is `vs` now).
	m = newChordReleaseModel(t)
	updated, _ = m.Update(keyMsg('V'))
	if got := updated.(releaseTuiModel); got.currentView != viewTable {
		t.Errorf("flat V still opened the docs panel (view=%q)", got.currentView)
	}

	// `x` was dropped from the selection toggle; space is retained.
	m = newChordReleaseModel(t)
	selected := m.plan.Repos["foo"].Selected
	_, _ = m.Update(keyMsg('x'))
	if m.plan.Repos["foo"].Selected != selected {
		t.Error("flat x still toggled selection")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.plan.Repos["foo"].Selected == selected {
		t.Error("space no longer toggles selection")
	}
}

// TestReleaseChordSeamConsumesEscAndStrayKeys covers the two which-key idioms.
func TestReleaseChordSeamConsumesEscAndStrayKeys(t *testing.T) {
	m := pressReleaseChord(t, newChordReleaseModel(t), "c")
	if !m.whichKey.Armed() {
		t.Fatal("`c` did not arm the Change namespace")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(releaseTuiModel); got.whichKey.IsPending() {
		t.Error("esc did not clear the armed chord")
	}

	m = pressReleaseChord(t, newChordReleaseModel(t), "t")
	if _, cmd := m.Update(keyMsg('q')); cmd != nil {
		t.Error("a stray `q` while armed leaked a command — it would have quit the TUI")
	}
}

// TestReleaseWhichKeyPopupRendersBottomAnchored asserts View() docks the popup
// at the bottom of the frame once the show-delay has elapsed.
func TestReleaseWhichKeyPopupRendersBottomAnchored(t *testing.T) {
	m := newChordReleaseModel(t)
	m.height = 40
	m.whichKey.Delay = 0
	m = pressReleaseChord(t, m, "c")

	out := m.View()
	if !strings.Contains(out, "Change (c…)") {
		t.Fatalf("which-key popup not rendered; view:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	tail := strings.Join(lines[len(lines)-10:], "\n")
	if !strings.Contains(tail, "regenerate docs section") {
		t.Errorf("popup is not bottom-anchored; last 10 lines:\n%s", tail)
	}
}
