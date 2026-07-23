package sdk

import (
	"path/filepath"
	"testing"
)

// TestGitViewerIsRegisteredForDelegation guards the generated registry: the
// flow → git-viewer handoff routes `grove git-viewer view` through the
// delegator, which only recognizes registered tools. Dropping git-viewer from
// a regeneration (its grove.toml must stay `managed = true`) would break the
// handoff with "unknown tool: git-viewer".
func TestGitViewerIsRegisteredForDelegation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	repoName, info, alias, found := FindTool("git-viewer")
	if !found {
		t.Fatal("git-viewer is absent from the generated tool registry")
	}
	if repoName != "git-viewer" || info.Alias != "git-viewer" || alias != "git-viewer" {
		t.Fatalf("git-viewer resolved oddly: repo=%q info=%+v alias=%q", repoName, info, alias)
	}
}
