package notescope

import (
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
)

// local builds a scanned notebook without touching a filesystem: these tests
// are about the row model, and a delta is a pure function of two descriptions.
func local(name, id string, shared, exists bool, notespaces ...string) Notebook {
	nb := Notebook{Name: name, Declared: "~/nb/" + name, Root: "/tmp/nb/" + name, Exists: exists, Shared: shared, SyncRecorded: shared}
	if id != "" {
		nb.Stamp = &notespace.NotebookStamp{ID: id, Name: name}
	}
	for _, ns := range notespaces {
		nb.Notespaces = append(nb.Notespaces, Notespace{Dir: ns, Root: nb.Root + "/notespaces/" + ns, Stamp: &notespace.NotespaceStamp{ID: ns, Name: ns}})
	}
	return nb
}

func serverNotebook(id, name, state string, members ...string) syncproto.InventoryNotebook {
	nb := syncproto.InventoryNotebook{ID: syncproto.NotebookID(id), Name: name, ShareState: state, Version: 3}
	for _, m := range members {
		nb.NotespaceIDs = append(nb.NotespaceIDs, syncproto.NotespaceID(m))
	}
	return nb
}

func findRow(rows []JoinRow, name string) (JoinRow, bool) {
	for _, row := range rows {
		if row.Kind == JoinRowNotebook && row.Name == name {
			return row, true
		}
	}
	return JoinRow{}, false
}

// The two directions each get exactly one key, and a notebook present on both
// sides gets none: share and pull are the notebook-grained acts, and a
// membership difference is a move, not a notebook act.
func TestJoinRowsBindOneKeyPerDirection(t *testing.T) {
	scanned := []Notebook{
		local("here-only", "NB-HERE", false, true, "NS-A"),
		local("both", "NB-BOTH", true, true, "NS-B"),
	}
	inventory := syncproto.InventoryResponse{Notebooks: []syncproto.InventoryNotebook{
		serverNotebook("NB-BOTH", "both", syncproto.NotebookShareStateShared, "NS-B", "NS-C"),
		serverNotebook("NB-THERE", "server-only", syncproto.NotebookShareStateShared, "NS-D"),
	}}

	rows := BuildJoinRows(Delta(scanned, inventory), scanned, nil)

	hereOnly, ok := findRow(rows, "here-only")
	if !ok {
		t.Fatal("a notebook recorded here but absent from the server produced no row")
	}
	if hereOnly.Action != JoinActionShare {
		t.Errorf("here-only action = %q, want %q", hereOnly.Action, JoinActionShare)
	}

	serverOnly, ok := findRow(rows, "server-only")
	if !ok {
		t.Fatal("a notebook the server holds and this machine does not produced no row")
	}
	if serverOnly.Action != JoinActionPull {
		t.Errorf("server-only action = %q, want %q", serverOnly.Action, JoinActionPull)
	}
	// Pull materializes into a RECORDED root. This machine records no notebook
	// of that name, and the row says so before the operator presses anything.
	if !strings.Contains(serverOnly.Reason, "records no notebook") {
		t.Errorf("server-only reason = %q, want it to name the missing recorded root", serverOnly.Reason)
	}

	both, ok := findRow(rows, "both")
	if !ok {
		t.Fatal("a notebook on both sides produced no row")
	}
	if both.Action != "" {
		t.Errorf("a notebook on both sides bound key %q; membership differences are `notespace move`", both.Action)
	}
	if !strings.Contains(both.Summary, "here only") || !strings.Contains(both.Summary, "there only") {
		t.Errorf("both-sides summary lost the membership legs: %q", both.Summary)
	}
	if !both.Expandable {
		t.Error("a notebook with a membership difference is not expandable to notespace grain")
	}
}

// D9: a notebook the server unshared is retained, not offered. No key acts on
// it, and the row states the reason rather than leaving a dead key.
func TestUnsharedServerNotebookOffersNoPull(t *testing.T) {
	inventory := syncproto.InventoryResponse{Notebooks: []syncproto.InventoryNotebook{
		serverNotebook("NB-GONE", "retired", syncproto.NotebookShareStateUnshared, "NS-X"),
	}}
	rows := BuildJoinRows(Delta(nil, inventory), nil, nil)
	row, ok := findRow(rows, "retired")
	if !ok {
		t.Fatal("an unshared server notebook was hidden from the comparison")
	}
	if row.Action != "" {
		t.Errorf("an unshared notebook bound %q; it is retained, not on offer", row.Action)
	}
	if !strings.Contains(row.Reason, "D9") {
		t.Errorf("reason = %q, want D9's retention statement", row.Reason)
	}
}

// D8: a duplicate notebook id refuses every acting verb, so the row must not
// advertise a key that is guaranteed to be refused — and the duplicate itself
// stays visible, because this is where an operator meets it.
func TestDuplicateNotebookIDBlocksActionAndStaysVisible(t *testing.T) {
	scanned := []Notebook{
		local("first", "NB-DUP", true, true, "NS-A"),
		local("second", "NB-DUP", false, true, "NS-B"),
	}
	rows := BuildJoinRows(Delta(scanned, syncproto.InventoryResponse{}), scanned, nil)
	// Both claiming names are on the row: which copy to re-mint is the
	// operator's decision, and it cannot be made from one of the two.
	row, ok := findRow(rows, "first, second")
	if !ok {
		t.Fatalf("the duplicated notebook produced no row naming both copies: %#v", rows)
	}
	if row.Action != "" {
		t.Errorf("a duplicate-id notebook bound %q", row.Action)
	}
	if !strings.Contains(row.Reason, "D8") {
		t.Errorf("reason = %q, want it to name the duplicate rule", row.Reason)
	}
	var notice bool
	for _, r := range rows {
		if r.Kind == JoinRowNotice && strings.Contains(r.Text, "duplicate notebook id") {
			notice = true
		}
	}
	if !notice {
		t.Error("the duplicate was not surfaced as a notice")
	}
}

// Expansion is what turns a notebook row into notespace grain, in both
// directions, and it happens only for the ids the caller says are expanded.
func TestJoinRowsExpandToNotespaceGrain(t *testing.T) {
	scanned := []Notebook{local("both", "NB", true, true, "NS-HERE")}
	inventory := syncproto.InventoryResponse{Notebooks: []syncproto.InventoryNotebook{
		serverNotebook("NB", "both", syncproto.NotebookShareStateShared, "NS-THERE"),
	}}
	delta := Delta(scanned, inventory)

	collapsed := BuildJoinRows(delta, scanned, nil)
	for _, row := range collapsed {
		if row.Kind == JoinRowNotespace {
			t.Fatalf("a collapsed notebook rendered notespace rows: %#v", row)
		}
	}

	expanded := BuildJoinRows(delta, scanned, map[string]bool{"NB": true})
	var here, there bool
	for _, row := range expanded {
		if row.Kind != JoinRowNotespace {
			continue
		}
		here = here || strings.Contains(row.Text, "here only") && strings.Contains(row.Text, "NS-HERE")
		there = there || strings.Contains(row.Text, "server only") && strings.Contains(row.Text, "NS-THERE")
	}
	if !here || !there {
		t.Errorf("expansion lost a direction: here=%v there=%v rows=%#v", here, there, expanded)
	}
}

// A notespace the server parents to nothing is a real state; the page groups
// it rather than dropping it.
func TestUnparentedServerNotespacesAreGrouped(t *testing.T) {
	inventory := syncproto.InventoryResponse{Notespaces: []syncproto.InventoryNotespace{
		{ID: "NS-ORPHAN", Name: "orphan", Subject: "local:x", Kind: "notes"},
	}}
	rows := BuildJoinRows(Delta(nil, inventory), nil, map[string]bool{"\x00unparented": true})
	var header, child bool
	for _, row := range rows {
		switch row.Kind {
		case JoinRowUnparented:
			header = strings.Contains(row.Text, "unparented on the server: 1 notespace")
		case JoinRowNotespace:
			child = row.ID == "NS-ORPHAN"
		}
	}
	if !header || !child {
		t.Errorf("unparented group header=%v child=%v rows=%#v", header, child, rows)
	}
}

// An unstamped notebook cannot appear in a comparison keyed by id — so the
// page says that out loud instead of letting it look absent from the server.
func TestUnstampedAndMissingRootsBecomeNotices(t *testing.T) {
	scanned := []Notebook{
		local("bare", "", false, true),
		local("gone", "NB-GONE", false, false),
	}
	rows := BuildJoinRows(Delta(scanned, syncproto.InventoryResponse{}), scanned, nil)
	var unstamped, missing bool
	for _, row := range rows {
		if row.Kind != JoinRowNotice {
			continue
		}
		unstamped = unstamped || strings.Contains(row.Text, "unstamped")
		missing = missing || strings.Contains(row.Text, "does not exist")
	}
	if !unstamped {
		t.Error("an unstamped recorded notebook was silently absent from the comparison")
	}
	if !missing {
		t.Error("a recorded root that is not there produced no notice")
	}
}
