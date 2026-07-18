package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// notebookRootSetting returns the Notebook page's root-dir descriptor.
func notebookRootSetting(t *testing.T) Setting {
	t.Helper()
	s := NotebookSettings()[0]
	if s.ID != "notebook_root" {
		t.Fatalf("first notebook setting is %q, want notebook_root", s.ID)
	}
	return s
}

// TestNotebookCommitFreshConfig: with no default rule configured, a commit
// creates definitions.personal.root_dir (~ expanded), points rules.default at
// it, mkdirs the root, and clobbers nothing else. No SettingAppliedMsg — the
// row has no apply domain (nothing hot-applies a notebook move).
func TestNotebookCommitFreshConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, _, workspace := newCuratedTestModel(t)
	s := notebookRootSetting(t)

	// Fresh config: the row suggests the wizard default.
	if got := s.Read(m.layered); got != "~/notebooks" {
		t.Errorf("fresh Read = %q, want ~/notebooks", got)
	}

	m2, applied := applySetting(t, m, s, "~/nbroot")
	if applied != nil {
		t.Error("notebook root emitted a SettingAppliedMsg despite having no apply domain")
	}

	want := filepath.Join(home, "nbroot")
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload after commit: %v", err)
	}
	nb := reloaded.Final.Notebooks
	if nb == nil || nb.Rules == nil || nb.Rules.Default != "personal" {
		t.Fatalf("rules.default not \"personal\" after fresh commit: %+v", nb)
	}
	if def := nb.Definitions["personal"]; def == nil || def.RootDir != want {
		t.Fatalf("definitions.personal.root_dir = %+v, want %q", def, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Errorf("notebook root not created: %v", err)
	}
	// Display value tracks the write, back in the ~-abbreviated dialect.
	if got := s.Read(m2.layered); got != "~/nbroot" {
		t.Errorf("Read after commit = %q, want ~/nbroot", got)
	}
	// The seeded theme survives — the whole file still parses.
	if reloaded.Final.TUI == nil || reloaded.Final.TUI.Theme != "kanagawa-dark" {
		t.Error("seeded tui.theme lost after notebook commit")
	}
}

// TestNotebookCommitExistingDefaultRule pins the no-clobber rule: when
// rules.default already names a definition, a commit edits THAT definition's
// root_dir in place — the rule is untouched and no "personal" definition is
// invented.
func TestNotebookCommitExistingDefaultRule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, globalPath, workspace := newCuratedTestModelSeeded(t, strings.Join([]string{
		"[tui]",
		"theme = \"kanagawa-dark\"",
		"",
		"[notebooks.definitions.nb]",
		"root_dir = \"/somewhere/else\"",
		"",
		"[notebooks.rules]",
		"default = \"nb\"",
		"",
	}, "\n"))
	s := notebookRootSetting(t)

	if got := s.Read(m.layered); got != "/somewhere/else" {
		t.Errorf("Read = %q, want the existing default's root_dir", got)
	}

	_, _ = applySetting(t, m, s, "~/moved")

	want := filepath.Join(home, "moved")
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload after commit: %v", err)
	}
	nb := reloaded.Final.Notebooks
	if nb == nil || nb.Rules == nil || nb.Rules.Default != "nb" {
		t.Fatalf("rules.default changed: %+v", nb)
	}
	if def := nb.Definitions["nb"]; def == nil || def.RootDir != want {
		t.Fatalf("definitions.nb.root_dir = %+v, want %q", def, want)
	}
	if _, exists := nb.Definitions["personal"]; exists {
		t.Error("a \"personal\" definition was invented despite an existing default rule")
	}
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if strings.Contains(string(raw), "personal") {
		t.Errorf("\"personal\" leaked into the file:\n%s", raw)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Errorf("notebook root not created: %v", err)
	}
}

// TestNotebookMkdirFailureKeepsConfig: mkdir runs strictly after the config
// writes, so a failing mkdir surfaces as a status error while the config on
// disk stays complete and parseable.
func TestNotebookMkdirFailureKeepsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A plain file where the parent directory should go makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(home, "blocked"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	m, _, workspace := newCuratedTestModel(t)
	s := notebookRootSetting(t)

	updated, _ := m.Update(setSettingMsg{setting: s, value: "~/blocked/nb"})
	m2 := updated.(Model)
	if !strings.HasPrefix(m2.statusMsg, "Error") {
		t.Errorf("mkdir failure not surfaced: %q", m2.statusMsg)
	}

	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload after failed mkdir: %v", err)
	}
	nb := reloaded.Final.Notebooks
	want := filepath.Join(home, "blocked", "nb")
	if nb == nil || nb.Definitions["personal"] == nil || nb.Definitions["personal"].RootDir != want {
		t.Fatalf("config writes did not land before the mkdir failure: %+v", nb)
	}
	if reloaded.Global == nil || reloaded.Final.TUI == nil || reloaded.Final.TUI.Theme != "kanagawa-dark" {
		t.Error("global config corrupted by the failed commit")
	}
}

// TestNotebookEssentials: the root row is the page's one essential —
// onboarding's density shows exactly it.
func TestNotebookEssentials(t *testing.T) {
	m, _, _ := newSinglePageTestModel(t, "notebook", SinglePageOpts{EssentialsOnly: true})
	got := m.curatedPages[0].Settings()
	if len(got) != 1 || got[0].ID != "notebook_root" {
		ids := make([]string, 0, len(got))
		for _, s := range got {
			ids = append(ids, s.ID)
		}
		t.Fatalf("essentials = %v, want [notebook_root]", ids)
	}
}

// TestNotebookPageOrderInFullPager: the full pager carries the Notebook tab
// after Themes, then Ecosystem, then Data last.
func TestNotebookPageOrderInFullPager(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	var names []string
	for _, p := range m.pager.Pages() {
		names = append(names, p.Name())
	}
	want := []string{"Appearance", "Layout", "Keys", "Themes", "Notebook", "Ecosystem", "Data"}
	if len(names) != len(want) {
		t.Fatalf("pages = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("pages = %v, want %v", names, want)
		}
	}
}
