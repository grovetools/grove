package doctorchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/doctor"
)

// setupScratchConfig points XDG_CONFIG_HOME at a scratch dir and returns the
// grove config dir inside it.
func setupScratchConfig(t *testing.T) string {
	t.Helper()
	scratch := t.TempDir()
	configHome := filepath.Join(scratch, "config")
	groveDir := filepath.Join(configHome, "grove")
	if err := os.MkdirAll(groveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROVE_HOME", "")
	t.Setenv("GROVE_CONFIG_OVERLAY", "")
	t.Setenv("XDG_CONFIG_HOME", configHome)

	workDir := filepath.Join(scratch, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workDir)
	return groveDir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigLayersCheck_FlagsBrokenFragment(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "grove.toml"), "version = \"1.0\"\n")
	write(t, filepath.Join(groveDir, "10-broken.toml"), "[notebooks.definitions.main\nroot_dir = \"~/notes\"\n")
	write(t, filepath.Join(groveDir, "20-good.toml"), "[tui]\ntheme = \"kanagawa\"\n")

	res := (&configLayersCheck{}).Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusFail {
		t.Fatalf("expected fail, got %s (%s)", res.Status, res.Message)
	}
	if !strings.Contains(res.Error, "10-broken.toml") {
		t.Errorf("expected error to name the broken fragment, got: %s", res.Error)
	}
}

func TestConfigLayersCheck_PassesOnCleanConfig(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "grove.toml"), "version = \"1.0\"\n")
	write(t, filepath.Join(groveDir, "20-good.toml"), "[tui]\ntheme = \"kanagawa\"\n")

	res := (&configLayersCheck{}).Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusOK {
		t.Fatalf("expected ok, got %s (%s / %s)", res.Status, res.Message, res.Error)
	}
}

func TestCollectLayerFiles_EnumeratesGlobalLayers(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "grove.toml"), "version = \"1.0\"\n")
	write(t, filepath.Join(groveDir, "frag.toml"), "[tui]\n")
	write(t, filepath.Join(groveDir, "grove.override.toml"), "version = \"1.0\"\n")

	cwd, _ := os.Getwd()
	files := collectLayerFiles(cwd)

	kinds := map[string]string{}
	for _, f := range files {
		kinds[filepath.Base(f.Path)] = f.Kind
	}
	if kinds["grove.toml"] != "global config" {
		t.Errorf("grove.toml kind = %q", kinds["grove.toml"])
	}
	if kinds["frag.toml"] != "global fragment" {
		t.Errorf("frag.toml kind = %q", kinds["frag.toml"])
	}
	if kinds["grove.override.toml"] != "global override" {
		t.Errorf("grove.override.toml kind = %q", kinds["grove.override.toml"])
	}
}
