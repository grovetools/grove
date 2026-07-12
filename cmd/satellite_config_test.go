package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/pelletier/go-toml/v2"
)

// TestSeedFragmentDenylist covers every denylist class: main configs,
// overrides, sync, secret/key conventions, laptop-topology fragments, the
// reserved custom-fragment name — plus path/charset hygiene and the .toml
// requirement. Denied names must ERROR (not skip).
func TestSeedFragmentDenylist(t *testing.T) {
	denied := []string{
		"grove.toml",                // VM main config
		"grove.yml",                 // VM main config, YAML form
		"grove.override.toml",       // override file
		"grove.override.local",      // grove.override.* pattern
		"sync.toml",                 // sync client config
		"secrets.toml",              // secrets*.toml
		"secrets-prod.toml",         // secrets*.toml
		"keys.toml",                 // keys*.toml
		"keys-extra.toml",           // keys*.toml
		"groves.toml",               // laptop topology
		"notebooks.toml",            // laptop topology
		"projects.toml",             // laptop topology
		satelliteCustomFragmentName, // reserved for the rendered custom fragment
		"../evil.toml",              // path escape
		"sub/dir.toml",              // not a basename
		"flow.yml",                  // not .toml (VM loader globs *.toml only)
		"",                          // empty
	}
	for _, name := range denied {
		if err := validateSeedFragmentName(name); err == nil {
			t.Errorf("validateSeedFragmentName(%q) = nil, want error", name)
		}
	}

	allowed := []string{"flow.toml", "claude-settings.toml", "skills.toml", "notifications.toml", "my-keysmith.toml"}
	for _, name := range allowed {
		if err := validateSeedFragmentName(name); err != nil {
			t.Errorf("validateSeedFragmentName(%q) = %v, want nil", name, err)
		}
	}
}

// TestSeedFragmentContentRefusal: even under an innocent filename, a fragment
// carrying a top-level 'satellites' key (registry recursion) or a [daemon]
// table (VM topology) is refused, as is unparseable TOML.
func TestSeedFragmentContentRefusal(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"satellites table", "[satellites.other]\nssh_addr = \"1.2.3.4:22\"\n", "satellites"},
		{"satellites scalar", "satellites = \"nope\"\n", "satellites"},
		{"daemon table", "[daemon]\nport = 9999\n", "daemon"},
		{"parse error", "not = valid = toml\n", "parse"},
	}
	for _, tc := range cases {
		err := vetSeedFragmentContent("innocent.toml", []byte(tc.content))
		if err == nil {
			t.Errorf("%s: vetSeedFragmentContent = nil, want error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.wantErr)
		}
	}

	ok := "[notifications]\ntopic = \"fine\"\n\n[flow]\nmodel = \"x\"\n"
	if err := vetSeedFragmentContent("fine.toml", []byte(ok)); err != nil {
		t.Fatalf("benign fragment refused: %v", err)
	}
}

// TestAssembleSatellitePush covers assembly order (seeds verbatim, custom
// last) and the hard error for a listed-but-missing fragment.
func TestAssembleSatellitePush(t *testing.T) {
	dir := t.TempDir()
	seed := "[notifications]\ntopic = \"seeded\"\n"
	if err := os.WriteFile(filepath.Join(dir, "notifications.toml"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	prop := satelliteConfigPropagation{
		SeedFragments: []string{"notifications.toml"},
		Config:        map[string]interface{}{"notifications": map[string]interface{}{"topic": "custom"}},
	}
	files, err := assembleSatellitePush("sat1", prop, dir)
	if err != nil {
		t.Fatalf("assembleSatellitePush: %v", err)
	}
	if len(files) != 2 || files[0].Name != "notifications.toml" || files[1].Name != satelliteCustomFragmentName {
		t.Fatalf("unexpected push set: %+v", files)
	}
	if string(files[0].Data) != seed {
		t.Fatalf("seed fragment not copied verbatim:\n%s", files[0].Data)
	}

	// Nonexistent listed fragment = hard error.
	prop.SeedFragments = []string{"notifications.toml", "missing.toml"}
	if _, err := assembleSatellitePush("sat1", prop, dir); err == nil {
		t.Fatal("missing seed fragment did not error")
	}

	// Nothing declared → empty set, no error.
	files, err = assembleSatellitePush("sat1", satelliteConfigPropagation{}, dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("empty propagation: files=%v err=%v", files, err)
	}
}

// TestRenderSatelliteCustomFragmentRoundTrip: the rendered fragment carries
// the generated-by header, the [_grove] priority override, and round-trips the
// config table through a real TOML parser.
func TestRenderSatelliteCustomFragmentRoundTrip(t *testing.T) {
	cfg := map[string]interface{}{
		"notifications": map[string]interface{}{
			"topic":  "solair-satellite",
			"levels": []interface{}{"error", "warn"},
		},
		"flow": map[string]interface{}{
			"defaults": map[string]interface{}{"model": "fable-5"},
		},
		"top_scalar": int64(7),
	}
	out, err := renderSatelliteCustomFragment("sat1", cfg)
	if err != nil {
		t.Fatalf("renderSatelliteCustomFragment: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "grove satellite config push") || !strings.Contains(text, `"sat1"`) {
		t.Fatalf("generated-by header missing satellite name/command:\n%s", text)
	}

	var back map[string]interface{}
	if err := toml.Unmarshal(out, &back); err != nil {
		t.Fatalf("rendered fragment does not parse: %v\n%s", err, text)
	}
	meta, ok := back["_grove"].(map[string]interface{})
	if !ok || meta["priority"] != int64(satelliteCustomFragmentPriority) {
		t.Fatalf("[_grove].priority = %v, want %d\n%s", back["_grove"], satelliteCustomFragmentPriority, text)
	}
	notif, _ := back["notifications"].(map[string]interface{})
	if notif["topic"] != "solair-satellite" {
		t.Fatalf("notifications.topic did not round-trip: %v", back["notifications"])
	}
	flow, _ := back["flow"].(map[string]interface{})
	defaults, _ := flow["defaults"].(map[string]interface{})
	if defaults["model"] != "fable-5" {
		t.Fatalf("flow.defaults.model did not round-trip: %v", back["flow"])
	}
	if back["top_scalar"] != int64(7) {
		t.Fatalf("top_scalar did not round-trip: %v", back["top_scalar"])
	}

	// Refusals: satellites / daemon / _grove at the top of the config table.
	for _, denied := range []string{"satellites", "daemon", "_grove"} {
		if _, err := renderSatelliteCustomFragment("sat1", map[string]interface{}{denied: map[string]interface{}{}}); err == nil {
			t.Errorf("config table with top-level %q was not refused", denied)
		}
	}
}

// TestSatelliteCustomFragmentPrecedence proves custom-over-seeded through the
// REAL loader: seeded fragments (default priority 50, one boosted to 60, one
// alphabetically after zz-) and the rendered custom fragment (priority 1000)
// written to a temp global config dir, then loaded via config.LoadFrom — the
// exact fragment-merging code path (core/config/config.go) the VM runs.
func TestSatelliteCustomFragmentPrecedence(t *testing.T) {
	configDir := setupGroveHome(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The VM's own main config (never touched by the push) — present for realism.
	write("grove.toml", "name = \"vm\"\n")
	// Seeded fragments trying to claim notifications.topic:
	write("notifications.toml", "[notifications]\ntopic = \"seeded-default-priority\"\n")
	write("aaa-boosted.toml", "[_grove]\npriority = 60\n\n[notifications]\ntopic = \"seeded-boosted\"\n")
	write("zzz-late-alpha.toml", "[notifications]\ntopic = \"seeded-alphabetically-last\"\n")

	rendered, err := renderSatelliteCustomFragment("sat1", map[string]interface{}{
		"notifications": map[string]interface{}{"topic": "custom-wins"},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(satelliteCustomFragmentName, string(rendered))

	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	var notif struct {
		Topic string `yaml:"topic"`
	}
	if err := cfg.UnmarshalExtension("notifications", &notif); err != nil {
		t.Fatalf("UnmarshalExtension(notifications): %v", err)
	}
	if notif.Topic != "custom-wins" {
		t.Fatalf("notifications.topic = %q through the real loader, want custom-wins (custom fragment must override seeded)", notif.Topic)
	}

	// Sanity: without the custom fragment the boosted seed wins (priority 60 >
	// 50 beats both alphabetical seeds) — confirming the priority direction the
	// custom fragment relies on.
	if err := os.Remove(filepath.Join(configDir, satelliteCustomFragmentName)); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("config.LoadFrom (no custom): %v", err)
	}
	notif.Topic = ""
	if err := cfg.UnmarshalExtension("notifications", &notif); err != nil {
		t.Fatal(err)
	}
	if notif.Topic != "seeded-boosted" {
		t.Fatalf("without custom fragment, topic = %q, want seeded-boosted (higher [_grove].priority merges later and wins)", notif.Topic)
	}
}

// fakeConfigTransport records pushes and serves canned remote files, so the
// push engine's diff/no-write and manifest behavior is testable without a VM.
type fakeConfigTransport struct {
	remote map[string]string
	pushed [][]satellitePushFile
}

func (f *fakeConfigTransport) fetchConfigFile(name string) (string, error) {
	return f.remote[name], nil
}

func (f *fakeConfigTransport) pushConfigFiles(files []satellitePushFile) error {
	f.pushed = append(f.pushed, files)
	return nil
}

// TestSatelliteConfigPushDiffModeNoWrite: --diff prints unified diffs (new,
// changed, unchanged) and performs NO writes — no fragments, no manifest.
func TestSatelliteConfigPushDiffModeNoWrite(t *testing.T) {
	tr := &fakeConfigTransport{remote: map[string]string{
		"flow.toml": "[flow]\nmodel = \"old\"\n",
		"same.toml": "[x]\ny = 1\n",
	}}
	files := []satellitePushFile{
		{Name: "flow.toml", Data: []byte("[flow]\nmodel = \"new\"\n")},
		{Name: "same.toml", Data: []byte("[x]\ny = 1\n")},
		{Name: satelliteCustomFragmentName, Data: []byte("[notifications]\ntopic = \"t\"\n")},
	}
	var out bytes.Buffer
	if err := runSatelliteConfigPush("sat1", files, tr, true, &out); err != nil {
		t.Fatalf("runSatelliteConfigPush(diff): %v", err)
	}
	if len(tr.pushed) != 0 {
		t.Fatalf("diff mode wrote to the transport: %v", tr.pushed)
	}
	text := out.String()
	for _, want := range []string{
		"-model = \"old\"", "+model = \"new\"", // changed file hunks
		"same.toml: unchanged",
		satelliteCustomFragmentName + ": new on VM",
		"nothing was written",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("diff output missing %q:\n%s", want, text)
		}
	}
}

// TestSatelliteConfigPushManifestAndRemovedWarning: default mode ships the
// fragments plus the manifest, and a fragment present in the previous
// manifest but no longer listed is WARNED about (named), not deleted.
func TestSatelliteConfigPushManifestAndRemovedWarning(t *testing.T) {
	tr := &fakeConfigTransport{remote: map[string]string{
		satelliteManifestName: "# header\nflow.toml\nold-fragment.toml\n" + satelliteCustomFragmentName + "\n",
	}}
	files := []satellitePushFile{
		{Name: "flow.toml", Data: []byte("[flow]\nmodel = \"new\"\n")},
		{Name: satelliteCustomFragmentName, Data: []byte("[notifications]\ntopic = \"t\"\n")},
	}
	var out bytes.Buffer
	if err := runSatelliteConfigPush("sat1", files, tr, false, &out); err != nil {
		t.Fatalf("runSatelliteConfigPush: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "old-fragment.toml") || !strings.Contains(text, "warning") {
		t.Fatalf("removed fragment not warned by name:\n%s", text)
	}
	if strings.Contains(text, "warning: previously pushed fragment(s) no longer listed: flow.toml") {
		t.Fatalf("still-listed fragment reported as removed:\n%s", text)
	}
	if !strings.Contains(text, "restart groved") {
		t.Fatalf("daemon-restart caveat missing:\n%s", text)
	}

	if len(tr.pushed) != 1 {
		t.Fatalf("expected exactly one push, got %d", len(tr.pushed))
	}
	shipped := tr.pushed[0]
	if len(shipped) != 3 || shipped[len(shipped)-1].Name != satelliteManifestName {
		t.Fatalf("push must ship fragments + trailing manifest, got %+v", shippedNames(shipped))
	}
	manifest := parseSatelliteManifest(string(shipped[len(shipped)-1].Data))
	want := []string{"flow.toml", satelliteCustomFragmentName}
	if len(manifest) != len(want) {
		t.Fatalf("manifest = %v, want %v", manifest, want)
	}
	for _, w := range want {
		found := false
		for _, m := range manifest {
			if m == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("manifest %v missing %q", manifest, w)
		}
	}

	// First push (no remote manifest): no removed-fragment warning.
	tr2 := &fakeConfigTransport{remote: map[string]string{}}
	out.Reset()
	if err := runSatelliteConfigPush("sat1", files, tr2, false, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "no longer listed") {
		t.Fatalf("first push produced a removed-fragment warning:\n%s", out.String())
	}

	// Empty push set: friendly no-op, no transport traffic.
	tr3 := &fakeConfigTransport{}
	out.Reset()
	if err := runSatelliteConfigPush("sat1", nil, tr3, false, &out); err != nil {
		t.Fatal(err)
	}
	if len(tr3.pushed) != 0 || !strings.Contains(out.String(), "nothing to push") {
		t.Fatalf("empty push set mishandled: pushed=%v out=%s", tr3.pushed, out.String())
	}
}

func shippedNames(files []satellitePushFile) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return names
}
