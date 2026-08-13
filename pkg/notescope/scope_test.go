package notescope

import (
	"os"
	"path/filepath"
	"testing"

	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/subject"
)

// Every test here runs against a temp GROVE_HOME. A scope test that read the
// developer's real notebooks.toml would pass or fail depending on the machine
// it ran on, which is the contamination job 79 documented.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir := filepath.Join(home, "config", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	coreconfig.ResetLoadCache()
	t.Cleanup(coreconfig.ResetLoadCache)
	return dir
}

// notebookAt creates a notebook root with the given notespace directories and
// records it. Stamps are minted only for the notespaces named in minted, so a
// test can hold the real "recorded but unminted" state rather than assume it
// away.
func notebookAt(t *testing.T, configDir, name string, shared *bool, notespaces []string, minted map[string]bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ns := range notespaces {
		if err := os.MkdirAll(filepath.Join(root, ContainerDir, ns), 0o755); err != nil {
			t.Fatal(err)
		}
		if minted[ns] {
			if _, err := notespace.MintNotespace(filepath.Join(root, ContainerDir, ns), notespace.NotespaceMutable{Name: ns, Subject: subject.MintLocal().String(), Kind: "notes"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	edits := coreconfig.NotebookEdits{Upserts: map[string]coderoot.Notebook{name: {Root: root}}}
	if _, err := coreconfig.WriteNotebooks(filepath.Join(configDir, "notebooks.toml"), edits); err != nil {
		t.Fatal(err)
	}
	if shared != nil {
		if _, err := coreconfig.WriteNotebooks(filepath.Join(configDir, "notebooks.toml"), coreconfig.NotebookEdits{SyncShare: map[string]bool{name: *shared}}); err != nil {
			t.Fatal(err)
		}
	}
	coreconfig.ResetLoadCache()
	return root
}

func scan(t *testing.T) []Notebook {
	t.Helper()
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := Scan(table)
	if err != nil {
		t.Fatal(err)
	}
	return scanned
}

// The sync column is derived from the notebook's recorded containment scope
// and from nothing else — there is no per-notespace fact that could produce a
// different answer for two notespaces in one notebook.
func TestSyncLabelIsDerivedFromNotebookContainment(t *testing.T) {
	dir := tempHome(t)
	yes, no := true, false
	notebookAt(t, dir, "shared", &yes, []string{"alpha", "bravo"}, map[string]bool{"alpha": true, "bravo": true})
	notebookAt(t, dir, "plain", nil, []string{"charlie"}, map[string]bool{"charlie": true})
	notebookAt(t, dir, "stopped", &no, nil, nil)

	byName := map[string]Notebook{}
	for _, nb := range scan(t) {
		byName[nb.Name] = nb
	}
	if got := byName["shared"].SyncLabel(); got != SyncLabelShared {
		t.Errorf("shared notebook label = %q, want %q", got, SyncLabelShared)
	}
	if got := byName["plain"].SyncLabel(); got != SyncLabelLocal {
		t.Errorf("unrecorded notebook label = %q, want %q", got, SyncLabelLocal)
	}
	// D9: recorded-as-unshared is out of scope now, so the column says local —
	// but the retention fact must not be silently folded away with it.
	if got := byName["stopped"].SyncLabel(); got != SyncLabelLocal {
		t.Errorf("unshared notebook label = %q, want %q", got, SyncLabelLocal)
	}
	if byName["stopped"].RetentionNote() == "" {
		t.Error("recorded `share = false` lost its D9 retention statement")
	}
	if byName["plain"].RetentionNote() != "" {
		t.Errorf("a never-recorded notebook claimed a retention decision: %q", byName["plain"].RetentionNote())
	}

	// Both notespaces of the shared notebook are shared BY CONTAINMENT: the
	// rows carry no per-notespace sync value that could disagree.
	if rows := BuildNotesRows(nil, nil); len(rows) != 0 {
		t.Fatalf("empty scan produced rows: %#v", rows)
	}
	for _, row := range BuildNotesRows(scan(t), map[string]bool{"shared": true}) {
		if row.Kind == NotesRowNotespace && row.Sync != "" {
			t.Errorf("notespace %s carries its own sync state %q — the notebook is the only knob", row.Dir, row.Sync)
		}
	}
}

func TestScanReportsMissingRootsAndUnmintedNotespaces(t *testing.T) {
	dir := tempHome(t)
	notebookAt(t, dir, "nb", nil, []string{"minted", "bare"}, map[string]bool{"minted": true})
	missingRoot := filepath.Join(t.TempDir(), "gone")
	if _, err := coreconfig.WriteNotebooks(filepath.Join(dir, "notebooks.toml"), coreconfig.NotebookEdits{Upserts: map[string]coderoot.Notebook{"absent": {Root: missingRoot}}}); err != nil {
		t.Fatal(err)
	}
	coreconfig.ResetLoadCache()

	byName := map[string]Notebook{}
	for _, nb := range scan(t) {
		byName[nb.Name] = nb
	}
	if byName["absent"].Exists {
		t.Error("a root that is not there was scanned as existing")
	}
	if len(byName["nb"].Notespaces) != 2 {
		t.Fatalf("notespaces = %#v", byName["nb"].Notespaces)
	}
	var bare Notespace
	for _, ns := range byName["nb"].Notespaces {
		if ns.Dir == "bare" {
			bare = ns
		}
	}
	if bare.Dir == "" {
		t.Fatal("an unstamped notespace directory was dropped from the scan")
	}
	if bare.ID() != "" {
		t.Errorf("unstamped notespace reported id %q", bare.ID())
	}
}

// A move never creates a notebook root, so a notebook whose recorded root is
// missing is not a destination this page may offer.
func TestMoveDestinationsExcludeSelfAndMissingRoots(t *testing.T) {
	dir := tempHome(t)
	notebookAt(t, dir, "here", nil, []string{"alpha"}, map[string]bool{"alpha": true})
	notebookAt(t, dir, "there", nil, nil, nil)
	if _, err := coreconfig.WriteNotebooks(filepath.Join(dir, "notebooks.toml"), coreconfig.NotebookEdits{Upserts: map[string]coderoot.Notebook{"absent": {Root: filepath.Join(t.TempDir(), "gone")}}}); err != nil {
		t.Fatal(err)
	}
	coreconfig.ResetLoadCache()

	got := MoveDestinations(scan(t), "here")
	if len(got) != 1 || got[0] != "there" {
		t.Fatalf("destinations = %v, want [there]", got)
	}
}

func TestLoadRefusesAMachineThatRecordsNoNotebooksFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	coreconfig.ResetLoadCache()
	t.Cleanup(coreconfig.ResetLoadCache)

	if _, _, err := Load(); err == nil {
		t.Fatal("Load accepted a machine with no notebooks.toml")
	}

	// Scan, which the TUI uses, tolerates it: nothing recorded is an empty
	// inventory, not an error, and the page renders its empty state.
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := Scan(table)
	if err != nil {
		t.Fatalf("Scan refused an unrecorded machine: %v", err)
	}
	if len(scanned) != 0 {
		t.Fatalf("scanned = %#v, want none", scanned)
	}
}
