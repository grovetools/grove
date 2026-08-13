package config

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/notescope"
)

func notesPage(t *testing.T) *NotesPage {
	t.Helper()
	p := NewNotesPage(nil, grovekeymap.NewConfigKeyMap(nil), 120, 40)
	p.active = true
	return p
}

func rowIndex(t *testing.T, p *NotesPage, kind notescope.NotesRowKind, key string) int {
	t.Helper()
	for i, row := range p.rows {
		if row.Kind == kind && row.Key == key {
			return i
		}
	}
	t.Fatalf("no %s row keyed %q in %#v", kind, key, p.rows)
	return -1
}

// The sync column is read off the notebook's recorded containment scope. There
// is no per-notespace toggle and no key that could create one: `m` is the only
// act on this page, and it moves a notespace between notebooks.
func TestNotesPageSyncColumnIsDerivedAndHasNoPerRowToggle(t *testing.T) {
	dir := scopeHome(t)
	recordNotebook(t, dir, "shared", true, "alpha", "bravo")
	recordNotebook(t, dir, "plain", false, "charlie")

	p := notesPage(t)
	view := p.View()
	if !strings.Contains(view, notescope.SyncLabelShared) {
		t.Errorf("shared notebook lost its %q column: %q", notescope.SyncLabelShared, view)
	}
	if !strings.Contains(view, notescope.SyncLabelLocal) {
		t.Errorf("unshared notebook lost its %q column: %q", notescope.SyncLabelLocal, view)
	}

	// Expand and confirm the notespaces beneath the shared notebook carry no
	// sync state of their own, and that the space key — the checkbox key on the
	// Code page — writes nothing here.
	p.cursor = rowIndex(t, p, notescope.NotesRowNotebook, "shared")
	if _, cmd := p.Update(runeKey('l')); cmd != nil {
		t.Fatalf("expanding emitted %#v", cmd())
	}
	alpha := rowIndex(t, p, notescope.NotesRowNotespace, "shared/alpha")
	if p.rows[alpha].Sync != "" {
		t.Errorf("notespace row carries sync state %q; containment is the only knob", p.rows[alpha].Sync)
	}
	p.cursor = alpha
	if _, cmd := p.Update(tea.KeyMsg{Type: tea.KeySpace}); cmd != nil {
		t.Fatalf("space on a notespace row emitted %#v — there is no per-notespace toggle", cmd())
	}
}

// D9: a notebook recorded `share = false` reads as local in the column, but
// the page must still state that the server retains its history.
func TestNotesPageStatesD9RetentionForARecordedUnshare(t *testing.T) {
	dir := scopeHome(t)
	root := recordNotebook(t, dir, "stopped", true)
	_ = root
	// Re-record it as explicitly unshared.
	unshareNotebook(t, dir, "stopped")

	p := notesPage(t)
	view := p.View()
	if !strings.Contains(view, notescope.SyncLabelLocal) {
		t.Errorf("an unshared notebook is not out of scope in the column: %q", view)
	}
	if !strings.Contains(view, "D9") {
		t.Errorf("the recorded unshare lost its retention statement: %q", view)
	}
}

// `m` is two keypresses, and only the second one acts. The verb it reaches is
// `grove notespace move`, called with the notespace's immutable id.
func TestNotesPageMoveTakesTwoKeypressesAndRunsTheVerb(t *testing.T) {
	dir := scopeHome(t)
	recordNotebook(t, dir, "here", false, "alpha")
	recordNotebook(t, dir, "there", true)

	fake := &fakeScope{}
	p := notesPage(t)
	m := Model{notesPage: p, scopeService: fake.provider}

	p.cursor = rowIndex(t, p, notescope.NotesRowNotebook, "here")
	_, _ = p.Update(runeKey('l'))
	alpha := rowIndex(t, p, notescope.NotesRowNotespace, "here/alpha")
	p.cursor = alpha
	id := p.rows[alpha].ID
	if id == "" {
		t.Fatal("the fixture notespace has no id")
	}

	// First press only arms the destination prompt.
	if _, cmd := p.Update(runeKey('m')); cmd == nil {
		t.Fatal("m did not focus the destination prompt")
	}
	if !p.IsTextEntryActive() {
		t.Fatal("m did not arm the destination prompt")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("arming the prompt reached the service: %v", fake.calls)
	}
	if got := p.moveInput.Value(); got != "there" {
		t.Errorf("prompt prefilled with %q, want the only eligible destination", got)
	}

	// Esc backs out without writing anything.
	_, _ = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.IsTextEntryActive() || len(fake.calls) != 0 {
		t.Fatalf("esc did not cancel cleanly: active=%v calls=%v", p.IsTextEntryActive(), fake.calls)
	}

	// Second press: arm, then confirm.
	_, _ = p.Update(runeKey('m'))
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no move intent")
	}
	msg, ok := cmd().(moveNotespaceMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want moveNotespaceMsg", cmd())
	}
	if msg.notespace != id || msg.to != "there" {
		t.Fatalf("move intent = %#v, want id %s -> there", msg, id)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the page ran the move itself: %v", fake.calls)
	}

	_, run := m.Update(msg)
	if run == nil {
		t.Fatal("the Model produced no move command")
	}
	done, ok := run().(scopeActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("move run = %#v", run())
	}
	if len(fake.calls) != 1 || fake.calls[0] != "move:"+id+"->there" {
		t.Fatalf("calls = %v", fake.calls)
	}
}

// A move preserves an immutable id, so an unminted notespace is refused with
// the verb that mints one rather than moved anyway.
func TestNotesPageMoveRefusesAnUnmintedNotespace(t *testing.T) {
	dir := scopeHome(t)
	root := recordNotebook(t, dir, "here", false)
	mkdirT(t, root+"/"+notescope.ContainerDir+"/bare")
	recordNotebook(t, dir, "there", false)

	p := notesPage(t)
	p.Refresh(nil)
	p.cursor = rowIndex(t, p, notescope.NotesRowNotebook, "here")
	_, _ = p.Update(runeKey('l'))
	p.cursor = rowIndex(t, p, notescope.NotesRowNotespace, "here/bare")
	if _, cmd := p.Update(runeKey('m')); cmd != nil {
		t.Fatalf("m on an unminted notespace emitted %#v", cmd())
	}
	if !strings.Contains(p.notice, "notebook share") {
		t.Errorf("notice = %q, want it to name the verb that mints an id", p.notice)
	}
}

// Moving is per notespace: pressing `m` on a notebook says so instead of
// guessing which of its notespaces was meant.
func TestNotesPageMoveOnANotebookRowExplainsTheGrain(t *testing.T) {
	dir := scopeHome(t)
	recordNotebook(t, dir, "here", false, "alpha")

	p := notesPage(t)
	p.cursor = rowIndex(t, p, notescope.NotesRowNotebook, "here")
	if _, cmd := p.Update(runeKey('m')); cmd != nil {
		t.Fatalf("m on a notebook row emitted %#v", cmd())
	}
	if !strings.Contains(p.notice, "per notespace") {
		t.Errorf("notice = %q", p.notice)
	}
}
