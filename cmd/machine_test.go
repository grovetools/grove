package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/machine"
)

// sandboxMachineHome redirects both paths.ConfigDir() and paths.StateDir()
// into a temp dir via GROVE_HOME, so no test ever mints an identity into or
// writes machine.toml under the developer's real home.
func sandboxMachineHome(t *testing.T) (configDir, stateDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	return filepath.Join(home, "config", "grove"), filepath.Join(home, "state", "grove")
}

func runMachineCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newMachineCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

func TestMachineInitMintsThenIsIdempotent(t *testing.T) {
	configDir, stateDir := sandboxMachineHome(t)

	out, err := runMachineCmd(t, "init", "--name", "mbp")
	if err != nil {
		t.Fatalf("machine init: %v (%s)", err, out)
	}
	if !strings.Contains(out, "minted machine id") {
		t.Errorf("first init did not report a mint:\n%s", out)
	}

	id, err := machine.Load()
	if err != nil || id == nil {
		t.Fatalf("identity not persisted: %v (%v)", id, err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, machine.IdentityFileName)); statErr != nil {
		t.Fatalf("machine.json not at %s: %v", stateDir, statErr)
	}

	cfg, err := config.LoadMachineConfigFrom(filepath.Join(configDir, "machine.toml"))
	if err != nil || cfg == nil {
		t.Fatalf("machine.toml not written: %v (%v)", cfg, err)
	}
	if cfg.Machine.Name != "mbp" {
		t.Fatalf("name = %q, want mbp", cfg.Machine.Name)
	}

	// Acceptance: repeated runs keep the same ID and stop claiming to mint.
	out, err = runMachineCmd(t, "init")
	if err != nil {
		t.Fatalf("second init: %v (%s)", err, out)
	}
	if strings.Contains(out, "minted machine id") {
		t.Errorf("second init re-minted:\n%s", out)
	}
	again, _ := machine.Load()
	if again.ID != id.ID {
		t.Fatalf("id changed across runs: %q → %q", id.ID, again.ID)
	}
	// A bare re-run keeps the declared name rather than resetting to hostname.
	if !strings.Contains(out, `"mbp"`) {
		t.Errorf("second init lost the declared name:\n%s", out)
	}
}

func TestMachineInitDefaultsNameToHostname(t *testing.T) {
	configDir, _ := sandboxMachineHome(t)

	if out, err := runMachineCmd(t, "init"); err != nil {
		t.Fatalf("machine init: %v (%s)", err, out)
	}

	cfg, err := config.LoadMachineConfigFrom(filepath.Join(configDir, "machine.toml"))
	if err != nil || cfg == nil {
		t.Fatalf("machine.toml not written: %v (%v)", cfg, err)
	}
	host, _ := os.Hostname()
	if cfg.Machine.Name != host {
		t.Fatalf("name = %q, want hostname %q", cfg.Machine.Name, host)
	}
}

// Deleting machine.json is how an operator asks to become a new machine.
func TestMachineInitRemintsAfterDeletion(t *testing.T) {
	sandboxMachineHome(t)

	if out, err := runMachineCmd(t, "init", "--name", "mbp"); err != nil {
		t.Fatalf("machine init: %v (%s)", err, out)
	}
	first, _ := machine.Load()
	if err := os.Remove(machine.IdentityPath()); err != nil {
		t.Fatalf("remove identity: %v", err)
	}

	if out, err := runMachineCmd(t, "init"); err != nil {
		t.Fatalf("machine init after delete: %v (%s)", err, out)
	}
	second, _ := machine.Load()
	if second.ID == first.ID {
		t.Fatalf("deleting machine.json did not mint a fresh id (%q)", second.ID)
	}
}

func TestMachineStatusBeforeAndAfterInit(t *testing.T) {
	configDir, stateDir := sandboxMachineHome(t)

	// Before init: status is honest about there being no id yet, and does not
	// mint one as a side effect of being asked.
	out, err := runMachineCmd(t, "status")
	if err != nil {
		t.Fatalf("machine status: %v (%s)", err, out)
	}
	if !strings.Contains(out, "grove machine init") {
		t.Errorf("status on a fresh machine should point at init:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, machine.IdentityFileName)); statErr == nil {
		t.Error("machine status minted an identity as a side effect")
	}

	if out, err := runMachineCmd(t, "init", "--name", "mbp"); err != nil {
		t.Fatalf("machine init: %v (%s)", err, out)
	}
	id, _ := machine.Load()

	out, err = runMachineCmd(t, "status")
	if err != nil {
		t.Fatalf("machine status: %v (%s)", err, out)
	}
	for _, want := range []string{
		machine.Describe("mbp", id.ID), // name (short id), never the bare name
		id.ID,
		filepath.Join(configDir, "machine.toml"),
		filepath.Join(stateDir, machine.IdentityFileName),
		"Origin:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
}

// The dead machines/ dir is surfaced by status too, naming the migration.
func TestMachineStatusFlagsLegacyMachinesDir(t *testing.T) {
	configDir, _ := sandboxMachineHome(t)
	if err := os.MkdirAll(filepath.Join(configDir, config.LegacyMachinesDirName), 0o755); err != nil {
		t.Fatalf("mkdir machines: %v", err)
	}

	out, err := runMachineCmd(t, "status")
	if err != nil {
		t.Fatalf("machine status: %v (%s)", err, out)
	}
	if !strings.Contains(out, "grove migrate") {
		t.Errorf("status did not flag the legacy machines/ dir:\n%s", out)
	}
}

func TestMachineCommandIsRegistered(t *testing.T) {
	var found *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "machine" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("`grove machine` is not registered on rootCmd")
	}
	names := map[string]bool{}
	for _, sub := range found.Commands() {
		names[sub.Name()] = true
	}
	for _, want := range []string{"init", "status"} {
		if !names[want] {
			t.Errorf("`grove machine %s` is missing (have %v)", want, names)
		}
	}
}
