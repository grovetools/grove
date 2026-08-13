package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `grove sync join` is the relationship verb: it records which server this
// machine talks to and reports the delta. These tests pin the two halves of
// that sentence — what it writes, and what it deliberately does NOT.

func TestSyncJoinRecordsRelationshipAndRendersDelta(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	// The server holds one notebook this machine also records (with one
	// notespace the machine does not have), one it has never heard of, one it
	// has unshared, and a notespace in no notebook at all.
	server.addNotebook(fixtureNotebookA, "research", "shared", 3)
	server.addNotebook(fixtureNotebookB, "archive", "unshared", 5)
	server.addNotebook(fixtureNotebookC, "field", "shared", 2)
	server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookA, 1, 12)
	server.addNotespace(fixtureNotespace2, "beta", fixtureNotebookA, 1, 4)
	server.addNotespace(fixtureNotespace3, "orphan", "", 0, 0)

	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true), Notespaces: []notespaceFixture{{Dir: "alpha", ID: fixtureNotespace1}}},
		"scratch":  {Stamp: "01ARZ3NDEKTSV4RRFFQ69G5FAX"},
		"unminted": {},
	})

	var out bytes.Buffer
	if err := runSyncJoin(context.Background(), &out, syncJoinOptions{server: server.URL, expand: true}); err != nil {
		t.Fatalf("sync join: %v", err)
	}
	got := out.String()

	// 1. The relationship is recorded, and nothing else in sync.toml is.
	syncTOML, err := os.ReadFile(filepath.Join(box.configDir, "sync.toml"))
	if err != nil {
		t.Fatalf("sync.toml was not written: %v", err)
	}
	if !strings.Contains(string(syncTOML), server.URL) {
		t.Fatalf("sync.toml does not record the server:\n%s", syncTOML)
	}
	if strings.Contains(string(syncTOML), "[[workspaces]]") {
		t.Fatalf("sync join wrote a subscription; it records the relationship only:\n%s", syncTOML)
	}
	// 2. Nothing was minted, bound or created: that is `grove join`'s work.
	if _, err := os.Stat(filepath.Join(box.configDir, "machine.toml")); err == nil {
		t.Fatalf("sync join wrote machine.toml; it must mutate nothing but the relationship")
	}
	if _, err := os.Stat(filepath.Join(box.notebooks, "unminted", ".notebook.toml")); err == nil {
		t.Fatalf("sync join minted a notebook stamp")
	}

	// 3. The delta names both directions, at notebook and notespace grain.
	requireContains(t, got, "pull: this machine does not record it", "server-only notebook")
	requireContains(t, got, "share: this server does not hold it", "local-only notebook")
	requireContains(t, got, "retained after an unshare; not on offer (D9)", "unshared server notebook")
	requireContains(t, got, "server only  "+fixtureNotespace2, "expanded notespace grain")
	requireContains(t, got, "unparented on the server: 1 notespace", "unparented notespaces")
	requireContains(t, got, "recorded here but unstamped", "unstamped local notebook")
	requireContains(t, got, "Nothing above moved.", "the delta describes, never acts")

	// 4. Evidence, with the server's own answer sealed into it.
	requireContains(t, got, `transition: "sync join"`, "transition evidence")
	requireContains(t, got, `"notespaces-server-only": 1`, "evidence counts")
	requireContains(t, got, "server accepted:", "server receipt")
}

func TestSyncJoinDeltaOnlyWritesNothing(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", "shared", 1)
	box.recordNotebooks(t, "research", map[string]notebookFixture{"research": {Stamp: fixtureNotebookA}})
	box.recordSyncServer(t, server.URL)

	before, err := os.ReadFile(filepath.Join(box.configDir, "sync.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSyncJoin(context.Background(), &out, syncJoinOptions{server: server.URL, deltaOnly: true}); err != nil {
		t.Fatalf("sync join --delta-only: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(box.configDir, "sync.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("--delta-only rewrote sync.toml:\n--- before\n%s\n--- after\n%s", before, after)
	}
	requireContains(t, out.String(), "not recorded (--delta-only)", "the run says it recorded nothing")
	requireContains(t, out.String(), `transition: "sync join"`, "transition evidence")
}

func TestSyncJoinRendersADuplicateNotebookIDInsteadOfHidingIt(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "research", "shared", 1)
	// One id, two recorded notebooks — the state the acting verbs refuse. The
	// delta must still render: this is where the operator meets it.
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Share: boolPtr(true)},
		"copy":     {Stamp: fixtureNotebookA},
	})

	var out bytes.Buffer
	if err := runSyncJoin(context.Background(), &out, syncJoinOptions{server: server.URL}); err != nil {
		t.Fatalf("sync join refused to render a duplicate: %v", err)
	}
	requireContains(t, out.String(), "duplicate notebook id", "the duplicate is reported")
	requireContains(t, out.String(), "(duplicate id)", "the row is marked")
	requireContains(t, out.String(), `transition: "sync join"`, "the delta still renders")
}

func TestSyncJoinReportsRecordedRegistryBindingWithoutCreatingOne(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{"research": {Stamp: fixtureNotebookA}})

	var out bytes.Buffer
	if err := runSyncJoin(context.Background(), &out, syncJoinOptions{server: server.URL}); err != nil {
		t.Fatalf("sync join: %v", err)
	}
	requireContains(t, out.String(), "none recorded — `grove join` binds one", "registry binding stays join's job")
	requireNotContains(t, out.String(), "machines", "no registry root was materialized")
}
