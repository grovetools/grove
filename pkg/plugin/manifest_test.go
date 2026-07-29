package plugin

import (
	"strings"
	"testing"
)

// validManifest is the smallest manifest that passes, and the base every
// negative case below mutates one field of.
const validManifest = `
schema_version = 1

[plugin]
name        = "hello"
description = "A hello-world sidecar panel"

[build]
command = ["go", "build", "-o", "bin/grove-panel-hello", "."]
binary  = "bin/grove-panel-hello"

[panel]
icon     = "H"
protocol = "embed/v1"

[[panel.keys]]
key         = "ctrl+f"
description = "jump to the notebook"
`

func TestParseManifestAcceptsTheDocumentedShape(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Plugin.Name != "hello" {
		t.Errorf("name = %q", m.Plugin.Name)
	}
	if got, want := strings.Join(m.Build.Command, " "), "go build -o bin/grove-panel-hello ."; got != want {
		t.Errorf("build command = %q, want %q", got, want)
	}
	if m.BinaryName() != "grove-panel-hello" {
		t.Errorf("BinaryName = %q", m.BinaryName())
	}
	if len(m.Panel.Keys) != 1 || m.Panel.Keys[0].Key != "ctrl+f" {
		t.Errorf("keys = %+v", m.Panel.Keys)
	}
	if len(m.Unknown) != 0 {
		t.Errorf("unexpected unknown keys: %v", m.Unknown)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		wantIn   string
	}{
		{"no schema version", strings.Replace(validManifest, "schema_version = 1", "", 1), "schema_version is required"},
		{"future schema version", strings.Replace(validManifest, "schema_version = 1", "schema_version = 2", 1), "newer than this grove"},
		{"no name", strings.Replace(validManifest, `name        = "hello"`, "", 1), "plugin.name is required"},
		{"name with a slash", strings.Replace(validManifest, `"hello"`, `"../../etc/hello"`, 1), "must be lowercase letters"},
		{"uppercase name", strings.Replace(validManifest, `"hello"`, `"Hello"`, 1), "must be lowercase letters"},
		{"no description", strings.Replace(validManifest, `description = "A hello-world sidecar panel"`, "", 1), "plugin.description is required"},
		{"no binary", strings.Replace(validManifest, `binary  = "bin/grove-panel-hello"`, "", 1), "build.binary is required"},
		{"absolute binary", strings.Replace(validManifest, `"bin/grove-panel-hello"
`, `"/usr/bin/env"
`, 1), "must be relative"},
		{"escaping binary", strings.Replace(validManifest, `binary  = "bin/grove-panel-hello"`, `binary  = "../../../bin/sh"`, 1), "must stay inside"},
		{"unknown protocol", strings.Replace(validManifest, `"embed/v1"`, `"embed/v9"`, 1), "not a protocol this host speaks"},
		{"bad timeout", strings.Replace(validManifest, `protocol = "embed/v1"`, `protocol_timeout = "soon"`, 1), "not a Go duration"},
		{"bad env", strings.Replace(validManifest, `icon     = "H"`, `env = ["not-an-assignment"]`, 1), "must be KEY=VALUE"},
		{"empty argv element", strings.Replace(validManifest, `"go", "build"`, `"go", ""`, 1), "build.command[1] is empty"},
		{"key without a description", strings.Replace(validManifest, `description = "jump to the notebook"`, "", 1), "description is required"},
		{"control character in the icon", strings.Replace(validManifest, `icon     = "H"`, `icon = "\u001b[2J"`, 1), "control character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.manifest))
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantIn)
			}
		})
	}
}

// A manifest written for a later grove must still install, with its
// unrecognized keys reported rather than swallowed or fatal. That is what
// keeps one plugin repo installable by two grove versions.
func TestParseManifestReportsUnknownKeysWithoutFailing(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest + "\n[panel.sandbox]\nnetwork = false\n"))
	if err != nil {
		t.Fatalf("an unknown key must not fail the parse: %v", err)
	}
	if len(m.Unknown) == 0 {
		t.Fatal("expected the unknown key to be reported")
	}
	if !strings.Contains(strings.Join(m.Unknown, ","), "sandbox") {
		t.Errorf("unknown = %v, want it to name panel.sandbox", m.Unknown)
	}
}

// An interpreted panel ships its program in the repo and needs no toolchain.
func TestParseManifestAllowsNoBuildStep(t *testing.T) {
	manifest := strings.Replace(validManifest, `command = ["go", "build", "-o", "bin/grove-panel-hello", "."]`, "", 1)
	m, err := ParseManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Build.Command) != 0 {
		t.Errorf("build.command = %v, want empty", m.Build.Command)
	}
}
