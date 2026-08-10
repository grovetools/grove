package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
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

func TestCodePageEmptyStateDoesNotScanUntilEnter(t *testing.T) {
	dir := recordedConfigDir(t)
	candidate := filepath.Join(t.TempDir(), "code")
	if err := os.MkdirAll(filepath.Join(candidate, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewCodePage(nil, grovekeymap.NewConfigKeyMap(nil), 100, 30)
	if len(p.rows) != 0 {
		t.Fatalf("fresh page scanned rows: %#v", p.rows)
	}
	if !strings.Contains(p.View(), "Where does your code live?") {
		t.Fatal("missing empty prompt")
	}
	m := Model{codePage: p}
	if err := m.addCodeRoot(candidate); err != nil {
		t.Fatal(err)
	}
	p.Refresh(nil)
	if len(p.rows) != 1 {
		t.Fatalf("recorded root rows=%d", len(p.rows))
	}
	p.expanded[p.rows[0].root] = true
	p.Refresh(nil)
	if len(p.rows) != 2 {
		t.Fatalf("scan-on-enter tree rows=%d", len(p.rows))
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
