package release

import (
	"os"
	"path/filepath"
	"testing"
)

// writeStagingTree seeds a staging dir with one repo carrying both a staged
// changelog and proposal run history, mirroring the live layout:
//
//	staging/notify/CHANGELOG.md
//	staging/notify/proposal/runs/x/PROPOSAL.md
func writeStagingTree(t *testing.T) string {
	t.Helper()
	staging := t.TempDir()
	repoDir := filepath.Join(staging, "notify")
	runDir := filepath.Join(repoDir, proposalDirName, "runs", "x")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "CHANGELOG.md"), []byte("changes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "PROPOSAL.md"), []byte("proposal"), 0o644); err != nil {
		t.Fatal(err)
	}
	return staging
}

// TestClearStagingDirPreservesProposals covers the default clear: everything
// under each repo's staging dir goes EXCEPT the proposal/ subtree (drafting
// run history awaiting review).
func TestClearStagingDirPreservesProposals(t *testing.T) {
	staging := writeStagingTree(t)

	if err := clearStagingDir(staging, false); err != nil {
		t.Fatalf("clearStagingDir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(staging, "notify", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Errorf("staged changelog should be removed, stat err = %v", err)
	}
	kept := filepath.Join(staging, "notify", proposalDirName, "runs", "x", "PROPOSAL.md")
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("proposal run history should be preserved, stat err = %v", err)
	}
}

// TestClearStagingDirIncludeProposals covers the full wipe: --include-proposals
// removes the entire staging tree, proposal history included.
func TestClearStagingDirIncludeProposals(t *testing.T) {
	staging := writeStagingTree(t)

	if err := clearStagingDir(staging, true); err != nil {
		t.Fatalf("clearStagingDir: %v", err)
	}

	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("full wipe should remove the staging tree, stat err = %v", err)
	}
}

// TestClearStagingDirRemovesRepoWithoutProposals asserts a repo dir with no
// proposal history disappears entirely on the default clear (no empty husks),
// and a missing staging dir is a no-op.
func TestClearStagingDirRemovesRepoWithoutProposals(t *testing.T) {
	staging := t.TempDir()
	repoDir := filepath.Join(staging, "flow")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "CHANGELOG.md"), []byte("changes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := clearStagingDir(staging, false); err != nil {
		t.Fatalf("clearStagingDir: %v", err)
	}
	if _, err := os.Stat(repoDir); !os.IsNotExist(err) {
		t.Errorf("repo dir without proposals should be removed, stat err = %v", err)
	}

	// Missing staging dir: no error.
	if err := clearStagingDir(filepath.Join(staging, "does-not-exist"), false); err != nil {
		t.Fatalf("missing staging dir should be a no-op, got %v", err)
	}
}
