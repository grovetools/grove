package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/pelletier/go-toml/v2"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// newTestModel builds a config TUI Model against a minimal layered config
// whose global layer points at a temp file.
func newTestModel(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "grove.toml")

	layered := &config.LayeredConfig{
		Final:     &config.Config{},
		FilePaths: map[config.ConfigSource]string{config.SourceGlobal: globalPath},
	}

	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil))
	m.workspacePath = dir
	return m, globalPath
}

// restoreTheme snapshots the process-wide theme and restores it when the
// test finishes, since preview/revert mutate package-global state.
func restoreTheme(t *testing.T) {
	t.Helper()
	orig := theme.DefaultTheme.Name
	t.Cleanup(func() { _ = theme.SetTheme(orig) })
}

// setBaselineTheme unpins GROVE_THEME and moves the process to a known
// registry theme so tests don't depend on the developer's own config.
func setBaselineTheme(t *testing.T) {
	t.Helper()
	restoreTheme(t)
	t.Setenv("GROVE_THEME", "")
	if err := theme.SetTheme("kanagawa-dark"); err != nil {
		t.Fatalf("setting baseline theme: %v", err)
	}
}

// previewTargetFor picks a registry theme from a different family than the
// page's saved baseline so a preview measurably changes the active theme
// (theme aliases may collapse same-family variant names onto one key).
func previewTargetFor(t *testing.T, p *ThemesPage) string {
	t.Helper()
	savedFamily := normalizeName(p.saved)
	if pal, ok := theme.Lookup(p.saved); ok {
		savedFamily = pal.Meta.Family
	}
	for _, meta := range theme.List() {
		if meta.Family != savedFamily {
			return meta.Name
		}
	}
	t.Fatal("registry has fewer than two theme families")
	return ""
}

// itemIndexOf returns the list index of the row selecting name.
func itemIndexOf(t *testing.T, p *ThemesPage, name string) int {
	t.Helper()
	for i, it := range p.items {
		if it.value == name {
			return i
		}
	}
	t.Fatalf("no list item for theme %q", name)
	return -1
}

func TestThemesPageRegistration(t *testing.T) {
	m, _ := newTestModel(t)

	pages := m.pager.Pages()
	if len(pages) != 5 {
		t.Fatalf("expected 5 pages (appearance, layout, keys, themes, data), got %d", len(pages))
	}
	tp, ok := pages[3].(*ThemesPage)
	if !ok {
		t.Fatalf("expected page 4 to be *ThemesPage, got %T", pages[3])
	}
	if tp.TabID() != "themes" {
		t.Errorf("TabID = %q, want %q", tp.TabID(), "themes")
	}
	if len(tp.items) == 0 {
		t.Error("themes page has no items — palette registry appears empty")
	}
}

// TestActiveLayerPageIndexSafety verifies that activeLayerPage resolves by
// type (unwrapping the DataPage), never by pager index: curated stubs and
// the Themes tab yield nil, the Data tab yields the wrapped LayerPage.
func TestActiveLayerPageIndexSafety(t *testing.T) {
	m, _ := newTestModel(t)

	// Curated stub tabs and the Themes tab have no layer page.
	for _, idx := range []int{0, 1, 2, 3} {
		m.pager.SetActive(idx)
		if got := m.activeLayerPage(); got != nil {
			t.Errorf("tab %d: activeLayerPage = %v, want nil", idx, got)
		}
	}

	// The Data tab (index 4) resolves to the DataPage's inner LayerPage.
	m.pager.SetActive(4)
	if got := m.activeLayerPage(); got != m.dataPage.inner {
		t.Errorf("activeLayerPage on Data tab = %p, want %p", got, m.dataPage.inner)
	}

	// refreshAllPages must handle the mixed page set without panicking.
	m.refreshAllPages()
}

func TestApplyThemeSavesToGlobalLayer(t *testing.T) {
	setBaselineTheme(t)
	m, globalPath := newTestModel(t)

	target := previewTargetFor(t, m.themesPage)

	model, _ := m.Update(applyThemeMsg{name: target})
	m = model.(Model)

	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	var parsed map[string]interface{}
	if err := toml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("written global config is not valid TOML: %v", err)
	}
	tui, ok := parsed["tui"].(map[string]interface{})
	if !ok {
		t.Fatalf("no [tui] table in written config: %s", data)
	}
	if got := tui["theme"]; got != target {
		t.Errorf("tui.theme = %v, want %q", got, target)
	}

	if m.themesPage.saved != target {
		t.Errorf("themesPage.saved = %q, want %q after save", m.themesPage.saved, target)
	}
	if m.themesPage.previewed {
		t.Error("previewed should be false after save")
	}
	if !strings.Contains(m.statusMsg, "saved") {
		t.Errorf("statusMsg = %q, expected save confirmation", m.statusMsg)
	}
}

func TestPreviewRevertsOnBlur(t *testing.T) {
	setBaselineTheme(t)

	layered := &config.LayeredConfig{Final: &config.Config{}, FilePaths: map[config.ConfigSource]string{}}
	p := NewThemesPage(layered, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	_ = p.Focus()

	orig := theme.DefaultTheme.Name
	target := previewTargetFor(t, p)

	p.setCursor(itemIndexOf(t, p, target))
	if theme.DefaultTheme.Name == orig {
		t.Fatalf("live preview did not apply: DefaultTheme.Name still %q after previewing %q", orig, target)
	}
	if !p.previewed {
		t.Fatal("previewed flag not set after cursor preview")
	}

	p.Blur()
	if theme.DefaultTheme.Name != orig {
		t.Errorf("Blur did not revert preview: DefaultTheme.Name = %q, want %q", theme.DefaultTheme.Name, orig)
	}
	if p.previewed {
		t.Error("previewed flag should be cleared after revert")
	}
}

func TestPreviewRevertsOnEsc(t *testing.T) {
	setBaselineTheme(t)

	layered := &config.LayeredConfig{Final: &config.Config{}, FilePaths: map[config.ConfigSource]string{}}
	p := NewThemesPage(layered, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	_ = p.Focus()

	orig := theme.DefaultTheme.Name
	target := previewTargetFor(t, p)
	p.setCursor(itemIndexOf(t, p, target))
	if theme.DefaultTheme.Name == orig {
		t.Fatalf("live preview did not apply")
	}

	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if theme.DefaultTheme.Name != orig {
		t.Errorf("esc did not revert preview: DefaultTheme.Name = %q, want %q", theme.DefaultTheme.Name, orig)
	}
}

func TestPreviewSkippedWhenPinned(t *testing.T) {
	setBaselineTheme(t)
	t.Setenv("GROVE_THEME", theme.DefaultTheme.Name)

	layered := &config.LayeredConfig{Final: &config.Config{}, FilePaths: map[config.ConfigSource]string{}}
	p := NewThemesPage(layered, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	_ = p.Focus()

	before := theme.DefaultTheme.Name
	target := previewTargetFor(t, p)
	p.setCursor(itemIndexOf(t, p, target))

	if theme.DefaultTheme.Name != before {
		t.Errorf("pinned process re-themed: DefaultTheme.Name = %q, want %q", theme.DefaultTheme.Name, before)
	}
	if p.previewed {
		t.Error("previewed flag should not be set while pinned")
	}
}

// TestThemesPageViewRenders is a smoke test over the list + preview pane
// composition (swatches, sample chrome) across every selectable theme.
func TestThemesPageViewRenders(t *testing.T) {
	setBaselineTheme(t)

	layered := &config.LayeredConfig{Final: &config.Config{}, FilePaths: map[config.ConfigSource]string{}}
	p := NewThemesPage(layered, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	_ = p.Focus()
	p.SetSize(100, 30)

	for i, it := range p.items {
		if it.value == "" {
			continue
		}
		p.setCursor(i)
		if out := p.View(); out == "" {
			t.Fatalf("empty View() for theme %q", it.value)
		}
	}
	if title := p.Title(); title == "" {
		t.Error("Title() should not be empty")
	}
}

func TestEnterEmitsApplyThemeMsg(t *testing.T) {
	setBaselineTheme(t)

	layered := &config.LayeredConfig{Final: &config.Config{}, FilePaths: map[config.ConfigSource]string{}}
	p := NewThemesPage(layered, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	_ = p.Focus()

	target := previewTargetFor(t, p)
	p.setCursor(itemIndexOf(t, p, target))

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg, ok := cmd().(applyThemeMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want applyThemeMsg", cmd())
	}
	if msg.name != target {
		t.Errorf("applyThemeMsg.name = %q, want %q", msg.name, target)
	}
}
