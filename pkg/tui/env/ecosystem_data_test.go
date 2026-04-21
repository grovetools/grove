package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/workspace"
)

// TestDetectLocalOrphans_CaseInsensitivePaths verifies that active
// worktrees are NOT re-emitted as orphans when their discovered paths
// differ in case from the ecosystemRoot (which on darwin has been
// lowercased by pathutil.NormalizeForLookup).
func TestDetectLocalOrphans_CaseInsensitivePaths(t *testing.T) {
	tmp := t.TempDir()
	wtName := "tier1-a"
	statePath := filepath.Join(tmp, ".grove-worktrees", wtName, ".grove", "env", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"provider":"docker"}`), 0644); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"provider":"docker"}`), 0644); err != nil {
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
