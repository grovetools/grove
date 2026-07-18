package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/embed"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
)

// layoutSettingByID finds a Layout row by ID (test helper).
func layoutSettingByID(t *testing.T, id string) Setting {
	t.Helper()
	for _, s := range LayoutSettings() {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no Layout setting with ID %q", id)
	return Setting{}
}

// TestLayoutSettingsRows pins the Layout page's descriptor table: row order,
// labels, TOML paths, control kinds, options, and apply domains.
func TestLayoutSettingsRows(t *testing.T) {
	want := []struct {
		id      string
		label   string
		path    []string
		control ControlKind
		options []string
		domain  string
	}{
		{"drawer_orientation", "Drawer position", []string{"tui", "drawer_orientation"}, ControlSelect, []string{"right", "bottom"}, embed.SettingDomainDrawerOrientation},
		{"drawer_expanded", "Drawer expanded on start", []string{"tui", "drawer_expanded"}, ControlBool, nil, embed.SettingDomainDrawerExpanded},
		{"sidebar_expanded", "Rail expanded on start", []string{"tui", "sidebar_expanded"}, ControlBool, nil, embed.SettingDomainSidebarExpanded},
		{"show_home_on_startup", "Show Home on startup", []string{"tui", "hide_splash_on_startup"}, ControlBool, nil, ""},
	}

	settings := LayoutSettings()
	if len(settings) != len(want) {
		t.Fatalf("LayoutSettings has %d rows, want %d", len(settings), len(want))
	}
	for i, w := range want {
		s := settings[i]
		if s.ID != w.id {
			t.Errorf("row %d: ID = %q, want %q", i, s.ID, w.id)
		}
		if s.Label != w.label {
			t.Errorf("%s: Label = %q, want %q", w.id, s.Label, w.label)
		}
		if !reflect.DeepEqual(s.Path, w.path) {
			t.Errorf("%s: Path = %v, want %v", w.id, s.Path, w.path)
		}
		if s.Control != w.control {
			t.Errorf("%s: Control = %d, want %d", w.id, s.Control, w.control)
		}
		if !reflect.DeepEqual(s.Options, w.options) {
			t.Errorf("%s: Options = %v, want %v", w.id, s.Options, w.options)
		}
		if s.ApplyDomain != w.domain {
			t.Errorf("%s: ApplyDomain = %q, want %q", w.id, s.ApplyDomain, w.domain)
		}
		if s.Read == nil {
			t.Errorf("%s: Read is nil", w.id)
		}
	}

	// Only the inverted row transforms its write.
	for _, s := range settings {
		wantTransform := s.ID == "show_home_on_startup"
		if (s.WriteTransform != nil) != wantTransform {
			t.Errorf("%s: WriteTransform presence = %v, want %v", s.ID, s.WriteTransform != nil, wantTransform)
		}
	}
}

// TestLayoutDefaultsWhenUnset: with no [tui] layout keys, rows display the
// shipped defaults (drawer right/collapsed, rail collapsed, Home shown).
func TestLayoutDefaultsWhenUnset(t *testing.T) {
	reads := map[string]string{
		"drawer_orientation":   "right",
		"drawer_expanded":      "false",
		"sidebar_expanded":     "false",
		"show_home_on_startup": "true", // hide_splash defaults false → Home shows
	}
	for id, wantVal := range reads {
		s := layoutSettingByID(t, id)
		if got := s.Read(nil); got != wantVal {
			t.Errorf("%s: Read(nil) = %q, want %q", id, got, wantVal)
		}
	}
}

// TestLayoutLiveRowsWriteThrough runs the real Layout descriptors through
// the Model write path: typed value on disk, fresh Final in the emitted
// SettingAppliedMsg with the row's domain, display value tracking the write,
// and the seeded config surviving (strict decode still passes).
func TestLayoutLiveRowsWriteThrough(t *testing.T) {
	cases := []struct {
		id        string
		value     string
		wantFinal func(t *testing.T, final *config.Config)
	}{
		{
			id: "drawer_orientation", value: "bottom",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || final.TUI.DrawerOrientation != "bottom" {
					t.Error("tui.drawer_orientation not \"bottom\" in reloaded Final")
				}
			},
		},
		{
			id: "drawer_expanded", value: "true",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || !final.TUI.DrawerExpanded {
					t.Error("tui.drawer_expanded not true in reloaded Final")
				}
			},
		},
		{
			id: "sidebar_expanded", value: "true",
			wantFinal: func(t *testing.T, final *config.Config) {
				if final.TUI == nil || !final.TUI.SidebarExpanded {
					t.Error("tui.sidebar_expanded not true in reloaded Final")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			m, _, workspace := newCuratedTestModel(t)
			s := layoutSettingByID(t, tc.id)

			m2, applied := applySetting(t, m, s, tc.value)

			if applied == nil {
				t.Fatal("expected a SettingAppliedMsg command")
			}
			if applied.Domain != s.ApplyDomain {
				t.Errorf("domain = %q, want %q", applied.Domain, s.ApplyDomain)
			}
			if applied.Config != m2.layered.Final {
				t.Error("SettingAppliedMsg.Config is not the reloaded layered.Final")
			}
			if got := s.Read(m2.layered); got != tc.value {
				t.Errorf("displayed value = %q, want %q", got, tc.value)
			}

			reloaded, err := config.LoadLayered(workspace)
			if err != nil {
				t.Fatalf("reload after write: %v", err)
			}
			tc.wantFinal(t, reloaded.Final)
			if reloaded.Global == nil {
				t.Fatal("global layer dropped after write — file no longer parses")
			}
			if reloaded.Final.TUI == nil || reloaded.Final.TUI.Theme != "kanagawa-dark" {
				t.Error("seeded tui.theme lost after write")
			}
		})
	}
}

// TestShowHomeOnStartupInverted is the inverted-bool contract: with
// hide_splash_on_startup=true on disk the control reads "false" (Home is
// NOT shown); toggling the row on via the page emits value "true"; the
// write lands hide_splash_on_startup = false as an UNQUOTED typed TOML
// bool; the whole config still loads; no SettingAppliedMsg is emitted
// (startup-only); and the control now reads "true".
func TestShowHomeOnStartupInverted(t *testing.T) {
	seed := "[tui]\ntheme = \"kanagawa-dark\"\nhide_splash_on_startup = true\n"
	m, globalPath, workspace := newCuratedTestModelSeeded(t, seed)

	s := layoutSettingByID(t, "show_home_on_startup")
	if got := s.Read(m.layered); got != "false" {
		t.Fatalf("with hide=true the control must read \"false\", got %q", got)
	}

	// Toggle through the real page so the emitted candidate proves the
	// direction: off ("false") toggles to on ("true").
	page := NewCuratedPage("Layout", LayoutSettings(), m.layered, grovekeymap.NewConfigKeyMap(nil), 80, 24, CuratedOpts{})
	page.Focus()
	for i := 0; i < 3; i++ { // cursor to row 3 (show_home_on_startup)
		_, _ = page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	_, cmd := page.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command from toggling the bool row")
	}
	msg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("expected setSettingMsg, got %T", cmd())
	}
	if msg.setting.ID != "show_home_on_startup" || msg.value != "true" {
		t.Fatalf("toggle emitted %s=%q, want show_home_on_startup=true", msg.setting.ID, msg.value)
	}

	m2, applied := applySetting(t, m, msg.setting, msg.value)
	if applied != nil {
		t.Errorf("startup-only row must not emit SettingAppliedMsg, got domain %q", applied.Domain)
	}

	// Raw file: the NEGATED key value, as an unquoted typed bool.
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if strings.Contains(string(raw), "\"false\"") || strings.Contains(string(raw), "'false'") {
		t.Fatalf("bool written as a quoted string:\n%s", raw)
	}
	if !strings.Contains(string(raw), "hide_splash_on_startup = false") {
		t.Fatalf("expected hide_splash_on_startup = false in file:\n%s", raw)
	}

	// Full round-trip through the strict loader: nothing dropped.
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("LoadLayered after inverted write: %v", err)
	}
	if reloaded.Global == nil {
		t.Fatal("global layer dropped after inverted write")
	}
	if reloaded.Final.TUI == nil || reloaded.Final.TUI.HideSplashOnStartup {
		t.Error("hide_splash_on_startup did not land false")
	}
	if reloaded.Final.TUI.Theme != "kanagawa-dark" {
		t.Error("seeded tui.theme lost — the whole file was dropped or clobbered")
	}

	// The control's displayed value reflects the new state.
	if got := s.Read(m2.layered); got != "true" {
		t.Errorf("after toggle the control must read \"true\", got %q", got)
	}
}

// TestNegateBool pins the write-transform helper.
func TestNegateBool(t *testing.T) {
	if got := negateBool(true); got != false {
		t.Errorf("negateBool(true) = %v", got)
	}
	if got := negateBool(false); got != true {
		t.Errorf("negateBool(false) = %v", got)
	}
	if got := negateBool("x"); got != "x" {
		t.Errorf("negateBool must pass non-bools through, got %v", got)
	}
}
