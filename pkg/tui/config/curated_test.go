package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/embed"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// newCuratedTestModel builds a config Model whose GLOBAL layer is a real
// grove.toml under a private GROVE_HOME, so that both the Model's write
// path (FilePaths[SourceGlobal]) and an independent config.LoadLayered
// resolve the same file. The file is pre-seeded with a theme key so tests
// can assert the whole config survives a typed write (the A1 hazard: a
// stringified bool makes the strict decoder drop the entire file).
func newCuratedTestModel(t *testing.T) (Model, string, string) {
	t.Helper()
	return newCuratedTestModelSeeded(t, "[tui]\ntheme = \"kanagawa-dark\"\n")
}

// newCuratedTestModelSeeded is newCuratedTestModel with a caller-provided
// global grove.toml body (e.g. pre-set hide_splash_on_startup for the
// inverted Layout row).
func newCuratedTestModelSeeded(t *testing.T, seed string) (Model, string, string) {
	t.Helper()

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

	workspace := t.TempDir() // no project config: global layer only

	layered, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("initial LoadLayered: %v", err)
	}
	if layered.FilePaths[config.SourceGlobal] != globalPath {
		t.Fatalf("global layer not resolved to temp file: %q", layered.FilePaths[config.SourceGlobal])
	}

	svc := setup.NewService(false)
	m := New(layered, setup.NewYAMLHandler(svc), setup.NewTOMLHandler(svc), grovekeymap.NewConfigKeyMap(nil))
	m.workspacePath = workspace
	return m, globalPath, workspace
}

// applySetting feeds a setSettingMsg through the Model and returns the
// updated Model plus the embed.SettingAppliedMsg captured from the returned
// command (nil when no command was emitted) — the fake host handler.
func applySetting(t *testing.T, m Model, s Setting, value string) (Model, *embed.SettingAppliedMsg) {
	t.Helper()

	updated, cmd := m.Update(setSettingMsg{setting: s, value: value})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update must return the value type Model, got %T", updated)
	}
	if strings.HasPrefix(m2.statusMsg, "Error") {
		t.Fatalf("save failed: %s", m2.statusMsg)
	}
	if cmd == nil {
		return m2, nil
	}
	msg := cmd()
	applied, ok := msg.(embed.SettingAppliedMsg)
	if !ok {
		t.Fatalf("expected embed.SettingAppliedMsg from cmd, got %T", msg)
	}
	return m2, &applied
}

// curatedWriteCase is one row of the write-through invariant table.
type curatedWriteCase struct {
	name    string
	setting Setting
	value   string
	// wantFinal asserts against the independently reloaded layered.Final.
	wantFinal func(t *testing.T, final *config.Config)
}

func curatedWriteCases() []curatedWriteCase {
	return []curatedWriteCase{
		{
			name: "bool vim pane nav",
			setting: Setting{
				ID:      "vim_pane_nav",
				Label:   "Direct Ctrl+hjkl pane nav",
				Path:    []string{"tui", "vim_control_hjkl_pane_nav"},
				Control: ControlBool,
				Read: func(lc *config.LayeredConfig) string {
					if lc != nil && lc.Final != nil && lc.Final.TUI != nil && lc.Final.TUI.VimControlHjklPaneNav {
						return "true"
					}
					return "false"
				},
				ApplyDomain: embed.SettingDomainVimPaneNav,
			},
			value: "true",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || !final.TUI.VimControlHjklPaneNav {
					t.Error("vim_control_hjkl_pane_nav not true in reloaded Final")
				}
			},
		},
		{
			name: "int focus thickness",
			setting: Setting{
				ID:      "focus_thickness",
				Label:   "Focus thickness",
				Path:    []string{"tui", "focus", "thickness"},
				Control: ControlInt,
				Read: func(lc *config.LayeredConfig) string {
					if lc != nil && lc.Final != nil && lc.Final.TUI != nil && lc.Final.TUI.Focus != nil {
						return fmt.Sprintf("%d", lc.Final.TUI.Focus.Thickness)
					}
					return ""
				},
				ApplyDomain: embed.SettingDomainFocus,
			},
			value: "2",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || final.TUI.Focus == nil || final.TUI.Focus.Thickness != 2 {
					t.Error("tui.focus.thickness not 2 in reloaded Final")
				}
			},
		},
		{
			name: "select drawer orientation",
			setting: Setting{
				ID:      "drawer_orientation",
				Label:   "Drawer position",
				Path:    []string{"tui", "drawer_orientation"},
				Control: ControlSelect,
				Options: []string{"right", "bottom"},
				Read: func(lc *config.LayeredConfig) string {
					if lc != nil && lc.Final != nil && lc.Final.TUI != nil {
						return lc.Final.TUI.DrawerOrientation
					}
					return ""
				},
				ApplyDomain: embed.SettingDomainDrawerOrientation,
			},
			value: "bottom",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || final.TUI.DrawerOrientation != "bottom" {
					t.Error("tui.drawer_orientation not \"bottom\" in reloaded Final")
				}
			},
		},
		{
			name: "color focus active color",
			setting: Setting{
				ID:      "focus_active_color",
				Label:   "Focus active color",
				Path:    []string{"tui", "focus", "active_color"},
				Control: ControlColor,
				Read: func(lc *config.LayeredConfig) string {
					if lc != nil && lc.Final != nil && lc.Final.TUI != nil && lc.Final.TUI.Focus != nil {
						return lc.Final.TUI.Focus.ActiveColor
					}
					return ""
				},
				ApplyDomain: embed.SettingDomainFocus,
			},
			value: "cyan",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || final.TUI.Focus == nil || final.TUI.Focus.ActiveColor != "cyan" {
					t.Error("tui.focus.active_color not \"cyan\" in reloaded Final")
				}
			},
		},
	}
}

// TestCuratedWriteThroughInvariant: applying a setting via setSettingMsg
// persists a TYPED value to the global grove.toml such that (a) an
// independent config.LoadLayered sees the new value in Final, (b) the
// value the control displays (Setting.Read) matches it, (c) the emitted
// SettingAppliedMsg carries the right domain and the fresh Final, and
// (d) the rest of the file — the seeded theme — survives, proving the
// strict decoder still accepts the whole file.
func TestCuratedWriteThroughInvariant(t *testing.T) {
	for _, tc := range curatedWriteCases() {
		t.Run(tc.name, func(t *testing.T) {
			m, _, workspace := newCuratedTestModel(t)

			m2, applied := applySetting(t, m, tc.setting, tc.value)

			// The fake host handler saw the right domain + fresh config.
			if applied == nil {
				t.Fatal("expected a SettingAppliedMsg command")
			}
			if applied.Domain != tc.setting.ApplyDomain {
				t.Errorf("domain = %q, want %q", applied.Domain, tc.setting.ApplyDomain)
			}
			if applied.Config != m2.layered.Final {
				t.Error("SettingAppliedMsg.Config is not the reloaded layered.Final")
			}

			// The control's displayed value tracks the write.
			if got := tc.setting.Read(m2.layered); got != tc.value {
				t.Errorf("displayed value = %q, want %q", got, tc.value)
			}

			// Independent reload: the write is on disk, typed, and the
			// global layer still parses in full.
			reloaded, err := config.LoadLayered(workspace)
			if err != nil {
				t.Fatalf("reload after write: %v", err)
			}
			tc.wantFinal(t, reloaded.Final)
			if reloaded.Global == nil {
				t.Fatal("global layer dropped after write — file no longer parses")
			}
			if reloaded.Final.TUI == nil || reloaded.Final.TUI.Theme != "kanagawa-dark" {
				t.Error("seeded tui.theme lost after write — file was clobbered or dropped")
			}
		})
	}
}

// TestCuratedBoolRoundTrip is the mandatory A1 regression: a bool curated
// setting must round-trip through config.LoadLayered as a real TOML bool.
// A quoted "true" would fail core's strict decode and silently drop the
// ENTIRE global config (see treemux/cmd/welcome_prefs.go).
func TestCuratedBoolRoundTrip(t *testing.T) {
	m, globalPath, workspace := newCuratedTestModel(t)

	bools := curatedWriteCases()[0]
	_, _ = applySetting(t, m, bools.setting, "true")

	// Raw file check: the value must be an unquoted TOML bool.
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if strings.Contains(string(raw), "\"true\"") || strings.Contains(string(raw), "'true'") {
		t.Fatalf("bool written as a quoted string:\n%s", raw)
	}
	if !strings.Contains(string(raw), "vim_control_hjkl_pane_nav = true") {
		t.Fatalf("expected typed bool key in file:\n%s", raw)
	}

	// Full round-trip: the whole config still loads, nothing dropped.
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("LoadLayered after bool write: %v", err)
	}
	if reloaded.Global == nil {
		t.Fatal("global layer dropped after bool write")
	}
	if reloaded.Final.TUI == nil || !reloaded.Final.TUI.VimControlHjklPaneNav {
		t.Error("bool value did not round-trip")
	}
	if reloaded.Final.TUI.Theme != "kanagawa-dark" {
		t.Error("pre-existing theme key lost — the whole file was dropped or clobbered")
	}
}

// TestTypedValuePerControlKind pins the ControlKind → Go type contract.
func TestTypedValuePerControlKind(t *testing.T) {
	cases := []struct {
		kind ControlKind
		raw  string
		want interface{}
	}{
		{ControlBool, "true", true},
		{ControlBool, "false", false},
		{ControlInt, "3", 3},
		{ControlSelect, "bottom", "bottom"},
		{ControlText, "hello", "hello"},
		{ControlColor, "cyan", "cyan"},
		{ControlKeyCapture, "ctrl+b", "ctrl+b"},
	}
	for _, tc := range cases {
		got, err := Setting{ID: "x", Control: tc.kind}.TypedValue(tc.raw)
		if err != nil {
			t.Errorf("kind %d raw %q: unexpected error %v", tc.kind, tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("kind %d raw %q: got %#v (%T), want %#v (%T)", tc.kind, tc.raw, got, got, tc.want, tc.want)
		}
	}

	if _, err := (Setting{ID: "b", Control: ControlBool}).TypedValue("yes"); err == nil {
		t.Error("expected error for invalid bool")
	}
	if _, err := (Setting{ID: "i", Control: ControlInt}).TypedValue("two"); err == nil {
		t.Error("expected error for invalid int")
	}
	if _, err := (Setting{ID: "l", Control: ControlLink}).TypedValue("themes"); err == nil {
		t.Error("expected error for link TypedValue")
	}
}

// essentialsFixture is a mixed settings slice for filter tests.
func essentialsFixture() []Setting {
	return []Setting{
		{ID: "leader", Label: "Leader key", Essential: true, Control: ControlKeyCapture, Path: []string{"tui", "leader_key"}},
		{ID: "pane_nav", Label: "Pane navigation", Essential: true, Control: ControlBool, Path: []string{"tui", "vim_control_hjkl_pane_nav"}, ApplyDomain: embed.SettingDomainVimPaneNav, Read: func(*config.LayeredConfig) string { return "false" }},
		{ID: "drawer", Label: "Drawer position", Control: ControlSelect, Options: []string{"right", "bottom"}},
		{ID: "rail", Label: "Rail expanded", Control: ControlBool},
	}
}

// TestEssentialsFilter: EssentialsOnly yields exactly the Essential-tagged
// rows; the full page keeps everything.
func TestEssentialsFilter(t *testing.T) {
	keys := grovekeymap.NewConfigKeyMap(nil)

	full := NewCuratedPage("Keys", essentialsFixture(), nil, keys, 80, 24, CuratedOpts{})
	if got := len(full.Settings()); got != 4 {
		t.Fatalf("full page: %d settings, want 4", got)
	}

	ess := NewCuratedPage("Keys", essentialsFixture(), nil, keys, 80, 24, CuratedOpts{EssentialsOnly: true})
	if got := len(ess.Settings()); got != 2 {
		t.Fatalf("essentials page: %d settings, want 2", got)
	}
	for _, s := range ess.Settings() {
		if !s.Essential {
			t.Errorf("non-essential setting %q on essentials page", s.ID)
		}
	}
}

// TestEssentialsPageUsesIdenticalWritePath: activating a bool row on an
// essentials-only page emits the same setSettingMsg the full page emits —
// one write path, two densities.
func TestEssentialsPageUsesIdenticalWritePath(t *testing.T) {
	keys := grovekeymap.NewConfigKeyMap(nil)
	ess := NewCuratedPage("Keys", essentialsFixture(), nil, keys, 80, 24, CuratedOpts{EssentialsOnly: true})
	ess.Focus()

	// Move to the bool row (pane_nav is the second essential).
	_, _ = ess.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	_, cmd := ess.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command from activating the bool row")
	}
	msg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("expected setSettingMsg, got %T", cmd())
	}
	if msg.setting.ID != "pane_nav" {
		t.Errorf("setting ID = %q, want pane_nav", msg.setting.ID)
	}
	if msg.value != "true" {
		t.Errorf("toggled value = %q, want true", msg.value)
	}
}

// TestCuratedPageControls covers the per-kind activation behaviors of the
// page frame: bool toggling, select cycling, text editing, and link tab
// switching.
func TestCuratedPageControls(t *testing.T) {
	keys := grovekeymap.NewConfigKeyMap(nil)
	current := map[string]string{"style": "gutter", "label": "old"}
	settings := []Setting{
		{
			ID: "style", Label: "Focus style", Control: ControlSelect,
			Options: []string{"border", "gutter", "title"},
			Read:    func(*config.LayeredConfig) string { return current["style"] },
		},
		{
			ID: "label", Label: "Some text", Control: ControlText,
			Read: func(*config.LayeredConfig) string { return current["label"] },
		},
		{
			ID: "theme", Label: "Theme", Control: ControlLink, Options: []string{"themes"},
		},
	}
	p := NewCuratedPage("Appearance", settings, nil, keys, 80, 24, CuratedOpts{})
	p.Focus()

	// Select cycles gutter → title.
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("select: expected command")
	}
	if msg := cmd().(setSettingMsg); msg.value != "title" {
		t.Errorf("select cycled to %q, want title", msg.value)
	}

	// Text control opens the inline editor; typing + enter commits.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.IsTextEntryActive() {
		t.Fatal("text: expected editing mode")
	}
	p.input.SetValue("new-value")
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("text: expected commit command")
	}
	if msg := cmd().(setSettingMsg); msg.value != "new-value" || msg.setting.ID != "label" {
		t.Errorf("text committed %+v", msg)
	}
	if p.IsTextEntryActive() {
		t.Error("text: editing mode should close on commit")
	}

	// Esc cancels editing without committing.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Error("esc: expected no command")
	}
	if p.IsTextEntryActive() {
		t.Error("esc: editing mode should close")
	}

	// Link emits embed.SwitchTabMsg for the target tab.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("link: expected command")
	}
	if msg, ok := cmd().(embed.SwitchTabMsg); !ok || msg.TabID != "themes" {
		t.Errorf("link emitted %#v, want SwitchTabMsg{TabID: themes}", cmd())
	}
}

// TestSaveGlobalSettingFallsBackToGlobalPath: with no FilePaths entry the
// helper resolves setup.GlobalTOMLConfigPath — but tests must not touch the
// real home, so this only asserts the error path with an empty resolution
// is impossible to hit silently. Covered indirectly by the invariant tests;
// here we pin that a nil layered still targets the canonical global path.
func TestSaveGlobalSettingNilLayered(t *testing.T) {
	// Redirect the canonical global path via HOME so the fallback write
	// lands in a temp dir. Clear higher-precedence portable/XDG roots.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROVE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	svc := setup.NewService(false)
	th := setup.NewTOMLHandler(svc)
	yh := setup.NewYAMLHandler(svc)

	if err := SaveGlobalSetting(th, yh, nil, []string{"tui", "sidebar_expanded"}, true); err != nil {
		t.Fatalf("SaveGlobalSetting with nil layered: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".config", "grove", "grove.toml"))
	if err != nil {
		t.Fatalf("expected fallback global file: %v", err)
	}
	if !strings.Contains(string(raw), "sidebar_expanded = true") {
		t.Errorf("typed bool missing from fallback file:\n%s", raw)
	}
}
