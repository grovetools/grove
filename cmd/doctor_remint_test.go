package cmd

import (
	"bytes"
	"strings"
	"testing"
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
