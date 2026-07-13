package cmd

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/grove/cmd/satelliteassets"
)

// TestSatelliteAssetsEmbeddedCompleteness pins what ships inside the binary:
// the bootstrap script is non-empty and starts with a shebang, the gcp
// terraform module carries its load-bearing files, and NO local terraform
// artifact (tfstate/tfvars/.terraform*) leaked into the embed.
func TestSatelliteAssetsEmbeddedCompleteness(t *testing.T) {
	script, err := satelliteassets.BootstrapScript()
	if err != nil {
		t.Fatalf("BootstrapScript: %v", err)
	}
	if len(script) == 0 {
		t.Fatal("embedded bootstrap script is empty")
	}
	if !strings.HasPrefix(string(script), "#!") {
		t.Fatalf("embedded bootstrap script does not start with a shebang: %q...", string(script[:20]))
	}

	tfFS, err := satelliteassets.TerraformFS("gcp")
	if err != nil {
		t.Fatalf("TerraformFS(gcp): %v", err)
	}
	for _, f := range []string{"variables.tf", "main.tf", "outputs.tf", "startup.sh.tpl"} {
		data, err := fs.ReadFile(tfFS, f)
		if err != nil {
			t.Errorf("embedded gcp module missing %s: %v", f, err)
		} else if len(data) == 0 {
			t.Errorf("embedded gcp module %s is empty", f)
		}
	}

	if err := fs.WalkDir(tfFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isSatelliteLocalTFArtifact(filepath.Base(p)) {
			t.Errorf("local terraform artifact leaked into the embed: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestResolveSatelliteTarget pins target resolution: empty defaults to gcp,
// gcp resolves, and an unknown target errors while listing the embedded
// targets (the enumeration comes from the embedded FS itself).
func TestResolveSatelliteTarget(t *testing.T) {
	if got, err := resolveSatelliteTarget(""); err != nil || got != "gcp" {
		t.Fatalf("resolveSatelliteTarget(\"\") = (%q, %v), want (gcp, nil)", got, err)
	}
	if got, err := resolveSatelliteTarget("gcp"); err != nil || got != "gcp" {
		t.Fatalf("resolveSatelliteTarget(gcp) = (%q, %v), want (gcp, nil)", got, err)
	}
	_, err := resolveSatelliteTarget("aws")
	if err == nil {
		t.Fatal("resolveSatelliteTarget(aws) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "gcp") {
		t.Fatalf("unknown-target error does not list the embedded targets: %v", err)
	}

	if got := satelliteassets.Targets(); len(got) != 1 || got[0] != "gcp" {
		t.Fatalf("Targets() = %v, want [gcp]", got)
	}
}

// TestExtractSatelliteTerraform covers the extraction rules: a fresh dir gets
// the full module; a re-extract overwrites drifted module files (they version
// with the binary) but never touches the satellite's own
// terraform.tfstate*/terraform.tfvars/.terraform* artifacts.
func TestExtractSatelliteTerraform(t *testing.T) {
	dir := t.TempDir()

	if err := extractSatelliteTerraform("gcp", dir); err != nil {
		t.Fatalf("extractSatelliteTerraform (fresh): %v", err)
	}
	wantMain, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatalf("main.tf not extracted: %v", err)
	}
	for _, f := range []string{"variables.tf", "outputs.tf", "startup.sh.tpl"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not extracted: %v", f, err)
		}
	}

	// Drift the module + plant local artifacts, then re-extract.
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# stale module from an older binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]string{
		"terraform.tfstate":        `{"serial": 7}`,
		"terraform.tfstate.backup": `{"serial": 6}`,
		"terraform.tfvars":         "project_id = \"p\"\n",
		".terraform.lock.hcl":      "# lock\n",
	}
	for f, content := range artifacts {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	providerDir := filepath.Join(dir, ".terraform", "providers")
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractSatelliteTerraform("gcp", dir); err != nil {
		t.Fatalf("extractSatelliteTerraform (re-extract): %v", err)
	}
	gotMain, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMain) != string(wantMain) {
		t.Error("re-extract did not overwrite a drifted main.tf with the embedded module")
	}
	for f, content := range artifacts {
		got, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("re-extract removed %s: %v", f, err)
		}
		if string(got) != content {
			t.Errorf("re-extract modified %s: %q", f, got)
		}
	}
	if _, err := os.Stat(filepath.Join(providerDir, "marker")); err != nil {
		t.Errorf("re-extract disturbed .terraform/: %v", err)
	}
	// No temp files may survive the atomic writes.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// TestResolveSatelliteTerraformDir pins the dir contract: without --tf-dir
// each satellite gets its own extracted module under the state dir
// (per-name isolation — the fix for the shared-dir tfstate collision); with
// --tf-dir the given dir is used as-is and NO extraction happens.
func TestResolveSatelliteTerraformDir(t *testing.T) {
	home := setupGroveHome(t)
	t.Chdir(t.TempDir()) // isolate the legacy-migration cwd walk

	dir1, err := resolveSatelliteTerraformDir("sat1", "gcp", "", io.Discard)
	if err != nil {
		t.Fatalf("resolveSatelliteTerraformDir sat1: %v", err)
	}
	dir2, err := resolveSatelliteTerraformDir("sat2", "gcp", "", io.Discard)
	if err != nil {
		t.Fatalf("resolveSatelliteTerraformDir sat2: %v", err)
	}
	if dir1 == dir2 {
		t.Fatalf("per-name dirs collide: %s", dir1)
	}
	stateRoot := filepath.Join(filepath.Dir(home), "..", "state") // GROVE_HOME/state
	for name, dir := range map[string]string{"sat1": dir1, "sat2": dir2} {
		want := filepath.Join(stateRoot, "grove", "satellites", name, "terraform")
		if abs, _ := filepath.Abs(want); dir != abs {
			t.Errorf("dir for %s = %s, want %s", name, dir, abs)
		}
		if _, err := os.Stat(filepath.Join(dir, "variables.tf")); err != nil {
			t.Errorf("module not extracted into %s: %v", dir, err)
		}
	}

	// --tf-dir: BYO dir used as-is, nothing extracted anywhere.
	byo := t.TempDir()
	got, err := resolveSatelliteTerraformDir("sat3", "gcp", byo, io.Discard)
	if err != nil {
		t.Fatalf("resolveSatelliteTerraformDir --tf-dir: %v", err)
	}
	if abs, _ := filepath.Abs(byo); got != abs {
		t.Fatalf("--tf-dir resolved to %s, want %s", got, abs)
	}
	if entries, _ := os.ReadDir(byo); len(entries) != 0 {
		t.Fatalf("--tf-dir dir was written to: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "grove", "satellites", "sat3")); !os.IsNotExist(err) {
		t.Fatalf("--tf-dir still created a per-name state dir (stat err %v)", err)
	}
}

// TestExtractSatelliteBootstrap pins the bootstrap extraction: the embedded
// script lands executable in the per-satellite state dir (bash needs a real
// file path — stdin is reserved for secrets) and matches the embed.
func TestExtractSatelliteBootstrap(t *testing.T) {
	setupGroveHome(t)
	path, err := extractSatelliteBootstrap("sat1")
	if err != nil {
		t.Fatalf("extractSatelliteBootstrap: %v", err)
	}
	if !strings.Contains(path, filepath.Join("satellites", "sat1", "bootstrap")) {
		t.Fatalf("bootstrap path %s not under the per-satellite state dir", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("bootstrap script not executable: %v", info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := satelliteassets.BootstrapScript()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("extracted bootstrap differs from the embedded script")
	}
}

// writeLegacyTerraformTree builds a fake pre-embed ecosystem worktree: a
// go.work root with cloud/poc/grove-satellite/terraform holding a tfstate +
// the tfvars the old `up` wrote (vm_name attributes the state to a name).
func writeLegacyTerraformTree(t *testing.T, vmName string) (root, legacyTF string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyTF = filepath.Join(root, "cloud", "poc", "grove-satellite", "terraform")
	if err := os.MkdirAll(legacyTF, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"terraform.tfstate":        `{"serial": 42}`,
		"terraform.tfstate.backup": `{"serial": 41}`,
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(legacyTF, f), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeSatelliteTFVars(legacyTF, "my-proj", "solair", "203.0.113.7/32", vmName, "", ""); err != nil {
		t.Fatal(err)
	}
	return root, legacyTF
}

// TestMigrateLegacySatelliteTFState covers `down`/`up`'s one-time adoption of
// worktree tfstate: from a subdirectory of a go.work ecosystem root, the
// matching satellite's tfstate* + tfvars are COPIED into the per-name dir
// (originals kept as backup, module extracted alongside), a name the legacy
// tfvars does not carry is never adopted, and an already-populated per-name
// dir is left alone.
func TestMigrateLegacySatelliteTFState(t *testing.T) {
	setupGroveHome(t)
	root, legacyTF := writeLegacyTerraformTree(t, "grove-satellite")
	// Run from a nested dir to exercise the walk-up (mirrors running from
	// e.g. <worktree>/daemon).
	nested := filepath.Join(root, "daemon", "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	var notice strings.Builder
	dir, err := resolveSatelliteTerraformDir("grove-satellite", "gcp", "", &notice)
	if err != nil {
		t.Fatalf("resolveSatelliteTerraformDir: %v", err)
	}

	// tfstate, backup, and tfvars copied; module extracted alongside.
	for f, want := range map[string]string{
		"terraform.tfstate":        `{"serial": 42}`,
		"terraform.tfstate.backup": `{"serial": 41}`,
	} {
		got, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("%s not migrated: %v", f, err)
		}
		if string(got) != want {
			t.Errorf("migrated %s = %q, want %q", f, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, satelliteTFVarsName)); err != nil {
		t.Fatalf("terraform.tfvars not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "variables.tf")); err != nil {
		t.Fatalf("module not extracted into the migrated dir: %v", err)
	}
	// Originals remain (backup), and the user is told about both halves.
	for _, f := range []string{"terraform.tfstate", "terraform.tfstate.backup", satelliteTFVarsName} {
		if _, err := os.Stat(filepath.Join(legacyTF, f)); err != nil {
			t.Errorf("legacy original %s removed by migration: %v", f, err)
		}
	}
	for _, want := range []string{"Migrating", legacyTF, dir, "BACKUP"} {
		if !strings.Contains(notice.String(), want) {
			t.Errorf("migration notice missing %q:\n%s", want, notice.String())
		}
	}

	// Re-resolve: per-name dir already has tfstate → legacy is NOT re-copied.
	if err := os.WriteFile(filepath.Join(legacyTF, "terraform.tfstate"), []byte(`{"serial": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSatelliteTerraformDir("grove-satellite", "gcp", "", io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"serial": 42}` {
		t.Errorf("second resolve re-copied legacy tfstate over the per-name state: %q", got)
	}

	// A different satellite name never adopts the shared legacy state.
	otherDir, err := resolveSatelliteTerraformDir("other-sat", "gcp", "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(otherDir, "terraform.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("name mismatch still migrated legacy tfstate (stat err %v)", err)
	}
}

// TestLegacyTFStateMatchGuards pins the attribution guard directly: no
// tfstate, no tfvars, unparsable tfvars, or a different vm_name all refuse
// the match.
func TestLegacyTFStateMatchGuards(t *testing.T) {
	dir := t.TempDir()
	if legacyTFStateMatchesSatellite(dir, "sat1") {
		t.Error("empty dir matched")
	}
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if legacyTFStateMatchesSatellite(dir, "sat1") {
		t.Error("tfstate without tfvars matched (state cannot be attributed)")
	}
	if err := os.WriteFile(filepath.Join(dir, satelliteTFVarsName), []byte("not = valid = hcl {{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if legacyTFStateMatchesSatellite(dir, "sat1") {
		t.Error("unparsable tfvars matched")
	}
	if err := writeSatelliteTFVars(dir, "p", "u", "203.0.113.7/32", "sat1", "", ""); err != nil {
		t.Fatal(err)
	}
	if !legacyTFStateMatchesSatellite(dir, "sat1") {
		t.Error("matching vm_name refused")
	}
	if legacyTFStateMatchesSatellite(dir, "sat2") {
		t.Error("vm_name mismatch matched")
	}
}

// TestMergeInfraTarget pins the --target flag>config precedence and the
// write-back rendering of the target field.
func TestMergeInfraTarget(t *testing.T) {
	cfg := satelliteInfraConfig{Target: "gcp", Project: "p"}
	if got := mergeInfra(cfg, infraFlagOverrides{}); got.Target != "gcp" {
		t.Errorf("unset flag clobbered config target: %+v", got)
	}
	if got := mergeInfra(cfg, infraFlagOverrides{Target: "aws", TargetSet: true}); got.Target != "aws" {
		t.Errorf("set flag did not win: %+v", got)
	}
	if got := mergeInfra(cfg, infraFlagOverrides{Target: "", TargetSet: true}); got.Target != "" {
		t.Errorf("set-to-empty flag did not clear config target: %+v", got)
	}

	rendered := renderSatelliteInfraTOML("sat1", satelliteInfraConfig{Target: "gcp", Project: "p"})
	if !strings.Contains(rendered, "target = \"gcp\"") {
		t.Errorf("write-back render missing target:\n%s", rendered)
	}
	rendered = renderSatelliteInfraTOML("sat1", satelliteInfraConfig{Project: "p"})
	if strings.Contains(rendered, "target") {
		t.Errorf("empty target must be omitted from the write-back:\n%s", rendered)
	}
}
