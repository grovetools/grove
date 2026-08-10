package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func runDoctorForTest(t *testing.T, jsonOutput bool) (string, error) {
	t.Helper()
	doctorFix = false
	doctorCheckID = ""
	doctorJSON = false
	doctorVerbose = false

	cmd := newDoctorCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	args := []string{"--check", "config_fragments"}
	if jsonOutput {
		args = append(args, "--json")
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestDoctorJSONSurfacesConfigDegradationAndKeepsIndependentCheck(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
	}{
		{name: "roots", file: "roots.toml", content: "[roots.code\npath = \"/code\"\n"},
		{name: "notebooks", file: "notebooks.toml", content: "[notebooks.main\nroot = \"/notes\"\n"},
		{name: "fragment", file: "10-broken.toml", content: "[tui\ntheme = \"dark\"\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configDir := sandboxConfigCLI(t)
			path := filepath.Join(configDir, tc.file)
			writeCLIConfig(t, path, tc.content)

			output, err := runDoctorForTest(t, true)
			if !errors.Is(err, errDoctorFailed) {
				t.Fatalf("error = %v, want doctor failure", err)
			}
			var results []doctorJSONResult
			if err := json.Unmarshal([]byte(output), &results); err != nil {
				t.Fatalf("invalid doctor JSON %q: %v", output, err)
			}
			if len(results) != 2 {
				t.Fatalf("results = %+v, want config degradation plus independent check", results)
			}
			if results[0].Check != "effective_config" || results[0].Status != "fail" || !strings.Contains(results[0].Error, path) {
				t.Errorf("missing path-qualified top-level degradation: %+v", results[0])
			}
			if results[1].Check != "config_fragments" {
				t.Errorf("independent check was suppressed: %+v", results)
			}
		})
	}
}

func TestDoctorHealthyJSONAndDegradedHumanBanner(t *testing.T) {
	sandboxConfigCLI(t)
	output, err := runDoctorForTest(t, true)
	if err != nil {
		t.Fatalf("healthy doctor failed: %v\n%s", err, output)
	}
	var results []doctorJSONResult
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Check != "config_fragments" || results[0].Status != "pass" {
		t.Fatalf("unexpected healthy results: %+v", results)
	}

	configDir := sandboxConfigCLI(t)
	path := filepath.Join(configDir, "10-broken.toml")
	writeCLIConfig(t, path, "[tui\ntheme = \"dark\"\n")
	output, err = runDoctorForTest(t, false)
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("error = %v, want doctor failure", err)
	}
	for _, want := range []string{"CONFIG DEGRADED", path, "config_fragments"} {
		if !strings.Contains(output, want) {
			t.Errorf("human output missing %q:\n%s", want, output)
		}
	}
}
