package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

func runSubscribeCmd(t *testing.T, name, path, notebook string, disabled bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runSubscribe(&out, name, path, notebook, disabled)
	return out.String(), err
}

// TestSubscribeWritesIntentOnly: the verb declares, it does not materialize.
// Nothing on disk changes besides machine.toml, and the output says what to
// run next.
func TestSubscribeWritesIntentOnly(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	dest := filepath.Join(home, "code", "grovetools")

	out, err := runSubscribeCmd(t, "grovetools", dest, "", false)
	if err != nil {
		t.Fatalf("subscribe: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("subscribe created something on disk at %s (%v)", dest, statErr)
	}
	if !strings.Contains(out, "grove ecosystem materialize grovetools") {
		t.Errorf("subscribe did not point at materialize:\n%s", out)
	}

	cfg, err := config.LoadMachineConfigFrom(filepath.Join(configDir, "machine.toml"))
	if err != nil || cfg == nil {
		t.Fatalf("machine.toml not written: %v (%v)", cfg, err)
	}
	if got := cfg.Machine.Ecosystems["grovetools"].Path; got != dest {
		t.Errorf("path = %q, want %q", got, dest)
	}
}

// TestSubscribePreservesTheRestOfMachineToml: machine.toml is hand-authored
// and dotfiles-portable, so the edit must be surgical — a marshaller round-trip
// would quietly rewrite the user's file.
func TestSubscribePreservesTheRestOfMachineToml(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	original := "# my machine\n[machine]\nname = \"mbp\"\n\n[machine.roots.chickens]\npath = \"~/code/chickens\"\nnotebook = \"nb\"\n"
	machinePath := filepath.Join(configDir, "machine.toml")
	if err := os.WriteFile(machinePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()

	if out, err := runSubscribeCmd(t, "grovetools", filepath.Join(home, "code", "grovetools"), "", false); err != nil {
		t.Fatalf("subscribe: %v\n%s", err, out)
	}
	data, err := os.ReadFile(machinePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# my machine", "name = \"mbp\"", "[machine.roots.chickens]", "notebook = \"nb\""} {
		if !strings.Contains(text, want) {
			t.Errorf("subscribe lost %q from machine.toml:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "[machine.ecosystems.grovetools]") {
		t.Errorf("the new subscription is missing:\n%s", text)
	}
}

// TestSubscribeIsIdempotent.
func TestSubscribeIsIdempotent(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	dest := filepath.Join(home, "code", "grovetools")

	if out, err := runSubscribeCmd(t, "grovetools", dest, "", false); err != nil {
		t.Fatalf("first: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(filepath.Join(configDir, "machine.toml"))

	out, err := runSubscribeCmd(t, "grovetools", dest, "", false)
	if err != nil {
		t.Fatalf("second: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(filepath.Join(configDir, "machine.toml"))
	if string(after) != string(before) {
		t.Errorf("second subscribe rewrote machine.toml:\n%s", after)
	}
	if !strings.Contains(out, "already declared") {
		t.Errorf("second subscribe did not report the entry as present:\n%s", out)
	}
}

// TestSubscribeDefaultsBesideExistingEcosystems: a second ecosystem lands next
// to the first rather than in a directory the user has never used.
func TestSubscribeDefaultsBesideExistingEcosystems(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	first := filepath.Join(home, "src", "alpha")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "machine.toml"),
		[]byte("[machine.ecosystems.alpha]\npath = \""+first+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()

	if out, err := runSubscribeCmd(t, "beta", "", "", false); err != nil {
		t.Fatalf("subscribe: %v\n%s", err, out)
	}
	cfg, err := config.LoadMachineConfigFrom(filepath.Join(configDir, "machine.toml"))
	if err != nil || cfg == nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "src", "beta")
	if got := cfg.Machine.Ecosystems["beta"].Path; got != want {
		t.Errorf("beta went to %q, want %q (beside alpha)", got, want)
	}
}

// TestSubscribeNoticesAnExistingCheckout: declaring intent for something that
// is already there is not an error, and the output must not imply work was done.
func TestSubscribeNoticesAnExistingCheckout(t *testing.T) {
	home, _, _ := sandboxAdoption(t)
	dest := filepath.Join(home, "code", "grovetools")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "grove.toml"), []byte("name = \"grovetools\"\nworkspaces = [\"*\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runSubscribeCmd(t, "grovetools", dest, "", false)
	if err != nil {
		t.Fatalf("subscribe: %v\n%s", err, out)
	}
	if !strings.Contains(out, "present on disk") {
		t.Errorf("subscribe did not notice the existing checkout:\n%s", out)
	}
	if strings.Contains(out, "materialize") {
		t.Errorf("subscribe offered materialize for an ecosystem that is already here:\n%s", out)
	}
}
