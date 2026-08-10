package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/tui/theme"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
)

func recordedConfigDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir := filepath.Join(home, "config", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodePageEmptyStateDoesNotScanUntilConfirmationStartsScan(t *testing.T) {
	dir := recordedConfigDir(t)
	candidate := filepath.Join(t.TempDir(), "code")
	if err := os.MkdirAll(filepath.Join(candidate, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewCodePage(nil, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	if len(p.rows) != 0 {
		t.Fatalf("fresh page scanned rows: %#v", p.rows)
	}
	if view := p.View(); !strings.Contains(view, "Where does your code live?") || strings.Contains(view, "Grove will scan") {
		t.Fatalf("empty state is not minimal: %q", view)
	}

	// Drive the real input confirmation -> apply message path. No test-only
	// mutation of expanded is allowed: confirmation itself owns the first scan.
	p.active = true
	p.editing = true
	p.input.SetValue(candidate)
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirmation did not emit applyCodeRootMsg")
	}
	m := Model{codePage: p, workspacePath: t.TempDir()}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if len(m.codePage.rows) != 2 {
		t.Fatalf("confirmed root did not scan immediately; rows=%#v status=%q", m.codePage.rows, m.statusMsg)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "roots.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "scan = true") {
		t.Fatalf("root not recorded as scan root:\n%s", raw)
	}
}

func TestCodePageCheckboxWritesExcludeThroughSharedWriter(t *testing.T) {
	dir := recordedConfigDir(t)
	root := filepath.Join(t.TempDir(), "code")
	if err := os.MkdirAll(filepath.Join(root, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := "code"
	if _, err := coreconfig.WriteNotebooks(filepath.Join(dir, "notebooks.toml"), coreconfig.NotebookEdits{Default: &name, Upserts: map[string]coderoot.Notebook{name: {Root: "~/notes"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := coreconfig.WriteCodeRoots(filepath.Join(dir, "roots.toml"), coreconfig.CodeRootEdits{Upserts: map[string]coderoot.Root{name: {Path: root, Scan: true, Notebook: name}}}); err != nil {
		t.Fatal(err)
	}
	m := Model{}
	if err := m.toggleCodeExclude(name, "repo"); err != nil {
		t.Fatal(err)
	}
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Roots[name].Exclude) != 1 || table.Roots[name].Exclude[0] != "repo" {
		t.Fatalf("exclude=%v", table.Roots[name].Exclude)
	}
}

func TestAddCodeRootSameBasenameDoesNotOverwrite(t *testing.T) {
	recordedConfigDir(t)
	m := Model{}
	first := filepath.Join(t.TempDir(), "one", "code")
	second := filepath.Join(t.TempDir(), "two", "code")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	firstName, err := m.addCodeRoot(first)
	if err != nil {
		t.Fatal(err)
	}
	secondName, err := m.addCodeRoot(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstName == secondName {
		t.Fatalf("same-basename roots reused key %q", firstName)
	}
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := table.Roots[firstName].Path; got != first {
		t.Fatalf("first binding overwritten: got %q want %q", got, first)
	}
	if got := table.Roots[secondName].Path; got != second {
		t.Fatalf("second binding missing: got %q want %q", got, second)
	}
}

func TestEcosystemRowsFollowManifestAndMembersAreReadOnly(t *testing.T) {
	eco := filepath.Join(t.TempDir(), "eco")
	for _, rel := range []string{"packages/alpha/.git", "nested/bravo/.git", "incidental/.git"} {
		if err := os.MkdirAll(filepath.Join(eco, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := "workspaces = [\"packages/alpha\", \"nested/*\"]\n"
	if err := os.WriteFile(filepath.Join(eco, "grove.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	rows := discoverEcosystemMembers("code", eco, "nb")
	if len(rows) != 2 || rows[0].label != "nested/bravo" || rows[1].label != "packages/alpha" {
		t.Fatalf("manifest members=%#v", rows)
	}
	for _, row := range rows {
		if row.repo != "eco" || row.level != 2 || !row.member {
			t.Fatalf("member lost parent/member identity: %#v", row)
		}
	}

	p := &CodePage{keys: grovekeymap.NewConfigKeyMap(nil), width: 100, height: 30, active: true, expanded: map[string]bool{"eco": true}}
	p.rebuild(coderoot.Table{Roots: map[string]coderoot.Root{"eco": {Path: eco, Scan: true, Notebook: "nb"}}})
	if len(p.rows) != 3 || p.rows[1].label != "nested/bravo" || p.rows[2].label != "packages/alpha" {
		t.Fatalf("recorded ecosystem did not render manifest members: %#v", p.rows)
	}
	p.cursor = 1
	if _, cmd := p.Update(tea.KeyMsg{Type: tea.KeySpace}); cmd != nil {
		t.Fatalf("member checkbox emitted ineffective exclusion: %#v", cmd())
	}
	view := p.View()
	if strings.Contains(view, "☑") || strings.Contains(view, "include/exclude") || !strings.Contains(view, theme.IconRepo) {
		t.Fatalf("member rendering is not read-only/minimal: %q", view)
	}
}

func TestContainingEcosystemCheckboxPersistsRepresentableExclude(t *testing.T) {
	dir := recordedConfigDir(t)
	scanRoot := filepath.Join(t.TempDir(), "code")
	eco := filepath.Join(scanRoot, "eco")
	if err := os.MkdirAll(filepath.Join(eco, "member", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eco, "grove.toml"), []byte("workspaces = [\"member\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "code"
	if _, err := coreconfig.WriteNotebooks(filepath.Join(dir, "notebooks.toml"), coreconfig.NotebookEdits{Default: &name, Upserts: map[string]coderoot.Notebook{name: {Root: "~/notes"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := coreconfig.WriteCodeRoots(filepath.Join(dir, "roots.toml"), coreconfig.CodeRootEdits{Upserts: map[string]coderoot.Root{name: {Path: scanRoot, Scan: true, Notebook: name}}}); err != nil {
		t.Fatal(err)
	}
	p := NewCodePage(nil, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	p.active = true
	p.expanded[name] = true
	p.expanded[name+"/eco"] = true
	p.Refresh(nil)
	if len(p.rows) != 3 || !p.rows[1].ecosystem || !p.rows[2].member {
		t.Fatalf("ecosystem tree=%#v", p.rows)
	}
	p.cursor = 1
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeySpace})
	if cmd == nil {
		t.Fatal("containing ecosystem has no editable checkbox")
	}
	msg, ok := cmd().(toggleCodeExcludeMsg)
	if !ok || msg.root != name || msg.repo != "eco" {
		t.Fatalf("exclude message=%#v", msg)
	}
	m := Model{}
	if err := m.toggleCodeExclude(msg.root, msg.repo); err != nil {
		t.Fatal(err)
	}
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := table.Roots[name].Exclude; len(got) != 1 || got[0] != "eco" {
		t.Fatalf("ecosystem exclude=%v", got)
	}
}

func TestNotesPageRendersTwoLinesAndCounts(t *testing.T) {
	dir := recordedConfigDir(t)
	root := filepath.Join(t.TempDir(), "nb")
	for _, f := range []string{"notes/a.md", "plans/p.md", "chats/c.md"} {
		path := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := "nb"
	if _, err := coreconfig.WriteNotebooks(filepath.Join(dir, "notebooks.toml"), coreconfig.NotebookEdits{Default: &name, Upserts: map[string]coderoot.Notebook{name: {Root: root}}}); err != nil {
		t.Fatal(err)
	}
	p := NewNotesPage(nil, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	view := p.View()
	for _, want := range []string{"nb", "1 notes", "1 plans", "1 chats"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %s", want, view)
		}
	}
	_, _ = p.Update(tea.KeyMsg{})
}
