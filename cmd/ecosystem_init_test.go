package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// TestEcosystemManifestScaffoldDefaultsToTOML pins the dialect a new ecosystem
// gets: grove.toml unless the caller asks for YAML. The default is the whole
// point — an ecosystem scaffolded as YAML is one every later `grove` read has
// to keep supporting.
func TestEcosystemManifestScaffoldDefaultsToTOML(t *testing.T) {
	for _, format := range []string{"", "toml", "TOML", " toml "} {
		name, content, err := ecosystemManifestScaffold(format, "solab")
		if err != nil {
			t.Fatalf("format %q: %v", format, err)
		}
		if name != "grove.toml" {
			t.Errorf("format %q: manifest = %q, want grove.toml", format, name)
		}
		if !strings.Contains(content, `name = "solab"`) || !strings.Contains(content, `workspaces = ["*"]`) {
			t.Errorf("format %q: content = %q, want TOML name/workspaces", format, content)
		}
		// The scaffold has to survive the config loader, not just look like TOML.
		dir := t.TempDir()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("format %q: loading the scaffold: %v", format, err)
		}
		if cfg.Name != "solab" || len(cfg.Workspaces) != 1 || cfg.Workspaces[0] != "*" {
			t.Errorf("format %q: parsed = %+v, want name=solab workspaces=[*]", format, cfg)
		}
	}
}

func TestEcosystemManifestScaffoldYAML(t *testing.T) {
	for _, format := range []string{"yaml", "yml", "YAML"} {
		name, content, err := ecosystemManifestScaffold(format, "solab")
		if err != nil {
			t.Fatalf("format %q: %v", format, err)
		}
		if name != "grove.yml" {
			t.Errorf("format %q: manifest = %q, want grove.yml", format, name)
		}
		if !strings.Contains(content, "name: solab") {
			t.Errorf("format %q: content = %q, want a YAML name", format, content)
		}
	}
}

func TestEcosystemManifestScaffoldRejectsUnknownFormat(t *testing.T) {
	if _, _, err := ecosystemManifestScaffold("json", "solab"); err == nil {
		t.Fatal("expected an error for an unknown --format")
	} else if !strings.Contains(err.Error(), "toml") {
		t.Errorf("error = %v, want it to name the valid formats", err)
	}
}

// TestRunEcosystemInitRefusesAnExistingManifest covers both dialects: the guard
// used to check grove.yml only, so a `grove.toml` ecosystem could be
// re-scaffolded on top of.
func TestRunEcosystemInitRefusesAnExistingManifest(t *testing.T) {
	for _, manifest := range []string{"grove.toml", "grove.yml"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, manifest), []byte("# hand-authored\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		withEcosystemInitFlags(t, false, "toml")
		err := runEcosystemInit(nil, []string{dir})
		if err == nil {
			t.Fatalf("%s: expected an error for a directory that is already an ecosystem", manifest)
		}
		if !strings.Contains(err.Error(), manifest) {
			t.Errorf("%s: error = %v, want it to name the existing manifest", manifest, err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, manifest)); string(got) != "# hand-authored\n" {
			t.Errorf("%s: the existing manifest was rewritten: %q", manifest, got)
		}
	}
}

// TestRunEcosystemInitRejectsBadFormatBeforeWriting: a rejected --format must
// not leave a half-created ecosystem directory behind.
func TestRunEcosystemInitRejectsBadFormatBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "eco")
	withEcosystemInitFlags(t, false, "json")
	if err := runEcosystemInit(nil, []string{target}); err == nil {
		t.Fatal("expected an error for an unknown --format")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the target directory was created despite the bad format (stat err = %v)", err)
	}
}

// withEcosystemInitFlags sets the command's package-level flag vars for the
// duration of a test and restores them afterwards (cobra flag vars are process
// state).
func withEcosystemInitFlags(t *testing.T, goSupport bool, format string) {
	t.Helper()
	oldGo, oldFormat := ecosystemInitGo, ecosystemInitFormat
	t.Cleanup(func() {
		ecosystemInitGo, ecosystemInitFormat = oldGo, oldFormat
	})
	ecosystemInitGo, ecosystemInitFormat = goSupport, format
}
