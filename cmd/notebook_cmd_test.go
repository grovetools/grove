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

// TestNotebookShareRefusalSaysWhatItLeftBehind pins what a rejected share is
// allowed to claim. notebooks.toml is never written — that is the whole point
// of writing it last — but "nothing was recorded locally" is a sentence about
// the DISK, and minting runs before the server is asked. So the refusal states
// whichever of the two is true, and the identity work that had already landed
// is named rather than denied.
func TestNotebookShareRefusalSaysWhatItLeftBehind(t *testing.T) {
	newRefusedShare := func(t *testing.T) (scopeSandbox, *fakeSync) {
		t.Helper()
		box := sandboxNotebookScope(t)
		server := newFakeSync(t)
		// The notespace already belongs to another notebook on the server,
		// which is a move, not a share — the server refuses the whole request.
		server.addNotebook(fixtureNotebookB, "other", "shared", 1)
		server.addNotespace(fixtureNotespace1, "alpha", fixtureNotebookB, 1, 0)
		box.recordNotebooks(t, "research", map[string]notebookFixture{
			"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
				{Dir: "alpha", ID: fixtureNotespace1, Subject: "local:" + fixtureNotespace1},
			}},
		})
		box.recordSyncServer(t, server.URL)
		return box, server
	}

	t.Run("nothing was written", func(t *testing.T) {
		box, _ := newRefusedShare(t)
		// The identity is already recorded, so this run writes nothing at all
		// before the server refuses.
		writeMachineIdentity(t, map[string]string{"local:" + fixtureNotespace1: fixtureNotespace1},
			map[string]string{canonicalPath(box.notespaceRoot("research", "alpha")): "local:" + fixtureNotespace1})

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
	})

	t.Run("identity records were written first", func(t *testing.T) {
		box, _ := newRefusedShare(t)

		err := runNotebookShare(context.Background(), &bytes.Buffer{}, "research", false)
		if err == nil {
			t.Fatal("share succeeded although the server rejected a member")
		}
		requireContains(t, err.Error(), "notebooks.toml records no share", "the write that comes last did not happen")
		requireContains(t, err.Error(), "machine.toml identity record", "and the writes that come first are named")
		requireNotContains(t, err.Error(), "nothing was recorded locally", "a refusal must not deny writes it made")
		if strings.Contains(box.readNotebooksTOML(t), "share = true") {
			t.Fatalf("a rejected share was recorded in notebooks.toml:\n%s", box.readNotebooksTOML(t))
		}
		// The records it did write are the ones it says it wrote.
		machineCfg, cfgErr := config.LoadMachineConfig()
		if cfgErr != nil || machineCfg == nil {
			t.Fatalf("machine.toml: %v", cfgErr)
		}
		if machineCfg.Primaries["local:"+fixtureNotespace1] != fixtureNotespace1 {
			t.Fatalf("[primaries] was not recorded: %+v", machineCfg.Primaries)
		}
	})
}

// TestNotebookShareRecordsIdentityForAlreadyStampedNotespaces closes the hole a
// mint-only transaction left. A stamp is written to disk before the machine.toml
// record, so a failure between the two produced an id that exists forever and a
// [primaries] entry nothing would ever write: the next run sees the stamp and
// mints nothing. Share therefore records what is ABSENT rather than only what it
// just minted, and converges on a re-run.
func TestNotebookShareRecordsIdentityForAlreadyStampedNotespaces(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	box.recordNotebooks(t, "research", map[string]notebookFixture{
		"research": {Stamp: fixtureNotebookA, Notespaces: []notespaceFixture{
			{Dir: "alpha", ID: fixtureNotespace1, Subject: "local:" + fixtureNotespace1},
		}},
	})
	box.recordSyncServer(t, server.URL)
	// machine.toml already records a DIFFERENT notespace, so the file exists and
	// the missing entry is a hole rather than an empty file.
	writeMachineIdentity(t, map[string]string{"local:" + fixtureNotespace2: fixtureNotespace2}, map[string]string{})

	var out bytes.Buffer
	if err := runNotebookShare(context.Background(), &out, "research", false); err != nil {
		t.Fatalf("notebook share: %v", err)
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil || machineCfg == nil {
		t.Fatalf("machine.toml: %v", err)
	}
	if machineCfg.Primaries["local:"+fixtureNotespace1] != fixtureNotespace1 {
		t.Fatalf("[primaries] does not record the stamped notespace: %+v", machineCfg.Primaries)
	}
	if machineCfg.Subjects[canonicalPath(box.notespaceRoot("research", "alpha"))] != "local:"+fixtureNotespace1 {
		t.Fatalf("[subjects] does not record the stamped notespace path: %+v", machineCfg.Subjects)
	}
	if machineCfg.Primaries["local:"+fixtureNotespace2] != fixtureNotespace2 {
		t.Fatalf("share disturbed an unrelated [primaries] entry: %+v", machineCfg.Primaries)
	}
	requireContains(t, out.String(), `"notespaces-minted": 0`, "nothing was minted; the record was repaired")
	requireContains(t, out.String(), `"machine-identity-records-written": 2`, "the repair is counted as evidence")

	// Converged: a second share writes nothing further.
	before := machineCfg.Primaries["local:"+fixtureNotespace1]
	if err := runNotebookShare(context.Background(), &bytes.Buffer{}, "research", false); err != nil {
		t.Fatalf("second share: %v", err)
	}
	after, cfgErr := config.LoadMachineConfig()
	if cfgErr != nil || after == nil || after.Primaries["local:"+fixtureNotespace1] != before {
		t.Fatalf("a second share changed a recorded primary: %v %+v", cfgErr, after)
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
	requireContains(t, got, `"notespaces-bound": 1`, "the members it gave a local identity")
	requireContains(t, got, `transition: "notebook pull"`, "transition evidence")

	// The awaited notespace is BOUND — an empty directory carrying the server's
	// stamp — because containment is what syncs it and containment recognizes a
	// notespace by its stamp. Delivery is still the daemon's.
	betaRoot := box.notespaceRoot("team", "beta")
	betaStamp, err := notespace.LoadNotespace(betaRoot)
	if err != nil || betaStamp == nil || betaStamp.ID != fixtureNotespace2 {
		t.Fatalf("pull did not bind the awaited notespace to the server's id: %v %+v", err, betaStamp)
	}
	if betaStamp.Subject != "local:"+fixtureNotespace2 || betaStamp.Kind != "notes" {
		t.Fatalf("the bound stamp did not carry the server's subject and kind: %+v", betaStamp)
	}
	requireContains(t, got, "bound        "+fixtureNotespace2, "the binding is printed per member")
	entries, err := os.ReadDir(betaRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".notespace.toml" {
			t.Fatalf("pull wrote %q into a bound notespace; documents are the daemon's", entry.Name())
		}
	}
	// The identity is RECORDED, not only stamped: a stamp with no [subjects]
	// entry is a notespace nothing resolves by identity.
	machine, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if machine.Primaries["local:"+fixtureNotespace2] != fixtureNotespace2 {
		t.Fatalf("pull bound a notespace without recording its primary: %+v", machine.Primaries)
	}
}

// Binding is the one act in pull that could CREATE the duplicate-stamp
// condition its own preflight refuses to act on, so a member whose id is
// already stamped on this machine is reported and skipped.
func TestNotebookPullWillNotBindADuplicateNotespaceID(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "shared", 4)
	server.addNotespace(fixtureNotespace2, "beta", fixtureNotebookC, 1, 3)
	// The same id is already stamped in another recorded notebook.
	box.recordNotebooks(t, "team", map[string]notebookFixture{
		"team":     {},
		"personal": {Notespaces: []notespaceFixture{{Dir: "beta-elsewhere", ID: fixtureNotespace2}}},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotebookPull(context.Background(), &out, "team", false); err != nil {
		t.Fatalf("notebook pull: %v", err)
	}
	got := out.String()
	requireContains(t, got, "not bound    "+fixtureNotespace2, "the skip is reported per member")
	requireContains(t, got, "would record one notespace id twice", "the skip says why")
	requireContains(t, got, `"notespaces-not-bound": 1`, "evidence counts the skip")
	if _, statErr := os.Stat(box.notespaceRoot("team", "beta")); statErr == nil {
		t.Fatal("pull bound an id this machine already stamps elsewhere")
	}
}

// A directory already sitting at that name under a different id is left
// exactly as it is: a stamp is immutable, and the operator has notes there.
func TestNotebookPullWillNotReKeyAnExistingNotespace(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookC, "team", "shared", 4)
	server.addNotespace(fixtureNotespace2, "beta", fixtureNotebookC, 1, 3)
	box.recordNotebooks(t, "team", map[string]notebookFixture{
		"team": {Notespaces: []notespaceFixture{{Dir: "beta", ID: fixtureNotespace3}}},
	})
	box.recordSyncServer(t, server.URL)

	var out bytes.Buffer
	if err := runNotebookPull(context.Background(), &out, "team", false); err != nil {
		t.Fatalf("notebook pull: %v", err)
	}
	requireContains(t, out.String(), "a stamp is immutable", "the skip says why")
	stamp, err := notespace.LoadNotespace(box.notespaceRoot("team", "beta"))
	if err != nil || stamp == nil || stamp.ID != fixtureNotespace3 {
		t.Fatalf("pull re-keyed an existing notespace: %v %+v", err, stamp)
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

// TestNotebookPullRefusesToMintADuplicateNotebookID closes the one way this
// package could CREATE the state it refuses everywhere else. Binding installs a
// server-supplied id into an unstamped root; doing that while another recorded
// root already carries that id would record one notebook id twice (D8), and the
// verb that produced it would be the same one whose preflight rejects it.
func TestNotebookPullRefusesToMintADuplicateNotebookID(t *testing.T) {
	box := sandboxNotebookScope(t)
	server := newFakeSync(t)
	server.addNotebook(fixtureNotebookA, "team", "shared", 1)
	box.recordNotebooks(t, "team", map[string]notebookFixture{
		// `mirror` already holds the id the server would hand `team`.
		"mirror": {Stamp: fixtureNotebookA},
		"team":   {},
	})
	box.recordSyncServer(t, server.URL)

	err := runNotebookPull(context.Background(), &bytes.Buffer{}, "team", false)
	if err == nil {
		t.Fatal("pull bound an id this machine already records elsewhere")
	}
	requireContains(t, err.Error(), "would record one notebook id twice", "the refusal names the duplicate it would create")
	requireContains(t, err.Error(), filepath.Join(box.notebooks, "mirror"), "and where the id already lives")
	if stamp, _ := notespace.LoadNotebook(filepath.Join(box.notebooks, "team")); stamp != nil {
		t.Fatalf("the refused pull stamped the root anyway: %+v", stamp)
	}
	if strings.Contains(box.readNotebooksTOML(t), "share = true") {
		t.Fatalf("a refused pull recorded a share:\n%s", box.readNotebooksTOML(t))
	}
}
