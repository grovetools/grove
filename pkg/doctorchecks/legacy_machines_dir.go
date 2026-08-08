package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
)

func init() {
	doctor.Register(&legacyMachinesDirCheck{})
}

// legacyMachinesDirCheck reports the dead ~/.config/grove/machines/ directory.
// Nothing in it is ever loaded, so whatever it declares is invisible to every
// grove surface until `grove machine migrate` imports it.
//
// This check is where the condition BELONGS. Config load used to warn about it
// instead, and every attempt to make that quiet failed the same way: the
// directory is a standing condition, true on every load of every binary until
// the operator acts, while grove runs processes constantly (hooks, status
// polls, daemons, TUI refreshes). Per-load dedupe left one line per process;
// making it structured-only just moved the pile from the console into the
// workspace logs — hundreds of identical WARNING lines. Doctor reports state on
// demand, so one unmigrated directory is exactly one line, when someone asks.
//
// REPORT-ONLY: the files are the operator's, and migrating them rewrites
// config. `grove machine migrate` does that, deliberately and visibly.
type legacyMachinesDirCheck struct{}

func (c *legacyMachinesDirCheck) ID() string   { return "legacy_machines_dir" }
func (c *legacyMachinesDirCheck) Name() string { return "no dead machines/ config directory" }

func (c *legacyMachinesDirCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	dir := config.LegacyMachinesDir()
	if dir == "" {
		res.Status = doctor.StatusOK
		res.Message = "machine intent lives in machine.toml; no legacy machines/ directory"
		return res
	}

	res.Status = doctor.StatusWarn
	res.Message = fmt.Sprintf("%s exists but is never loaded", dir)
	if names := legacyMachinesFiles(dir); len(names) > 0 {
		res.Error = fmt.Sprintf("ignored config file(s): %s", strings.Join(names, ", "))
	}
	res.Resolution = "run `grove machine migrate` to import it into machine.toml, then delete the directory"
	return res
}

func (c *legacyMachinesDirCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: run `grove machine migrate`, review the result, then delete the directory", doctor.ErrNotFixable)
}

// legacyMachinesFiles names the config files being ignored, so the report says
// what is actually stranded rather than only that a directory exists. It
// mirrors the extensions `grove machine migrate` imports.
func legacyMachinesFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".yml", ".yaml", ".toml":
			out = append(out, entry.Name())
		}
	}
	return out
}
