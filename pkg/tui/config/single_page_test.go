package config

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/keybind"
	"github.com/grovetools/core/tui/embed"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// newSinglePageTestModel builds a NewSinglePage Model over a real global
// grove.toml under a private GROVE_HOME — the newCuratedTestModel fixture
// applied to the single-page constructor.
func newSinglePageTestModel(t *testing.T, pageID string, opts SinglePageOpts) (Model, string, string) {
	t.Helper()

	groveHome := t.TempDir()
	t.Setenv("GROVE_HOME", groveHome)
	t.Setenv("GROVE_CONFIG_OVERLAY", "")

	globalDir := groveHome + "/config/grove"
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	globalPath := globalDir + "/grove.toml"
	if err := os.WriteFile(globalPath, []byte("[tui]\ntheme = \"kanagawa-dark\"\n"), 0o600); err != nil {
		t.Fatalf("seed global config: %v", err)
	}

	workspace := t.TempDir()
	layered, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("initial LoadLayered: %v", err)
	}

	svc := setup.NewService(false)
	m, err := NewSinglePage(pageID, layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil), opts)
	if err != nil {
		t.Fatalf("NewSinglePage(%q): %v", pageID, err)
	}
	m.workspacePath = workspace
	return m, globalPath, workspace
}

// updateModel feeds one message through the Model, asserting value semantics.
func updateModel(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update must return the value type Model, got %T", updated)
	}
	return m2, cmd
}

// TestNewSinglePageShape: exactly one page, tab chrome suppressed, and the
// requested page active; unknown IDs error.
func TestNewSinglePageShape(t *testing.T) {
	for pageID, wantTab := range map[string]string{
		"themes":     "themes",
		"appearance": "appearance",
		"layout":     "layout",
		"keys":       "keys",
		"notebook":   "notebook",
	} {
		m, _, _ := newSinglePageTestModel(t, pageID, SinglePageOpts{})
		if got := len(m.pager.Pages()); got != 1 {
			t.Errorf("%s: %d pages, want 1", pageID, got)
		}
		if !m.pager.Config().HideTabBar {
			t.Errorf("%s: tab bar not hidden", pageID)
		}
		active := m.pager.Active()
		if id, ok := active.(interface{ TabID() string }); !ok || id.TabID() != wantTab {
			t.Errorf("%s: active page is not %q", pageID, wantTab)
		}
		// Smoke: the sized view renders without tab-bar chrome rows panicking.
		m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
		if m.View() == "" {
			t.Errorf("%s: empty view", pageID)
		}
	}

	if _, err := NewSinglePage("nope", nil, nil, nil, grovekeymap.NewConfigKeyMap(nil), SinglePageOpts{}); err == nil {
		t.Error("unknown page ID did not error")
	}
}

// TestSinglePageKeysEssentials: the essentials-filtered Keys page carries
// exactly the onboarding subset — leader capture, pane-nav choice, and the
// two-chord explainer.
func TestSinglePageKeysEssentials(t *testing.T) {
	m, _, _ := newSinglePageTestModel(t, "keys", SinglePageOpts{EssentialsOnly: true})
	got := m.keysPage.Settings()
	ids := make([]string, 0, len(got))
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if len(ids) != 3 || ids[0] != "leader" || ids[1] != "pane_nav" || ids[2] != "chords_help" {
		t.Fatalf("essentials = %v, want [leader pane_nav chords_help]", ids)
	}
}

// TestSinglePageCommitPath: driving the essentials Keys page through real
// keystrokes commits via the Model's full write path — a typed value lands
// in the global grove.toml, the config reloads, and SettingAppliedMsg is
// emitted for the host. Nothing is reimplemented by the constructor.
func TestSinglePageCommitPath(t *testing.T) {
	m, globalPath, workspace := newSinglePageTestModel(t, "keys", SinglePageOpts{EssentialsOnly: true})

	// Row 1 (pane_nav) is a select: j down, enter cycles prefix-only →
	// direct and commits.
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("activating pane_nav produced no command")
	}
	setMsg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("expected setSettingMsg, got %T", cmd())
	}
	m, cmd = updateModel(t, m, setMsg)
	if cmd == nil {
		t.Fatal("expected a SettingAppliedMsg command after the save")
	}
	applied, ok := cmd().(embed.SettingAppliedMsg)
	if !ok {
		t.Fatalf("expected embed.SettingAppliedMsg, got %T", cmd())
	}
	if applied.Domain != embed.SettingDomainVimPaneNav {
		t.Errorf("domain = %q, want %q", applied.Domain, embed.SettingDomainVimPaneNav)
	}

	// Typed write on disk, whole file still parses, seed preserved.
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(raw), "vim_control_hjkl_pane_nav = true") {
		t.Fatalf("typed bool missing from file:\n%s", raw)
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Final.TUI == nil || !reloaded.Final.TUI.VimControlHjklPaneNav || reloaded.Final.TUI.Theme != "kanagawa-dark" {
		t.Error("write did not round-trip (or clobbered the seeded theme)")
	}
}

// TestSinglePageKeyCapture: the leader capture row arms via
// embed.KeyCaptureMsg (the host contract), TextEntryActive reports the raw
// input claim while armed, the captured chord stages, and enter commits it
// through the same global write path.
func TestSinglePageKeyCapture(t *testing.T) {
	syntheticStack(t, keybind.NewStack())
	m, globalPath, _ := newSinglePageTestModel(t, "keys", SinglePageOpts{EssentialsOnly: true})

	// Activate the leader row (cursor starts on it): arms the capture.
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("activating the leader row produced no command")
	}
	arm, ok := cmd().(embed.KeyCaptureMsg)
	if !ok || !arm.Active {
		t.Fatalf("expected arming KeyCaptureMsg, got %#v", cmd())
	}
	if !m.TextEntryActive() {
		t.Fatal("TextEntryActive false while a capture is armed")
	}

	// The captured keystroke stages the candidate and releases the claim.
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.TextEntryActive() {
		t.Fatal("TextEntryActive still true after the captured key")
	}

	// Enter commits the staged chord through the full write path.
	m, cmd = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("committing the staged chord produced no command")
	}
	setMsg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("expected setSettingMsg, got %T", cmd())
	}
	if setMsg.value != "ctrl+x" {
		t.Fatalf("staged chord = %q, want ctrl+x", setMsg.value)
	}
	m, cmd = updateModel(t, m, setMsg)
	if cmd == nil {
		t.Fatal("expected a SettingAppliedMsg command")
	}
	if applied := cmd().(embed.SettingAppliedMsg); applied.Domain != embed.SettingDomainLeaderKey {
		t.Errorf("domain = %q, want %q", applied.Domain, embed.SettingDomainLeaderKey)
	}
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(raw), "leader_key = 'ctrl+x'") {
		t.Errorf("raw bubbletea chord missing from file:\n%s", raw)
	}
}
