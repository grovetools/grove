package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	coreplugin "github.com/grovetools/core/pkg/plugin"
)

// fixtureStatus is one `grove plugin list` row with every lockfile field
// populated, so a field the JSON forgets to carry shows up as a zero value
// rather than as an ambiguity.
func fixtureStatus(t *testing.T) coreplugin.Status {
	t.Helper()
	root := t.TempDir()

	// The bin dir entry is a real link into a real version directory: it is
	// what built_commit is derived from, and a fabricated string would not
	// exercise that.
	const commit = "274ca8258f1149a0d5ca6d5f0f6d3a7b4c8e9f01"
	versionBinary := filepath.Join(root, "versions", "demo", commit, "bin", "grove-panel-demo")
	if err := os.MkdirAll(filepath.Dir(versionBinary), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(versionBinary, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write: %v", err)
	}
	binary := filepath.Join(root, "bin", "grove-panel-demo")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(versionBinary, binary); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	return coreplugin.Status{
		Name:            "demo",
		FragmentPresent: true,
		BinaryPresent:   true,
		Approved:        true,
		Pin: &coreplugin.Pin{
			Spec:           "github.com/user/grove-panel-demo@v1.0.0",
			URL:            "https://github.com/user/grove-panel-demo",
			Ref:            "v1.0.0",
			Commit:         commit,
			ManifestDigest: "sha256:manifest",
			ConsentDigest:  "sha256:consent",
			Consent: coreplugin.ConsentFacts{
				Name:        "demo",
				Description: "a test panel",
				Source:      "github.com/user/grove-panel-demo@v1.0.0",
				Commit:      commit,
				Protocol:    coreplugin.ProtocolEmbedV1,
				Run:         []string{binary},
				Keys:        []string{"ctrl+f — a claimed chord"},
				Views:       []string{"compact — one line (what a drawer pane gets by default)"},
				Settings:    []string{"work_minutes = 25"},
			},
			SourceDir:     filepath.Join(root, "src", "grove-panel-demo"),
			VersionBinary: versionBinary,
			Binary:        binary,
			Fragment:      filepath.Join(root, "config", "plugins", "demo.toml"),
			InstalledAt:   "2026-08-07T09:00:00Z",
		},
	}
}

// The keys `list --json` shipped with are a contract: a reader written against
// the first version must keep working. This names them one by one, so removing
// or renaming one fails here rather than in whatever is parsing it.
func TestPluginListJSONKeepsItsOriginalKeys(t *testing.T) {
	st := fixtureStatus(t)
	row := pluginListRows([]coreplugin.Status{st})[0]

	for key, want := range map[string]any{
		"name":     "demo",
		"source":   st.Pin.Consent.Source,
		"ref":      "v1.0.0",
		"commit":   st.Pin.Commit,
		"binary":   st.Pin.Binary,
		"fragment": st.Pin.Fragment,
		"protocol": coreplugin.ProtocolEmbedV1,
		"dev":      false,
		"approved": true,
		"intact":   true,
	} {
		if got := row[key]; got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
}

// Everything the lockfile holds and the first version dropped. The panel reads
// its roster from here, so a missing field is a question it cannot answer
// without parsing grove's private state itself.
func TestPluginListJSONCarriesTheWholePin(t *testing.T) {
	st := fixtureStatus(t)
	row := pluginListRows([]coreplugin.Status{st})[0]

	for key, want := range map[string]any{
		"installed_at":    "2026-08-07T09:00:00Z",
		"source_dir":      st.Pin.SourceDir,
		"version_binary":  st.Pin.VersionBinary,
		"manifest_digest": "sha256:manifest",
		"consent_digest":  "sha256:consent",
		"built_commit":    st.Pin.Commit,
	} {
		if got := row[key]; got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}

	// The consent block travels whole, and it has to survive the JSON round
	// trip — it is the declaration side of the panel's declared-vs-observed
	// comparison.
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Consent coreplugin.ConsentFacts `json:"consent"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Consent.Keys) != 1 || len(decoded.Consent.Views) != 1 || len(decoded.Consent.Settings) != 1 {
		t.Errorf("the consent block lost its declarations: %+v", decoded.Consent)
	}
	if decoded.Consent.Protocol != coreplugin.ProtocolEmbedV1 || decoded.Consent.Description != "a test panel" {
		t.Errorf("consent = %+v", decoded.Consent)
	}
}

// built_commit is read off the link, so a bin dir entry that is missing or is
// not one of grove's has nothing to report.
func TestPluginListJSONBuiltCommitIsEmptyWithoutALink(t *testing.T) {
	st := fixtureStatus(t)
	st.Pin.Binary = filepath.Join(t.TempDir(), "gone")
	st.BinaryPresent = false

	row := pluginListRows([]coreplugin.Status{st})[0]
	if got := row["built_commit"]; got != "" {
		t.Errorf("built_commit = %#v, want empty", got)
	}
	if row["intact"] != false {
		t.Error("a missing binary is not intact")
	}
}
