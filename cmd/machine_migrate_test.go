package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// sandboxMachineConfig points paths.ConfigDir() at a temp dir. Every test in
// this file must use it: `grove machine migrate` writes config, and a test
// that resolved the real config dir would edit the developer's own machine.
func sandboxMachineConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir := filepath.Join(home, "config", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return dir
}

func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// The real ~/.config/grove/groves.toml shape: a priority-tagged fragment of
// [groves.<name>] tables carrying description/enabled/notebook/path.
func TestMachineMigrateRoundTripsTheRealGrovesFragment(t *testing.T) {
	dir := sandboxMachineConfig(t)
	code := t.TempDir()

	// An ecosystem (carries a manifest) and a plain directory of repos.
	writeAt(t, filepath.Join(code, "grovetools", "grove.toml"), "name = \"grovetools\"\nworkspaces = [\"*\"]\n")
	if err := os.MkdirAll(filepath.Join(code, "chickens"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeAt(t, filepath.Join(dir, "grove.toml"), "name = \"global\"\n")
	writeAt(t, filepath.Join(dir, "groves.toml"), `# Grove source directories
# Machine-specific paths for project discovery

[_grove]
priority = 80

[groves.chickens]
description = "chickens"
enabled = true
notebook = "nb"
path = "`+filepath.Join(code, "chickens")+`"

[groves.grovetools]
description = "Grove ecosystem tools"
enabled = true
notebook = "grovetools"
path = "`+filepath.Join(code, "grovetools")+`"

[groves.gone]
description = "not on this machine"
enabled = false
notebook = "nb"
path = "`+filepath.Join(code, "gone")+`"
`)

	var out bytes.Buffer
	if err := runMachineMigrate(&out, false, true); err != nil {
		t.Fatalf("runMachineMigrate: %v\n%s", err, out.String())
	}

	machineCfg, err := config.LoadMachineConfigFrom(filepath.Join(dir, "machine.toml"))
	if err != nil {
		t.Fatalf("LoadMachineConfigFrom: %v", err)
	}
	if machineCfg == nil {
		t.Fatalf("migrate wrote no machine.toml\n%s", out.String())
	}

	// Manifest present -> ecosystem subscription, with the notebook override
	// and description carried over.
	eco, ok := machineCfg.Machine.Ecosystems["grovetools"]
	if !ok {
		t.Fatalf("grovetools not migrated as an ecosystem: %+v", machineCfg.Machine)
	}
	if eco.Notebook != "grovetools" || eco.Description != "Grove ecosystem tools" {
		t.Fatalf("grovetools subscription lost fields: %+v", eco)
	}
	if eco.Path != filepath.Join(code, "grovetools") {
		t.Fatalf("grovetools path = %q", eco.Path)
	}

	// No manifest -> bare root. Both stay first-class.
	if root, ok := machineCfg.Machine.Roots["chickens"]; !ok || root.Notebook != "nb" {
		t.Fatalf("chickens not migrated as a root: %+v", machineCfg.Machine.Roots)
	}
	// Absent path -> conservatively a root, and the explicit disable survives.
	gone, ok := machineCfg.Machine.Roots["gone"]
	if !ok {
		t.Fatalf("absent-path grove not migrated as a root: %+v", machineCfg.Machine.Roots)
	}
	if gone.Enabled == nil || *gone.Enabled {
		t.Fatalf("enabled = false was not carried over: %+v", gone)
	}

	// The originals are annotated, not deleted — they keep winning until the
	// user removes them.
	fragment := readAt(t, filepath.Join(dir, "groves.toml"))
	if !strings.Contains(fragment, "grove machine migrate") {
		t.Fatalf("groves.toml was not annotated:\n%s", fragment)
	}
	if !strings.Contains(fragment, "[groves.grovetools]") {
		t.Fatalf("migrate deleted a [groves.*] entry; it must only annotate:\n%s", fragment)
	}
	if !strings.Contains(fragment, "priority = 80") {
		t.Fatalf("migrate lost unrelated content from groves.toml:\n%s", fragment)
	}

	// And behavior is unchanged: the legacy entries still win.
	config.ResetLoadCache()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	for _, name := range []string{"grovetools", "chickens", "gone"} {
		if _, ok := cfg.Groves[name]; !ok {
			t.Fatalf("grove %q vanished from the compiled config: %v", name, cfg.Groves)
		}
	}

	// Idempotent: a second run neither duplicates entries nor re-annotates.
	before := readAt(t, filepath.Join(dir, "machine.toml"))
	var out2 bytes.Buffer
	if err := runMachineMigrate(&out2, false, true); err != nil {
		t.Fatalf("second runMachineMigrate: %v", err)
	}
	if after := readAt(t, filepath.Join(dir, "machine.toml")); after != before {
		t.Fatalf("second migrate changed machine.toml:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if after := readAt(t, filepath.Join(dir, "groves.toml")); strings.Count(after, groveDeprecationNote) != 3 {
		t.Fatalf("second migrate re-annotated groves.toml:\n%s", after)
	}
}

// The dead ~/.config/grove/machines/ directory is never loaded by the cascade,
// so its groves are invisible until migrate imports them. The incumbent on the
// author's machine is a full grove Config in YAML.
func TestMachineMigrateImportsTheLegacyMachinesDir(t *testing.T) {
	dir := sandboxMachineConfig(t)
	code := t.TempDir()
	if err := os.MkdirAll(filepath.Join(code, "legacy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeAt(t, filepath.Join(dir, "grove.toml"), "name = \"global\"\n")
	writeAt(t, filepath.Join(dir, config.LegacyMachinesDirName, "solair.yml"), `agent:
  enabled: true
groves:
  code_root:
    description: Main code directory
    enabled: true
    notebook: nb
    path: `+filepath.Join(code, "legacy")+`
notebooks:
  definitions:
    nb:
      root_dir: ~/notebooks/nb
`)

	var out bytes.Buffer
	if err := runMachineMigrate(&out, false, true); err != nil {
		t.Fatalf("runMachineMigrate: %v\n%s", err, out.String())
	}

	machineCfg, err := config.LoadMachineConfigFrom(filepath.Join(dir, "machine.toml"))
	if err != nil || machineCfg == nil {
		t.Fatalf("LoadMachineConfigFrom: %v (%+v)", err, machineCfg)
	}
	root, ok := machineCfg.Machine.Roots["code_root"]
	if !ok {
		t.Fatalf("legacy machines/ grove not imported: %+v\n%s", machineCfg.Machine, out.String())
	}
	if root.Notebook != "nb" || root.Path != filepath.Join(code, "legacy") {
		t.Fatalf("imported root = %+v", root)
	}
	// Only the groves are imported; the rest of that file stays ignored.
	content := readAt(t, filepath.Join(dir, "machine.toml"))
	if strings.Contains(content, "agent") || strings.Contains(content, "notebooks") {
		t.Fatalf("migrate imported more than groves from the legacy file:\n%s", content)
	}
}

func TestMachineMigrateDryRunWritesNothing(t *testing.T) {
	dir := sandboxMachineConfig(t)
	writeAt(t, filepath.Join(dir, "grove.toml"), "name = \"global\"\n")
	writeAt(t, filepath.Join(dir, "groves.toml"), "[groves.x]\npath = \"/tmp/x\"\n")

	var out bytes.Buffer
	if err := runMachineMigrate(&out, true, true); err != nil {
		t.Fatalf("runMachineMigrate --dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "machine.toml")); !os.IsNotExist(err) {
		t.Fatalf("--dry-run created machine.toml (stat err = %v)", err)
	}
	if content := readAt(t, filepath.Join(dir, "groves.toml")); strings.Contains(content, "DEPRECATED") {
		t.Fatalf("--dry-run annotated the original:\n%s", content)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("--dry-run did not say so:\n%s", out.String())
	}
}

// A hand-authored machine.toml is never clobbered: migrate leaves existing
// subscriptions alone and preserves unrelated content byte for byte.
func TestMachineMigratePreservesAnExistingMachineConfig(t *testing.T) {
	dir := sandboxMachineConfig(t)
	code := t.TempDir()
	if err := os.MkdirAll(filepath.Join(code, "solutils"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeAt(t, filepath.Join(dir, "grove.toml"), "name = \"global\"\n")
	writeAt(t, filepath.Join(dir, "groves.toml"), `[groves.solutils]
path = "`+filepath.Join(code, "solutils")+`"
notebook = "from-groves"
`)
	writeAt(t, filepath.Join(dir, "machine.toml"), `# hand written, keep me
[machine]
name = "mbp"

[machine.roots.solutils]
path = "/hand/written"
notebook = "hand"
`)

	var out bytes.Buffer
	if err := runMachineMigrate(&out, false, true); err != nil {
		t.Fatalf("runMachineMigrate: %v", err)
	}

	machineCfg, err := config.LoadMachineConfigFrom(filepath.Join(dir, "machine.toml"))
	if err != nil {
		t.Fatalf("LoadMachineConfigFrom: %v", err)
	}
	if got := machineCfg.Machine.Roots["solutils"].Path; got != "/hand/written" {
		t.Fatalf("existing subscription overwritten: %q", got)
	}
	if machineCfg.Machine.Name != "mbp" {
		t.Fatalf("machine name lost: %q", machineCfg.Machine.Name)
	}
	if content := readAt(t, filepath.Join(dir, "machine.toml")); !strings.Contains(content, "# hand written, keep me") {
		t.Fatalf("comment lost:\n%s", content)
	}
	if !strings.Contains(out.String(), "already declared") {
		t.Fatalf("migrate did not report the skip:\n%s", out.String())
	}
}

func readAt(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
