package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
)

// `grove notespace move` is the one mechanism: sharing is transferring. These
// tests pin all three cases (local, into shared, out of shared) plus the two
// safety properties the verb claims — the id survives, and a failed server step
// leaves the tree where it was.

func TestNotespaceMoveLocalPreservesIdentityAndPrimary(t *testing.T) {
	box := sandboxNotebookScope(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1, Subject: "local:" + fixtureNotespace1}}},
		"archive":  {},
	})
	// Both the primary (id-keyed) and the subject path (location-keyed) are
	// recorded before the move, so the test can show which one a move touches.
	writeMachineIdentity(t, map[string]string{"local:" + fixtureNotespace1: fixtureNotespace1},
		map[string]string{canonicalPath(box.notespaceRoot("research", "alpha")): "local:" + fixtureNotespace1})

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: "alpha", to: "archive"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}

	if _, err := os.Stat(box.notespaceRoot("research", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("the source still exists after the move: %v", err)
	}
	moved, err := notespace.LoadNotespace(box.notespaceRoot("archive", "alpha"))
	if err != nil || moved == nil || moved.ID != fixtureNotespace1 {
		t.Fatalf("the id did not survive the move: %v %+v", err, moved)
	}
	if _, err := os.Stat(filepath.Join(box.notespaceRoot("archive", "alpha"), "note.md")); err != nil {
		t.Fatalf("the content did not move: %v", err)
	}

	machineCfg, err := config.LoadMachineConfig()
	if err != nil || machineCfg == nil {
		t.Fatalf("machine.toml: %v", err)
	}
	if machineCfg.Primaries["local:"+fixtureNotespace1] != fixtureNotespace1 {
		t.Fatalf("[primaries] changed: %+v — it is id-keyed, so a move must not touch it", machineCfg.Primaries)
	}
	if got := machineCfg.Subjects[canonicalPath(box.notespaceRoot("archive", "alpha"))]; got != "local:"+fixtureNotespace1 {
		t.Fatalf("[subjects] was not re-keyed to the new location: %+v", machineCfg.Subjects)
	}
	if _, stale := machineCfg.Subjects[canonicalPath(box.notespaceRoot("research", "alpha"))]; stale {
		t.Fatalf("[subjects] still records the old location: %+v", machineCfg.Subjects)
	}

	got := out.String()
	requireContains(t, got, "server       none (neither notebook is recorded as shared)", "no server call for a local move")
	requireContains(t, got, "Reversible: grove notespace move "+fixtureNotespace1+" --to research", "the inverse invocation")
	requireContains(t, got, `transition: "notespace move"`, "transition evidence")
}

// TestNotespaceMoveOutOfSharedDetachesOnTheServer is the correction the whole
// out-of-shared leg turns on. A local-only move-out left the notespace in the
// notebook's server-side roll, so every later join delta offered to pull it
// back onto the machine that had just moved it out. The protocol's answer is a
// re-parent to no notebook, and this pins the whole sentence: the server is
// asked, its own retention statement is what the operator reads, and the join
// delta afterwards no longer offers the notespace back.
func TestNotespaceMoveOutOfSharedDetachesOnTheServer(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", "shared", 2)
	// Membership version 6 is a value the inventory does not carry, so the
	// detach has to learn it from the server's own precondition refusal exactly
	// as a re-parent does.
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookA, 6, 42)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"private":  {Share: boolPtr(false)},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}
	got := out.String()

	if len(server.Reparents) != 2 {
		t.Fatalf("detach attempts = %d, want a probe and its corrected retry: %+v", len(server.Reparents), server.Reparents)
	}
	final := server.Reparents[len(server.Reparents)-1]
	if !final.Detaching() {
		t.Fatalf("the out-of-shared leg must ask for a detach (empty to_notebook_id): %+v", final)
	}
	if final.FromNotebookID.String() != fixtureNotebookA || final.ExpectedVersion != 6 {
		t.Fatalf("detach named the wrong membership: %+v", final)
	}
	if held := server.Notespaces[fixtureNotespace1].NotebookID; held != "" {
		t.Fatalf("the server still parents the notespace under %q; a move out that leaves membership behind stays on offer", held)
	}

	requireContains(t, got, "detached from notebook "+fixtureNotebookA, "the server action")
	requireContains(t, got, syncproto.DetachRetentionStatement, "D9's retention sentence, in the server's own words")
	requireContains(t, got, "here AND on the server", "the change is named on both sides")
	requireContains(t, got, "a join delta will not offer it back", "the reason the server was asked at all")
	requireContains(t, got, "nothing was deleted anywhere", "forward-only means retained")
	requireContains(t, got, "cursor       42", "the event-log head, as evidence the stream was untouched")
	requireNotContains(t, got, syncproto.UnshareRetentionStatement, "a detach is per-notespace; the notebook-grained sentence would overstate it")
	if _, err := notespace.LoadNotespace(box.notespaceRoot("private", "alpha")); err != nil {
		t.Fatalf("the notespace did not arrive: %v", err)
	}

	// D9, end to end: what the server retains is not what it offers. After the
	// detach the notespace is unparented rather than a member, so the join
	// delta reports it as retained-and-unparented instead of listing it as a
	// notespace `research` still holds there.
	var delta bytes.Buffer
	if err := runSyncJoin(context.Background(), &delta, syncJoinOptions{server: server.URL, deltaOnly: true, expand: true}); err != nil {
		t.Fatalf("sync join: %v", err)
	}
	joined := delta.String()
	requireContains(t, joined, "unparented on the server: 1 notespace", "the detached notespace is still held, and said to be")
	requireNotContains(t, joined, "server only  "+fixtureNotespace1, "a detached notespace must not be offered back to the machine that moved it out")
}

// TestNotespaceMoveOutOfSharedWithNothingToDetach covers the honest no-op: the
// server holds no membership to withdraw, which is a different fact from "the
// server was never asked" and is reported as such.
func TestNotespaceMoveOutOfSharedWithNothingToDetach(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", "shared", 2)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"private":  {Share: boolPtr(false)},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}
	if len(server.Reparents) != 0 {
		t.Fatalf("nothing was registered, so nothing should have been detached: %+v", server.Reparents)
	}
	got := out.String()
	requireContains(t, got, "No membership was withdrawn because", "the no-op is explained, not silent")
	requireContains(t, got, "does not hold notespace "+fixtureNotespace1, "in the server's terms")
	requireContains(t, got, syncproto.DetachRetentionStatement, "D9 still applies to what the server does hold")
	if _, err := notespace.LoadNotespace(box.notespaceRoot("private", "alpha")); err != nil {
		t.Fatalf("the local move did not happen: %v", err)
	}
}

// TestNotespaceMoveOutOfSharedRefusalsProtectMembership pins the two states in
// which a move out of a shared notebook must not proceed at all: the server
// cannot be reached (so the membership could not be withdrawn), and the server
// parents the notespace somewhere this move was not asked to touch.
func TestNotespaceMoveOutOfSharedRefusalsProtectMembership(t *testing.T) {
	t.Run("no server recorded", func(t *testing.T) {
		box := sandboxNotebookScope(t)
		box.recordNotebooks(t, "research", map[string]notebookFixture{
			"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
			"private":  {Share: boolPtr(false)},
		})
		err := runNotespaceMove(context.Background(), &bytes.Buffer{}, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"})
		if err == nil {
			t.Fatal("a move out of a shared notebook succeeded without withdrawing anything")
		}
		requireContains(t, err.Error(), "would leave the server offering it back", "the refusal says what a local-only move-out would cost")
		if _, statErr := os.Stat(box.notespaceRoot("private", "alpha")); !os.IsNotExist(statErr) {
			t.Fatalf("the refused move still moved the tree: %v", statErr)
		}
		if _, loadErr := notespace.LoadNotespace(box.notespaceRoot("research", "alpha")); loadErr != nil {
			t.Fatalf("the source was disturbed by a refusal: %v", loadErr)
		}
	})

	t.Run("server parents it elsewhere", func(t *testing.T) {
		box := sandboxNotebookScope(t)
		server := newFakeSync(t)
		server.addNotebook(fixtureNotebookA, "research", "shared", 2)
		server.addNotebook(fixtureNotebookC, "elsewhere", "shared", 1)
		server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookC, 1, 3)
		box.recordNotebooks(t, "research", map[string]notebookFixture{
			"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
			"private":  {Share: boolPtr(false)},
		})
		box.recordSyncServer(t, server.URL)

		err := runNotespaceMove(context.Background(), &bytes.Buffer{}, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"})
		if err == nil {
			t.Fatal("the move detached a membership the operator never named")
		}
		requireContains(t, err.Error(), "the server holds it in notebook "+fixtureNotebookC, "the disagreement names both sides")
		if len(server.Reparents) != 0 {
			t.Fatalf("a refused move still asked the server to change membership: %+v", server.Reparents)
		}
		if server.Notespaces[fixtureNotespace1].NotebookID != fixtureNotebookC {
			t.Fatalf("membership changed on a refusal: %+v", server.Notespaces[fixtureNotespace1])
		}
	})
}

func TestNotespaceMoveBetweenSharedNotebooksReparents(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", "shared", 2)
	server.addNotebook(fixtureNotebookB, "team", "shared", 3)
	// membership version 4 is a value no client could have guessed: the
	// inventory does not carry it, so the verb has to learn it from the
	// server's own precondition refusal.
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookA, 4, 17)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"team":     {Stamp: fixtureNotebookB, Share: boolPtr(true)},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: "alpha", to: "team"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}
	got := out.String()

	if len(server.Reparents) != 2 {
		t.Fatalf("re-parent attempts = %d, want a probe and its corrected retry: %+v", len(server.Reparents), server.Reparents)
	}
	final := server.Reparents[len(server.Reparents)-1]
	if final.FromNotebookID.String() != fixtureNotebookA || final.ToNotebookID.String() != fixtureNotebookB {
		t.Fatalf("re-parent moved the wrong membership: %+v", final)
	}
	if final.ExpectedVersion != 4 {
		t.Fatalf("re-parent expected version = %d, want the version the server reported", final.ExpectedVersion)
	}
	if server.Notespaces[fixtureNotespace1].NotebookID != fixtureNotebookB {
		t.Fatalf("the server did not re-parent: %+v", server.Notespaces[fixtureNotespace1])
	}
	moved, err := notespace.LoadNotespace(box.notespaceRoot("team", "alpha"))
	if err != nil || moved == nil || moved.ID != fixtureNotespace1 {
		t.Fatalf("the id did not survive: %v %+v", err, moved)
	}
	requireContains(t, got, "re-parented "+fixtureNotebookA+" → "+fixtureNotebookB, "the server action")
	requireContains(t, got, "cursor       17", "the event-log head, as evidence that the stream was untouched")
	requireContains(t, got, "server accepted:", "server receipt")
}

func TestNotespaceMoveIntoSharedRegistersAndAttaches(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookB, "team", "shared", 1)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"team":     {Stamp: fixtureNotebookB, Share: boolPtr(true)},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: "alpha", to: "team"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}
	if len(server.Registers) != 1 {
		t.Fatalf("an unregistered notespace must be registered before it can be attached: %+v", server.Registers)
	}
	if len(server.Shares) != 1 || len(server.Shares[0].Members) != 1 {
		t.Fatalf("attach request = %+v", server.Shares)
	}
	if server.Notespaces[fixtureNotespace1].NotebookID != fixtureNotebookB {
		t.Fatalf("the notespace was not attached: %+v", server.Notespaces[fixtureNotespace1])
	}
	requireContains(t, out.String(), "attached to notebook "+fixtureNotebookB, "the server action")
}

func TestNotespaceMoveRollsBackWhenTheServerRefuses(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookB, "team", "shared", 1)
	server.ShareError = syncproto.ErrorMembershipConflict
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"team":     {Stamp: fixtureNotebookB, Share: boolPtr(true)},
	})
	box.recordSyncServer(t, server.URL)

	err := runNotespaceMove(context.Background(), &bytes.Buffer{}, notespaceMoveOptions{notespace: "alpha", to: "team"})
	if err == nil {
		t.Fatal("the move reported success although the server refused it")
	}
	requireContains(t, err.Error(), "the local move was rolled back", "the failure says what was undone")
	requireContains(t, err.Error(), "is now registered on this server but belongs to no notebook",
		"and does not imply the server is untouched when a registration landed first")
	if _, statErr := os.Stat(box.notespaceRoot("team", "alpha")); !os.IsNotExist(statErr) {
		t.Fatalf("the destination survived a rolled-back move: %v", statErr)
	}
	stamp, loadErr := notespace.LoadNotespace(box.notespaceRoot("research", "alpha"))
	if loadErr != nil || stamp == nil || stamp.ID != fixtureNotespace1 {
		t.Fatalf("the source was not restored: %v %+v", loadErr, stamp)
	}
	// A refused share changes no membership, which is what makes rolling the
	// local move back the honest answer here.
	if held := server.Notespaces[fixtureNotespace1]; held != nil && held.NotebookID != "" {
		t.Fatalf("the server attached a member it said it refused: %+v", held)
	}
}

// TestNotespaceMoveDoesNotRollBackAnAppliedServerChange is the other half of
// the rollback contract. Rolling the tree back is honest only while the server
// still holds the old membership; once it has accepted the new one, undoing the
// move locally produces the same disagreement in the other direction. So a
// failure after the server has applied its change reports rather than reverses.
func TestNotespaceMoveDoesNotRollBackAnAppliedServerChange(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", "shared", 2)
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookA, 0, 9)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"private":  {Share: boolPtr(false)},
	})
	box.recordSyncServer(t, server.URL)
	// machine.toml is the last thing the move touches, and an unreadable one
	// fails only AFTER the server has detached the notespace.
	if err := os.WriteFile(filepath.Join(box.configDir, "machine.toml"), []byte("[primaries\nbroken = "), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()

	err := runNotespaceMove(context.Background(), &bytes.Buffer{}, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"})
	if err == nil {
		t.Fatal("the move reported success although machine.toml could not be re-keyed")
	}
	requireContains(t, err.Error(), "the move completed", "the failure says what stands")
	requireContains(t, err.Error(), "the server change stands", "and that the server half stands too")

	if server.Notespaces[fixtureNotespace1].NotebookID != "" {
		t.Fatalf("the applied detach was undone: %+v", server.Notespaces[fixtureNotespace1])
	}
	if _, statErr := os.Stat(box.notespaceRoot("research", "alpha")); !os.IsNotExist(statErr) {
		t.Fatalf("the tree was rolled back under an applied server change: %v", statErr)
	}
	moved, loadErr := notespace.LoadNotespace(box.notespaceRoot("private", "alpha"))
	if loadErr != nil || moved == nil || moved.ID != fixtureNotespace1 {
		t.Fatalf("the destination does not hold the notespace: %v %+v", loadErr, moved)
	}
}

// TestNotespaceMoveRefusesAnUnexplainedServerRefusal pins the one shape
// doJSONStatus cannot judge alone: a non-2xx whose body decodes but names no
// protocol error. Reading it as success would record a membership the server
// never granted.
func TestNotespaceMoveRefusesAnUnexplainedServerRefusal(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookB, "team", "shared", 1)
	server.UnexplainedRefusalPath = "/sync/register"
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"team":     {Stamp: fixtureNotebookB, Share: boolPtr(true)},
	})
	box.recordSyncServer(t, server.URL)

	err := runNotespaceMove(context.Background(), &bytes.Buffer{}, notespaceMoveOptions{notespace: "alpha", to: "team"})
	if err == nil {
		t.Fatal("an unexplained HTTP 409 was read as success")
	}
	requireContains(t, err.Error(), "names no protocol error", "the refusal explains why it cannot be read")
	requireContains(t, err.Error(), "the local move was rolled back", "nothing was applied, so the move is undone")
	if _, loadErr := notespace.LoadNotespace(box.notespaceRoot("research", "alpha")); loadErr != nil {
		t.Fatalf("the source was not restored: %v", loadErr)
	}
}

// TestNotespaceMoveDetachFallsBackToTheProtocolRetention pins the labelling: a
// server that sends no retention sentence gets the protocol's, and the operator
// is told that is where it came from.
func TestNotespaceMoveDetachFallsBackToTheProtocolRetention(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.SuppressDetachRetention = true
	server.addNotebook(fixtureNotebookA, "research", "shared", 2)
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookA, 0, 4)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"private":  {Share: boolPtr(false)},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}
	requireContains(t, out.String(), "this server sent no retention statement; that is the protocol's",
		"a borrowed sentence is labelled as borrowed")
}

func TestNotespaceMoveRefusals(t *testing.T) {
	box := sandboxNotebookScope(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}, {Dir: "unminted"}}},
		"archive":  {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace2}}},
		"ghost":    {Missing: true},
	})

	cases := []struct {
		name string
		opts notespaceMoveOptions
		want string
	}{
		{"ambiguous name", notespaceMoveOptions{notespace: "alpha", to: "archive"}, "name it by its immutable id"},
		{"unknown notespace", notespaceMoveOptions{notespace: "nope", to: "archive"}, "no recorded notebook contains a notespace"},
		{"unminted notespace", notespaceMoveOptions{notespace: "unminted", to: "archive"}, "a move preserves an immutable id"},
		{"missing destination root", notespaceMoveOptions{notespace: fixtureNotespace1, to: "ghost"}, "a move never creates a notebook root"},
		{"unrecorded destination", notespaceMoveOptions{notespace: fixtureNotespace1, to: "nowhere"}, "records no notebook \"nowhere\""},
		{"same notebook", notespaceMoveOptions{notespace: fixtureNotespace1, to: "research"}, "is already in notebook"},
		{"destination already holds the name", notespaceMoveOptions{notespace: fixtureNotespace1, to: "archive"}, "refuses to merge two notespaces into one path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runNotespaceMove(context.Background(), &bytes.Buffer{}, tc.opts)
			if err == nil {
				t.Fatalf("move accepted %+v", tc.opts)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
	// Nothing above moved anything.
	if _, err := notespace.LoadNotespace(box.notespaceRoot("research", "alpha")); err != nil {
		t.Fatalf("a refused move disturbed the source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(box.notebooks, "ghost")); err == nil {
		t.Fatal("a refused move created the missing destination root")
	}
}

// writeMachineIdentity seeds machine.toml's identity tables directly, so a test
// can show which of them a move touches.
func writeMachineIdentity(t *testing.T, primaries, subjects map[string]string) {
	t.Helper()
	known := map[string]struct{}{}
	for _, id := range primaries {
		known[id] = struct{}{}
	}
	if _, _, err := config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{KnownNotespaceIDs: known}, func(machine *config.MachineConfig) error {
		machine.Primaries = primaries
		machine.Subjects = subjects
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
}
