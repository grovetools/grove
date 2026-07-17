package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/workspace"
	"gopkg.in/yaml.v3"
)

// sandboxXDG isolates a test from the host grove data dir so WorktreesDir()
// resolves inside the test tmp dir. GROVE_HOME must be cleared explicitly —
// it beats XDG_DATA_HOME in paths.getDataHome().
func sandboxXDG(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GROVE_HOME", "")
}

// TestIsActiveWorktreeDir pins the contract that decides whether a directory
// discovered under an XDG/worktree base counts as a live worktree (kept in the
// active list) or a stateless leftover (filtered out so `grove env prune` can
// report it as a host-orphan). A dir is active iff it carries a
// .grove/workspace marker OR a git-worktree registration (a .git FILE, not a
// dir). Bare and .git-directory dirs are inactive.
func TestIsActiveWorktreeDir(t *testing.T) {
	// (a) .grove/workspace marker → active.
	withMarker := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withMarker, ".grove"), 0o755); err != nil {
		t.Fatalf("mkdir .grove: %v", err)
	}
	if err := os.WriteFile(filepath.Join(withMarker, ".grove", "workspace"), []byte("owner: eco\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// (b) .git FILE (git-worktree pointer) → active.
	withGitFile := t.TempDir()
	if err := os.WriteFile(filepath.Join(withGitFile, ".git"), []byte("gitdir: /somewhere/.git/worktrees/x\n"), 0o600); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	// (c) .git DIRECTORY (a full clone, not a linked worktree pointer) → NOT a
	// per-worktree registration; on its own it does not make a base-dir entry
	// count as active.
	withGitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withGitDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git dir: %v", err)
	}

	// (d) empty stateless dir → inactive (this is the XDG-orphan case).
	empty := t.TempDir()

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"grove-marker", withMarker, true},
		{"git-file", withGitFile, true},
		{"git-dir", withGitDir, false},
		{"empty", empty, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isActiveWorktreeDir(tc.path); got != tc.want {
				t.Errorf("isActiveWorktreeDir(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestPathsEqual_SymlinkResolving covers the symlink-naive compare defect: on a
// /var → /private/var style symlinked filesystem the old string compare
// (strings.EqualFold(filepath.Clean(a), filepath.Clean(b))) returned false for
// two paths that name the same directory, which emptied belongsToEcosystem's
// active list and made `grove env prune` bail. pathsEqual now resolves symlinks
// so the symlinked and real paths compare equal.
func TestPathsEqual_SymlinkResolving(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "eco-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Sanity: the old naive comparison would NOT have matched these — proves
	// this test exercises the real bug, not a tautology.
	if strings.EqualFold(filepath.Clean(link), filepath.Clean(real)) {
		t.Fatalf("precondition: symlink path %q unexpectedly string-equals real path %q", link, real)
	}

	if !pathsEqual(link, real) {
		t.Errorf("pathsEqual(%q, %q) = false, want true (symlink must resolve to the same dir)", link, real)
	}
	// Symmetry and self-equality.
	if !pathsEqual(real, link) {
		t.Errorf("pathsEqual is not symmetric for symlinked paths")
	}
	if !pathsEqual(real, real) {
		t.Errorf("pathsEqual(real, real) = false, want true")
	}
}

// TestBelongsToEcosystem_SymlinkedRoot is the belongsToEcosystem-level view of
// the same defect: when the ecosystem root is reached through a symlink, a
// worktree node whose ParentEcosystemPath names the real path must still be
// recognized as belonging — otherwise the active list empties and prune bails.
func TestBelongsToEcosystem_SymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "eco-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	root := &workspace.WorkspaceNode{Path: link}
	node := &workspace.WorkspaceNode{
		Path:                filepath.Join(real, ".grove-worktrees", "wt"),
		ParentEcosystemPath: real,
	}
	if !belongsToEcosystem(node, root) {
		t.Errorf("belongsToEcosystem returned false for a node under the symlink-resolved root; active list would empty and prune would bail")
	}
}

// TestEnumerateWorktreeStates_FiltersStatelessXDGDir is the live-path test for
// the XDG-orphan blind spot. Discovery (discoverXDGEcosystemWorktrees in
// core/pkg/workspace) promotes EVERY directory under the XDG worktrees base to
// a KindEcosystemWorktree node. This drives real discovery through
// EnumerateWorktreeStates and asserts a stateless dir (no marker, no .git) is
// NOT returned as an active worktree state, while a marker-bearing sibling is —
// so the stateless dir is left for prune to detect as a host-orphan.
func TestEnumerateWorktreeStates_FiltersStatelessXDGDir(t *testing.T) {
	sandboxXDG(t)

	// Isolate discovery from the real user environment: config + home live in
	// a private tmp tree, and cx repo discovery is disabled so DiscoverAll does
	// not scan the host's repos.
	rootDir := resolveTmp(t, t.TempDir())
	homeDir := filepath.Join(rootDir, "home")
	workDir := filepath.Join(rootDir, "work")
	globalConfigDir := filepath.Join(homeDir, ".config", "grove")
	if err := os.MkdirAll(globalConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir global config: %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("GROVE_CONFIG_OVERLAY", filepath.Join(globalConfigDir, "grove.yml"))

	emptyStr := ""
	globalCfg := config.Config{
		SearchPaths: map[string]config.SearchPathConfig{
			"work": {Path: workDir, Enabled: true},
		},
		Context: &config.ContextConfig{ReposDir: &emptyStr},
	}
	globalBytes, _ := yaml.Marshal(globalCfg)
	if err := os.WriteFile(filepath.Join(globalConfigDir, "grove.yml"), globalBytes, 0o644); err != nil {
		t.Fatalf("write global grove.yml: %v", err)
	}

	// The ecosystem root: a grove.yml with a 'workspaces' key.
	ecoRoot := filepath.Join(workDir, "my-eco")
	if err := os.MkdirAll(ecoRoot, 0o755); err != nil {
		t.Fatalf("mkdir eco root: %v", err)
	}
	ecoCfg := config.Config{Name: "my-eco", Workspaces: []string{"*"}}
	ecoBytes, _ := yaml.Marshal(ecoCfg)
	if err := os.WriteFile(filepath.Join(ecoRoot, "grove.yml"), ecoBytes, 0o644); err != nil {
		t.Fatalf("write eco grove.yml: %v", err)
	}

	// Two dirs under the ecosystem's XDG worktree base: one stateless (orphan),
	// one bearing the .grove/workspace marker (live).
	xdgBase := filepath.Join(paths.WorktreesDir(), workspace.DirIdentifier(ecoRoot))
	orphanDir := filepath.Join(xdgBase, "orphan-xdg")
	liveDir := filepath.Join(xdgBase, "live-xdg")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("mkdir orphan xdg: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(liveDir, ".grove"), 0o755); err != nil {
		t.Fatalf("mkdir live xdg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, ".grove", "workspace"), []byte("owner: my-eco\n"), 0o600); err != nil {
		t.Fatalf("write live marker: %v", err)
	}

	root, err := workspace.GetProjectByPath(ecoRoot)
	if err != nil || root == nil {
		t.Fatalf("GetProjectByPath(%s): node=%v err=%v", ecoRoot, root, err)
	}
	if !root.IsEcosystem() {
		t.Fatalf("discovered root is not an ecosystem: kind=%s", root.Kind)
	}

	states, err := EnumerateWorktreeStates(root)
	if err != nil {
		t.Fatalf("EnumerateWorktreeStates: %v", err)
	}

	names := map[string]bool{}
	for _, s := range states {
		if s.Workspace != nil {
			names[s.Workspace.Name] = true
		}
	}
	if !names["live-xdg"] {
		t.Errorf("expected the marker-bearing XDG worktree 'live-xdg' to be an active state; got %v", names)
	}
	if names["orphan-xdg"] {
		t.Errorf("stateless XDG dir 'orphan-xdg' must NOT be an active state (it would mask the host-orphan from prune); got %v", names)
	}
}

// resolveTmp resolves symlinks in a temp dir so a discovered path (which the
// discovery walker may canonicalize) matches the path this test computes
// DirIdentifier / XDG bases from. macOS temp dirs live under a symlinked
// /var → /private/var, so an unresolved path would mismatch.
func resolveTmp(t *testing.T, d string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		return d
	}
	return resolved
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
