package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/notespace"
)

// Phase 4's siblings, verified against a temp GROVE_HOME. No test here reaches a
// server: primariness is a machine-local routing pointer, so every fact these
// verbs read and write is on this machine's disk.

const siblingSubject = "example.com/org/core"

// siblingSandbox seeds one subject with a primary notespace in `research`, a
// second recorded notebook `personal`, and an unrelated subject in `research`
// so a verb that touched the wrong entry would be visible.
func siblingSandbox(t *testing.T) scopeSandbox {
	t.Helper()
	box := sandboxNotebookScope(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
			{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
			{Dir: "other", ID: fixtureNotespace3, Subject: "example.com/org/other", Kind: "repo"},
		}},
		"personal": {Stamp: fixtureNotebookB},
	})
	writeMachineIdentity(t, map[string]string{
		siblingSubject:          fixtureNotespace1,
		"example.com/org/other": fixtureNotespace3,
	}, nil)
	return box
}

func machineTOML(t *testing.T, box scopeSandbox) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(box.configDir, "machine.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNotespaceNewCreatesASiblingAndLeavesRoutingAlone(t *testing.T) {
	box := siblingSandbox(t)
	before := machineTOML(t, box)

	var out bytes.Buffer
	if err := runNotespaceNew(&out, notespaceNewOptions{subject: siblingSubject, in: "personal"}); err != nil {
		t.Fatalf("notespace new: %v", err)
	}

	// Same name in another notebook: the notebook disambiguates, so nothing is
	// uniquified.
	root := box.notespaceRoot("personal", "core")
	stamp, err := notespace.LoadNotespace(root)
	if err != nil || stamp == nil {
		t.Fatalf("stamp at %s = %+v, %v", root, stamp, err)
	}
	if stamp.ID == fixtureNotespace1 {
		t.Fatal("the sibling reused the primary's id")
	}
	if stamp.Subject != siblingSubject || stamp.Kind != "repo" || stamp.Name != "core" {
		t.Fatalf("stamp = %+v", stamp)
	}
	if after := machineTOML(t, box); after != before {
		t.Fatalf("machine.toml changed:\n--- before\n%s\n--- after\n%s", before, after)
	}
	requireContains(t, out.String(), "unchanged; machine.toml was not written", "the verb says what it did not touch")
	requireContains(t, out.String(), "notespaces-created", "the transition prints evidence")
}

func TestNotespaceNewUniquifiesASameNotebookSibling(t *testing.T) {
	box := siblingSandbox(t)

	var out bytes.Buffer
	if err := runNotespaceNew(&out, notespaceNewOptions{subject: siblingSubject, in: "research"}); err != nil {
		t.Fatalf("notespace new: %v", err)
	}
	stamp, err := notespace.LoadNotespace(box.notespaceRoot("research", "core-2"))
	if err != nil || stamp == nil {
		t.Fatalf("uniquified sibling = %+v, %v", stamp, err)
	}
	requireContains(t, out.String(), "uniquified", "the derivation is stated")

	// The original is untouched, so both are legal in one notebook.
	primary, err := notespace.LoadNotespace(box.notespaceRoot("research", "core"))
	if err != nil || primary == nil || primary.ID != fixtureNotespace1 {
		t.Fatalf("primary = %+v, %v", primary, err)
	}

	// A third one keeps counting rather than colliding.
	if err := runNotespaceNew(&bytes.Buffer{}, notespaceNewOptions{subject: siblingSubject, in: "research"}); err != nil {
		t.Fatalf("second sibling: %v", err)
	}
	if _, err := os.Stat(box.notespaceRoot("research", "core-3")); err != nil {
		t.Fatalf("third notespace: %v", err)
	}
}

func TestNotespaceNewRefusals(t *testing.T) {
	box := siblingSandbox(t)
	// A subject with notes but no recorded primary, and a notebook that is
	// recorded without existing.
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
			{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
			{Dir: "loose", ID: fixtureNotespace2, Subject: "example.com/org/loose", Kind: "repo"},
		}},
		"personal": {Stamp: fixtureNotebookB},
		"ghost":    {Missing: true},
	})
	// example.com/org/absent points at an id nothing on this machine carries;
	// example.com/org/mixed points at one stamped for a different subject.
	writeMachineIdentity(t, map[string]string{
		siblingSubject:           fixtureNotespace1,
		"example.com/org/absent": "01ARZ3NDEKTSV4RRFFQ69G5F09",
		"example.com/org/mixed":  fixtureNotespace2,
	}, nil)

	cases := []struct {
		name string
		opts notespaceNewOptions
		want string
	}{
		{"no destination", notespaceNewOptions{subject: siblingSubject}, "--in <notebook> is required"},
		{"unknown notebook", notespaceNewOptions{subject: siblingSubject, in: "nowhere"}, "records no notebook \"nowhere\""},
		{"missing root", notespaceNewOptions{subject: siblingSubject, in: "ghost"}, "never creates a notebook root"},
		{"invalid subject", notespaceNewOptions{subject: "not a subject", in: "personal"}, "recorded subjects:"},
		{"unrecorded primary", notespaceNewOptions{subject: "example.com/org/loose", in: "personal"}, "no recorded primary notespace"},
		{"dangling primary", notespaceNewOptions{subject: "example.com/org/absent", in: "personal"}, "has no stamped root"},
		{"mis-keyed primary", notespaceNewOptions{subject: "example.com/org/mixed", in: "personal"}, "is stamped for subject"},
		{"name already taken", notespaceNewOptions{subject: siblingSubject, in: "research", name: "core"}, "already holds a notespace directory named"},
		{"name is not a directory", notespaceNewOptions{subject: siblingSubject, in: "personal", name: "a/b"}, "not a single directory name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runNotespaceNew(&bytes.Buffer{}, tc.opts)
			if err == nil {
				t.Fatalf("new accepted %+v", tc.opts)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
	// A refused creation leaves no directory behind.
	entries, err := os.ReadDir(filepath.Join(box.notebooks, "personal", "notespaces"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("a refused creation left %d directories behind", len(entries))
	}
	if _, err := os.Stat(filepath.Join(box.notebooks, "ghost")); err == nil {
		t.Fatal("a refused creation materialized the missing notebook root")
	}
}

func TestNotespacePrimaryFlipsExactlyOneEntry(t *testing.T) {
	box := siblingSandbox(t)
	if err := runNotespaceNew(&bytes.Buffer{}, notespaceNewOptions{subject: siblingSubject, in: "personal"}); err != nil {
		t.Fatalf("notespace new: %v", err)
	}
	sibling, err := notespace.LoadNotespace(box.notespaceRoot("personal", "core"))
	if err != nil || sibling == nil {
		t.Fatalf("sibling = %+v, %v", sibling, err)
	}

	var out bytes.Buffer
	if err := runNotespacePrimary(&out, sibling.ID, false); err != nil {
		t.Fatalf("notespace primary: %v", err)
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if machineCfg.Primaries[siblingSubject] != sibling.ID {
		t.Fatalf("[primaries] = %+v", machineCfg.Primaries)
	}
	if machineCfg.Primaries["example.com/org/other"] != fixtureNotespace3 {
		t.Fatalf("the flip disturbed another subject: %+v", machineCfg.Primaries)
	}
	// The old primary's content is untouched — a flip is a pointer move.
	if _, err := os.Stat(filepath.Join(box.notespaceRoot("research", "core"), "note.md")); err != nil {
		t.Fatalf("the old primary's content: %v", err)
	}
	requireContains(t, out.String(), fixtureNotespace1, "the previous primary is named")
	requireContains(t, out.String(), "Nothing moved", "the verb states what it did not do")
	requireContains(t, out.String(), "primaries-rewritten", "the transition prints evidence")

	// Re-running is a no-op that says so rather than rewriting the file.
	before := machineTOML(t, box)
	var again bytes.Buffer
	if err := runNotespacePrimary(&again, sibling.ID, false); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if after := machineTOML(t, box); after != before {
		t.Fatal("a no-op flip rewrote machine.toml")
	}
	requireContains(t, again.String(), "already records this notespace as the primary", "the no-op explains itself")
}

func TestNotespacePrimaryRepairsAMissingEntry(t *testing.T) {
	box := siblingSandbox(t)
	// The primary root is deleted, which is the state doctor reports and this
	// verb repairs: the surviving sibling is designated by name.
	if err := runNotespaceNew(&bytes.Buffer{}, notespaceNewOptions{subject: siblingSubject, in: "personal"}); err != nil {
		t.Fatalf("notespace new: %v", err)
	}
	if err := os.RemoveAll(box.notespaceRoot("research", "core")); err != nil {
		t.Fatal(err)
	}
	writeMachineIdentity(t, map[string]string{"example.com/org/other": fixtureNotespace3}, nil)

	var out bytes.Buffer
	if err := runNotespacePrimary(&out, "core", false); err != nil {
		t.Fatalf("notespace primary: %v", err)
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := notespace.LoadNotespace(box.notespaceRoot("personal", "core"))
	if err != nil {
		t.Fatal(err)
	}
	if machineCfg.Primaries[siblingSubject] != sibling.ID {
		t.Fatalf("[primaries] = %+v", machineCfg.Primaries)
	}
	requireContains(t, out.String(), "nothing recorded", "a repair says there was no previous pointer")
}

func TestNotespacePrimaryRefusals(t *testing.T) {
	box := siblingSandbox(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
			{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
			{Dir: "unminted"},
		}},
		"personal": {Stamp: fixtureNotebookB, Notespaces: []notespaceFixture{
			{Dir: "core", ID: fixtureNotespace2, Subject: siblingSubject, Kind: "repo"},
		}},
	})
	// fixtureNotespace2 is stamped for siblingSubject but recorded as the
	// primary of another subject: one notespace is the primary of at most one.
	writeMachineIdentity(t, map[string]string{
		siblingSubject:          fixtureNotespace1,
		"example.com/org/other": fixtureNotespace2,
	}, nil)

	cases := []struct {
		name string
		want string
		arg  string
	}{
		{"ambiguous name", "name it by its immutable id", "core"},
		{"unknown", "no recorded notebook contains a notespace", "nope"},
		{"unminted", "carries no .notespace.toml", "unminted"},
		{"primary of another subject", "at most one subject", fixtureNotespace2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runNotespacePrimary(&bytes.Buffer{}, tc.arg, false); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if machineCfg.Primaries[siblingSubject] != fixtureNotespace1 {
		t.Fatalf("a refused flip changed [primaries]: %+v", machineCfg.Primaries)
	}
}

func TestNotespaceListGroupsSiblingsPrimaryFirst(t *testing.T) {
	box := siblingSandbox(t)
	if err := runNotespaceNew(&bytes.Buffer{}, notespaceNewOptions{subject: siblingSubject, in: "personal"}); err != nil {
		t.Fatalf("notespace new: %v", err)
	}
	sibling, err := notespace.LoadNotespace(box.notespaceRoot("personal", "core"))
	if err != nil {
		t.Fatal(err)
	}
	// An unminted directory and a subject with no primary are both reported.
	if err := os.MkdirAll(box.notespaceRoot("research", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runNotespaceList(&out, false); err != nil {
		t.Fatalf("notespace list: %v", err)
	}
	text := out.String()
	requireContains(t, text, siblingSubject, "the subject heads its group")
	requireContains(t, text, "primary", "the primary is marked")
	requireContains(t, text, "personal", "the parent notebook is shown")
	requireContains(t, text, "scratch", "an unminted directory is reported, not dropped")

	primaryAt := strings.Index(text, fixtureNotespace1)
	siblingAt := strings.Index(text, sibling.ID)
	if primaryAt < 0 || siblingAt < 0 || primaryAt > siblingAt {
		t.Fatalf("the primary is not listed first (%d vs %d)\n%s", primaryAt, siblingAt, text)
	}

	// The flip reorders the group without moving anything on disk.
	if err := runNotespacePrimary(&bytes.Buffer{}, sibling.ID, false); err != nil {
		t.Fatalf("notespace primary: %v", err)
	}
	var flipped bytes.Buffer
	if err := runNotespaceList(&flipped, false); err != nil {
		t.Fatal(err)
	}
	text = flipped.String()
	if strings.Index(text, sibling.ID) > strings.Index(text, fixtureNotespace1) {
		t.Fatalf("the new primary is not listed first\n%s", text)
	}
}

func TestNotespaceListReportsBrokenPrimariesAsJSON(t *testing.T) {
	siblingSandbox(t)
	// A dangling entry plus a subject with notes and no entry at all.
	writeMachineIdentity(t, map[string]string{siblingSubject: fixtureNotespace2}, nil)

	var out bytes.Buffer
	if err := runNotespaceList(&out, true); err != nil {
		t.Fatalf("notespace list --json: %v", err)
	}
	text := out.String()
	requireContains(t, text, `"primary_notespace_id"`, "the group carries the recorded pointer")
	requireContains(t, text, "dangling primary", "a dangling entry is reported")
	requireContains(t, text, "missing primary", "a subject with no entry is reported")
	requireNotContains(t, text, `"is_primary"`, "primariness is a relationship, never a per-notespace field")
}
