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
	requireContains(t, got, "server       none (the destination notebook is not shared)", "no server call for a local move")
	requireContains(t, got, "Reversible: grove notespace move "+fixtureNotespace1+" --to research", "the inverse invocation")
	requireContains(t, got, `transition: "notespace move"`, "transition evidence")
}

func TestNotespaceMoveOutOfSharedStatesForwardOnlyRetention(t *testing.T) {
	box := sandboxNotebookScope(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"private":  {Share: boolPtr(false)},
	})

	var out bytes.Buffer
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: fixtureNotespace1, to: "private"}); err != nil {
		t.Fatalf("notespace move: %v", err)
	}
	got := out.String()
	requireContains(t, got, "has left a shared notebook", "the state change is named")
	requireContains(t, got, syncproto.UnshareRetentionStatement, "D9's retention sentence, in the server's own words")
	requireContains(t, got, "nothing was deleted anywhere", "forward-only means retained")
	if _, err := notespace.LoadNotespace(box.notespaceRoot("private", "alpha")); err != nil {
		t.Fatalf("the notespace did not arrive: %v", err)
	}
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
	if err := runNotespaceMove(context.Background(), &out, notespaceMoveOptions{notespace: "alpha", to: "team", expectedVersion: -1}); err != nil {
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
	if _, statErr := os.Stat(box.notespaceRoot("team", "alpha")); !os.IsNotExist(statErr) {
		t.Fatalf("the destination survived a rolled-back move: %v", statErr)
	}
	stamp, loadErr := notespace.LoadNotespace(box.notespaceRoot("research", "alpha"))
	if loadErr != nil || stamp == nil || stamp.ID != fixtureNotespace1 {
		t.Fatalf("the source was not restored: %v %+v", loadErr, stamp)
	}
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
