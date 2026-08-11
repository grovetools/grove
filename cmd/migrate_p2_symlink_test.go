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

// The P2 symlink tests run entirely inside the GROVE_HOME sandbox migrateSandbox
// installs. Nothing here reads or writes a real ~/.config, notebook, sync DB,
// daemon or server: p2Command is stubbed and every run is --local-only.

type p2Dotfiles struct {
	syncLink, syncTarget       string
	machineLink, machineTarget string
}

// p2SymlinkSandbox is p2Sandbox with the canonical machine.toml and sync.toml
// replaced by symlinks into a machine-specific dotfiles directory — the shape
// this fleet already runs and the shape P2 must not destroy.
func p2SymlinkSandbox(t *testing.T) (string, string, p2Dotfiles) {
	t.Helper()
	dir, nb := p2Sandbox(t)
	dotfiles := t.TempDir()
	df := p2Dotfiles{
		syncLink:      config.SyncConfigPath(),
		syncTarget:    filepath.Join(dotfiles, "machine-sync.toml"),
		machineLink:   config.MachineConfigPath(),
		machineTarget: filepath.Join(dotfiles, "machine-identity.toml"),
	}

	body := migrateRead(t, df.syncLink)
	if err := os.Remove(df.syncLink); err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, df.syncTarget, body)
	if err := os.Chmod(df.syncTarget, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(df.syncTarget, df.syncLink); err != nil {
		t.Fatal(err)
	}

	migrateWrite(t, df.machineTarget, "# dotfiles-owned\n[machine]\nname = \"laptop\"\n")
	if err := os.Chmod(df.machineTarget, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(df.machineTarget, df.machineLink); err != nil {
		t.Fatal(err)
	}

	config.ResetLoadCache()
	return dir, nb, df
}

func assertMigrationLink(t *testing.T, link, want string) {
	t.Helper()
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat %s: %v", link, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink; the migration replaced the link", link)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s -> %q, want %q", link, got, want)
	}
}

func stubP2Command(t *testing.T) {
	t.Helper()
	old := p2Command
	p2Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"already_current":true}`), nil
	}
	t.Cleanup(func() { p2Command = old })
}

func p2SymlinkOptions(t *testing.T, nb string) p2MigrationOptions {
	t.Helper()
	return p2MigrationOptions{
		Yes:           true,
		LocalOnly:     true,
		NotebookRoots: []string{"nb=" + nb},
		ManifestPath:  filepath.Join(t.TempDir(), "manifest.json"),
		SyncDBPath:    filepath.Join(t.TempDir(), "sync.db"),
	}
}

func p2ManifestEntry(t *testing.T, manifestPath, path string) p2FileBackup {
	t.Helper()
	var manifest p2MigrationManifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, file := range manifest.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("manifest has no record for %s", path)
	return p2FileBackup{}
}

func TestP2MigrationPreservesDotfilesSymlinksThroughDryRunApplyAndUndo(t *testing.T) {
	_, nb, df := p2SymlinkSandbox(t)
	stubP2Command(t)
	opts := p2SymlinkOptions(t, nb)
	syncBefore := migrateRead(t, df.syncTarget)
	machineBefore := migrateRead(t, df.machineTarget)

	dry := opts
	dry.DryRun = true
	var out bytes.Buffer
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), dry, time.Unix(100, 0)); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out.String())
	}
	assertMigrationLink(t, df.syncLink, df.syncTarget)
	assertMigrationLink(t, df.machineLink, df.machineTarget)
	if got := migrateRead(t, df.syncTarget); got != syncBefore {
		t.Fatalf("dry-run mutated the sync dotfiles target:\n%s", got)
	}
	if got := migrateRead(t, df.machineTarget); got != machineBefore {
		t.Fatalf("dry-run mutated the machine dotfiles target:\n%s", got)
	}

	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(101, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}

	assertMigrationLink(t, df.syncLink, df.syncTarget)
	assertMigrationLink(t, df.machineLink, df.machineTarget)
	machineAfter := migrateRead(t, df.machineTarget)
	if !strings.Contains(machineAfter, "[primaries]") || !strings.Contains(machineAfter, "# dotfiles-owned") {
		t.Fatalf("machine identity did not land surgically in the dotfiles target:\n%s", machineAfter)
	}
	syncAfter := migrateRead(t, df.syncTarget)
	if syncAfter == syncBefore {
		t.Fatal("compiled sync config did not reach the dotfiles target")
	}
	if _, err := config.LoadSyncConfigFrom(df.syncLink); err != nil {
		t.Fatalf("compiled sync config is not loadable through the link: %v", err)
	}
	for path, want := range map[string]os.FileMode{df.syncTarget: 0o640, df.machineTarget: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("dotfiles target %s mode = %v (%v), want %o", path, info.Mode().Perm(), err, want)
		}
	}

	// Both halves of the identity are represented in the manifest.
	resolvedSync, err := filepath.EvalSymlinks(df.syncTarget)
	if err != nil {
		t.Fatal(err)
	}
	entry := p2ManifestEntry(t, opts.ManifestPath, df.syncLink)
	if !entry.Symlink || entry.LinkTarget != df.syncTarget || entry.ResolvedPath != resolvedSync {
		t.Fatalf("manifest lost the sync link identity: %+v", entry)
	}
	if entry.AfterLinkTarget != df.syncTarget || entry.BeforeHash == "" || entry.AfterHash == "" {
		t.Fatalf("manifest lost a before/after half for the sync link: %+v", entry)
	}
	machineEntry := p2ManifestEntry(t, opts.ManifestPath, df.machineLink)
	if !machineEntry.Symlink || machineEntry.LinkTarget != df.machineTarget || machineEntry.AfterLinkTarget != df.machineTarget {
		t.Fatalf("manifest lost the machine link identity: %+v", machineEntry)
	}

	out.Reset()
	undo := p2MigrationOptions{Undo: true, ManifestPath: opts.ManifestPath}
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), undo, time.Unix(102, 0)); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	assertMigrationLink(t, df.syncLink, df.syncTarget)
	assertMigrationLink(t, df.machineLink, df.machineTarget)
	if got := migrateRead(t, df.syncTarget); got != syncBefore {
		t.Fatalf("undo did not restore the sync target byte-for-byte:\nwant %q\n got %q", syncBefore, got)
	}
	if got := migrateRead(t, df.machineTarget); got != machineBefore {
		t.Fatalf("undo did not restore the machine target byte-for-byte:\nwant %q\n got %q", machineBefore, got)
	}
}

// The staged sync input is consumed by design. A consumed link must neither
// trip the apply-time link post-condition nor be lost by undo.
func TestP2MigrationRestoresAConsumedStagedSymlink(t *testing.T) {
	_, nb, _ := p2SymlinkSandbox(t)
	stagePath := config.SyncConfigPath() + ".p2-staged"
	stagedTarget := filepath.Join(t.TempDir(), "staged-sync.toml")
	stagedBefore := migrateRead(t, stagePath)
	if err := os.Remove(stagePath); err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, stagedTarget, stagedBefore)
	if err := os.Symlink(stagedTarget, stagePath); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()

	stubP2Command(t)
	opts := p2SymlinkOptions(t, nb)
	var out bytes.Buffer
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(107, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	if _, err := os.Lstat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("apply did not consume the staged link: %v", err)
	}

	undo := p2MigrationOptions{Undo: true, ManifestPath: opts.ManifestPath}
	out.Reset()
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), undo, time.Unix(108, 0)); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	assertMigrationLink(t, stagePath, stagedTarget)
	if got := migrateRead(t, stagedTarget); got != stagedBefore {
		t.Fatalf("undo did not restore the staged target:\nwant %q\n got %q", stagedBefore, got)
	}
}

func TestP2MigrationInjectedFailureRollsBackThroughDotfilesSymlinks(t *testing.T) {
	_, nb, df := p2SymlinkSandbox(t)
	stubP2Command(t)
	syncBefore := migrateRead(t, df.syncTarget)
	machineBefore := migrateRead(t, df.machineTarget)

	old := migrationFailureHook
	migrationFailureHook = func(path string) error {
		// Fail after the sync write, so both config writes have already happened.
		if path == config.SyncConfigPath() {
			return fmt.Errorf("injected")
		}
		return nil
	}
	t.Cleanup(func() { migrationFailureHook = old })

	err := runP2Migration(context.Background(), &bytes.Buffer{}, strings.NewReader(""), p2SymlinkOptions(t, nb), time.Unix(103, 0))
	if err == nil || !strings.Contains(err.Error(), "local state restored") {
		t.Fatalf("injected failure = %v, want a rolled-back failure", err)
	}
	assertMigrationLink(t, df.syncLink, df.syncTarget)
	assertMigrationLink(t, df.machineLink, df.machineTarget)
	if got := migrateRead(t, df.syncTarget); got != syncBefore {
		t.Fatalf("rollback did not restore the sync target:\nwant %q\n got %q", syncBefore, got)
	}
	if got := migrateRead(t, df.machineTarget); got != machineBefore {
		t.Fatalf("rollback did not restore the machine target:\nwant %q\n got %q", machineBefore, got)
	}
	if _, err := os.Stat(config.SyncConfigPath() + ".p2-staged"); err != nil {
		t.Fatalf("staged input not restored: %v", err)
	}
}

func TestP2MigrationRefusesUnreviewableCanonicalConfigLinks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, link string)
	}{
		{"dangling", func(t *testing.T, link string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "not-checked-out.toml"), link); err != nil {
				t.Fatal(err)
			}
		}},
		{"cyclic", func(t *testing.T, link string) {
			hop := link + ".hop"
			if err := os.Symlink(hop, link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, hop); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory-target", func(t *testing.T, link string) {
			if err := os.Symlink(t.TempDir(), link); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, nb := p2Sandbox(t)
			stubP2Command(t)
			link := config.SyncConfigPath()
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			tc.stage(t, link)
			config.ResetLoadCache()

			opts := p2SymlinkOptions(t, nb)
			opts.DryRun = true
			err := runP2Migration(context.Background(), &bytes.Buffer{}, strings.NewReader(""), opts, time.Unix(104, 0))
			if err == nil {
				t.Fatal("planning accepted an unreviewable canonical config link")
			}
			if !strings.Contains(err.Error(), "unreviewable config symlink") && !strings.Contains(err.Error(), "target is not a regular file") {
				t.Fatalf("refusal = %v, want a reviewed-target refusal", err)
			}
			// The refusal must leave the link exactly as staged.
			if info, lerr := os.Lstat(link); lerr != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("refusal disturbed the link: mode=%v err=%v", info.Mode(), lerr)
			}
		})
	}
}

func TestP2UndoRefusesARepointedCanonicalLink(t *testing.T) {
	_, nb, df := p2SymlinkSandbox(t)
	stubP2Command(t)
	opts := p2SymlinkOptions(t, nb)
	var out bytes.Buffer
	if err := runP2Migration(context.Background(), &out, strings.NewReader(""), opts, time.Unix(105, 0)); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}

	// Repoint the canonical link at a byte-identical copy. The content hash
	// cannot see this — only the recorded link identity can.
	decoy := filepath.Join(t.TempDir(), "decoy-sync.toml")
	migrateWrite(t, decoy, migrateRead(t, df.syncTarget))
	if err := os.Remove(df.syncLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, df.syncLink); err != nil {
		t.Fatal(err)
	}

	undo := p2MigrationOptions{Undo: true, ManifestPath: opts.ManifestPath}
	err := runP2Migration(context.Background(), &bytes.Buffer{}, strings.NewReader(""), undo, time.Unix(106, 0))
	if err == nil || !strings.Contains(err.Error(), "changed after migration") {
		t.Fatalf("undo over a repointed link = %v, want a refusal", err)
	}
	if got, _ := os.Readlink(df.syncLink); got != decoy {
		t.Fatalf("refused undo mutated the link: %q", got)
	}
}

func TestRestoreP2FileRepairsAReplacedCanonicalLink(t *testing.T) {
	dir := t.TempDir()
	dotfiles := t.TempDir()
	target := filepath.Join(dotfiles, "sync.toml")
	link := filepath.Join(dir, "sync.toml")
	migrateWrite(t, target, "server = \"http://before\"\n")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	backup, err := backupP2File(link)
	if err != nil {
		t.Fatal(err)
	}
	if !backup.Symlink || backup.LinkTarget != target {
		t.Fatalf("backup lost the link identity: %+v", backup)
	}

	// Simulate the pre-fix damage: the link replaced by a regular file.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	migrateWrite(t, link, "server = \"http://after\"\n")

	if err := restoreP2File(backup, []byte("server = \"http://before\"\n")); err != nil {
		t.Fatal(err)
	}
	assertMigrationLink(t, link, target)
	if got := migrateRead(t, target); got != "server = \"http://before\"\n" {
		t.Fatalf("restored bytes landed outside the dotfiles target: %q", got)
	}
}

func TestAtomicMigrationWriteIsSymlinkSafe(t *testing.T) {
	dir := t.TempDir()
	dotfiles := t.TempDir()
	target := filepath.Join(dotfiles, "roots.toml")
	link := filepath.Join(dir, "roots.toml")
	migrateWrite(t, target, "old\n")
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := atomicMigrationWrite(link, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	assertMigrationLink(t, link, target)
	if got := migrateRead(t, target); got != "new\n" {
		t.Fatalf("target = %q, want the new bytes", got)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("target mode = %v (%v), want 0640", info.Mode().Perm(), err)
	}

	// Regular-file and create behaviour are unchanged.
	plain := filepath.Join(dir, "plain.toml")
	if err := atomicMigrationWrite(plain, []byte("created\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(plain)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("created file = %v (%v), want a 0600 regular file", info.Mode(), err)
	}
	if err := atomicMigrationWrite(plain, []byte("rewritten\n")); err != nil {
		t.Fatal(err)
	}
	if got := migrateRead(t, plain); got != "rewritten\n" {
		t.Fatalf("plain rewrite = %q", got)
	}

	dangling := filepath.Join(dir, "dangling.toml")
	if err := os.Symlink(filepath.Join(dir, "nowhere.toml"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := atomicMigrationWrite(dangling, []byte("x\n")); err == nil || !strings.Contains(err.Error(), "unreviewable config symlink") {
		t.Fatalf("write through a dangling link = %v, want a refusal", err)
	}
	if info, err := os.Lstat(dangling); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("refusal replaced the dangling link: %v %v", info.Mode(), err)
	}
}
