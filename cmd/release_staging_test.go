package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/grove/pkg/release"
)

// TestGetStagingDirPathUnderStateDir asserts the staging dir is unified under
// paths.StateDir()/release/staging (the same tree clear-plan removes), not the
// legacy ~/.grove/release_staging location.
func TestGetStagingDirPathUnderStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)

	got, err := getStagingDirPath()
	if err != nil {
		t.Fatalf("getStagingDirPath: %v", err)
	}
	want := filepath.Join(paths.StateDir(), "release", "staging")
	if got != want {
		t.Fatalf("staging dir = %q, want %q", got, want)
	}
	// Must live under the release state dir that ClearPlan cleans.
	if rel := filepath.Join(paths.StateDir(), "release"); filepath.Dir(got) != rel {
		t.Fatalf("staging parent = %q, want %q", filepath.Dir(got), rel)
	}
}

// TestGetStagingDirPathMigratesLegacy asserts legacy ~/.grove/release_staging
// content is migrated into the unified location on first resolve, so nothing is
// orphaned.
func TestGetStagingDirPathMigratesLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	// UserHomeDir drives the legacy path; point HOME at a temp dir too.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	legacy := filepath.Join(fakeHome, ".grove", "release_staging", "notify")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "CHANGELOG.md")
	if err := os.WriteFile(marker, []byte("legacy content"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := getStagingDirPath()
	if err != nil {
		t.Fatalf("getStagingDirPath: %v", err)
	}

	migrated := filepath.Join(got, "notify", "CHANGELOG.md")
	data, err := os.ReadFile(migrated)
	if err != nil {
		t.Fatalf("expected migrated changelog at %s: %v", migrated, err)
	}
	if string(data) != "legacy content" {
		t.Fatalf("migrated content = %q, want %q", data, "legacy content")
	}
	// Legacy dir should be gone after the rename.
	if _, err := os.Stat(filepath.Join(fakeHome, ".grove", "release_staging")); !os.IsNotExist(err) {
		t.Fatalf("legacy staging dir should have been migrated away, stat err = %v", err)
	}
}

// TestAutoApproveStagedRepos exercises the --auto-approve gate: only selected
// repos with staged gen output are approved; unstaged or unselected repos are
// left as-is.
func TestAutoApproveStagedRepos(t *testing.T) {
	plan := &release.ReleasePlan{
		Repos: map[string]*release.RepoReleasePlan{
			"docs-and-cl": {Selected: true, DocsGenerated: true, ChangelogStaged: true, Status: "Pending Review"},
			"cl-only":     {Selected: true, ChangelogStaged: true, Status: "Pending Review"},
			"docs-only":   {Selected: true, DocsGenerated: true, Status: "Pending Review"},
			"nothing":     {Selected: true, Status: "Pending Review"},
			"unselected":  {Selected: false, DocsGenerated: true, ChangelogStaged: true, Status: "Pending Review"},
		},
	}

	approved := autoApproveStagedRepos(plan)

	wantApproved := map[string]bool{"docs-and-cl": true, "cl-only": true, "docs-only": true}
	if len(approved) != len(wantApproved) {
		t.Fatalf("approved = %v, want keys %v", approved, wantApproved)
	}
	for _, name := range approved {
		if !wantApproved[name] {
			t.Fatalf("unexpected auto-approved repo %q", name)
		}
		if plan.Repos[name].Status != "Approved" {
			t.Fatalf("repo %q status = %q, want Approved", name, plan.Repos[name].Status)
		}
	}
	// Sorted output.
	for i := 1; i < len(approved); i++ {
		if approved[i-1] > approved[i] {
			t.Fatalf("approved not sorted: %v", approved)
		}
	}
	// Untouched repos keep their prior status.
	if plan.Repos["nothing"].Status != "Pending Review" {
		t.Fatalf("repo with no staged output was modified: %q", plan.Repos["nothing"].Status)
	}
	if plan.Repos["unselected"].Status != "Pending Review" {
		t.Fatalf("unselected repo was modified: %q", plan.Repos["unselected"].Status)
	}
}
