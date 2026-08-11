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

func TestP2MigrationDryRunApplyUndoAndScopedRewrite(t *testing.T) {
	_, nb := p2Sandbox(t)
	manifest := filepath.Join(t.TempDir(), "manifest.json")
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
	var receipt p2MigrationManifest
	data, _ := os.ReadFile(manifest)
	_ = json.Unmarshal(data, &receipt)
	if receipt.State != "undone" || !strings.Contains(receipt.ServerState, "NOT undone") {
		t.Fatalf("receipt=%+v", receipt)
	}
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
