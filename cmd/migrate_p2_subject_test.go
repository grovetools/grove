package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
)

// p2FleetLabManifest is the exact Machine A ecosystem manifest this fix exists
// for: identity in the card, and no git remotes at the ecosystem root at all.
const p2FleetLabManifest = `name = "fleet-lab"
workspaces = ["*"]

[ecosystem]
id = "01KZME1YNY6Q0XEVTBNWQ72P1Z"
layout = "flat"

[[ecosystem.remotes]]
name = "repo-a"
url = "https://forge.matthew.solar/matt/fleet-lab-repo-a.git"

[[ecosystem.remotes]]
name = "repo-b"
url = "https://forge.matthew.solar/matt/fleet-lab-repo-b.git"

[ecosystem.notebooks.fleet-lab-notes]
default = true
`

const p2FleetLabSubject = "eco:01KZME1YNY6Q0XEVTBNWQ72P1Z"

// p2BindCodeRoot records an explicit [roots.alpha] binding for codeRoot, the
// same shape P1 leaves behind, so the notespace named alpha resolves to a real
// matched code root instead of the no-root mint path.
func p2BindCodeRoot(t *testing.T, configDir, codeRoot string) {
	t.Helper()
	migrateWrite(t, filepath.Join(configDir, "roots.toml"), "[roots.alpha]\npath = "+quoteTOML(codeRoot)+"\nnotebook = \"nb\"\n")
	config.ResetLoadCache()
}

func p2GitRoot(t *testing.T, remotes ...[2]string) string {
	t.Helper()
	dir := t.TempDir()
	p2GitInit(t, dir, remotes...)
	return dir
}

func p2GitInit(t *testing.T, dir string, remotes ...[2]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	for _, remote := range remotes {
		run("remote", "add", remote[0], remote[1])
	}
}

func p2StubCommands(t *testing.T) {
	t.Helper()
	old := p2Command
	p2Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"already_current":true}`), nil
	}
	t.Cleanup(func() { p2Command = old })
}

func p2PlanSubject(t *testing.T, nb string) string {
	t.Helper()
	manifest := p2PlanManifest(t, nb)
	if len(manifest.Notespaces) != 1 {
		t.Fatalf("plan produced %d notespaces, want 1", len(manifest.Notespaces))
	}
	return manifest.Notespaces[0].Subject
}

func p2PlanManifest(t *testing.T, nb string) *p2MigrationManifest {
	t.Helper()
	manifest, err := planP2Migration(p2MigrationOptions{DryRun: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Now())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return manifest
}

func p2LoadMachine(t *testing.T) *config.MachineConfig {
	t.Helper()
	machine, err := config.LoadMachineConfig()
	if err != nil || machine == nil {
		t.Fatalf("load machine identity: %+v err=%v", machine, err)
	}
	return machine
}

func TestP2MigrationDerivesEcosystemSubjectFromTheCard(t *testing.T) {
	dir, nb := p2Sandbox(t)
	eco := t.TempDir()
	migrateWrite(t, filepath.Join(eco, "grove.toml"), p2FleetLabManifest)
	p2BindCodeRoot(t, dir, eco)
	p2StubCommands(t)

	if got := p2PlanSubject(t, nb); got != p2FleetLabSubject {
		t.Fatalf("subject = %q, want %q", got, p2FleetLabSubject)
	}
}

func TestP2MigrationRefusesUnusableEcosystemIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
	}{
		{"malformed id", "name = \"broken\"\n\n[ecosystem]\nid = \"not-a-ulid\"\n"},
		{"un-minted card", "name = \"unminted\"\n\n[ecosystem]\nlayout = \"flat\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, nb := p2Sandbox(t)
			eco := t.TempDir()
			migrateWrite(t, filepath.Join(eco, "grove.toml"), tc.manifest)
			p2BindCodeRoot(t, dir, eco)
			p2StubCommands(t)

			var out bytes.Buffer
			err := runP2Migration(context.Background(), &out, strings.NewReader(""), p2MigrationOptions{Yes: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json"), SyncDBPath: filepath.Join(t.TempDir(), "db")}, time.Now())
			if err == nil {
				t.Fatalf("unusable ecosystem identity was migrated anyway:\n%s", out.String())
			}
			if !strings.Contains(err.Error(), "subject for alpha") {
				t.Fatalf("error does not name the failing notespace: %v", err)
			}
			// Preflight refuses before any mutation: the layout is untouched.
			if _, statErr := os.Stat(filepath.Join(nb, "notespaces")); !os.IsNotExist(statErr) {
				t.Fatalf("refused plan still moved the layout: %v", statErr)
			}
			if _, statErr := os.Stat(config.SyncConfigPath() + ".p2-staged"); statErr != nil {
				t.Fatalf("refused plan consumed the staged input: %v", statErr)
			}
		})
	}
}

func TestP2MigrationRepoRootUsesCanonicalRemoteSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		remotes [][2]string
		want    string
	}{
		{
			name:    "origin wins over other remotes",
			remotes: [][2]string{{"upstream", "https://github.com/grovetools/upstream.git"}, {"origin", "git@github.com:grovetools/core.git"}},
			want:    "github.com/grovetools/core",
		},
		{
			name:    "sole remote is the subject",
			remotes: [][2]string{{"fork", "https://forge.matthew.solar/matt/fleet-lab-repo-a.git"}},
			want:    "forge.matthew.solar/matt/fleet-lab-repo-a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, nb := p2Sandbox(t)
			p2BindCodeRoot(t, dir, p2GitRoot(t, tc.remotes...))
			p2StubCommands(t)

			if got := p2PlanSubject(t, nb); got != tc.want {
				t.Fatalf("subject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestP2MigrationTrueLocalRootMintsThenReusesTheRecord(t *testing.T) {
	dir, nb := p2Sandbox(t)
	code := p2GitRoot(t)
	p2BindCodeRoot(t, dir, code)
	p2StubCommands(t)

	minted := p2PlanSubject(t, nb)
	if !strings.HasPrefix(minted, "local:") {
		t.Fatalf("remoteless non-ecosystem root derived %q, want a local subject", minted)
	}
	if again := p2PlanSubject(t, nb); again == minted {
		t.Fatalf("a mint with nothing recorded reproduced %q; the record is what makes it stable", minted)
	}

	// Once the mint is recorded in [subjects], derivation reproduces it rather
	// than minting a second identity for the same tree.
	canonical, err := filepath.EvalSymlinks(code)
	if err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, config.MachineConfigPath(), "[subjects]\n"+quoteTOML(canonical)+" = "+quoteTOML(minted)+"\n")
	config.ResetLoadCache()
	if got := p2PlanSubject(t, nb); got != minted {
		t.Fatalf("subject = %q, want the recorded %q", got, minted)
	}
}

func TestP2MigrationEcosystemSubjectSurvivesUndoAndPinnedReapply(t *testing.T) {
	dir, nb := p2Sandbox(t)
	eco := t.TempDir()
	migrateWrite(t, filepath.Join(eco, "grove.toml"), p2FleetLabManifest)
	p2BindCodeRoot(t, dir, eco)
	p2StubCommands(t)

	manifestPath := filepath.Join(t.TempDir(), "m.json")
	opts := p2MigrationOptions{Yes: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: manifestPath, SyncDBPath: filepath.Join(t.TempDir(), "sync.db")}
	var out bytes.Buffer
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(200, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}

	var applied p2MigrationManifest
	data, _ := os.ReadFile(manifestPath)
	if err := json.Unmarshal(data, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Notespaces[0].Subject != p2FleetLabSubject {
		t.Fatalf("applied subject = %q, want %q", applied.Notespaces[0].Subject, p2FleetLabSubject)
	}
	notespaceID := applied.Notespaces[0].ID
	if machine := p2LoadMachine(t); machine.Primaries[p2FleetLabSubject] != notespaceID {
		t.Fatalf("[primaries] = %v, want %s -> %s", machine.Primaries, p2FleetLabSubject, notespaceID)
	}
	// An ecosystem identity is never a machine-local record.
	if machine := p2LoadMachine(t); machine.Subjects[eco] != "" {
		t.Fatalf("[subjects] recorded an ecosystem subject: %v", machine.Subjects)
	}

	undo := p2MigrationOptions{Undo: true, ManifestPath: manifestPath}
	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), undo, time.Unix(201, 0)); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}

	// Machine B's inheritance shape: the notespace id is pinned on the wire, and
	// the subject must come back out of the card rather than being minted anew.
	reapply := opts
	reapply.ManifestPath = filepath.Join(t.TempDir(), "reapply.json")
	reapply.NotespaceIDs = []string{"nb/alpha=" + notespaceID}
	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), reapply, time.Unix(202, 0)); err != nil {
		t.Fatalf("reapply: %v\n%s", err, out.String())
	}

	var reapplied p2MigrationManifest
	data, _ = os.ReadFile(reapply.ManifestPath)
	if err := json.Unmarshal(data, &reapplied); err != nil {
		t.Fatal(err)
	}
	if reapplied.Notespaces[0].Subject != p2FleetLabSubject || reapplied.Notespaces[0].ID != notespaceID {
		t.Fatalf("reapply drifted: subject=%q id=%q, want %q / %q", reapplied.Notespaces[0].Subject, reapplied.Notespaces[0].ID, p2FleetLabSubject, notespaceID)
	}
	machine := p2LoadMachine(t)
	if len(machine.Primaries) != 1 || machine.Primaries[p2FleetLabSubject] != notespaceID {
		t.Fatalf("[primaries] after reapply = %v, want exactly %s -> %s", machine.Primaries, p2FleetLabSubject, notespaceID)
	}

	stamp, err := os.ReadFile(filepath.Join(nb, "notespaces", "alpha", ".notespace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stamp, []byte(p2FleetLabSubject)) || !bytes.Contains(stamp, []byte(notespaceID)) {
		t.Fatalf("stamp lost the inherited identity:\n%s", stamp)
	}

	// Re-running against the consumed input is a reasoned no-op, not a new mint.
	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), reapply, time.Unix(203, 0)); err != nil || !strings.Contains(out.String(), "already consumed") {
		t.Fatalf("idempotent rerun: %v\n%s", err, out.String())
	}
}

// p2BindCollectionRoot records a root named AFTER THE COLLECTION, not the
// notespace: the ecosystem-style layout machine reports showed, where recorded
// roots point at directories of repos and notespaces are named per-repo.
func p2BindCollectionRoot(t *testing.T, configDir string, collections ...[2]string) {
	t.Helper()
	var body strings.Builder
	for _, c := range collections {
		body.WriteString("[roots." + c[0] + "]\npath = " + quoteTOML(c[1]) + "\nnotebook = \"nb\"\n")
	}
	migrateWrite(t, filepath.Join(configDir, "roots.toml"), body.String())
	config.ResetLoadCache()
}

func TestP2MigrationDerivesSubjectForRepoNestedUnderCollectionRoot(t *testing.T) {
	dir, nb := p2Sandbox(t)
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha"), [2]string{"origin", "https://github.com/broadinstitute/gnomad-browser.git"})
	p2BindCollectionRoot(t, dir, [2]string{"browsers", collection})
	p2StubCommands(t)

	if got := p2PlanSubject(t, nb); got != "github.com/broadinstitute/gnomad-browser" {
		t.Fatalf("subject = %q, want the nested repo's remote identity", got)
	}
}

func TestP2MigrationRefusesConflictingNestedIdentities(t *testing.T) {
	dir, nb := p2Sandbox(t)
	collA, collB := t.TempDir(), t.TempDir()
	p2GitInit(t, filepath.Join(collA, "alpha"), [2]string{"origin", "https://github.com/one/alpha.git"})
	p2GitInit(t, filepath.Join(collB, "alpha"), [2]string{"origin", "https://github.com/two/alpha.git"})
	p2BindCollectionRoot(t, dir, [2]string{"a", collA}, [2]string{"b", collB})
	p2StubCommands(t)

	_, err := planP2Migration(p2MigrationOptions{DryRun: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "conflicting identities") || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("conflicting nested identities were not refused: %v", err)
	}
}

func TestP2MigrationNestedDirectoryWithoutIdentityMintsKeyedByThatDirectory(t *testing.T) {
	dir, nb := p2Sandbox(t)
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha")) // repo, no remotes, no card
	p2BindCollectionRoot(t, dir, [2]string{"browsers", collection})
	p2StubCommands(t)

	manifest := p2PlanManifest(t, nb)
	ns := manifest.Notespaces[0]
	if !strings.HasPrefix(ns.Subject, "local:") {
		t.Fatalf("identityless nested repo derived %q, want a local mint", ns.Subject)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(collection, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if ns.CodeRoot != want {
		t.Fatalf("mint is keyed by %q, want the nested directory %q", ns.CodeRoot, want)
	}
}

func TestP2MigrationRefusesDuplicateDerivedSubjects(t *testing.T) {
	dir, nb := p2Sandbox(t)
	if err := os.MkdirAll(filepath.Join(nb, "workspaces", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	collection := t.TempDir()
	p2GitInit(t, filepath.Join(collection, "alpha"), [2]string{"origin", "https://github.com/one/same.git"})
	p2GitInit(t, filepath.Join(collection, "beta"), [2]string{"origin", "https://github.com/one/same.git"})
	p2BindCollectionRoot(t, dir, [2]string{"coll", collection})
	p2StubCommands(t)

	_, err := planP2Migration(p2MigrationOptions{DryRun: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "exactly one primary per subject") || !strings.Contains(err.Error(), "github.com/one/same") {
		t.Fatalf("duplicate derived subjects were not refused at plan time: %v", err)
	}
}

func TestP2MigrationConsumesExplicitEmptyStagedIntent(t *testing.T) {
	dir, nb := p2Sandbox(t)
	// A machine that never configured sync: no live sync.toml, and P1 staged
	// the explicit empty intent instead of an exact copy.
	if err := os.Remove(filepath.Join(dir, "sync.toml")); err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, filepath.Join(dir, "sync.toml.p2-staged"), emptySyncIntentStage)
	config.ResetLoadCache()
	p2StubCommands(t)

	var out bytes.Buffer
	opts := p2MigrationOptions{Yes: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json"), SyncDBPath: filepath.Join(t.TempDir(), "db")}
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(300, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "sync_bindings=0") {
		t.Fatalf("empty intent compiled bindings:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "sync.toml.p2-staged")); !os.IsNotExist(err) {
		t.Fatalf("staged empty intent was not consumed: %v", err)
	}
	settled, err := config.LoadSyncConfigFrom(config.SyncConfigPath())
	if err != nil {
		t.Fatalf("compiled sync config unreadable: %v", err)
	}
	if settled != nil && (settled.Server != "" || len(settled.Workspaces) != 0) {
		t.Fatalf("empty intent compiled to non-empty sync config: %+v", settled)
	}
}
