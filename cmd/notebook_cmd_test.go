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

// ---- share ---------------------------------------------------------------------

func TestNotebookShareMintsRegistersAndRecordsShare(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Notespaces: []notespaceFixture{
			{Dir: "alpha", ID: fixtureNotespace1},
			{Dir: "beta"}, // unminted: share is the verb that mints it
		}},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotebookShare(context.Background(), &out, "research", false); err != nil {
		t.Fatalf("notebook share: %v", err)
	}
	got := out.String()

	// 1. Identity: the notebook and the unminted notespace both have stamps now,
	// and the already-stamped one was left exactly as it was.
	notebookStamp, err := notespace.LoadNotebook(filepath.Join(box.notebooks, "research"))
	if err != nil || notebookStamp == nil {
		t.Fatalf("notebook stamp: %v %+v", err, notebookStamp)
	}
	betaStamp, err := notespace.LoadNotespace(box.notespaceRoot("research", "beta"))
	if err != nil || betaStamp == nil {
		t.Fatalf("beta stamp: %v %+v", err, betaStamp)
	}
	if !strings.HasPrefix(betaStamp.Subject, "local:") {
		t.Fatalf("minted subject = %q, want a machine-local subject", betaStamp.Subject)
	}
	alphaStamp, err := notespace.LoadNotespace(box.notespaceRoot("research", "alpha"))
	if err != nil || alphaStamp == nil || alphaStamp.ID != fixtureNotespace1 {
		t.Fatalf("share re-keyed an existing stamp: %v %+v", err, alphaStamp)
	}

	// 2. The mint is RECORDED, not left to be re-derived (D3/D4).
	machineCfg, err := config.LoadMachineConfig()
	if err != nil || machineCfg == nil {
		t.Fatalf("machine.toml: %v", err)
	}
	if machineCfg.Primaries[betaStamp.Subject] != betaStamp.ID {
		t.Fatalf("[primaries] %q = %q, want %q", betaStamp.Subject, machineCfg.Primaries[betaStamp.Subject], betaStamp.ID)
	}
	if machineCfg.Subjects[canonicalPath(box.notespaceRoot("research", "beta"))] != betaStamp.Subject {
		t.Fatalf("[subjects] does not record the minted notespace path: %+v", machineCfg.Subjects)
	}

	// 3. Every member was registered before the share, and the share named them all.
	if len(server.Registers) != 2 {
		t.Fatalf("registrations = %d, want one per notespace: %+v", len(server.Registers), server.Registers)
	}
	if len(server.Shares) != 1 || len(server.Shares[0].Members) != 2 {
		t.Fatalf("share request = %+v", server.Shares)
	}
	if server.Shares[0].NotebookID.String() != notebookStamp.ID {
		t.Fatalf("share used notebook id %q, want the stamped %q", server.Shares[0].NotebookID, notebookStamp.ID)
	}

	// 4. Only then is the answer recorded in notebooks.toml.
	recorded := box.readNotebooksTOML(t)
	if !strings.Contains(recorded, "[notebooks.research.sync]") || !strings.Contains(recorded, "share = true") {
		t.Fatalf("notebooks.toml does not record the share:\n%s", recorded)
	}

	// 5. Evidence is per notespace, and the retention rule is stated up front.
	requireContains(t, got, "attached     "+fixtureNotespace1, "per-notespace disposition")
	requireContains(t, got, "registered   "+fixtureNotespace1, "per-notespace registration")
	requireContains(t, got, "minted       "+betaStamp.ID, "minted notespace evidence")
	requireContains(t, got, syncproto.UnshareRetentionStatement, "forward-only unshare copy")
	requireContains(t, got, `transition: "notebook share"`, "transition evidence")
	requireContains(t, got, `"notespaces-minted": 1`, "evidence counts")
	requireContains(t, got, "server accepted:", "server receipt")
}

func TestNotebookShareRefusesMissingRoot(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {},
		"ghost":    {Missing: true},
	})
	box.recordSyncServer(t, server.URL)

	err := runNotebookShare(context.Background(), &bytes.Buffer{}, "ghost", false)
	if err == nil {
		t.Fatal("share accepted a notebook whose recorded root does not exist")
	}
	if !strings.Contains(err.Error(), "share never creates a notebook root") {
		t.Fatalf("refusal does not explain itself: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(box.notebooks, "ghost")); statErr == nil {
		t.Fatal("share created the missing root")
	}
	if len(server.Shares) != 0 {
		t.Fatalf("share reached the server: %+v", server.Shares)
	}
}

func TestNotebookShareRefusalRecordsNothingLocally(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	// The notespace already belongs to another notebook on the server, which is
	// a move, not a share — the server refuses the whole request.
	server.addNotebook(fixtureNotebookB, "other", "shared", 1)
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookB, 1, 0)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	err := runNotebookShare(context.Background(), &out, "research", false)
	if err == nil {
		t.Fatal("share succeeded although the server rejected a member")
	}
	requireContains(t, out.String(), "rejected", "per-member rejection evidence")
	requireContains(t, err.Error(), "nothing was recorded locally", "the refusal says what was not written")
	if strings.Contains(box.readNotebooksTOML(t), "share = true") {
		t.Fatalf("a rejected share was recorded in notebooks.toml:\n%s", box.readNotebooksTOML(t))
	}
}

func TestNotebookShareDecidesAgainstTheVersionItRead(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	// The server already holds this notebook at version 2. A client that
	// assumed "0, surely new" would be refused as stale; this one reads the
	// version out of the inventory first.
	server.addNotebook(fixtureNotebookA, "research", "shared", 2)
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookA, 1, 0)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotebookShare(context.Background(), &out, "research", false); err != nil {
		t.Fatalf("re-sharing an already shared notebook: %v", err)
	}
	if len(server.Shares) != 1 || server.Shares[0].ExpectedVersion != 2 {
		t.Fatalf("share expected_version = %+v, want the version the inventory reported", server.Shares)
	}
	requireContains(t, out.String(), "already-member", "an unchanged share still reports full evidence")
}

func TestNotebookShareRefusesADuplicateNotebookID(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	// The copied-stamp state of D8: one id recorded by two notebooks.
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA},
		"copy":     {Stamp: fixtureNotebookA},
	})
	box.recordSyncServer(t, server.URL)

	err := runNotebookShare(context.Background(), &bytes.Buffer{}, "research", false)
	if err == nil {
		t.Fatal("share acted on a machine recording one notebook id twice")
	}
	requireContains(t, err.Error(), "duplicate notebook id", "the refusal names the condition")
	if len(server.Shares) != 0 {
		t.Fatalf("a duplicate-id machine reached the server: %+v", server.Shares)
	}
	if strings.Contains(box.readNotebooksTOML(t), "share = true") {
		t.Fatal("a refused share was recorded")
	}
}

// ---- pull ----------------------------------------------------------------------

func TestNotebookPullBindsServerNotebookAtRecordedRoot(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "shared", 4)
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookC, 1, 9)
	server.addNotespace(fixtureNotespace2, "beta", fixtureNotebookC, 1, 3)
	box.recordNotebooks(t, "team", map[string]notebookFixture{
		"team": {Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotebookPull(context.Background(), &out, "team", false); err != nil {
		t.Fatalf("notebook pull: %v", err)
	}
	got := out.String()

	stamp, err := notespace.LoadNotebook(filepath.Join(box.notebooks, "team"))
	if err != nil || stamp == nil || stamp.ID != fixtureNotebookC {
		t.Fatalf("pull did not bind the server's notebook id: %v %+v", err, stamp)
	}
	if !strings.Contains(box.readNotebooksTOML(t), "share = true") {
		t.Fatalf("pull did not record the notebook as shared:\n%s", box.readNotebooksTOML(t))
	}
	requireContains(t, got, "here         "+fixtureNotespace1, "notespaces already present")
	requireContains(t, got, "awaiting     "+fixtureNotespace2, "notespaces the server still owes")
	requireContains(t, got, "this verb wrote none", "pull writes no documents")
	requireContains(t, got, `"notespaces-awaiting-delivery": 1`, "evidence counts")
	requireContains(t, got, `transition: "notebook pull"`, "transition evidence")

	// Pull materializes nothing itself: the awaited notespace has no directory.
	if _, statErr := os.Stat(box.notespaceRoot("team", "beta")); statErr == nil {
		t.Fatal("pull created a notespace directory; delivery is the daemon's")
	}
}

func TestNotebookPullRefusesUnrecordedNotebook(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "shared", 1)
	box.recordNotebooks(t, "research", map[string]notebookFixture{"research": {}})
	box.recordSyncServer(t, server.URL)

	err := runNotebookPull(context.Background(), &bytes.Buffer{}, "team", false)
	if err == nil {
		t.Fatal("pull invented a notebook this machine never recorded")
	}
	requireContains(t, err.Error(), "records no notebook \"team\"", "the refusal names the file and the name")
	requireContains(t, err.Error(), "pull materializes into a RECORDED root", "the refusal says what to record")
	if _, statErr := os.Stat(filepath.Join(box.notebooks, "team")); statErr == nil {
		t.Fatal("pull created a root for an unrecorded notebook")
	}
}

func TestNotebookPullRefusesMissingRecordedRoot(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "shared", 1)
	box.recordNotebooks(t, "team", map[string]notebookFixture{"team": {Missing: true}})
	box.recordSyncServer(t, server.URL)

	err := runNotebookPull(context.Background(), &bytes.Buffer{}, "team", false)
	if err == nil {
		t.Fatal("pull accepted a recorded root that does not exist")
	}
	requireContains(t, err.Error(), "refuses a missing root rather than creating one", "the refusal explains itself")
	if _, statErr := os.Stat(filepath.Join(box.notebooks, "team")); statErr == nil {
		t.Fatal("pull recreated the missing root")
	}
}

func TestNotebookPullRefusesUnsharedServerNotebook(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "unshared", 7)
	box.recordNotebooks(t, "team", map[string]notebookFixture{"team": {Stamp: fixtureNotebookC}})
	box.recordSyncServer(t, server.URL)

	err := runNotebookPull(context.Background(), &bytes.Buffer{}, "team", false)
	if err == nil {
		t.Fatal("pull offered a notebook the server has unshared")
	}
	requireContains(t, err.Error(), "is unshared on this server", "the refusal names the state")
	requireContains(t, err.Error(), syncproto.UnshareRetentionStatement, "the server's own retention sentence")
	if strings.Contains(box.readNotebooksTOML(t), "share = true") {
		t.Fatalf("a refused pull recorded a share:\n%s", box.readNotebooksTOML(t))
	}
}

func TestNotebookPullRefusesToReKeyAStampedRoot(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "shared", 1)
	// The root is already stamped as a DIFFERENT notebook; a stamp is immutable.
	box.recordNotebooks(t, "team", map[string]notebookFixture{"team": {Stamp: fixtureNotebookA}})
	box.recordSyncServer(t, server.URL)

	err := runNotebookPull(context.Background(), &bytes.Buffer{}, "team", false)
	if err == nil {
		t.Fatal("pull re-keyed a stamped root")
	}
	requireContains(t, err.Error(), "which this server does not hold", "the refusal compares the two ids")
	stamp, _ := notespace.LoadNotebook(filepath.Join(box.notebooks, "team"))
	if stamp == nil || stamp.ID != fixtureNotebookA {
		t.Fatalf("the stamp changed: %+v", stamp)
	}
}
