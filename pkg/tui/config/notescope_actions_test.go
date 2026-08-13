package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/notescope"
)

// One act at a time.
//
// Bubbletea runs every Cmd in its own goroutine, so a held-down `s` would
// otherwise start a second `grove notebook share` while the first is still
// writing notebooks.toml and minting ids — two read-modify-write cycles over
// one file, and the duplicate-stamp state (D8) minted by the page that reports
// it. The gate is on the Model because that is the one place all four intents
// pass through.
func TestScopeActsDoNotRunConcurrently(t *testing.T) {
	scopeHome(t)
	fake := &fakeScope{}
	m, _ := joinModel(t, fake)

	updated, first := m.Update(shareNotebookMsg{notebook: "here"})
	m = updated.(Model)
	if first == nil {
		t.Fatal("the first share produced no command")
	}

	// Still in flight: every further intent is refused, and none of them
	// reaches the service.
	for _, msg := range []tea.Msg{
		shareNotebookMsg{notebook: "here"},
		pullNotebookMsg{notebook: "remote"},
		moveNotespaceMsg{notespace: "NS-A", to: "there"},
		fetchJoinDeltaMsg{},
	} {
		next, cmd := m.Update(msg)
		m = next.(Model)
		if cmd != nil {
			t.Fatalf("%T ran while an act was in flight: %#v", msg, cmd())
		}
		if !strings.Contains(m.statusMsg, "already running") {
			t.Errorf("%T was dropped without saying why: %q", msg, m.statusMsg)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("a queued intent reached the service before the first act finished: %v", fake.calls)
	}

	// The first act runs exactly once, and the gate lifts when it reports.
	done := first()
	if _, ok := done.(scopeActionDoneMsg); !ok {
		t.Fatalf("the in-flight act produced %T", done)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "share:here" {
		t.Fatalf("calls = %v, want exactly one share:here", fake.calls)
	}
	m = settleScope(t, m, done)
	if m.scopeBusy {
		t.Fatal("the gate never lifted; the page is now permanently inert")
	}
	if _, cmd := m.Update(pullNotebookMsg{notebook: "remote"}); cmd == nil {
		t.Fatal("a settled Model still refuses the next act")
	}
}

// A refused fetch must not leave the page spinning: the page armed its own
// loading flag on the keypress, so the Model that declines the fetch owns
// disarming it.
func TestRefusedFetchDisarmsTheSpinner(t *testing.T) {
	scopeHome(t)
	fake := &fakeScope{}
	m, page := joinModel(t, fake)

	updated, _ := m.Update(shareNotebookMsg{notebook: "here"})
	m = updated.(Model)

	// `r` arms the spinner on the page itself, then the Model declines.
	if _, cmd := page.Update(runeKey('r')); cmd == nil {
		t.Fatal("r produced no fetch intent")
	}
	if !page.Loading() {
		t.Fatal("r did not arm the page's loading state")
	}
	if _, cmd := m.Update(fetchJoinDeltaMsg{}); cmd != nil {
		t.Fatalf("the declined fetch still ran: %#v", cmd())
	}
	if page.Loading() {
		t.Error("the page is still spinning for a fetch that was declined")
	}
}

// A failed fetch keeps its reason across the config reloads that re-derive the
// local half. Folding the two errors into one field made an unrelated config
// write erase why the server said no, leaving a blank page and no cause.
func TestFetchErrorSurvivesALocalRefresh(t *testing.T) {
	scopeHome(t)
	fake := &fakeScope{invErr: errNoSyncServer}
	m, page := joinModel(t, fake)

	m = settleScope(t, m, fetchJoinDeltaMsg{})
	if !strings.Contains(page.View(), errNoSyncServer.Error()) {
		t.Fatalf("the failed fetch never reported its reason: %q", page.View())
	}
	page.Refresh(nil)
	if !strings.Contains(page.View(), errNoSyncServer.Error()) {
		t.Errorf("a config reload erased why the last fetch failed: %q", page.View())
	}
}

var errNoSyncServer = errors.New("sync is not configured; run `grove join <server-url>` first")

// A binary that links the pages but not the verbs — treemux's embedded config
// panel — must SAY so, not render the same key legend as the host where the
// keys work and let the operator discover the difference by pressing.
func TestPagesWithoutTheActsSayWhichCommandsToRun(t *testing.T) {
	scopeHome(t)

	join := NewJoinPage(nil, grovekeymap.NewConfigKeyMap(nil), 120, 40)
	join.active = true
	join.acts = func() bool { return false }
	view := join.View()
	if !strings.Contains(view, "cannot run here") {
		t.Errorf("the join page hides that this build carries no acts: %q", view)
	}
	if !strings.Contains(view, "grove notebook share") {
		t.Errorf("the join page does not name the CLI verbs to run instead: %q", view)
	}

	// And the Notes page refuses at the FIRST keypress rather than after a
	// destination has been typed and confirmed.
	dir := scopeHome(t)
	recordNotebook(t, dir, "here", false, "alpha")
	notes := notesPage(t)
	notes.acts = func() bool { return false }
	notes.cursor = rowIndex(t, notes, notescope.NotesRowNotebook, "here")
	_, _ = notes.Update(expandKey())
	notes.cursor = rowIndex(t, notes, notescope.NotesRowNotespace, "here/alpha")
	if _, cmd := notes.Update(runeKey('m')); cmd != nil {
		t.Fatalf("m armed a prompt in a build with no move verb: %#v", cmd())
	}
	if notes.IsTextEntryActive() {
		t.Error("the destination prompt opened in a build that cannot use the answer")
	}
	if !strings.Contains(notes.notice, "grove notespace move") {
		t.Errorf("notice = %q, want it to name the CLI verb", notes.notice)
	}
}

// Every key these pages act on is DECLARED in ConfigKeyMap, so `?` help lists
// it, `keys audit` compares it, and `[keymaps.grove.config]` can rebind it.
//
// The `l`/`h` half is the regression this pins: canon 60 §4.4 dropped them
// from this keymap because they mean left/right in nine other TUIs, and the
// first two pages written against raw key strings put them straight back.
func TestScopePagesBindOnlyDeclaredKeys(t *testing.T) {
	dir := scopeHome(t)
	recordNotebook(t, dir, "here", false, "alpha")

	km := grovekeymap.NewConfigKeyMap(nil)
	for _, tc := range []struct {
		field string
		bind  key.Binding
		want  string
	}{
		{"MoveNotespace", km.MoveNotespace, "m"},
		{"ShareNotebook", km.ShareNotebook, "s"},
		{"PullNotebook", km.PullNotebook, "p"},
		{"FetchJoinDelta", km.FetchJoinDelta, "r"},
	} {
		if keys := tc.bind.Keys(); len(keys) != 1 || keys[0] != tc.want {
			t.Errorf("%s is bound to %v, want [%q]", tc.field, keys, tc.want)
		}
		if tc.bind.Help().Desc == "" {
			t.Errorf("%s has no help text, so `?` cannot list it", tc.field)
		}
	}

	notes := notesPage(t)
	notes.cursor = rowIndex(t, notes, notescope.NotesRowNotebook, "here")
	if _, _ = notes.Update(runeKey('l')); len(notes.rows) != 1 {
		t.Errorf("`l` folded on the Notes page; canon 60 §4.4 dropped l/h from this keymap")
	}
	_, _ = notes.Update(expandKey())
	before := len(notes.rows)
	if _, _ = notes.Update(runeKey('h')); len(notes.rows) != before {
		t.Errorf("`h` unfolded on the Notes page; canon 60 §4.4 dropped l/h from this keymap")
	}
}
