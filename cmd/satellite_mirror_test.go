package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// TestResolveRemoteCodeDirPrecedence pins the configurable remote root: an
// explicit flag beats the target's declared code_dir, which beats the path this
// CLI's own provisioning uses. Nothing in the mirror hardcodes a root any more.
func TestResolveRemoteCodeDirPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		flagValue  string
		flagSet    bool
		configured string
		want       string
	}{
		{"nothing set falls back to the bootstrap root", "", false, "", bootstrapRemoteCodeDir},
		{"config supplies the root", "", false, "~/src/peer", "~/src/peer"},
		{"blank config is not a value", "", false, "   ", bootstrapRemoteCodeDir},
		{"explicit flag wins over config", "/srv/eco", true, "~/src/peer", "/srv/eco"},
		{"flag typed as the legacy default still wins", bootstrapRemoteCodeDir, true, "~/src/peer", bootstrapRemoteCodeDir},
		{"pre-seeded value without cobra", "~/other", false, "", "~/other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRemoteCodeDir(tc.flagValue, tc.flagSet, tc.configured)
			if err != nil {
				t.Fatalf("resolveRemoteCodeDir: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveRemoteCodeDir = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestValidateRemoteCodeDir: the value is interpolated into generated remote
// shell, so shell-active characters and relative paths are refused at the
// boundary. Ordinary paths — including the ones the E2E harness passes — stay
// legal.
func TestValidateRemoteCodeDir(t *testing.T) {
	ok := []string{
		"~",
		"~/code/grovetools",
		"~/code/tend-sat-sim-a1b2c3",
		"/home/u/code/grovetools",
		"/home/u/.local/share/grove/worktrees/grovetools-ab12cd34/plan-x",
		"~/code/my ecosystem",
	}
	for _, dir := range ok {
		if err := validateRemoteCodeDir(dir); err != nil {
			t.Errorf("validateRemoteCodeDir(%q) = %v, want nil", dir, err)
		}
	}
	bad := map[string]string{
		"":                     "empty",
		"code/grovetools":      "absolute",
		"~/code/$(id)":         "unsafe",
		"~/code/x\"; rm -rf /": "unsafe",
		"~/code/`id`":          "unsafe",
		"~/code/../../etc":     "..",
	}
	for dir, want := range bad {
		err := validateRemoteCodeDir(dir)
		if err == nil {
			t.Errorf("validateRemoteCodeDir(%q) = nil, want an error mentioning %q", dir, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validateRemoteCodeDir(%q) = %v, want an error mentioning %q", dir, err, want)
		}
	}
}

// TestResolveRemoteCodeDirRejectsUnsafeConfig: a bad value in config fails the
// verb rather than reaching a generated script.
func TestResolveRemoteCodeDirRejectsUnsafeConfig(t *testing.T) {
	if _, err := resolveRemoteCodeDir("", false, "~/code/$(whoami)"); err == nil {
		t.Fatal("an unsafe configured code_dir must not resolve")
	}
	if _, err := resolveRemoteCodeDir("relative/path", true, ""); err == nil {
		t.Fatal("a relative --remote-code-dir must not resolve")
	}
}

// TestRemoteCodeDirExprHonorsResolvedRoots keeps the resolver and the script
// generator agreed: whatever resolution returns is what lands in the remote
// scripts, ~ expanded to $HOME.
func TestRemoteCodeDirExprHonorsResolvedRoots(t *testing.T) {
	dir, err := resolveRemoteCodeDir("", false, "~/src/peer")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := remoteCodeDirExpr(dir), `"$HOME/src/peer"`; got != want {
		t.Errorf("remoteCodeDirExpr = %s, want %s", got, want)
	}
	if got, want := remoteCodeDirExpr("/srv/eco"), `"/srv/eco"`; got != want {
		t.Errorf("remoteCodeDirExpr = %s, want %s", got, want)
	}
}

// TestSatelliteReposOptionsCodeDirDecodes proves the new key rides the existing
// CLI-only [satellites.<name>.repos] subtable (kept out of the registry table
// so it cannot look like a daemon row).
func TestSatelliteReposOptionsCodeDirDecodes(t *testing.T) {
	configDir := setupGroveHome(t)
	content := `[satellites.sat1.repos]
workspaces = ["grove", "core"]
code_dir = "~/src/peer"

[satellites.other.sync]
workspaces = ["cloud"]
`
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts, err := satelliteReposOptionsFromConfig(cfg, "sat1")
	if err != nil {
		t.Fatalf("satelliteReposOptionsFromConfig: %v", err)
	}
	if opts.CodeDir != "~/src/peer" {
		t.Errorf("CodeDir = %q, want ~/src/peer", opts.CodeDir)
	}
	if len(opts.Workspaces) != 2 {
		t.Errorf("Workspaces = %v, want the two declared repos", opts.Workspaces)
	}
	// An entry without the key resolves to the provisioning default.
	empty, err := satelliteReposOptionsFromConfig(cfg, "other")
	if err != nil {
		t.Fatalf("satelliteReposOptionsFromConfig(other): %v", err)
	}
	got, err := resolveRemoteCodeDir("", false, empty.CodeDir)
	if err != nil || got != bootstrapRemoteCodeDir {
		t.Errorf("unset code_dir = %q (%v), want %q", got, err, bootstrapRemoteCodeDir)
	}
}
