package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/notespace"
)

const (
	resubjectTestID    = "01KZVCMCZ1W2PTT7028GYT0T7Y"
	resubjectTestLocal = "local:01KZVCMCZ136SJZKKR2N7NA9KD"
)

// resubjectSandbox builds machine A's post-P2 shape: a migrated notebook whose
// notespace carries a local: stamp, [primaries] recording the mint, and a
// [subjects] record keyed by the notespace path.
func resubjectSandbox(t *testing.T, names ...string) (string, string) {
	t.Helper()
	if len(names) == 0 {
		names = []string{"alpha"}
	}
	dir := migrateSandbox(t)
	nbRoot := filepath.Join(t.TempDir(), "notebook")
	migrateWrite(t, filepath.Join(dir, "notebooks.toml"), "default=\"nb\"\n[notebooks.nb]\nroot="+quoteTOML(nbRoot)+"\n")
	var machine strings.Builder
	machine.WriteString("[primaries]\n")
	var subjects strings.Builder
	subjects.WriteString("[subjects]\n")
	for i, name := range names {
		nsRoot := filepath.Join(nbRoot, "notespaces", name)
		id, local := resubjectTestID, resubjectTestLocal
		if i > 0 {
			id = id[:len(id)-1] + "X"
			local = local[:len(local)-1] + "X"
		}
		migrateWrite(t, filepath.Join(nsRoot, ".notespace.toml"), "id = "+quoteTOML(id)+"\nname = "+quoteTOML(name)+"\nsubject = "+quoteTOML(local)+"\nkind = \"notes\"\n")
		machine.WriteString(quoteTOML(local) + " = " + quoteTOML(id) + "\n")
		subjects.WriteString(quoteTOML(nsRoot) + " = " + quoteTOML(local) + "\n")
	}
	migrateWrite(t, config.MachineConfigPath(), machine.String()+"\n"+subjects.String())
	config.ResetLoadCache()
	return dir, nbRoot
}

func TestResubjectMovesStampAndMachineRecordsTogetherAndUndoes(t *testing.T) {
	dir, nbRoot := resubjectSandbox(t)
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha"), [2]string{"origin", "https://github.com/one/alpha.git"})
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "[roots.coll]\npath="+quoteTOML(collection)+"\nnotebook=\"nb\"\n")
	config.ResetLoadCache()

	manifestPath := filepath.Join(t.TempDir(), "resubject.json")
	var out bytes.Buffer
	if err := runResubjectMigration(&out, strings.NewReader(""), resubjectOptions{Yes: true, ManifestPath: manifestPath}, time.Unix(400, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	nsRoot := filepath.Join(nbRoot, "notespaces", "alpha")
	stamp, err := notespace.LoadNotespace(nsRoot)
	if err != nil || stamp == nil {
		t.Fatalf("stamp: %+v err=%v", stamp, err)
	}
	if stamp.ID != resubjectTestID || stamp.Subject != "github.com/one/alpha" {
		t.Fatalf("stamp id=%q subject=%q, want preserved id and derived subject", stamp.ID, stamp.Subject)
	}
	config.ResetLoadCache()
	machine := p2LoadMachine(t)
	if machine.Primaries["github.com/one/alpha"] != resubjectTestID {
		t.Fatalf("[primaries] = %v, want github.com/one/alpha -> %s", machine.Primaries, resubjectTestID)
	}
	if _, exists := machine.Primaries[resubjectTestLocal]; exists {
		t.Fatalf("[primaries] kept the retired local subject: %v", machine.Primaries)
	}
	if _, exists := machine.Subjects[nsRoot]; exists {
		t.Fatalf("[subjects] = %v, want the retired mint record deleted (that table records local identities only)", machine.Subjects)
	}

	out.Reset()
	if err := runResubjectMigration(&out, strings.NewReader(""), resubjectOptions{Undo: true, ManifestPath: manifestPath}, time.Unix(401, 0)); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	stamp, err = notespace.LoadNotespace(nsRoot)
	if err != nil || stamp == nil || stamp.Subject != resubjectTestLocal || stamp.ID != resubjectTestID {
		t.Fatalf("undo did not restore the stamp: %+v err=%v", stamp, err)
	}
	config.ResetLoadCache()
	machine = p2LoadMachine(t)
	if machine.Primaries[resubjectTestLocal] != resubjectTestID || machine.Subjects[nsRoot] != resubjectTestLocal {
		t.Fatalf("undo did not restore machine records: primaries=%v subjects=%v", machine.Primaries, machine.Subjects)
	}
}

func TestResubjectDryRunChangesNothing(t *testing.T) {
	dir, nbRoot := resubjectSandbox(t)
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha"), [2]string{"origin", "https://github.com/one/alpha.git"})
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "[roots.coll]\npath="+quoteTOML(collection)+"\nnotebook=\"nb\"\n")
	config.ResetLoadCache()

	before := migrateRead(t, config.MachineConfigPath())
	manifestPath := filepath.Join(t.TempDir(), "resubject.json")
	var out bytes.Buffer
	if err := runResubjectMigration(&out, strings.NewReader(""), resubjectOptions{DryRun: true, ManifestPath: manifestPath}, time.Unix(402, 0)); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "resubject  nb/alpha") || !strings.Contains(out.String(), "github.com/one/alpha") {
		t.Fatalf("dry-run plan is missing the change:\n%s", out.String())
	}
	stamp, err := notespace.LoadNotespace(filepath.Join(nbRoot, "notespaces", "alpha"))
	if err != nil || stamp == nil || stamp.Subject != resubjectTestLocal {
		t.Fatalf("dry-run touched the stamp: %+v err=%v", stamp, err)
	}
	if got := migrateRead(t, config.MachineConfigPath()); got != before {
		t.Fatalf("dry-run touched machine.toml:\nbefore: %s\nafter: %s", before, got)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a manifest: %v", err)
	}
}

func TestResubjectKeepsNotespaceWhenDerivedSubjectHasAnotherPrimary(t *testing.T) {
	dir, nbRoot := resubjectSandbox(t)
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha"), [2]string{"origin", "https://github.com/one/alpha.git"})
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "[roots.coll]\npath="+quoteTOML(collection)+"\nnotebook=\"nb\"\n")
	otherID := "01KZVCMCZ81FG4ZAFDK3P7M4YP"
	migrateWrite(t, config.MachineConfigPath(), "[primaries]\n"+quoteTOML(resubjectTestLocal)+" = "+quoteTOML(resubjectTestID)+"\n\"github.com/one/alpha\" = "+quoteTOML(otherID)+"\n")
	config.ResetLoadCache()

	var out bytes.Buffer
	if err := runResubjectMigration(&out, strings.NewReader(""), resubjectOptions{Yes: true, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Unix(403, 0)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "already has primary") || !strings.Contains(out.String(), "changes=0") {
		t.Fatalf("conflict was not kept and reported:\n%s", out.String())
	}
	stamp, err := notespace.LoadNotespace(filepath.Join(nbRoot, "notespaces", "alpha"))
	if err != nil || stamp == nil || stamp.Subject != resubjectTestLocal {
		t.Fatalf("conflicting notespace was rewritten anyway: %+v err=%v", stamp, err)
	}
}

func TestResubjectKeepsEveryClaimantWhenSeveralDeriveTheSameSubject(t *testing.T) {
	dir, nbRoot := resubjectSandbox(t, "alpha", "beta")
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha"), [2]string{"origin", "https://github.com/one/same.git"})
	p2GitInit(t, filepath.Join(collection, "beta"), [2]string{"origin", "https://github.com/one/same.git"})
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "[roots.coll]\npath="+quoteTOML(collection)+"\nnotebook=\"nb\"\n")
	config.ResetLoadCache()

	var out bytes.Buffer
	if err := runResubjectMigration(&out, strings.NewReader(""), resubjectOptions{Yes: true, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Unix(404, 0)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "exactly one notespace may answer for it") || !strings.Contains(out.String(), "changes=0") {
		t.Fatalf("duplicate claimants were not kept and reported:\n%s", out.String())
	}
	for _, name := range []string{"alpha", "beta"} {
		stamp, err := notespace.LoadNotespace(filepath.Join(nbRoot, "notespaces", name))
		if err != nil || stamp == nil || !strings.HasPrefix(stamp.Subject, "local:") {
			t.Fatalf("claimant %s was rewritten anyway: %+v err=%v", name, stamp, err)
		}
	}
}

func TestResubjectLeavesGenuinelyLocalNotespacesAlone(t *testing.T) {
	dir, nbRoot := resubjectSandbox(t)
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "")
	config.ResetLoadCache()

	var out bytes.Buffer
	if err := runResubjectMigration(&out, strings.NewReader(""), resubjectOptions{Yes: true, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Unix(405, 0)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "nothing to change") {
		t.Fatalf("no-op was not reported as such:\n%s", out.String())
	}
	stamp, err := notespace.LoadNotespace(filepath.Join(nbRoot, "notespaces", "alpha"))
	if err != nil || stamp == nil || stamp.Subject != resubjectTestLocal {
		t.Fatalf("unrooted notespace was rewritten: %+v err=%v", stamp, err)
	}
}
