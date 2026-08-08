package doctorchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
)

func TestLegacyMachinesDirCheck_OKWithoutTheDeadDirectory(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "grove.toml"), "version = \"1.0\"\n")

	res := (&legacyMachinesDirCheck{}).Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusOK {
		t.Fatalf("expected ok, got %s (%s)", res.Status, res.Message)
	}
}

func TestLegacyMachinesDirCheck_WarnsAndNamesStrandedFiles(t *testing.T) {
	groveDir := setupScratchConfig(t)
	write(t, filepath.Join(groveDir, "grove.toml"), "version = \"1.0\"\n")
	machinesDir := filepath.Join(groveDir, config.LegacyMachinesDirName)
	if err := os.MkdirAll(machinesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(machinesDir, "laptop.yml"), "groves:\n  code:\n    path: ~/Code\n")
	write(t, filepath.Join(machinesDir, "notes.txt"), "not a config\n")

	res := (&legacyMachinesDirCheck{}).Run(context.Background(), doctor.RunOptions{})
	if res.Status != doctor.StatusWarn {
		t.Fatalf("expected warn, got %s (%s)", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, machinesDir) {
		t.Errorf("expected the message to name the directory, got: %s", res.Message)
	}
	if !strings.Contains(res.Error, "laptop.yml") {
		t.Errorf("expected the stranded config file to be named, got: %s", res.Error)
	}
	if strings.Contains(res.Error, "notes.txt") {
		t.Errorf("only config files are stranded config; got: %s", res.Error)
	}
	if !strings.Contains(res.Resolution, "grove machine migrate") {
		t.Errorf("expected the resolution to name the migration command, got: %s", res.Resolution)
	}
}
