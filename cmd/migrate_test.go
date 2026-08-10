package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/grove/pkg/setup"
)

func migrateSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir := filepath.Join(home, "config", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return dir
}

func migrateWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func migrateRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateFrozenParserPrecedenceYAMLSearchPathsAndSymlink(t *testing.T) {
	dir := migrateSandbox(t)
	real := filepath.Join(t.TempDir(), "real-grove.toml")
	migrateWrite(t, real, "[notebooks.definitions.nb]\nroot_dir = \"/notes/nb\"\n[notebooks.rules]\ndefault = \"nb\"\n[groves.same]\npath = \"/global\"\nnotebook = \"nb\"\n")
	if err := os.Symlink(real, filepath.Join(dir, "grove.toml")); err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, filepath.Join(dir, "20-low.toml"), "[_grove]\npriority=20\n[groves.same]\npath=\"/low\"\nnotebook=\"nb\"\n")
	migrateWrite(t, filepath.Join(dir, "80-high.toml"), "[_grove]\npriority=80\n[groves.same]\npath=\"/high\"\nnotebook=\"nb\"\n")
	migrateWrite(t, filepath.Join(dir, "grove.override.yml"), "search_paths:\n  yaml_root:\n    path: /yaml\n    enabled: true\n")
	m, err := collectLegacyMigration()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Roots["same"].Root.Path; got != "/high" {
		t.Fatalf("same path=%q", got)
	}
	if got := m.Roots["yaml_root"].Root.Path; got != "/yaml" || !m.Roots["yaml_root"].Root.Scan {
		t.Fatalf("yaml root=%+v", m.Roots["yaml_root"])
	}
	wantResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	foundSymlink := false
	for _, s := range m.Sources {
		if s.Path == filepath.Join(dir, "grove.toml") && s.Resolved == wantResolved {
			foundSymlink = true
		}
	}
	if !foundSymlink {
		t.Fatal("symlink source was not attributed to its resolved write target")
	}
	var out bytes.Buffer
	if err := runLegacyMigrate(&out, strings.NewReader(""), true, false, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fragment priority 80") || !strings.Contains(out.String(), "--- ") {
		t.Fatalf("missing deterministic source/diff output:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "roots.toml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote roots: %v", err)
	}
	if !strings.Contains(migrateRead(t, real), "[groves.same]") {
		t.Fatal("dry-run changed symlink target")
	}
}

func TestMigrateMachineTablesCardDefaultApplyBackupAndIdempotence(t *testing.T) {
	dir := migrateSandbox(t)
	eco := filepath.Join(t.TempDir(), "eco")
	migrateWrite(t, filepath.Join(eco, "grove.toml"), "[ecosystem.notebooks.cardnotes]\ndefault=true\n")
	migrateWrite(t, filepath.Join(dir, "grove.toml"), "name=\"keep\"\n[notebooks.definitions.cardnotes]\nroot_dir=\"/notes/card\"\n[notebooks.definitions.nb]\nroot_dir=\"/notes/nb\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.scan]\npath=\"/code\"\nnotebook=\"nb\"\ndepth=2\ninclude_repos=[\"a\"]\n")
	migrateWrite(t, filepath.Join(dir, "machine.toml"), "[machine]\nname=\"laptop\"\n[machine.ecosystems.eco]\npath="+quoteTOML(eco)+"\n[machine.roots.bare]\npath=\"/bare\"\nnotebook=\"nb\"\nexclude=[\"tmp\"]\n")
	stamp := time.Date(2026, 8, 10, 12, 34, 56, 0, time.UTC)
	var out bytes.Buffer
	if err := runLegacyMigrate(&out, strings.NewReader(""), false, true, stamp); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out.String())
	}
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	if r := table.Roots["eco"]; r.Scan || r.Notebook != "cardnotes" {
		t.Fatalf("ecosystem=%+v", r)
	}
	if r := table.Roots["scan"]; !r.Scan || r.Depth == nil || *r.Depth != 2 || len(r.Repos) != 1 {
		t.Fatalf("scan=%+v", r)
	}
	if r := table.Roots["bare"]; !r.Scan || len(r.Exclude) != 1 {
		t.Fatalf("bare=%+v", r)
	}
	if got := migrateRead(t, filepath.Join(dir, "machine.toml")); !strings.Contains(got, "name=\"laptop\"") || strings.Contains(got, "machine.ecosystems") || strings.Contains(got, "machine.roots") {
		t.Fatalf("machine edit not surgical:\n%s", got)
	}
	if got := migrateRead(t, filepath.Join(dir, "grove.toml")); !strings.Contains(got, "name=\"keep\"") || strings.Contains(got, "[groves.scan]") {
		t.Fatalf("global edit not surgical:\n%s", got)
	}
	card := migrateRead(t, filepath.Join(eco, "grove.toml"))
	if !strings.Contains(card, cardDeprecationComment) || !strings.Contains(card, "[ecosystem.notebooks.cardnotes]") {
		t.Fatalf("card default was not retained and annotated:\n%s", card)
	}
	if !strings.Contains(out.String(), filepath.Join(eco, "grove.toml")) || !strings.Contains(out.String(), cardDeprecationComment) {
		t.Fatalf("card source/diff missing from migration plan:\n%s", out.String())
	}
	for _, p := range []string{filepath.Join(dir, "grove.toml"), filepath.Join(dir, "machine.toml"), filepath.Join(eco, "grove.toml")} {
		if _, err := os.Stat(p + ".20260810T123456Z.bak"); err != nil {
			t.Fatalf("backup %s: %v", p, err)
		}
	}
	rootsBefore := migrateRead(t, filepath.Join(dir, "roots.toml"))
	var second bytes.Buffer
	if err := runLegacyMigrate(&second, strings.NewReader(""), false, true, stamp.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if migrateRead(t, filepath.Join(dir, "roots.toml")) != rootsBefore {
		t.Fatal("second run changed roots.toml")
	}
	if !strings.Contains(second.String(), "already migrated") {
		t.Fatalf("second run output:\n%s", second.String())
	}
}

func TestMigrateImportsDeadMachineYAML(t *testing.T) {
	dir := migrateSandbox(t)
	migrateWrite(t, filepath.Join(dir, "machines", "old.yml"), "notebooks:\n  definitions:\n    nb:\n      root_dir: /notes\n  rules:\n    default: nb\ngroves:\n  old:\n    path: /old-code\n    notebook: nb\n")
	var out bytes.Buffer
	if err := runLegacyMigrate(&out, strings.NewReader(""), false, true, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	table, err := coderoot.Load()
	if err != nil {
		t.Fatal(err)
	}
	if table.Roots["old"].Path != "/old-code" {
		t.Fatalf("roots=%+v", table.Roots)
	}
	if strings.Contains(migrateRead(t, filepath.Join(dir, "machines", "old.yml")), "groves:") {
		t.Fatal("dead YAML grove declaration survived")
	}
}

func TestMigrateRefusesUnresolvedNotebookWithoutMutation(t *testing.T) {
	dir := migrateSandbox(t)
	p := filepath.Join(dir, "grove.toml")
	migrateWrite(t, p, "[groves.x]\npath=\"/x\"\nnotebook=\"missing\"\n")
	var out bytes.Buffer
	err := runLegacyMigrate(&out, strings.NewReader(""), false, true, time.Now())
	if err == nil || !strings.Contains(err.Error(), "refusing to infer a path") {
		t.Fatalf("err=%v", err)
	}
	if got := migrateRead(t, p); !strings.Contains(got, "[groves.x]") {
		t.Fatal("failure mutated source")
	}
	if _, err := os.Stat(filepath.Join(dir, "roots.toml")); !os.IsNotExist(err) {
		t.Fatalf("failure wrote roots: %v", err)
	}
}

func TestMigrateRequiresConfirmation(t *testing.T) {
	dir := migrateSandbox(t)
	path := filepath.Join(dir, "grove.toml")
	migrateWrite(t, path, "[notebooks.definitions.nb]\nroot_dir=\"/notes\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.x]\npath=\"/x\"\nnotebook=\"nb\"\n")
	var out bytes.Buffer
	err := runLegacyMigrate(&out, strings.NewReader("no\n"), false, false, time.Now())
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(migrateRead(t, path), "[groves.x]") {
		t.Fatal("declined confirmation mutated source")
	}
}

func TestMigrateRefusesLegacySyncOrdering(t *testing.T) {
	dir := migrateSandbox(t)
	migrateWrite(t, filepath.Join(dir, "sync.toml"), "[[workspaces]]\nname=\"nb\"\n")
	var out bytes.Buffer
	err := runLegacyMigrate(&out, strings.NewReader(""), true, false, time.Now())
	if err == nil || !strings.Contains(err.Error(), "--stage-sync") || !strings.Contains(err.Error(), "P2 input") {
		t.Fatalf("err=%v", err)
	}
}

func TestMigrateStagesSyncIntentForP2WithoutLoss(t *testing.T) {
	dir := migrateSandbox(t)
	legacy := "mode=\"keep\"\n[[workspaces]]\nname=\"nb\"\n[notebooks.personal]\nremote=\"origin\"\n"
	migrateWrite(t, filepath.Join(dir, "sync.toml"), legacy)
	migrateWrite(t, filepath.Join(dir, "grove.toml"), "[notebooks.definitions.nb]\nroot_dir=\"/notes\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.x]\npath=\"/x\"\nnotebook=\"nb\"\n")
	var out bytes.Buffer
	err := runLegacyMigrateWithOptions(&out, strings.NewReader(""), migrationOptions{Yes: true, StageSync: true}, time.Unix(10, 0))
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, out.String())
	}
	if got := migrateRead(t, filepath.Join(dir, "sync.toml.p2-staged")); got != legacy {
		t.Fatalf("staged sync intent changed:\n%s", got)
	}
	live := migrateRead(t, filepath.Join(dir, "sync.toml"))
	if !strings.Contains(live, `mode="keep"`) || strings.Contains(live, "workspaces") || strings.Contains(live, "notebooks") {
		t.Fatalf("live sync was not staged surgically:\n%s", live)
	}
	if !strings.Contains(out.String(), "sync staging") || !strings.Contains(out.String(), "equivalence: verified") {
		t.Fatalf("missing staging/equivalence evidence:\n%s", out.String())
	}
}

func TestMigrateRemovesEveryAcceptedTOMLDeclarationShape(t *testing.T) {
	cases := map[string]string{
		"inline":    `groves = { x = { path = "/x", notebook = "nb" } }`,
		"dotted":    `groves.x = { path = "/x", notebook = "nb" }`,
		"commented": "[groves.x] # local root\npath = \"/x\"\nnotebook = \"nb\"",
	}
	for name, declaration := range cases {
		t.Run(name, func(t *testing.T) {
			dir := migrateSandbox(t)
			path := filepath.Join(dir, "grove.toml")
			migrateWrite(t, path, "keep = \"yes\"\n"+declaration+"\n[notebooks.definitions.nb]\nroot_dir = \"/notes\"\n[notebooks.rules]\ndefault = \"nb\"\n")
			var out bytes.Buffer
			if err := runLegacyMigrate(&out, strings.NewReader(""), false, true, time.Unix(2, 0)); err != nil {
				t.Fatalf("migrate: %v\n%s", err, out.String())
			}
			got := migrateRead(t, path)
			if strings.Contains(got, "groves") || !strings.Contains(got, `keep = "yes"`) {
				t.Fatalf("migration was not surgical:\n%s", got)
			}
		})
	}

	input := []byte(`machine = { name = "laptop", ecosystems = { eco = { path = "/eco" } }, roots = { bare = { path = "/bare" } } }
keep = "yes"
`)
	got, err := removeTOMLPrefixes(input, []string{"machine.ecosystems", "machine.roots"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "ecosystems") || strings.Contains(text, "roots") || !strings.Contains(text, `name = "laptop"`) || !strings.Contains(text, `keep = "yes"`) {
		t.Fatalf("inline machine migration was not surgical:\n%s", text)
	}
}

func TestMigrateMutatesSymlinkUsingLogicalSourceDialect(t *testing.T) {
	dir := migrateSandbox(t)
	target := filepath.Join(t.TempDir(), "legacy-config")
	migrateWrite(t, target, "keep=\"yes\"\n[notebooks.definitions.nb]\nroot_dir=\"/notes\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.x]\npath=\"/x\"\nnotebook=\"nb\"\n")
	if err := os.Symlink(target, filepath.Join(dir, "grove.toml")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runLegacyMigrate(&out, strings.NewReader(""), false, true, time.Unix(3, 0)); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out.String())
	}
	got := migrateRead(t, target)
	if strings.Contains(got, "groves.x") || !strings.Contains(got, `keep="yes"`) {
		t.Fatalf("symlink target edited with wrong dialect:\n%s", got)
	}
}

func TestMigrationRejectsConflictingAliasDialects(t *testing.T) {
	target := filepath.Join(t.TempDir(), "shared")
	_, err := sourceForTarget([]legacySource{
		{Path: "grove.toml", Resolved: target, TOML: true, RemoveGlobal: true},
		{Path: "grove.override.yml", Resolved: target, TOML: false, RemoveGlobal: true},
	}, target)
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("err=%v", err)
	}
}

func TestMigrationBackupCollisionIsImmutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grove.toml")
	migrateWrite(t, path, "original")
	stamp := "20260810T123456Z"
	backup := path + "." + stamp + ".bak"
	migrateWrite(t, backup, "first backup")
	if err := backupMigrationFile(path, stamp); err == nil {
		t.Fatal("backup collision unexpectedly overwrote existing backup")
	}
	if got := migrateRead(t, backup); got != "first backup" {
		t.Fatalf("existing backup changed to %q", got)
	}
}

func TestCutoverHelpAndPreviewDescribeRecordedTopology(t *testing.T) {
	m := newSetupModel(setup.NewService(false), nil)
	m.width = 100
	preview := m.viewConfigFormatStep()
	if strings.Contains(preview, "groves.projects") || strings.Contains(preview, "groves:") || !strings.Contains(preview, "roots.toml") {
		t.Fatalf("stale setup preview:\n%s", preview)
	}
	help := newSatelliteUpCmd().Long
	for _, want := range []string{"five-file recorded", "roots.toml", "notebooks.toml"} {
		if !strings.Contains(help, want) {
			t.Errorf("satellite help missing %q", want)
		}
	}
	if strings.Contains(help, "[machine.ecosystems.grovetools]") {
		t.Fatal("satellite help still advertises machine ecosystem topology")
	}
}

func TestMigrateRollbackAfterEveryWriteRestoresByteIdenticalInputs(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprintf("write-%d", failAt), func(t *testing.T) {
			dir := migrateSandbox(t)
			eco := filepath.Join(t.TempDir(), "eco")
			cardPath := filepath.Join(eco, "grove.toml")
			globalPath := filepath.Join(dir, "grove.toml")
			global := "keep=\"yes\"\n[notebooks.definitions.nb]\nroot_dir=\"/notes\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.eco]\npath=" + quoteTOML(eco) + "\n"
			card := "name=\"eco\"\n[ecosystem.notebooks.nb]\ndefault=true\n"
			migrateWrite(t, globalPath, global)
			migrateWrite(t, cardPath, card)
			writes := 0
			migrationFailureHook = func(string) error {
				writes++
				if writes == failAt {
					return fmt.Errorf("injected write %d", failAt)
				}
				return nil
			}
			t.Cleanup(func() { migrationFailureHook = nil })
			var out bytes.Buffer
			err := runLegacyMigrate(&out, strings.NewReader(""), false, true, time.Unix(11, 0))
			if err == nil || !strings.Contains(err.Error(), "restored every changed file byte-for-byte") {
				t.Fatalf("err=%v\n%s", err, out.String())
			}
			if got := migrateRead(t, globalPath); got != global {
				t.Fatalf("global input changed after rollback:\n%s", got)
			}
			if got := migrateRead(t, cardPath); got != card {
				t.Fatalf("card input changed after rollback:\n%s", got)
			}
			for _, path := range []string{filepath.Join(dir, "roots.toml"), filepath.Join(dir, "notebooks.toml"), globalPath + ".19700101T000011Z.bak", cardPath + ".19700101T000011Z.bak"} {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("rollback residue %s: %v", path, statErr)
				}
			}
		})
	}
}

func TestMigrateEffectiveMismatchRollsBack(t *testing.T) {
	dir := migrateSandbox(t)
	path := filepath.Join(dir, "grove.toml")
	original := "[notebooks.definitions.nb]\nroot_dir=\"/notes\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.x]\npath=\"/x\"\nnotebook=\"nb\"\n"
	migrateWrite(t, path, original)
	migrationFailureHook = func(written string) error {
		if written == resolvedLegacyPath(path) {
			roots := filepath.Join(dir, "roots.toml")
			data := strings.ReplaceAll(migrateRead(t, roots), `path = "/x"`, `path = "/different"`)
			migrateWrite(t, roots, data)
		}
		return nil
	}
	t.Cleanup(func() { migrationFailureHook = nil })
	var out bytes.Buffer
	err := runLegacyMigrate(&out, strings.NewReader(""), false, true, time.Unix(12, 0))
	if err == nil || !strings.Contains(err.Error(), "effective-config equivalence check") {
		t.Fatalf("err=%v", err)
	}
	if got := migrateRead(t, path); got != original {
		t.Fatalf("source changed after mismatch rollback:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "roots.toml")); !os.IsNotExist(err) {
		t.Fatalf("roots survived mismatch rollback: %v", err)
	}
}

func TestMigrateIdempotentNoOpHasTransitionJSONEvidence(t *testing.T) {
	dir := migrateSandbox(t)
	migrateWrite(t, filepath.Join(dir, "notebooks.toml"), "default=\"nb\"\n[notebooks.nb]\nroot=\"/notes\"\n")
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "[roots.x]\npath=\"/x\"\nscan=true\nnotebook=\"nb\"\n")
	var out bytes.Buffer
	if err := runLegacyMigrateWithOptions(&out, strings.NewReader(""), migrationOptions{Yes: true, JSON: true}, time.Unix(13, 0)); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), `"action": "grove migrate step 1"`) || !strings.Contains(out.String(), `"reason": "legacy configuration is already migrated`) {
		t.Fatalf("missing/invalid no-op transition evidence:\n%s", out.String())
	}
}

func TestMigrateApplyJSONIsOnlyTransitionEvidence(t *testing.T) {
	dir := migrateSandbox(t)
	migrateWrite(t, filepath.Join(dir, "grove.toml"), "[notebooks.definitions.nb]\nroot_dir=\"/notes\"\n[notebooks.rules]\ndefault=\"nb\"\n[groves.x]\npath=\"/x\"\nnotebook=\"nb\"\n")
	var out bytes.Buffer
	if err := runLegacyMigrateWithOptions(&out, strings.NewReader(""), migrationOptions{Yes: true, JSON: true}, time.Unix(14, 0)); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || strings.Contains(out.String(), "Migration step 1 plan") || !strings.Contains(out.String(), `"counts"`) {
		t.Fatalf("apply JSON is not pure transition evidence:\n%s", out.String())
	}
}

func TestTopLevelMigrateCommand(t *testing.T) {
	cmd := newMigrateCmd()
	if got := cmd.Name(); got != "migrate" {
		t.Fatalf("name=%q", got)
	}
	if cmd.Flags().Lookup("stage-sync") == nil || cmd.Flags().Lookup("json") == nil {
		t.Fatal("migrate command omitted staging or transition JSON flag")
	}
}

func quoteTOML(s string) string { return "\"" + strings.ReplaceAll(s, "\\", "\\\\") + "\"" }
