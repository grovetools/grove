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

// retireSandbox builds the shape --retire exists for: a notebook whose
// notespace is an empty skeleton (plan-scaffold rules only) carrying a local:
// stamp, with both machine.toml sides recorded.
func retireSandbox(t *testing.T, names ...string) (string, string) {
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
		migrateWrite(t, filepath.Join(nsRoot, "plans", "myfeat", "rules", "default.rules"), "**/*.go\n")
		machine.WriteString(quoteTOML(local) + " = " + quoteTOML(id) + "\n")
		subjects.WriteString(quoteTOML(nsRoot) + " = " + quoteTOML(local) + "\n")
	}
	migrateWrite(t, config.MachineConfigPath(), machine.String()+"\n"+subjects.String())
	config.ResetLoadCache()
	return dir, nbRoot
}

func TestRetireRemovesDirectoryAndMachineRecordsTogetherAndUndoes(t *testing.T) {
	_, nbRoot := retireSandbox(t)
	nsRoot := filepath.Join(nbRoot, "notespaces", "alpha")
	stampBefore := migrateRead(t, filepath.Join(nsRoot, ".notespace.toml"))
	machineBefore := migrateRead(t, config.MachineConfigPath())

	manifestPath := filepath.Join(t.TempDir(), "retire.json")
	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Yes: true, ManifestPath: manifestPath, Targets: []string{"nb/alpha"}}, time.Unix(500, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(nsRoot); !os.IsNotExist(err) {
		t.Fatalf("notespace directory survived retire: %v", err)
	}
	config.ResetLoadCache()
	machine := p2LoadMachine(t)
	if _, exists := machine.Primaries[resubjectTestLocal]; exists {
		t.Fatalf("[primaries] kept the retired subject: %v", machine.Primaries)
	}
	if _, exists := machine.Subjects[nsRoot]; exists {
		t.Fatalf("[subjects] kept the retired mint: %v", machine.Subjects)
	}

	out.Reset()
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Undo: true, ManifestPath: manifestPath}, time.Unix(501, 0)); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if got := migrateRead(t, filepath.Join(nsRoot, ".notespace.toml")); got != stampBefore {
		t.Fatalf("undo did not restore the stamp byte-for-byte:\nbefore: %s\nafter: %s", stampBefore, got)
	}
	if got := migrateRead(t, filepath.Join(nsRoot, "plans", "myfeat", "rules", "default.rules")); got != "**/*.go\n" {
		t.Fatalf("undo did not restore scaffold content: %q", got)
	}
	if got := migrateRead(t, config.MachineConfigPath()); got != machineBefore {
		t.Fatalf("undo did not restore machine.toml byte-for-byte:\nbefore: %s\nafter: %s", machineBefore, got)
	}
}

func TestRetireDryRunChangesNothing(t *testing.T) {
	_, nbRoot := retireSandbox(t)
	nsRoot := filepath.Join(nbRoot, "notespaces", "alpha")
	before := migrateRead(t, config.MachineConfigPath())

	manifestPath := filepath.Join(t.TempDir(), "retire.json")
	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{DryRun: true, ManifestPath: manifestPath, Targets: []string{"nb/alpha"}}, time.Unix(502, 0)); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "retire  nb/alpha") || !strings.Contains(out.String(), "changes=1") {
		t.Fatalf("dry-run plan is missing the change:\n%s", out.String())
	}
	if _, err := os.Stat(nsRoot); err != nil {
		t.Fatalf("dry-run touched the directory: %v", err)
	}
	if got := migrateRead(t, config.MachineConfigPath()); got != before {
		t.Fatalf("dry-run touched machine.toml:\nbefore: %s\nafter: %s", before, got)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a manifest: %v", err)
	}
}

func TestRetireRefusesContentWithoutAllowContent(t *testing.T) {
	_, nbRoot := retireSandbox(t)
	nsRoot := filepath.Join(nbRoot, "notespaces", "alpha")
	migrateWrite(t, filepath.Join(nsRoot, "inbox", "real-note.md"), "keep me\n")

	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Yes: true, ManifestPath: filepath.Join(t.TempDir(), "m.json"), Targets: []string{"nb/alpha"}}, time.Unix(503, 0)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "--allow-content") || !strings.Contains(out.String(), "changes=0") {
		t.Fatalf("content target was not kept and reported:\n%s", out.String())
	}
	if _, err := os.Stat(nsRoot); err != nil {
		t.Fatalf("content target was removed anyway: %v", err)
	}

	out.Reset()
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Yes: true, AllowContent: true, ManifestPath: filepath.Join(t.TempDir(), "m2.json"), Targets: []string{"nb/alpha"}}, time.Unix(504, 0)); err != nil {
		t.Fatalf("allow-content run: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(nsRoot); !os.IsNotExist(err) {
		t.Fatalf("--allow-content did not retire the target: %v", err)
	}
}

func TestRetireUnstampedResidueRemovesSubjectKeyRecords(t *testing.T) {
	dir := migrateSandbox(t)
	nbRoot := filepath.Join(t.TempDir(), "notebook")
	migrateWrite(t, filepath.Join(dir, "notebooks.toml"), "default=\"nb\"\n[notebooks.nb]\nroot="+quoteTOML(nbRoot)+"\n")
	nsRoot := filepath.Join(nbRoot, "notespaces", "grove-tend-cx-residue-123")
	migrateWrite(t, filepath.Join(nsRoot, "context", "generated", "context"), "<context/>\n")
	migrateWrite(t, config.MachineConfigPath(), "[primaries]\n\n[subjects]\n"+quoteTOML(nsRoot)+" = "+quoteTOML(resubjectTestLocal)+"\n")
	config.ResetLoadCache()

	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Yes: true, AllowContent: true, ManifestPath: filepath.Join(t.TempDir(), "m.json"), Targets: []string{"nb/grove-tend-cx-residue-123"}}, time.Unix(505, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "unstamped") {
		t.Fatalf("plan did not label the unstamped target:\n%s", out.String())
	}
	if _, err := os.Stat(nsRoot); !os.IsNotExist(err) {
		t.Fatalf("unstamped residue survived retire: %v", err)
	}
	config.ResetLoadCache()
	machine := p2LoadMachine(t)
	if _, exists := machine.Subjects[nsRoot]; exists {
		t.Fatalf("[subjects] kept the stranded path record: %v", machine.Subjects)
	}
}

func TestRetireKeepsTargetWhenPrimariesDisagreeWithStamp(t *testing.T) {
	_, nbRoot := retireSandbox(t)
	otherID := "01KZVCMCZ81FG4ZAFDK3P7M4YP"
	nsRoot := filepath.Join(nbRoot, "notespaces", "alpha")
	migrateWrite(t, config.MachineConfigPath(), "[primaries]\n"+quoteTOML(resubjectTestLocal)+" = "+quoteTOML(otherID)+"\n")
	config.ResetLoadCache()

	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Yes: true, ManifestPath: filepath.Join(t.TempDir(), "m.json"), Targets: []string{"nb/alpha"}}, time.Unix(506, 0)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "disagree") || !strings.Contains(out.String(), "changes=0") {
		t.Fatalf("disagreement was not kept and reported:\n%s", out.String())
	}
	if stamp, err := notespace.LoadNotespace(nsRoot); err != nil || stamp == nil {
		t.Fatalf("disagreeing target was removed anyway: %+v err=%v", stamp, err)
	}
}

func TestRetireWarnsWhenRegistryReferencesTarget(t *testing.T) {
	_, nbRoot := retireSandbox(t)
	migrateWrite(t, filepath.Join(nbRoot, "notespaces", "registry", "machines", "card.md"), "ecosystem uses alpha as a fixture\n")

	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{DryRun: true, ManifestPath: filepath.Join(t.TempDir(), "m.json"), Targets: []string{"nb/alpha"}}, time.Unix(507, 0)); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "WARNING") || !strings.Contains(out.String(), "registry references") {
		t.Fatalf("registry reference was not surfaced:\n%s", out.String())
	}
}

func TestRetireUndoRefusesWhenMachineConfigChangedAfterApply(t *testing.T) {
	_, _ = retireSandbox(t)
	manifestPath := filepath.Join(t.TempDir(), "retire.json")
	var out bytes.Buffer
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Yes: true, ManifestPath: manifestPath, Targets: []string{"nb/alpha"}}, time.Unix(508, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	migrateWrite(t, config.MachineConfigPath(), migrateRead(t, config.MachineConfigPath())+"\n# drifted\n")
	if err := runRetireMigration(&out, strings.NewReader(""), retireOptions{Undo: true, ManifestPath: manifestPath}, time.Unix(509, 0)); err == nil || !strings.Contains(err.Error(), "refusing undo") {
		t.Fatalf("undo did not hash-guard machine.toml drift: %v", err)
	}
}
