package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/grove/pkg/notescope"
)

// The config TUI's P3 pages run these verbs, so the seam has to be the verbs
// and not a lookalike. These tests drive the registered Service against the
// same hermetic sandbox + fake grove-syncd the CLI tests use, and assert the
// verb's own evidence and its own refusals come back through it.

func resolveScopeService(t *testing.T) notescope.Service {
	t.Helper()
	service, err := notescope.ResolveService()
	if err != nil {
		t.Fatalf("grove registered no notebook scope service: %v", err)
	}
	return service
}

func TestScopeServiceRunsTheShareVerbAndReturnsItsEvidence(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
	})
	box.recordSyncServer(t, server.URL)

	result, err := resolveScopeService(t).Share(context.Background(), "research")
	if err != nil {
		t.Fatalf("share through the TUI seam: %v", err)
	}
	// The evidence is the verb's own, per-notespace, not a count.
	if !strings.Contains(result.Output, fixtureNotespace1) {
		t.Errorf("share evidence names no notespace:\n%s", result.Output)
	}
	if !strings.Contains(box.readNotebooksTOML(t), "share = true") {
		t.Error("share through the seam did not record the notebook as shared")
	}
}

// Pull's missing-root refusal is the load-bearing safety rule of this phase.
// Reached from a keypress it must refuse exactly as it does from a terminal —
// a TUI that quietly created the root would be the resurrection path the verb
// exists to close.
func TestScopeServicePullStillRefusesAMissingRoot(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", syncproto.NotebookShareStateShared, 2)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Missing: true},
	})
	box.recordSyncServer(t, server.URL)

	_, err := resolveScopeService(t).Pull(context.Background(), "research")
	if err == nil {
		t.Fatal("pull through the seam accepted a root that does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("refusal = %v, want it to name the missing root", err)
	}
}

// A move out of a shared notebook needs the server, and the seam must carry
// the verb's whole sentence — including D9's retention statement — rather than
// an exit code.
func TestScopeServiceMoveRunsTheMoveVerb(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	box.recordNotebooks(t, "personal", map[string]notebookFixture{
		"personal": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"archive":  {},
	})
	box.recordSyncServer(t, server.URL)

	result, err := resolveScopeService(t).Move(context.Background(), fixtureNotespace1, "archive")
	if err != nil {
		t.Fatalf("move through the TUI seam: %v", err)
	}
	if !strings.Contains(result.Output, "archive") {
		t.Errorf("move evidence names no destination:\n%s", result.Output)
	}
	stamp, stampErr := notespace.LoadNotespace(box.notespaceRoot("archive", "alpha"))
	if stampErr != nil || stamp == nil || stamp.ID != fixtureNotespace1 {
		t.Fatalf("the notespace did not land in the destination with its id: %v %+v", stampErr, stamp)
	}
}
