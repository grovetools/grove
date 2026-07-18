package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// newEcosystemTestModel builds a single-page "ecosystem" Model over a private
// GROVE_HOME with a caller-provided global grove.toml seed, plus a private
// HOME so ~ paths land in a temp dir.
func newEcosystemTestModel(t *testing.T, seed string) (Model, string, string, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	groveHome := t.TempDir()
	t.Setenv("GROVE_HOME", groveHome)
	t.Setenv("GROVE_CONFIG_OVERLAY", "")

	globalDir := filepath.Join(groveHome, "config", "grove")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, "grove.toml")
	if err := os.WriteFile(globalPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed global config: %v", err)
	}

	workspace := t.TempDir()
	layered, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("initial LoadLayered: %v", err)
	}

	svc := setup.NewService(false)
	m, err := NewSinglePage("ecosystem", layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil), SinglePageOpts{})
	if err != nil {
		t.Fatalf("NewSinglePage(ecosystem): %v", err)
	}
	m.workspacePath = workspace
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, globalPath, workspace, home
}

// commitEcosystemPath drives the form through real keystrokes: enter opens
// the editor, ctrl+u clears the prefilled default, the path is typed, and
// enter commits — returning the Model after the applyEcosystemMsg round-trip.
func commitEcosystemPath(t *testing.T, m Model, path string) Model {
	t.Helper()
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.TextEntryActive() {
		t.Fatal("enter did not open the path editor (TextEntryActive false)")
	}
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(path)})
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("committing the path produced no command")
	}
	msg, ok := cmd().(applyEcosystemMsg)
	if !ok {
		t.Fatalf("expected applyEcosystemMsg, got %T", cmd())
	}
	m, cmd = updateModel(t, m, msg)
	if cmd != nil {
		t.Fatalf("ecosystem commit emitted an unexpected command: %#v", cmd())
	}
	return m
}

// TestEcosystemCommitCreate: a fresh directory is scaffolded (manifest,
// README, .gitignore, git init) and registered as a typed groves entry —
// scaffold first, global write last.
func TestEcosystemCommitCreate(t *testing.T) {
	m, globalPath, workspace, home := newEcosystemTestModel(t, "[tui]\ntheme = \"kanagawa-dark\"\n")

	// Fresh config: intro + demo pointer + CREATE preview for the default.
	view := m.View()
	for _, want := range []string{"first ecosystem", "CREATE", "demo (d)"} {
		if !strings.Contains(view, want) {
			t.Errorf("fresh view missing %q", want)
		}
	}

	m = commitEcosystemPath(t, m, "~/eco-fresh")
	if strings.HasPrefix(m.statusMsg, "Error") {
		t.Fatalf("commit failed: %s", m.statusMsg)
	}
	if !strings.Contains(m.statusMsg, "created") {
		t.Errorf("status = %q, want a created notice", m.statusMsg)
	}

	dir := filepath.Join(home, "eco-fresh")
	for _, f := range []string{"grove.toml", "README.md", ".gitignore", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("scaffold missing %s: %v", f, err)
		}
	}

	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	g, ok := reloaded.Final.Groves["eco-fresh"]
	if !ok {
		t.Fatalf("groves entry missing: %+v", reloaded.Final.Groves)
	}
	if g.Path != dir || g.Enabled == nil || !*g.Enabled || g.Notebook != "nb" || g.Description != "My projects" {
		t.Errorf("groves entry wrong: %+v", g)
	}
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(raw), "enabled = true") {
		t.Errorf("typed bool missing from file:\n%s", raw)
	}
	if reloaded.Final.TUI == nil || reloaded.Final.TUI.Theme != "kanagawa-dark" {
		t.Error("seeded theme lost — global config clobbered")
	}

	// The reload reset the form to the default; the registered ecosystem now
	// lists read-only and the demo pointer is gone.
	view = m.View()
	for _, want := range []string{"Configured ecosystems", "eco-fresh", "Add another", "~/Code"} {
		if !strings.Contains(view, want) {
			t.Errorf("post-commit view missing %q", want)
		}
	}
	if strings.Contains(view, "demo (d)") {
		t.Error("demo pointer still shown with a configured ecosystem")
	}
}

// TestEcosystemCommitImport: a directory that already carries a grove
// manifest is registered as-is — entry written, nothing scaffolded.
func TestEcosystemCommitImport(t *testing.T) {
	m, _, workspace, home := newEcosystemTestModel(t, "[tui]\ntheme = \"kanagawa-dark\"\n")

	dir := filepath.Join(home, "eco-imp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "name = \"custom\"\n"
	if err := os.WriteFile(filepath.Join(dir, "grove.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	// The preview announces the import before the commit.
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~/eco-imp")})
	if !strings.Contains(m.View(), "IMPORT") {
		t.Error("preview does not announce IMPORT for a manifest-bearing dir")
	}
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, cmd().(applyEcosystemMsg))

	if !strings.Contains(m.statusMsg, "imported") {
		t.Errorf("status = %q, want an imported notice", m.statusMsg)
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g, ok := reloaded.Final.Groves["eco-imp"]; !ok || g.Path != dir {
		t.Fatalf("groves entry missing/wrong: %+v", reloaded.Final.Groves)
	}
	manifest, _ := os.ReadFile(filepath.Join(dir, "grove.toml"))
	if string(manifest) != seed {
		t.Errorf("import rewrote the manifest:\n%s", manifest)
	}
	for _, f := range []string{"README.md", ".gitignore", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("import scaffolded %s", f)
		}
	}
}

// TestEcosystemCommitCollision: an already-configured name is refused with a
// status-line error — nothing scaffolded, nothing written.
func TestEcosystemCommitCollision(t *testing.T) {
	seed := strings.Join([]string{
		"[tui]",
		"theme = \"kanagawa-dark\"",
		"",
		"[groves.eco-x]",
		"path = \"/somewhere\"",
		"enabled = true",
		"",
	}, "\n")
	m, globalPath, workspace, home := newEcosystemTestModel(t, seed)

	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~/eco-x")})
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, cmd().(applyEcosystemMsg))

	if !strings.HasPrefix(m.statusMsg, "Error") || !strings.Contains(m.statusMsg, "eco-x") {
		t.Errorf("collision not refused: %q", m.statusMsg)
	}
	if _, err := os.Stat(filepath.Join(home, "eco-x")); err == nil {
		t.Error("collision still scaffolded the directory")
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g := reloaded.Final.Groves["eco-x"]; g.Path != "/somewhere" {
		t.Errorf("existing grove overwritten: %+v", g)
	}
	raw, _ := os.ReadFile(globalPath)
	if strings.Count(string(raw), "eco-x") != 1 {
		t.Errorf("groves table changed:\n%s", raw)
	}
}

// TestEcosystemScaffoldFailureNoRegistration pins the commit ordering: the
// global groves write runs LAST, so a scaffold failure leaves no dangling
// registration.
func TestEcosystemScaffoldFailureNoRegistration(t *testing.T) {
	m, _, workspace, home := newEcosystemTestModel(t, "[tui]\ntheme = \"kanagawa-dark\"\n")

	// A plain file where the parent directory should go makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(home, "blocked"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}

	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~/blocked/eco")})
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, cmd().(applyEcosystemMsg))

	if !strings.HasPrefix(m.statusMsg, "Error") {
		t.Errorf("scaffold failure not surfaced: %q", m.statusMsg)
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Final.Groves) != 0 {
		t.Errorf("scaffold failure left a dangling registration: %+v", reloaded.Final.Groves)
	}
}

// TestEcosystemEssentialsOnlyNoOp: EssentialsOnly must not blank the page —
// the whole form is the essential content.
func TestEcosystemEssentialsOnlyNoOp(t *testing.T) {
	m, _, _ := newSinglePageTestModel(t, "ecosystem", SinglePageOpts{EssentialsOnly: true})
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	view := m.View()
	if !strings.Contains(view, "Directory") || !strings.Contains(view, "CREATE") {
		t.Errorf("EssentialsOnly blanked the ecosystem page:\n%s", view)
	}
}

// TestEcosystemEditorEscCancels: esc closes the editor without committing and
// restores the shown value.
func TestEcosystemEditorEscCancels(t *testing.T) {
	m, _, workspace, _ := newEcosystemTestModel(t, "[tui]\ntheme = \"kanagawa-dark\"\n")

	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.TextEntryActive() {
		t.Error("esc left the editor open")
	}
	if !strings.Contains(m.View(), defaultEcosystemPath) {
		t.Error("esc did not restore the shown path")
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Final.Groves) != 0 {
		t.Errorf("esc wrote config: %+v", reloaded.Final.Groves)
	}
}
