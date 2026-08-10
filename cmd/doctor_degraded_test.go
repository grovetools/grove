package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func runDoctorCheckForTest(t *testing.T, checkID string, jsonOutput bool) (string, error) {
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
	args := []string{"--check", checkID}
	if jsonOutput {
		args = append(args, "--json")
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func runDoctorForTest(t *testing.T, jsonOutput bool) (string, error) {
	return runDoctorCheckForTest(t, "config_fragments", jsonOutput)
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

func TestDoctorRootsBindingsJSONIncludesIDAndInventoryDetail(t *testing.T) {
	configDir := sandboxConfigCLI(t)
	root := t.TempDir()
	notes := t.TempDir()
	writeCLIConfig(t, filepath.Join(configDir, "roots.toml"), "[roots.alpha]\npath = \""+root+"\"\nscan = true\nnotebook = \"main\"\n")
	writeCLIConfig(t, filepath.Join(configDir, "notebooks.toml"), "default = \"main\"\n[notebooks.main]\nroot = \""+notes+"\"\n")

	output, err := runDoctorCheckForTest(t, "roots_bindings", true)
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, output)
	}
	var results []doctorJSONResult
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Check != "roots_bindings" || results[0].Status != "pass" {
		t.Fatalf("unexpected roots result: %+v", results)
	}
	for _, want := range []string{"alpha", "kind=scan", "expanded=", "canonical=", "notebook=\"main\"", "notebook_root="} {
		if !strings.Contains(results[0].Detail, want) {
			t.Errorf("detail missing %q: %s", want, results[0].Detail)
		}
	}
}

func TestDoctorRootsBindingsJSONFailureDetail(t *testing.T) {
	configDir := sandboxConfigCLI(t)
	missing := filepath.Join(t.TempDir(), "missing")
	writeCLIConfig(t, filepath.Join(configDir, "roots.toml"), "[roots.alpha]\npath = \""+missing+"\"\nnotebook = \"main\"\n")
	writeCLIConfig(t, filepath.Join(configDir, "notebooks.toml"), "default = \"main\"\n[notebooks.main]\nroot = \""+t.TempDir()+"\"\n")

	output, err := runDoctorCheckForTest(t, "roots_bindings", true)
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("error = %v, want doctor failure: %s", err, output)
	}
	var results []doctorJSONResult
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Check != "roots_bindings" || results[0].Status != "fail" {
		t.Fatalf("unexpected roots result: %+v", results)
	}
	for _, want := range []string{"alpha", "state=missing", "kind=specific", "enabled = false"} {
		if !strings.Contains(results[0].Detail+results[0].Resolution, want) {
			t.Errorf("JSON result missing %q: %+v", want, results[0])
		}
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
