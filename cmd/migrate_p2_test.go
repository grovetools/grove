package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
)

func p2Sandbox(t *testing.T) (string, string) {
	t.Helper()
	dir := migrateSandbox(t)
	nb := filepath.Join(t.TempDir(), "notebook")
	if err := os.MkdirAll(filepath.Join(nb, "workspaces", "alpha", "concepts", "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, filepath.Join(nb, "workspaces", "alpha", "concepts", "one", "overview.md"), "Preset: `cx rules load "+nb+"/workspaces/alpha/context/presets/x.rules`\n")
	migrateWrite(t, filepath.Join(dir, "notebooks.toml"), "default=\"nb\"\n[notebooks.nb]\nroot="+quoteTOML(nb)+"\n")
	migrateWrite(t, filepath.Join(dir, "roots.toml"), "")
	migrateWrite(t, filepath.Join(dir, "sync.toml"), "server=\"http://unused\"\n")
	migrateWrite(t, filepath.Join(dir, "sync.toml.p2-staged"), "server=\"http://unused\"\n[[workspaces]]\nname=\"alpha\"\nrole=\"registry\"\npull=true\n")
	config.ResetLoadCache()
	return dir, nb
}

func assertP2FullConfigAndDoctor(t *testing.T) {
	t.Helper()
	config.ResetLoadCache()
	if _, err := config.LoadDefault(); err != nil {
		t.Fatalf("full config loader rejected migration state: %v", err)
	}
	doctorFix, doctorCheckID, doctorJSON, doctorVerbose = false, "", false, false
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--json"})
	_ = cmd.Execute() // Unrelated sandbox checks may fail; inspect config results.
	var results []doctorJSONResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("full doctor emitted invalid JSON %q: %v", out.String(), err)
	}
	foundFragments, foundSchema := false, false
	for _, result := range results {
		if result.Check == "effective_config" {
			t.Fatalf("full doctor reported effective config degradation: %+v", result)
		}
		if result.Check == "config_fragments" {
			foundFragments = true
			if result.Status != "pass" {
				t.Fatalf("full doctor rejected config fragments: %+v", result)
			}
		}
		if result.Check == "config_schema" {
			foundSchema = true
			if result.Status != "pass" {
				t.Fatalf("full doctor applied layered schema to standalone config: %+v", result)
			}
		}
	}
	if !foundFragments || !foundSchema {
		t.Fatalf("full doctor omitted config checks: fragments=%t schema=%t", foundFragments, foundSchema)
	}
}

func TestP2MigrationDryRunApplyUndoAndScopedRewrite(t *testing.T) {
	_, nb := p2Sandbox(t)
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	stagePath := config.SyncConfigPath() + ".p2-staged"
	stagedBefore := []byte(migrateRead(t, stagePath))
	liveBefore := []byte(migrateRead(t, config.SyncConfigPath()))
	db := filepath.Join(t.TempDir(), "sync.db")
	oldCommand := p2Command
	p2Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`{"already_current":true}`), nil
	}
	t.Cleanup(func() { p2Command = oldCommand })
	opts := p2MigrationOptions{DryRun: true, Yes: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ContentRoots: []string{nb}, ManifestPath: manifest, SyncDBPath: db, GrovedBin: "groved"}
	var out bytes.Buffer
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(100, 0)); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nb, "notespaces")); !os.IsNotExist(err) {
		t.Fatal("dry-run mutated layout")
	}
	if got := []byte(migrateRead(t, stagePath)); !bytes.Equal(got, stagedBefore) {
		t.Fatalf("dry-run changed authoritative staged bytes\nwant: %q\n got: %q", stagedBefore, got)
	}
	if got := []byte(migrateRead(t, config.SyncConfigPath())); !bytes.Equal(got, liveBefore) {
		t.Fatalf("dry-run changed live sync config\nwant: %q\n got: %q", liveBefore, got)
	}
	opts.DryRun = false
	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(101, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	stamp, err := os.ReadFile(filepath.Join(nb, "notespaces", "alpha", ".notespace.toml"))
	if err != nil || !bytes.Contains(stamp, []byte("subject")) {
		t.Fatalf("stamp=%s err=%v", stamp, err)
	}
	body := migrateRead(t, filepath.Join(nb, "notespaces", "alpha", "concepts", "one", "overview.md"))
	if strings.Contains(body, "/workspaces/") || !strings.Contains(body, "/notespaces/") {
		t.Fatalf("rewrite=%s", body)
	}
	machine := migrateRead(t, config.MachineConfigPath())
	if !strings.Contains(machine, "[primaries]") || !strings.Contains(machine, "[sync.registry]") {
		t.Fatalf("machine=%s", machine)
	}
	compiled := []byte(migrateRead(t, config.SyncConfigPath()))
	if bytes.Equal(compiled, stagedBefore) {
		t.Fatal("apply copied legacy staged bytes instead of compiling typed sync config")
	}
	if _, err := config.LoadSyncConfigFrom(config.SyncConfigPath()); err != nil {
		t.Fatalf("compiled sync config rejected by typed loader: %v\n%s", err, compiled)
	}
	if _, err := os.Stat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("apply did not consume staged source: %v", err)
	}
	var applied p2MigrationManifest
	data, _ := os.ReadFile(manifest)
	_ = json.Unmarshal(data, &applied)
	if len(applied.SyncConversion.Bindings) != 1 || applied.SyncConversion.Bindings[0].Role != config.SyncRoleRegistry || applied.SyncConversion.StagedHash != hashBytes(stagedBefore) || applied.SyncConversion.FinalHash != hashBytes(compiled) {
		t.Fatalf("conversion evidence=%+v", applied.SyncConversion)
	}
	assertP2FullConfigAndDoctor(t)
	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(101, 0)); err != nil || !strings.Contains(out.String(), "already consumed") {
		t.Fatalf("idempotent rerun: %v\n%s", err, out.String())
	}
	undo := p2MigrationOptions{Undo: true, ManifestPath: manifest}
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), undo, time.Unix(102, 0)); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nb, "workspaces", "alpha", ".notespace.toml")); !os.IsNotExist(err) {
		t.Fatal("undo retained minted stamp")
	}
	if _, err := os.Stat(filepath.Join(nb, "notespaces")); !os.IsNotExist(err) {
		t.Fatal("undo retained notespaces dir")
	}
	if got := []byte(migrateRead(t, stagePath)); !bytes.Equal(got, stagedBefore) {
		t.Fatalf("undo did not restore authoritative staged bytes exactly\nwant: %q\n got: %q", stagedBefore, got)
	}
	if got := []byte(migrateRead(t, config.SyncConfigPath())); !bytes.Equal(got, liveBefore) {
		t.Fatalf("undo did not restore prior live sync bytes exactly\nwant: %q\n got: %q", liveBefore, got)
	}
	assertP2FullConfigAndDoctor(t)
	var receipt p2MigrationManifest
	data, _ = os.ReadFile(manifest)
	_ = json.Unmarshal(data, &receipt)
	if receipt.State != "undone" || !strings.Contains(receipt.ServerState, "NOT undone") {
		t.Fatalf("receipt=%+v", receipt)
	}

	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(103, 0)); err != nil {
		t.Fatalf("reapply: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("reapply did not consume staged source: %v", err)
	}
	assertP2FullConfigAndDoctor(t)
}

func TestP2MigrationInjectedFailureRollsBackEveryLocalMutation(t *testing.T) {
	_, nb := p2Sandbox(t)
	oldCommand, oldHook := p2Command, migrationFailureHook
	p2Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"already_current":true}`), nil
	}
	migrationFailureHook = func(path string) error {
		if path == config.MachineConfigPath() {
			return fmt.Errorf("injected")
		}
		return nil
	}
	t.Cleanup(func() { p2Command, migrationFailureHook = oldCommand, oldHook })
	err := runP2Migration(context.Background(), &bytes.Buffer{}, strings.NewReader(""), p2MigrationOptions{Yes: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json"), SyncDBPath: filepath.Join(t.TempDir(), "db")}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "local state restored") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(nb, "workspaces", "alpha")); err != nil {
		t.Fatalf("old layout not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nb, "notespaces")); !os.IsNotExist(err) {
		t.Fatalf("new layout retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nb, "workspaces", "alpha", ".notespace.toml")); !os.IsNotExist(err) {
		t.Fatal("minted stamp retained")
	}
	if _, err := os.Stat(config.SyncConfigPath() + ".p2-staged"); err != nil {
		t.Fatalf("staged input not restored: %v", err)
	}
}

func TestP2SyncCompilePreservesTypedIntentAndRejectsUnknownKeys(t *testing.T) {
	_, nb := p2Sandbox(t)
	stage := config.SyncConfigPath() + ".p2-staged"
	t.Setenv("P2_SYNC_SERVER", "https://sync.example.com")
	legacy := "server = \"${P2_SYNC_SERVER}\"\ntoken_command = \"secret-tool lookup grove sync\"\n[[workspaces]]\nname = \"alpha\"\nrole = \"registry\"\nmode = \"plans-only\"\npull = true\nexcludes = [\"private/\", \"tmp/\"]\nmax_file_size = 4096\n"
	migrateWrite(t, stage, legacy)
	manifest := &p2MigrationManifest{Notespaces: []p2NotespacePlan{{Notebook: "nb", Name: "alpha", Root: filepath.Join(nb, "notespaces", "alpha"), ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Subject: "local:test", Kind: "notes"}}}

	compiled, receipt, err := compileP2StagedSync(manifest)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !bytes.Contains(compiled, []byte("${P2_SYNC_SERVER}")) {
		t.Fatalf("compile baked environment expansion into final config:\n%s", compiled)
	}
	path := filepath.Join(t.TempDir(), "sync.toml")
	migrateWrite(t, path, string(compiled))
	got, err := config.LoadSyncConfigFrom(path)
	if err != nil || got == nil || len(got.Workspaces) != 1 {
		t.Fatalf("compiled typed config did not load: %+v err=%v\n%s", got, err, compiled)
	}
	ws := got.Workspaces[0]
	if got.Server != "https://sync.example.com" || got.TokenCommand != "secret-tool lookup grove sync" || ws.Mode != config.SyncModePlansOnly || !ws.Pull || ws.MaxFileSize != 4096 || len(ws.Excludes) != 2 {
		t.Fatalf("compiled semantics changed: %+v / %+v", got, ws)
	}
	if len(receipt.Bindings) != 1 || receipt.Bindings[0].NotespaceID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" || receipt.StagedHash != hashBytes([]byte(legacy)) || receipt.FinalHash != hashBytes(compiled) {
		t.Fatalf("conversion receipt=%+v", receipt)
	}

	migrateWrite(t, stage, legacy+"future_intent = \"must-not-drop\"\n")
	if _, _, err := compileP2StagedSync(manifest); err == nil || !strings.Contains(err.Error(), "unknown workspaces[0] key \"future_intent\"") {
		t.Fatalf("unknown legacy intent was silently dropped: %v", err)
	}
}

func TestP2MigrationPinsInheritedNotespaceID(t *testing.T) {
	_, nb := p2Sandbox(t)
	const inheritedID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	opts := p2MigrationOptions{DryRun: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, NotespaceIDs: []string{"nb/alpha=" + inheritedID}, ManifestPath: filepath.Join(t.TempDir(), "m.json")}
	manifest, err := planP2Migration(opts, time.Now())
	if err != nil {
		t.Fatalf("plan pinned inherited id: %v", err)
	}
	if len(manifest.Notespaces) != 1 || manifest.Notespaces[0].ID != inheritedID || manifest.SyncConversion.Bindings[0].NotespaceID != inheritedID {
		t.Fatalf("pinned identity did not reach stamps/config receipt: %+v / %+v", manifest.Notespaces, manifest.SyncConversion)
	}
	bad := opts
	bad.NotespaceIDs = []string{"nb/missing=" + inheritedID}
	if _, err := planP2Migration(bad, time.Now()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unknown pinned identity was accepted: %v", err)
	}
}

func TestP2MigrationRefusesImplicitRootAndMissingStagedInput(t *testing.T) {
	if err := runP2Migration(context.Background(), &bytes.Buffer{}, strings.NewReader(""), p2MigrationOptions{DryRun: true, LocalOnly: true}, time.Now()); err == nil || !strings.Contains(err.Error(), "explicit --notebook-root") {
		t.Fatalf("error=%v", err)
	}
	_, nb := p2Sandbox(t)
	if err := os.Remove(config.SyncConfigPath() + ".p2-staged"); err != nil {
		t.Fatal(err)
	}
	err := runP2Migration(context.Background(), &bytes.Buffer{}, strings.NewReader(""), p2MigrationOptions{DryRun: true, LocalOnly: true, NotebookRoots: []string{"nb=" + nb}, ManifestPath: filepath.Join(t.TempDir(), "m.json")}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "required exact P2 input") {
		t.Fatalf("error=%v", err)
	}
}
