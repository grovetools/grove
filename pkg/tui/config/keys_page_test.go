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
)

// syntheticStack installs a hand-built keybinding stack for the duration of
// a test so no live shell/tmux probing runs.
func syntheticStack(t *testing.T, stack *keybind.Stack) {
	t.Helper()
	orig := buildConflictStack
	buildConflictStack = func() *keybind.Stack { return stack }
	t.Cleanup(func() { buildConflictStack = orig })
}

// TestNormalizeTuimuxChord mirrors normalize_test.go for the exact call the
// capture widgets make: bubbletea "ctrl+b" → standard "C-B".
func TestNormalizeTuimuxChord(t *testing.T) {
	cases := map[string]string{
		"ctrl+b": "C-B",
		"ctrl+g": "C-G",
		"alt+f":  "M-F",
		"ctrl+t": "C-T",
	}
	for raw, want := range cases {
		if got := keybind.Normalize(raw, "tuimux"); got != want {
			t.Errorf("Normalize(%q, tuimux) = %q, want %q", raw, got, want)
		}
	}
}

// TestConflictNoteSyntheticStack: conflict lookup against a hand-built
// stack — layer + action surfaced in raw bubbletea spelling, modified-only
// alternatives suggested, tuimux layers probed explicitly, free keys
// reported as free (not as conflicts).
func TestConflictNoteSyntheticStack(t *testing.T) {
	stack := keybind.NewStack()
	stack.AddBinding(keybind.Binding{
		Key:        "C-B",
		Layer:      keybind.LayerShell,
		Source:     "bash",
		Action:     "backward-char",
		Provenance: keybind.ProvenanceDefault,
	})
	stack.AddBinding(keybind.Binding{
		Key:        "C-X",
		Layer:      keybind.LayerTuimuxGlobal,
		Source:     "tuimux",
		Action:     "popup",
		Provenance: keybind.ProvenanceUserConfig,
	})

	note, conflict := conflictNote(stack, "ctrl+b")
	if !conflict {
		t.Errorf("ctrl+b not reported as a conflict: %s", note)
	}
	for _, want := range []string{"ctrl+b is taken by", "backward-char", "Shell", "default"} {
		if !strings.Contains(note, want) {
			t.Errorf("conflict note missing %q: %s", want, note)
		}
	}
	// The message speaks raw bubbletea spelling throughout — no normalized
	// C-B forms leak into the user-facing copy.
	if strings.Contains(note, "C-B") {
		t.Errorf("normalized spelling leaked into note: %s", note)
	}
	idx := strings.Index(note, "alternatives: ")
	if idx < 0 {
		t.Fatalf("conflict note has no alternatives line: %s", note)
	}
	for _, alt := range strings.Split(note[idx+len("alternatives: "):], ", ") {
		if !strings.Contains(alt, "+") {
			t.Errorf("alternative %q carries no modifier (bare keys are useless leaders): %s", alt, note)
		}
	}

	// FindBindingForKey's layer order skips the tuimux layers; the checker
	// must still surface a clash with tuimux's own global binds.
	tuimuxNote, tuimuxConflict := conflictNote(stack, "ctrl+x")
	if !tuimuxConflict || !strings.Contains(tuimuxNote, "popup") || !strings.Contains(tuimuxNote, "Tuimux Global") {
		t.Errorf("tuimux-layer conflict not surfaced: %s", tuimuxNote)
	}

	free, freeConflict := conflictNote(stack, "ctrl+y")
	if freeConflict {
		t.Errorf("free key reported as a conflict: %s", free)
	}
	if !strings.Contains(free, "ctrl+y looks free") {
		t.Errorf("free key not reported as free in raw spelling: %s", free)
	}
}

// TestRawKeyDisplay pins the normalized→bubbletea spelling used for
// suggestion display.
func TestRawKeyDisplay(t *testing.T) {
	cases := map[string]string{
		"C-B":   "ctrl+b",
		"M-B":   "alt+b",
		"C-M-B": "ctrl+alt+b",
		"S-B":   "shift+b",
		"B":     "b",
		"Enter": "enter",
	}
	for in, want := range cases {
		if got := rawKeyDisplay(in); got != want {
			t.Errorf("rawKeyDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}

// keysPageForTest builds a focused Keys CuratedPage over KeysSettings with a
// synthetic (empty) conflict stack.
func keysPageForTest(t *testing.T, layered *config.LayeredConfig) *CuratedPage {
	t.Helper()
	syntheticStack(t, keybind.NewStack())
	p := NewCuratedPage("Keys", KeysSettings(), layered, grovekeymap.NewConfigKeyMap(nil), 100, 40, CuratedOpts{})
	p.Focus()
	return p
}

// settingIndex finds a setting row by ID.
func settingIndex(t *testing.T, p *CuratedPage, id string) int {
	t.Helper()
	for i, s := range p.Settings() {
		if s.ID == id {
			return i
		}
	}
	t.Fatalf("setting %q not on page", id)
	return -1
}

// TestKeysCaptureFlow: activate → KeyCaptureMsg armed → synthetic KeyMsg
// staged as the raw candidate → enter commits the typed write through the
// full Model path.
func TestKeysCaptureFlow(t *testing.T) {
	m, _, workspace := newCuratedTestModel(t)
	p := keysPageForTest(t, m.layered)

	// Cursor starts on the leader row (index 0).
	if p.Settings()[0].ID != "leader" {
		t.Fatalf("first Keys row is %q, want leader", p.Settings()[0].ID)
	}

	// Activate: page enters capture mode and asks the host to arm.
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected an arm command from activating the capture row")
	}
	arm, ok := cmd().(embed.KeyCaptureMsg)
	if !ok || !arm.Active {
		t.Fatalf("expected KeyCaptureMsg{Active:true}, got %#v", cmd())
	}
	if !p.IsTextEntryActive() {
		t.Error("capturing page must report text-entry active")
	}

	// The captured keystroke stages the RAW bubbletea string.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if p.capturing {
		t.Error("capture must be single-shot on the page too")
	}
	if p.pendingIdx != 0 || p.pendingValue != "ctrl+t" {
		t.Fatalf("staged candidate = (%d, %q), want (0, ctrl+t)", p.pendingIdx, p.pendingValue)
	}

	// Enter commits the staged candidate as a setSettingMsg…
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a commit command")
	}
	msg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("expected setSettingMsg, got %T", cmd())
	}
	if msg.setting.ID != "leader" || msg.value != "ctrl+t" {
		t.Fatalf("commit = (%s, %q), want (leader, ctrl+t)", msg.setting.ID, msg.value)
	}

	// …which the Model persists as a typed string write with the right
	// live-apply domain.
	_, applied := applySetting(t, m, msg.setting, msg.value)
	if applied == nil || applied.Domain != embed.SettingDomainLeaderKey {
		t.Fatalf("expected leader_key SettingAppliedMsg, got %#v", applied)
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload after capture write: %v", err)
	}
	if reloaded.Final.TUI == nil || reloaded.Final.TUI.LeaderKey != "ctrl+t" {
		t.Errorf("tui.leader_key = %q, want ctrl+t", reloaded.Final.TUI.LeaderKey)
	}
}

// TestKeysCaptureResetToDefault: backspace during capture stages the
// explicit default; committing writes "ctrl+b" (never deletes the key —
// the treemux apply handler guards on empty).
func TestKeysCaptureResetToDefault(t *testing.T) {
	m, globalPath, _ := newCuratedTestModel(t)
	p := keysPageForTest(t, m.layered)

	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if p.pendingValue != defaultLeaderKey {
		t.Fatalf("backspace staged %q, want %q", p.pendingValue, defaultLeaderKey)
	}

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg, ok := cmd().(setSettingMsg)
	if !ok {
		t.Fatalf("expected setSettingMsg, got %T", cmd())
	}
	_, _ = applySetting(t, m, msg.setting, msg.value)

	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(raw), `leader_key = 'ctrl+b'`) && !strings.Contains(string(raw), `leader_key = "ctrl+b"`) {
		t.Errorf("expected explicit leader_key = ctrl+b in file:\n%s", raw)
	}
}

// TestKeysCaptureEscCancels: esc during capture leaves nothing staged and
// emits the host disarm.
func TestKeysCaptureEscCancels(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	p := keysPageForTest(t, m.layered)

	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.capturing {
		t.Error("esc did not exit capture")
	}
	if p.pendingIdx != -1 {
		t.Errorf("esc staged a candidate: (%d, %q)", p.pendingIdx, p.pendingValue)
	}
	if cmd == nil {
		t.Fatal("expected a disarm command")
	}
	if disarm, ok := cmd().(embed.KeyCaptureMsg); !ok || disarm.Active {
		t.Fatalf("expected KeyCaptureMsg{Active:false}, got %#v", cmd())
	}
}

// TestKeysCaptureConflictWarning: staging a conflicting candidate surfaces
// the layer + action in the row's preview block.
func TestKeysCaptureConflictWarning(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	stack := keybind.NewStack()
	stack.AddBinding(keybind.Binding{
		Key:        "C-T",
		Layer:      keybind.LayerShell,
		Source:     "bash",
		Action:     "transpose-chars",
		Provenance: keybind.ProvenanceDefault,
	})
	syntheticStack(t, stack)
	p := NewCuratedPage("Keys", KeysSettings(), m.layered, grovekeymap.NewConfigKeyMap(nil), 100, 40, CuratedOpts{})
	p.Focus()

	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyCtrlT})

	view := p.View()
	if !strings.Contains(view, "transpose-chars") || !strings.Contains(view, "Shell") {
		t.Errorf("conflict warning not rendered in view:\n%s", view)
	}

	// Abandoning the candidate clears the warning.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(p.View(), "transpose-chars") {
		t.Error("conflict warning survived revert")
	}
}

// TestKeysCaptureDefaultNotScolded: re-capturing the shipped default leader
// (ctrl+b) must never produce a conflict warning — even though it genuinely
// collides with readline's backward-char — only a neutral status-quo line.
func TestKeysCaptureDefaultNotScolded(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	stack := keybind.NewStack()
	stack.AddBinding(keybind.Binding{
		Key:        "C-B",
		Layer:      keybind.LayerShell,
		Source:     "bash",
		Action:     "backward-char",
		Provenance: keybind.ProvenanceDefault,
	})
	syntheticStack(t, stack)
	p := NewCuratedPage("Keys", KeysSettings(), m.layered, grovekeymap.NewConfigKeyMap(nil), 100, 40, CuratedOpts{})
	p.Focus()

	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyCtrlB})

	view := p.View()
	if strings.Contains(view, "taken by") || strings.Contains(view, "backward-char") {
		t.Errorf("re-picking the default leader was scolded with a conflict:\n%s", view)
	}
	if !strings.Contains(view, "the default leader key") {
		t.Errorf("neutral default note missing:\n%s", view)
	}
}

// TestKeysCaptureCurrentValueNotScolded: re-capturing the currently saved
// leader is the status quo, not a conflict — neutral note only.
func TestKeysCaptureCurrentValueNotScolded(t *testing.T) {
	stack := keybind.NewStack()
	stack.AddBinding(keybind.Binding{
		Key:        "C-T",
		Layer:      keybind.LayerShell,
		Source:     "bash",
		Action:     "transpose-chars",
		Provenance: keybind.ProvenanceDefault,
	})
	syntheticStack(t, stack)

	lc := &config.LayeredConfig{Final: &config.Config{TUI: &config.TUIConfig{LeaderKey: "ctrl+t"}}}
	p := NewCuratedPage("Keys", KeysSettings(), lc, grovekeymap.NewConfigKeyMap(nil), 100, 40, CuratedOpts{})
	p.Focus()

	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyCtrlT})

	view := p.View()
	if strings.Contains(view, "taken by") || strings.Contains(view, "transpose-chars") {
		t.Errorf("re-picking the saved leader was scolded with a conflict:\n%s", view)
	}
	if !strings.Contains(view, "already your leader key") {
		t.Errorf("neutral status-quo note missing:\n%s", view)
	}
}

// TestKeysPaneNavTypedWrite: the pane-nav select maps its labels onto a
// typed bool at tui.vim_control_hjkl_pane_nav.
func TestKeysPaneNavTypedWrite(t *testing.T) {
	m, globalPath, workspace := newCuratedTestModel(t)
	settings := KeysSettings()
	var paneNav Setting
	for _, s := range settings {
		if s.ID == "pane_nav" {
			paneNav = s
		}
	}
	if paneNav.ID == "" {
		t.Fatal("pane_nav setting missing")
	}

	_, applied := applySetting(t, m, paneNav, paneNavDirect)
	if applied == nil || applied.Domain != embed.SettingDomainVimPaneNav {
		t.Fatalf("expected vim_pane_nav SettingAppliedMsg, got %#v", applied)
	}

	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(raw), "vim_control_hjkl_pane_nav = true") {
		t.Fatalf("expected typed bool in file:\n%s", raw)
	}
	reloaded, err := config.LoadLayered(workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Final.TUI == nil || !reloaded.Final.TUI.VimControlHjklPaneNav {
		t.Error("pane nav bool did not round-trip")
	}
	if got := paneNav.Read(reloaded); got != paneNavDirect {
		t.Errorf("Read after write = %q, want %q", got, paneNavDirect)
	}
}

// TestKeysSettingsEssentials: EssentialsOnly yields exactly the leader
// capture, the pane-nav choice, and the two-chord explainer (the onboarding
// contract, spec 23 round 3: the Keys step teaches leader vs action).
func TestKeysSettingsEssentials(t *testing.T) {
	p := NewCuratedPage("Keys", KeysSettings(), nil, grovekeymap.NewConfigKeyMap(nil), 80, 24, CuratedOpts{EssentialsOnly: true})
	got := p.Settings()
	if len(got) != 3 || got[0].ID != "leader" || got[1].ID != "pane_nav" || got[2].ID != "chords_help" {
		ids := make([]string, 0, len(got))
		for _, s := range got {
			ids = append(ids, s.ID)
		}
		t.Fatalf("essentials = %v, want [leader pane_nav chords_help]", ids)
	}
}

// TestKeysBindingsSummaryProviderAndFallback: the summary renders the
// host-installed provider rows when present and the static leader excerpt
// otherwise.
func TestKeysBindingsSummaryProviderAndFallback(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	p := keysPageForTest(t, m.layered)
	idx := settingIndex(t, p, "bindings_summary")
	for i := 0; i < idx; i++ {
		_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}

	// Fallback: static leader excerpt.
	t.Cleanup(func() { keysBindingsProvider = nil })
	keysBindingsProvider = nil
	view := p.View()
	if !strings.Contains(view, "<leader>") || !strings.Contains(view, "zoom pane") {
		t.Errorf("fallback summary not rendered:\n%s", view)
	}

	// Provider path: host rows replace the fallback.
	m.SetBindingsProvider(func() []BindingRow {
		return []BindingRow{{Key: "Ctrl+F", Action: "Nav · workspaces", Scope: "global"}}
	})
	view = p.View()
	if !strings.Contains(view, "Nav · workspaces") {
		t.Errorf("provider summary not rendered:\n%s", view)
	}
	if strings.Contains(view, "zoom pane") {
		t.Errorf("fallback rows leaked into provider summary:\n%s", view)
	}
}

// TestKeysKeymapLinkEmitsNavigate: activating the keymap row deep-links into
// the host's keymap panel (inert standalone).
func TestKeysKeymapLinkEmitsNavigate(t *testing.T) {
	m, _, _ := newCuratedTestModel(t)
	p := keysPageForTest(t, m.layered)
	idx := settingIndex(t, p, "keymap_link")
	for i := 0; i < idx; i++ {
		_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a navigate command")
	}
	nav, ok := cmd().(embed.NavigateMsg)
	if !ok || nav.PanelID != "keymap" {
		t.Fatalf("expected NavigateMsg{PanelID:keymap}, got %#v", cmd())
	}
}
