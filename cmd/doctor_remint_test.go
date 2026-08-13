package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/notespace"
)

// --remint writes a stamp on disk. It is gated on --fix so that "apply things"
// is always something the operator said, never something a flag implied. Both
// refusals happen before any check runs, so nothing is touched.
func TestDoctorRemintRequiresFix(t *testing.T) {
	t.Setenv("GROVE_HOME", t.TempDir())
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--remint", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--fix") {
		t.Fatalf("err = %v, want a refusal naming --fix", err)
	}
}

func TestDoctorRemintIsNotAJSONSurface(t *testing.T) {
	t.Setenv("GROVE_HOME", t.TempDir())
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--fix", "--json", "--remint", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--json") {
		t.Fatalf("err = %v, want a refusal naming --json", err)
	}
}

// duplicateMachine records one notebook whose notespaces/ holds two roots
// carrying one stamp id — the D8 state --remint repairs.
func duplicateMachine(t *testing.T) (loser string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	configDir := filepath.Join(home, "config", "grove")
	nb := filepath.Join(home, "notebook")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "notebooks.toml"),
		[]byte("default=\"nb\"\n[notebooks.nb]\nroot=\""+nb+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "roots.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "machine.toml"), []byte("[primaries]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := notespace.InstallNotebook(nb, notespace.NotebookStamp{
		ID: "01ABCDEFGHJKMNPQRSTVWXYZ01", Name: "nb",
	}); err != nil {
		t.Fatal(err)
	}
	stamp := notespace.NotespaceStamp{
		ID: "01ABCDEFGHJKMNPQRSTVWXYZ02", Subject: "local:01ABCDEFGHJKMNPQRSTVWXYZ03", Kind: "notes",
	}
	for _, name := range []string{"one", "two"} {
		stamp.Name = name
		if _, err := notespace.InstallNotespace(filepath.Join(nb, "notespaces", name), stamp); err != nil {
			t.Fatal(err)
		}
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return filepath.Join(nb, "notespaces", "two")
}

func runDoctorCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// A repair verb's exit code answers for the repair.
//
// --remint runs the repair and then the normal diagnostics over the repaired
// state, and every unrelated check on the machine is in that suite. Letting one
// of them decide the exit code made a SUCCESSFUL re-mint indistinguishable from
// a failed one to anything reading $? — so the lab had to judge the repair on
// printed text, and no script could use the verb at all.
//
// The failures are still rendered, and the command says in as many words that
// its status does not answer for them.
func TestDoctorRemintExitsOnTheRepairNotTheDiagnostics(t *testing.T) {
	// Establish that this sandbox DOES fail some unrelated check, so the
	// assertion below is not vacuously true.
	duplicateMachine(t)
	if _, err := runDoctorCmd(t, "--fix"); err == nil {
		t.Skip("this sandbox passes every diagnostic; the exit-code contract cannot be distinguished here")
	}

	loser := duplicateMachine(t)
	out, err := runDoctorCmd(t, "--fix", "--remint", loser)
	if err != nil {
		t.Fatalf("a successful re-mint reported failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "re-mint succeeded") {
		t.Errorf("the exit-code contract is not stated in the evidence:\n%s", out)
	}
	if !strings.Contains(out, "grove doctor") {
		t.Errorf("the evidence does not name the command whose status DOES answer for the checks:\n%s", out)
	}
}

// A designation that names no duplicate is refused by the repair itself, with
// the reason, rather than being turned into a diagnostics run that says nothing
// happened.
func TestDoctorRemintSurfacesTheRepairRefusal(t *testing.T) {
	t.Setenv("GROVE_HOME", t.TempDir())
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--fix", "--remint", t.TempDir()})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("re-minting a root that is not a recorded notespace was accepted")
	}
}
