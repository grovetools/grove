package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldEcosystemFresh: a missing directory gets the full skeleton —
// dir, TOML manifest (name/stock description/workspaces), .gitignore,
// README.md, and a git repository.
func TestScaffoldEcosystemFresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "eco")
	svc := NewService(false)

	if err := svc.ScaffoldEcosystem(dir, "eco", ManifestTOML); err != nil {
		t.Fatalf("ScaffoldEcosystem: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "grove.toml"))
	if err != nil {
		t.Fatalf("read grove.toml: %v", err)
	}
	for _, needle := range []string{`name = "eco"`, `description = "A Grove ecosystem"`, `workspaces = ["*"]`} {
		if !strings.Contains(string(manifest), needle) {
			t.Errorf("grove.toml missing %q:\n%s", needle, manifest)
		}
	}
	for _, f := range []string{".gitignore", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not scaffolded: %v", f, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		t.Errorf("git not initialized: %v", err)
	}
}

// TestScaffoldEcosystemYAML: the wizard's YAML format writes grove.yml.
func TestScaffoldEcosystemYAML(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "eco")
	if err := NewService(false).ScaffoldEcosystem(dir, "eco", ManifestYAML); err != nil {
		t.Fatalf("ScaffoldEcosystem: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "grove.yml"))
	if err != nil {
		t.Fatalf("read grove.yml: %v", err)
	}
	if !strings.Contains(string(manifest), "name: eco") {
		t.Errorf("grove.yml missing name:\n%s", manifest)
	}
	if _, err := os.Stat(filepath.Join(dir, "grove.toml")); err == nil {
		t.Error("YAML format also wrote grove.toml")
	}
}

// TestScaffoldEcosystemImportNoOp: a directory that already carries a grove
// manifest is left completely untouched (import mode — register-only).
func TestScaffoldEcosystemImportNoOp(t *testing.T) {
	dir := t.TempDir()
	seed := "name = \"custom\"\n"
	if err := os.WriteFile(filepath.Join(dir, "grove.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	if err := NewService(false).ScaffoldEcosystem(dir, "other-name", ManifestTOML); err != nil {
		t.Fatalf("ScaffoldEcosystem: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "grove.toml"))
	if err != nil {
		t.Fatalf("read grove.toml: %v", err)
	}
	if string(manifest) != seed {
		t.Errorf("existing manifest rewritten:\n%s", manifest)
	}
	for _, f := range []string{".gitignore", "README.md", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("import mode scaffolded %s", f)
		}
	}
}

// TestHasEcosystemManifest covers both manifest dialects and the negative.
func TestHasEcosystemManifest(t *testing.T) {
	dir := t.TempDir()
	if HasEcosystemManifest(dir) {
		t.Error("empty dir reported a manifest")
	}
	if HasEcosystemManifest(filepath.Join(dir, "missing")) {
		t.Error("missing dir reported a manifest")
	}
	if err := os.WriteFile(filepath.Join(dir, "grove.yml"), []byte("name: x\n"), 0o600); err != nil {
		t.Fatalf("seed grove.yml: %v", err)
	}
	if !HasEcosystemManifest(dir) {
		t.Error("grove.yml not detected")
	}
}

// TestDeriveEcosystemName pins the wizard's derivation rule and its
// degenerate-path fallback.
func TestDeriveEcosystemName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for input, want := range map[string]string{
		"~/Code":     "Code",
		"/tmp/foo":   "foo",
		"/tmp/foo/":  "foo",
		"/":          "my-projects",
		"":           "my-projects",
		".":          "my-projects",
		"  ~/Code  ": "Code",
	} {
		if got := DeriveEcosystemName(input); got != want {
			t.Errorf("DeriveEcosystemName(%q) = %q, want %q", input, got, want)
		}
	}
}
