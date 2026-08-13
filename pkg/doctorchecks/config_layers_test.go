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
	write(t, filepath.Join(groveDir, "sync.toml"), "server = \"https://sync.example.com\"\n[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")

	res := (&configLayersCheck{}).Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusOK {
		t.Fatalf("expected ok, got %s (%s / %s)", res.Status, res.Message, res.Error)
	}
}

func TestConfigLayersCheck_UsesStandaloneSyncLoader(t *testing.T) {
	groveDir := setupScratchConfig(t)
	path := filepath.Join(groveDir, "sync.toml")
	write(t, path, "[[workspaces]]\nname = \"registry\"\nrole = \"invented\"\n")

	res := (&configLayersCheck{}).Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusFail || !strings.Contains(res.Error, path) || !strings.Contains(res.Error, "invalid role") {
		t.Fatalf("typed sync diagnostic missing: %+v", res)
	}
}

func TestCollectLayerFiles_EnumeratesGlobalLayers(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "grove.toml"), "version = \"1.0\"\n")
	write(t, filepath.Join(groveDir, "frag.toml"), "[tui]\n")
	write(t, filepath.Join(groveDir, "roots.toml"), "[roots]\n")
	write(t, filepath.Join(groveDir, "notebooks.toml"), "[notebooks]\n")
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
	if kinds["roots.toml"] != "recorded code roots (standalone typed loader)" {
		t.Errorf("roots.toml kind = %q", kinds["roots.toml"])
	}
	if kinds["notebooks.toml"] != "recorded notebooks (standalone typed loader)" {
		t.Errorf("notebooks.toml kind = %q", kinds["notebooks.toml"])
	}
}

func TestConfigLayersCheck_UsesStrictRecordedParsers(t *testing.T) {
	for _, tc := range []struct{ name, file, content, want string }{
		{name: "roots unknown field", file: "roots.toml", content: "[roots.alpha]\npath = \"/tmp/a\"\ntyop = true\n", want: "strict mode"},
		{name: "roots missing path", file: "roots.toml", content: "[roots.alpha]\nenabled = true\nnotebook = \"main\"\n", want: "[roots.alpha] has no path"},
		// [notebooks.<n>.sync] stopped being a reserved empty table in P3 (core
		// c2ef2f2): it is typed now, with `share` as its one key, so the strict
		// diagnostic names the closed key set instead of the reservation.
		{name: "notebooks unknown sync key", file: "notebooks.toml", content: "[notebooks.main]\nroot = \"/tmp/n\"\n[notebooks.main.sync]\nmode = \"x\"\n", want: "accepts only share"},
		{name: "notebooks non-boolean share", file: "notebooks.toml", content: "[notebooks.main]\nroot = \"/tmp/n\"\n[notebooks.main.sync]\nshare = \"yes\"\n", want: "share must be a boolean"},
		{name: "duplicate notebook table", file: "notebooks.toml", content: "[notebooks.main]\nroot = \"/tmp/a\"\n[notebooks.main]\nroot = \"/tmp/b\"\n", want: "table main already exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groveDir := setupScratchConfig(t)
			path := filepath.Join(groveDir, tc.file)
			write(t, path, tc.content)
			res := (&configLayersCheck{}).Run(context.Background(), doctor.RunOptions{})
			if res.Status != doctor.StatusFail {
				t.Fatalf("expected fail, got %s: %+v", res.Status, res)
			}
			if !strings.Contains(res.Error, path) || !strings.Contains(res.Error, tc.want) {
				t.Errorf("strict diagnostic missing path/%q: %s", tc.want, res.Error)
			}
			if strings.Contains(res.Message, "silently skipped") {
				t.Errorf("stale skip wording remains: %s", res.Message)
			}
		})
	}
}
