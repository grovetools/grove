package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/workspace"
)

// sandboxXDG isolates a test from the host grove data dir so WorktreesDir()
// resolves inside the test tmp dir. GROVE_HOME must be cleared explicitly —
// it beats XDG_DATA_HOME in paths.getDataHome().
func sandboxXDG(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GROVE_HOME", "")
}

// TestDetectLocalOrphans_CaseInsensitivePaths verifies that active
// worktrees are NOT re-emitted as orphans when their discovered paths
// differ in case from the ecosystemRoot (which on darwin has been
// lowercased by pathutil.NormalizeForLookup).
func TestDetectLocalOrphans_CaseInsensitivePaths(t *testing.T) {
	tmp := t.TempDir()
	wtName := "tier1-a"
	statePath := filepath.Join(tmp, ".grove-worktrees", wtName, ".grove", "env", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"provider":"docker"}`), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}

	// Simulate the real-world mismatch: ecosystemRoot is lowercased, but
	// the discovered worktree's Workspace.Path retains its original casing.
	loweredRoot := strings.ToLower(tmp)
	upperedWtPath := filepath.Join(tmp, ".grove-worktrees", wtName)

	worktrees := []WorktreeState{{
		Workspace: &workspace.WorkspaceNode{Name: wtName, Path: upperedWtPath},
	}}

	orphans := DetectLocalOrphans(loweredRoot, worktrees)
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans for case-mismatched active worktree, got %d", len(orphans))
	}
}

// TestDetectLocalOrphans_TrueOrphan verifies that a state.json with no
// matching known worktree is correctly reported as an orphan.
func TestDetectLocalOrphans_TrueOrphan(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, ".grove-worktrees", "ghost", ".grove", "env", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"provider":"docker"}`), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}

	orphans := DetectLocalOrphans(tmp, nil)
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].OrphanStatePath != statePath {
		t.Errorf("OrphanStatePath = %q, want %q", orphans[0].OrphanStatePath, statePath)
	}
}

// TestDetectLocalOrphans_XDGTrueOrphan verifies a stale state.json under the
// XDG worktree base (WorktreesDir()/<DirIdentifier>/<name>) — not just the
// legacy <ecoRoot>/.grove-worktrees path — is reported as an orphan.
func TestDetectLocalOrphans_XDGTrueOrphan(t *testing.T) {
	sandboxXDG(t)

	ecoRoot := filepath.Join(t.TempDir(), "my-eco")
	xdgBase := filepath.Join(paths.WorktreesDir(), workspace.DirIdentifier(ecoRoot))
	statePath := filepath.Join(xdgBase, "ghost-xdg", ".grove", "env", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"provider":"docker"}`), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}

	orphans := DetectLocalOrphans(ecoRoot, nil)
	if len(orphans) != 1 {
		t.Fatalf("expected 1 XDG orphan, got %d", len(orphans))
	}
	if orphans[0].OrphanStatePath != statePath {
		t.Errorf("OrphanStatePath = %q, want %q", orphans[0].OrphanStatePath, statePath)
	}
}

// TestDetectLocalOrphans_XDGCaseInsensitivePaths verifies the
// case-insensitivity behavior is preserved for XDG worktrees: an active XDG
// worktree whose discovered Workspace.Path differs only in case from the
// glob-discovered state path is NOT re-emitted as an orphan.
func TestDetectLocalOrphans_XDGCaseInsensitivePaths(t *testing.T) {
	sandboxXDG(t)

	ecoRoot := filepath.Join(t.TempDir(), "my-eco")
	xdgBase := filepath.Join(paths.WorktreesDir(), workspace.DirIdentifier(ecoRoot))
	wtName := "tier1-a"
	wtPath := filepath.Join(xdgBase, wtName)
	statePath := filepath.Join(wtPath, ".grove", "env", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"provider":"docker"}`), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}

	// Discovered worktree path retains its original casing; the lookup map
	// must lowercase both sides or this active worktree resurfaces as orphan.
	worktrees := []WorktreeState{{
		Workspace: &workspace.WorkspaceNode{Name: wtName, Path: strings.ToUpper(wtPath)},
	}}

	orphans := DetectLocalOrphans(ecoRoot, worktrees)
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans for case-mismatched active XDG worktree, got %d", len(orphans))
	}
}
