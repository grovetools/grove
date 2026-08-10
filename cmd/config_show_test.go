package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sandboxConfigCLI(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, "config", "grove")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(home, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROVE_HOME", home)
	t.Setenv("GROVE_CONFIG_OVERLAY", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "unused-xdg"))
	t.Chdir(workDir)
	writeCLIConfig(t, filepath.Join(configDir, "grove.toml"), "version = \"1.0\"\n")
	return configDir
}

func writeCLIConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runConfigShowForTest(t *testing.T, human bool) (string, error) {
	t.Helper()
	cmd := newConfigShowCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	args := []string{"--effective"}
	if !human {
		args = append(args, "--json")
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestConfigShowEffectiveJSONDegradedForMalformedRecordedAndFragmentFiles(t *testing.T) {
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

			output, err := runConfigShowForTest(t, false)
			if !errors.Is(err, errConfigDegraded) {
				t.Fatalf("error = %v, want degraded status", err)
			}
			var envelope effectiveConfigEnvelope
			if err := json.Unmarshal([]byte(output), &envelope); err != nil {
				t.Fatalf("invalid JSON %q: %v", output, err)
			}
			if envelope.SchemaVersion != 1 || !envelope.Degraded || envelope.Error == nil {
				t.Fatalf("unexpected envelope: %+v", envelope)
			}
			if !strings.Contains(*envelope.Error, path) {
				t.Errorf("error does not name %s: %s", path, *envelope.Error)
			}
			if envelope.Effective == nil {
				t.Fatal("degraded envelope omitted partial effective configuration")
			}
		})
	}
}

func TestConfigShowEffectiveIncludesCanonicalRecordedTopology(t *testing.T) {
	configDir := sandboxConfigCLI(t)
	writeCLIConfig(t, filepath.Join(configDir, "notebooks.toml"), "default=\"nb\"\n[notebooks.nb]\nroot=\"/notes\"\n")
	writeCLIConfig(t, filepath.Join(configDir, "roots.toml"), "[roots.code]\npath=\"/code\"\nscan=true\nnotebook=\"nb\"\n")
	output, err := runConfigShowForTest(t, false)
	if err != nil {
		t.Fatal(err)
	}
	var envelope effectiveConfigEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatal(err)
	}
	groves, ok := envelope.Effective["groves"].(map[string]interface{})
	if !ok {
		t.Fatalf("effective topology missing: %+v", envelope.Effective)
	}
	code, ok := groves["code"].(map[string]interface{})
	if !ok || code["path"] != "/code" || code["notebook"] != "nb" {
		t.Fatalf("effective code root = %+v", groves["code"])
	}
}

func TestConfigShowEffectiveHealthyAndHumanDegradedOutput(t *testing.T) {
	sandboxConfigCLI(t)
	output, err := runConfigShowForTest(t, false)
	if err != nil {
		t.Fatal(err)
	}
	var envelope effectiveConfigEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.Degraded || envelope.Error != nil || envelope.Effective == nil {
		t.Fatalf("unexpected healthy envelope: %+v", envelope)
	}

	configDir := sandboxConfigCLI(t)
	path := filepath.Join(configDir, "notebooks.toml")
	writeCLIConfig(t, path, "[notebooks.main\nroot = \"/notes\"\n")
	output, err = runConfigShowForTest(t, true)
	if !errors.Is(err, errConfigDegraded) {
		t.Fatalf("error = %v, want degraded status", err)
	}
	for _, want := range []string{"CONFIG DEGRADED", path, "Effective configuration:"} {
		if !strings.Contains(output, want) {
			t.Errorf("human output missing %q:\n%s", want, output)
		}
	}
}
