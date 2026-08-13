package config

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/subject"
	"github.com/grovetools/core/pkg/syncproto"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/notescope"
)

// fakeScope is the whole server and the whole verb layer. Every P3 keypath in
// this package is driven against it: no test here opens a device session,
// reads the developer's config, or runs a verb for real.
type fakeScope struct {
	inventory syncproto.InventoryResponse
	invErr    error
	calls     []string
	err       error
}

func (f *fakeScope) Inventory(context.Context) (syncproto.InventoryResponse, error) {
	f.calls = append(f.calls, "inventory")
	return f.inventory, f.invErr
}

func (f *fakeScope) Share(_ context.Context, notebook string) (notescope.ActionResult, error) {
	f.calls = append(f.calls, "share:"+notebook)
	return notescope.ActionResult{Action: "notebook share " + notebook, Output: "  registered   NS-A  alpha\n\n  " + notebook + " is shared at version 4.\n"}, f.err
}

func (f *fakeScope) Pull(_ context.Context, notebook string) (notescope.ActionResult, error) {
	f.calls = append(f.calls, "pull:"+notebook)
	return notescope.ActionResult{Action: "notebook pull " + notebook, Output: "  bound        NB-X  /tmp/" + notebook + "\n"}, f.err
}

func (f *fakeScope) Move(_ context.Context, ns, to string) (notescope.ActionResult, error) {
	f.calls = append(f.calls, "move:"+ns+"->"+to)
	return notescope.ActionResult{Action: "notespace move", Output: "  moved " + ns + " into " + to + "\n"}, f.err
}

func (f *fakeScope) provider() (notescope.Service, error) { return f, nil }

// scopeHome records notebooks in a temp GROVE_HOME and returns the config dir.
func scopeHome(t *testing.T) string {
	t.Helper()
	dir := recordedConfigDir(t)
	coreconfig.ResetLoadCache()
	t.Cleanup(coreconfig.ResetLoadCache)
	return dir
}

// recordNotebook writes one [notebooks.<name>] table, creates its root, and
// mints the named notespaces beneath it.
func recordNotebook(t *testing.T, configDir, name string, shared bool, notespaces ...string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := coreconfig.WriteNotebooks(configDir+"/notebooks.toml", coreconfig.NotebookEdits{Upserts: map[string]coderoot.Notebook{name: {Root: root}}}); err != nil {
		t.Fatal(err)
	}
	if shared {
		if _, err := coreconfig.WriteNotebooks(configDir+"/notebooks.toml", coreconfig.NotebookEdits{SyncShare: map[string]bool{name: true}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := notespace.MintNotebook(root, name); err != nil {
		t.Fatal(err)
	}
	for _, ns := range notespaces {
		dir := root + "/" + notescope.ContainerDir + "/" + ns
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := notespace.MintNotespace(dir, notespace.NotespaceMutable{Name: ns, Subject: subject.MintLocal().String(), Kind: "notes"}); err != nil {
			t.Fatal(err)
		}
	}
	coreconfig.ResetLoadCache()
	return root
}

// unshareNotebook records D9's explicit `share = false`.
func unshareNotebook(t *testing.T, configDir, name string) {
	t.Helper()
	if _, err := coreconfig.WriteNotebooks(configDir+"/notebooks.toml", coreconfig.NotebookEdits{SyncShare: map[string]bool{name: false}}); err != nil {
		t.Fatal(err)
	}
	coreconfig.ResetLoadCache()
}

func mkdirT(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func joinModel(t *testing.T, fake *fakeScope) (Model, *JoinPage) {
	t.Helper()
	page := NewJoinPage(nil, grovekeymap.NewConfigKeyMap(nil), 120, 40)
	page.active = true
	return Model{joinPage: page, scopeService: fake.provider}, page
}

// The load-bearing rule of this page: it talks to nothing until a key is
// pressed. Construction, refresh and non-key messages must all be silent.
func TestJoinPageFetchesNothingUntilAKeyIsPressed(t *testing.T) {
	scopeHome(t)
	fake := &fakeScope{}
	m, page := joinModel(t, fake)

	page.Refresh(nil)
	_, _ = page.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_ = page.View()
	if len(fake.calls) != 0 {
		t.Fatalf("the page reached the service before any keypress: %v", fake.calls)
	}
	if !strings.Contains(page.View(), "No comparison yet") {
		t.Errorf("empty state does not say a fetch is pending: %q", page.View())
	}

	// `r` asks — and even that only produces an intent the Model turns into
	// one call.
	_, cmd := page.Update(runeKey('r'))
	if cmd == nil {
		t.Fatal("r produced no fetch intent")
	}
	if _, ok := cmd().(fetchJoinDeltaMsg); !ok {
		t.Fatalf("r emitted %T, want fetchJoinDeltaMsg", cmd())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the page called the service itself: %v", fake.calls)
	}

	updated, fetchCmd := m.Update(fetchJoinDeltaMsg{})
	m = updated.(Model)
	if fetchCmd == nil {
		t.Fatal("the Model produced no fetch command")
	}
	loaded, ok := fetchCmd().(joinDeltaLoadedMsg)
	if !ok {
		t.Fatalf("fetch produced %T", fetchCmd())
	}
	if loaded.err != nil {
		t.Fatalf("fetch failed: %v", loaded.err)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "inventory" {
		t.Fatalf("service calls = %v, want exactly one inventory", fake.calls)
	}
}

// `p` and `s` act only on the row they are pressed on, only in the direction
// that row states, and only through the verb.
func TestJoinPagePullAndShareActOnlyOnTheirOwnRows(t *testing.T) {
	dir := scopeHome(t)
	recordNotebook(t, dir, "here", false, "alpha")

	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := notescope.Scan(table)
	if err != nil {
		t.Fatal(err)
	}
	// One notebook recorded here and absent from the server (shareable), one
	// the server holds that this machine does not (pullable).
	fake := &fakeScope{inventory: syncproto.InventoryResponse{Notebooks: []syncproto.InventoryNotebook{
		{ID: "NB-SERVER", Name: "remote", ShareState: syncproto.NotebookShareStateShared, Version: 2},
	}}}
	m, page := joinModel(t, fake)
	page.SetInventory(fake.inventory, scanned)

	shareIdx, pullIdx := -1, -1
	for i, row := range page.rows {
		switch row.Action {
		case notescope.JoinActionShare:
			shareIdx = i
		case notescope.JoinActionPull:
			pullIdx = i
		}
	}
	if shareIdx < 0 || pullIdx < 0 {
		t.Fatalf("expected one shareable and one pullable row: %#v", page.rows)
	}

	// `p` on the shareable row does nothing but explain itself.
	page.cursor = shareIdx
	if _, cmd := page.Update(runeKey('p')); cmd != nil {
		t.Fatalf("p on a share row emitted %#v", cmd())
	}
	page.cursor = pullIdx
	if _, cmd := page.Update(runeKey('s')); cmd != nil {
		t.Fatalf("s on a pull row emitted %#v", cmd())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("a mismatched key still reached the service: %v", fake.calls)
	}

	// The matching keys emit intents, and the Model runs the verb.
	page.cursor = shareIdx
	_, cmd := page.Update(runeKey('s'))
	if cmd == nil {
		t.Fatal("s on a share row produced no intent")
	}
	shareMsg, ok := cmd().(shareNotebookMsg)
	if !ok || shareMsg.notebook != "here" {
		t.Fatalf("share intent = %#v", cmd())
	}
	updated, actCmd := m.Update(shareMsg)
	m = updated.(Model)
	done, ok := actCmd().(scopeActionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("share run = %#v", actCmd())
	}
	if len(fake.calls) != 1 || fake.calls[0] != "share:here" {
		t.Fatalf("calls = %v, want share:here", fake.calls)
	}

	page.cursor = pullIdx
	_, cmd = page.Update(runeKey('p'))
	pullMsg, ok := cmd().(pullNotebookMsg)
	if !ok || pullMsg.notebook != "remote" {
		t.Fatalf("pull intent = %#v", cmd())
	}
	if _, pullRun := m.Update(pullMsg); pullRun == nil {
		t.Fatal("the Model produced no pull command")
	} else if _, ok := pullRun().(scopeActionDoneMsg); !ok {
		t.Fatalf("pull run = %#v", pullRun())
	}
	if len(fake.calls) != 2 || fake.calls[1] != "pull:remote" {
		t.Fatalf("calls = %v, want pull:remote second", fake.calls)
	}
}

// A refused verb is reported with the verb's own words: the TUI does not
// re-decide, and it does not swallow.
func TestJoinPageSurfacesAVerbRefusal(t *testing.T) {
	scopeHome(t)
	fake := &fakeScope{err: errors.New("notebook \"remote\" records root /gone, which does not exist")}
	m, _ := joinModel(t, fake)

	_, cmd := m.Update(pullNotebookMsg{notebook: "remote"})
	done, ok := cmd().(scopeActionDoneMsg)
	if !ok {
		t.Fatalf("pull produced %T", cmd())
	}
	if done.err == nil {
		t.Fatal("a refused pull reported success")
	}
	updated, _ := m.Update(done)
	if status := updated.(Model).statusMsg; !strings.Contains(status, "does not exist") {
		t.Errorf("status = %q, want the verb's refusal", status)
	}
}

// A host that registered no service says which commands to run instead of
// failing silently.
func TestJoinPageWithoutAServiceNamesTheVerbs(t *testing.T) {
	scopeHome(t)
	m := Model{joinPage: NewJoinPage(nil, grovekeymap.NewConfigKeyMap(nil), 100, 30), scopeService: func() (notescope.Service, error) { return nil, notescope.ErrNoService }}
	_, cmd := m.Update(fetchJoinDeltaMsg{})
	loaded, ok := cmd().(joinDeltaLoadedMsg)
	if !ok || loaded.err == nil {
		t.Fatalf("fetch without a service = %#v", cmd())
	}
	if !strings.Contains(loaded.err.Error(), "grove notebook share") {
		t.Errorf("error = %q, want it to name the verbs", loaded.err)
	}
}
