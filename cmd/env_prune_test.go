package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grovetools/core/pkg/prune"
	"github.com/grovetools/core/pkg/workspace"

	envtui "github.com/grovetools/grove/pkg/tui/env"
)

// sandboxXDG isolates a test from the host grove data dir so WorktreesDir()
// resolves inside the test tmp dir. GROVE_HOME must be cleared explicitly — it
// beats XDG_DATA_HOME in paths.getDataHome().
func sandboxXDG(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GROVE_HOME", "")
}

func containsSlug(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestCollectSlugs_SeesXDGBaseDirs verifies grove's collectSlugs enumerates the
// XDG worktree base (paths.WorktreesDir()/<DirIdentifier>) — not just the legacy
// <eco>/.grove-worktrees — so a worktree directory that exists only in the XDG
// layout still registers as a known (inactive) slug for the prune dictionary.
func TestCollectSlugs_SeesXDGBaseDirs(t *testing.T) {
	sandboxXDG(t)

	ecoRoot := t.TempDir()
	xdgDead := workspace.ResolveNewWorktreePath(ecoRoot, "dead-xdg", true)
	if err := os.MkdirAll(xdgDead, 0o755); err != nil {
		t.Fatalf("mkdir XDG worktree: %v", err)
	}

	// One discovered, active worktree so the active list is non-empty.
	states := []envtui.WorktreeState{
		{Workspace: &workspace.WorkspaceNode{Name: "alive"}},
	}

	active, inactive := collectSlugs(ecoRoot, states)
	if !containsSlug(active, "alive") {
		t.Errorf("active = %v, want it to contain 'alive'", active)
	}
	if !containsSlug(inactive, "dead-xdg") {
		t.Errorf("inactive = %v, want it to contain the XDG-base dir 'dead-xdg'", inactive)
	}
}

// TestEnvPruneWiring_DeletesXDGOrphanRefusesSiblingClone exercises the exact
// Inputs construction runEnvPrune uses (WorktreeBases =
// workspace.WorktreeBases(root)) and asserts the destructive-path contract for
// the XDG layout end to end through prune.Run:
//
//   - an XDG worktree directory whose slug is not active IS detected and deleted;
//   - the XDG base and identifier dir survive;
//   - a sibling clone — a same-basename worktree of a DIFFERENT ecosystem whose
//     path hashes to a different identifier dir — is NOT deleted, because it
//     lives outside this ecosystem's worktree bases (the DirIdentifier hash is
//     what keeps two same-named ecosystems from intermixing).
func TestEnvPruneWiring_DeletesXDGOrphanRefusesSiblingClone(t *testing.T) {
	sandboxXDG(t)

	ecoRoot := t.TempDir()
	xdgOrphan := workspace.ResolveNewWorktreePath(ecoRoot, "dead", true)
	if err := os.MkdirAll(filepath.Join(xdgOrphan, ".grove", "env"), 0o755); err != nil {
		t.Fatalf("mkdir XDG orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(xdgOrphan, ".grove", "env", "state.json"),
		[]byte(`{"provider":"docker"}`), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}

	// Sibling clone: a DIFFERENT ecosystem (distinct path → distinct
	// DirIdentifier) with a worktree of the SAME basename "dead".
	siblingRoot := t.TempDir()
	siblingClone := workspace.ResolveNewWorktreePath(siblingRoot, "dead", true)
	if err := os.MkdirAll(siblingClone, 0o755); err != nil {
		t.Fatalf("mkdir sibling clone: %v", err)
	}
	// Guard: the two ecosystems must hash to different identifier dirs, or
	// the test would be vacuous.
	if filepath.Dir(xdgOrphan) == filepath.Dir(siblingClone) {
		t.Fatalf("sibling clone shares the identifier dir with the orphan; DirIdentifier collision")
	}

	// Mirror runEnvPrune's Inputs construction. Active is non-empty (the
	// ecosystem's own checkout would supply at least one live slug); the
	// orphan's slug "dead" is deliberately absent.
	in := prune.Inputs{
		GitRoot:       ecoRoot,
		WorktreeBases: workspace.WorktreeBases(ecoRoot),
		Active:        []string{"alive"},
	}
	res, err := prune.Run(in, prune.Options{DryRun: false})
	if err != nil {
		t.Fatalf("prune.Run: %v", err)
	}

	if len(res.Deleted) != 1 || res.Deleted[0].Name != xdgOrphan {
		t.Fatalf("expected only the XDG orphan deleted, got deleted=%+v failed=%+v", res.Deleted, res.Failed)
	}
	if _, err := os.Stat(xdgOrphan); !os.IsNotExist(err) {
		t.Errorf("XDG orphan should be removed, stat err=%v", err)
	}
	// The identifier dir and the XDG base are containers — never deleted.
	if _, err := os.Stat(filepath.Dir(xdgOrphan)); err != nil {
		t.Errorf("identifier dir should survive: %v", err)
	}
	// The sibling clone lives under a different ecosystem's bases and must be
	// untouched by this ecosystem's prune.
	if _, err := os.Stat(siblingClone); err != nil {
		t.Errorf("sibling clone must survive (refused — outside this ecosystem's worktree bases): %v", err)
	}
}
