package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// The name is uniquified even though the destination notebook holds no
	// `core` directory: one name per subject is what keeps `notespace primary`
	// and `notespace move` able to resolve this notespace by name at all.
	root := box.notespaceRoot("personal", "core-2")
	stamp, err := notespace.LoadNotespace(root)
	if err != nil || stamp == nil {
		t.Fatalf("stamp at %s = %+v, %v", root, stamp, err)
	}
	if stamp.ID == fixtureNotespace1 {
		t.Fatal("the sibling reused the primary's id")
	}
	if stamp.Subject != siblingSubject || stamp.Kind != "repo" || stamp.Name != "core-2" {
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

// TestNotespaceNewNamesSiblingsUniquelyAcrossNotebooks pins the reason the
// default name is uniquified even when the destination is empty.
//
// `notespace primary` and `notespace move` resolve a name across EVERY recorded
// notebook, so two siblings of one subject sharing a name would cost both verbs
// their name rung the moment the second one existed — they would refuse and
// demand the immutable id. The uniquified default keeps naming possible; an
// explicit --name is still honoured, because what an operator names, they get.
func TestNotespaceNewNamesSiblingsUniquelyAcrossNotebooks(t *testing.T) {
	box := siblingSandbox(t)

	var out bytes.Buffer
	if err := runNotespaceNew(&out, notespaceNewOptions{subject: siblingSubject, in: "personal"}); err != nil {
		t.Fatalf("notespace new: %v", err)
	}
	requireContains(t, out.String(), "already carried by a sibling of this subject",
		"the derivation names the collision it actually met")
	requireNotContains(t, out.String(), "is taken in this notebook",
		"the destination held no such directory, so that is not why the name moved")

	// A third notebook keeps counting across the whole subject rather than
	// restarting per destination.
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
			{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
			{Dir: "other", ID: fixtureNotespace3, Subject: "example.com/org/other", Kind: "repo"},
		}},
		"personal": {Stamp: fixtureNotebookB, Notespaces: []notespaceFixture{
			{Dir: "core-2", ID: fixtureNotespace2, Subject: siblingSubject, Kind: "repo", Name: "core-2"},
		}},
		"archive": {Stamp: fixtureNotebookC},
	})
	if err := runNotespaceNew(&bytes.Buffer{}, notespaceNewOptions{subject: siblingSubject, in: "archive"}); err != nil {
		t.Fatalf("third sibling: %v", err)
	}
	if _, err := os.Stat(box.notespaceRoot("archive", "core-3")); err != nil {
		t.Fatalf("the third sibling did not continue the subject's numbering: %v", err)
	}

	// An explicit name is judged against the destination alone, so an operator
	// may still take a name a sibling elsewhere carries.
	if err := runNotespaceNew(&bytes.Buffer{}, notespaceNewOptions{subject: siblingSubject, in: "archive", name: "core"}); err != nil {
		t.Fatalf("explicit name: %v", err)
	}
	stamp, err := notespace.LoadNotespace(box.notespaceRoot("archive", "core"))
	if err != nil || stamp == nil || stamp.Name != "core" {
		t.Fatalf("an explicit name was adjusted: %+v, %v", stamp, err)
	}

	// The names the DEFAULT chose stay reachable by name, which is the whole
	// point of uniquifying them; the one an operator took deliberately is the
	// one that now has to be addressed by id.
	if err := runNotespacePrimary(&bytes.Buffer{}, "core-3", false); err != nil {
		t.Fatalf("a uniquified name does not resolve: %v", err)
	}
	if err := runNotespacePrimary(&bytes.Buffer{}, "core", false); err == nil ||
		!strings.Contains(err.Error(), "name it by its immutable id") {
		t.Fatalf("the reused explicit name resolved anyway: %v", err)
	}
}

// TestNotespaceNewStatesTheDestinationsShareState pins F1: containment is
// consent, so creating a notespace inside a shared notebook publishes it on the
// daemon's next reconcile. The server leg is deferred rather than absent, which
// is exactly why there is no receipt to print and why saying it is the only way
// to state it at all.
func TestNotespaceNewStatesTheDestinationsShareState(t *testing.T) {
	cases := []struct {
		name    string
		shared  bool
		want    string
		notWant string
	}{
		{"shared destination", true, "the daemon registers this notespace with the sync server", "nothing is published"},
		{"local destination", false, "nothing is published", "the daemon registers this notespace with the sync server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			box := sandboxNotebookScope(t)
			box.recordNotebooks(t, "research", map[string]notebookFixture{
				"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
					{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
				}},
				"personal": {Stamp: fixtureNotebookB, Share: boolPtr(tc.shared)},
			})
			writeMachineIdentity(t, map[string]string{siblingSubject: fixtureNotespace1}, nil)

			var out bytes.Buffer
			if err := runNotespaceNew(&out, notespaceNewOptions{subject: siblingSubject, in: "personal"}); err != nil {
				t.Fatalf("notespace new: %v", err)
			}
			requireContains(t, out.String(), tc.want, "the verb states the destination's share state")
			requireNotContains(t, out.String(), tc.notWant, "and states only the one that is true")

			// The evidence carries it too, so a scripted caller reading JSON is
			// told the same thing the human output says.
			var asJSON bytes.Buffer
			box.recordNotebooks(t, "research", map[string]notebookFixture{
				"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
					{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
				}},
				"personal": {Stamp: fixtureNotebookB, Share: boolPtr(tc.shared), Notespaces: []notespaceFixture{
					{Dir: "core-2", ID: fixtureNotespace2, Subject: siblingSubject, Kind: "repo", Name: "core-2"},
				}},
			})
			if err := runNotespaceNew(&asJSON, notespaceNewOptions{subject: siblingSubject, in: "personal", asJSON: true}); err != nil {
				t.Fatalf("notespace new --json: %v", err)
			}
			requireContains(t, asJSON.String(), tc.want, "the transition reason carries the deferred server leg")
		})
	}
}

// TestNotespaceNewRefusesRatherThanAdoptsUnderConcurrency pins F2. The old
// Lstat-then-MkdirAll pair was two acts with a window between them, and neither
// half refused what was already there: MkdirAll succeeds on an existing
// directory and MintNotespace is load-first, so it RETURNS an existing stamp.
// Two runs deriving the same name would therefore both report creating one
// notespace. os.Mkdir makes the create itself the check, so exactly one wins.
func TestNotespaceNewRefusesRatherThanAdoptsUnderConcurrency(t *testing.T) {
	box := siblingSandbox(t)

	const racers = 6
	var wg sync.WaitGroup
	outputs := make([]string, racers)
	errs := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out bytes.Buffer
			errs[i] = runNotespaceNew(&out, notespaceNewOptions{subject: siblingSubject, in: "personal"})
			outputs[i] = out.String()
		}()
	}
	wg.Wait()

	// Every run that claims a creation must have created a DIFFERENT notespace.
	// An adoption looks like two successes naming one id.
	claimed := map[string]int{}
	for i, err := range errs {
		if err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("racer %d failed for an unexpected reason: %v", i, err)
			}
			continue
		}
		for _, line := range strings.Split(outputs[i], "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "created ") {
				claimed[strings.TrimSpace(line)]++
			}
		}
	}
	if len(claimed) == 0 {
		t.Fatalf("no racer created anything: %v", errs)
	}
	for line, count := range claimed {
		if count != 1 {
			t.Fatalf("%d runs reported the same creation, so one of them adopted: %q", count, line)
		}
	}

	// And the directories on disk match the creations claimed, one each.
	entries, err := os.ReadDir(filepath.Join(box.notebooks, "personal", "notespaces"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(claimed) {
		t.Fatalf("%d directories on disk for %d claimed creations", len(entries), len(claimed))
	}
}

// TestRollbackMintedNotespaceUndoesWhatItRefuses pins F6. The duplicate-id
// refusal is issued AFTER the stamp is on disk, so "nothing was recorded" is a
// claim that has to be made true rather than merely asserted: a freshly stamped
// root carrying a duplicated id is the D8 state every verb here refuses to act
// on, and containment would enrol it if the notebook were shared.
func TestRollbackMintedNotespaceUndoesWhatItRefuses(t *testing.T) {
	t.Run("the mint is removed", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "core")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := notespace.MintNotespace(root, notespace.NotespaceMutable{Name: "core", Subject: siblingSubject, Kind: "repo"}); err != nil {
			t.Fatal(err)
		}
		said := rollbackMintedNotespace(root)
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatalf("the refused mint still stands at %s (%v)", root, err)
		}
		requireContains(t, said, "the directory this run created was removed", "the refusal says the world matches its claim")
	})

	t.Run("what it cannot remove, it names", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "core")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := notespace.MintNotespace(root, notespace.NotespaceMutable{Name: "core", Subject: siblingSubject, Kind: "repo"}); err != nil {
			t.Fatal(err)
		}
		// Content the rollback must not force its way through: the removal is
		// non-recursive precisely so a surprise is left alone and reported.
		if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("# kept\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		said := rollbackMintedNotespace(root)
		if _, err := os.Lstat(filepath.Join(root, "note.md")); err != nil {
			t.Fatalf("the rollback removed content it did not create: %v", err)
		}
		requireContains(t, said, "still stands", "a rollback that could not finish says so")
		requireContains(t, said, "remove it by hand", "and names the repair")
	})
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
	sibling, err := notespace.LoadNotespace(box.notespaceRoot("personal", "core-2"))
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

	// The surviving sibling is named rather than pointed at by id, which only
	// works because its derived name was uniquified across the subject.
	var out bytes.Buffer
	if err := runNotespacePrimary(&out, "core-2", false); err != nil {
		t.Fatalf("notespace primary: %v", err)
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := notespace.LoadNotespace(box.notespaceRoot("personal", "core-2"))
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
	sibling, err := notespace.LoadNotespace(box.notespaceRoot("personal", "core-2"))
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

// TestNotespaceListReportsADuplicatedIDOnce pins F3's list half. A duplicated
// id is one condition on disk. The primary audit meets it from the binding
// table's side and can say nothing about it the D8 line does not already say —
// and the D8 line is the one carrying the repair, so it is the one that stands.
// Two framings of one fault leave an operator wondering which to act on.
func TestNotespaceListReportsADuplicatedIDOnce(t *testing.T) {
	sandboxNotebookScope(t).recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
			{Dir: "core", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo"},
		}},
		// The copied stamp of D8: one id carried by two physical roots, and
		// that id is also what [primaries] routes this subject to.
		"personal": {Stamp: fixtureNotebookB, Notespaces: []notespaceFixture{
			{Dir: "copy", ID: fixtureNotespace1, Subject: siblingSubject, Kind: "repo", Name: "core"},
		}},
	})
	writeMachineIdentity(t, map[string]string{siblingSubject: fixtureNotespace1}, nil)

	var out bytes.Buffer
	if err := runNotespaceList(&out, false); err != nil {
		t.Fatalf("notespace list: %v", err)
	}
	text := out.String()
	requireContains(t, text, "duplicate notespace id "+fixtureNotespace1+" (D8)", "the physical duplicate is reported with its repair")
	requireNotContains(t, text, "unresolvable primary", "the audit's restatement of the same fact is dropped")
	if n := strings.Count(text, "duplicate notespace id"); n != 1 {
		t.Fatalf("one duplicated id produced %d problem lines:\n%s", n, text)
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
