package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// loadInfraViaConfig loads the merged grove config the way `up` does and
// returns the infra block for name plus its presence flag (mirrors
// loadProvisionViaConfig).
func loadInfraViaConfig(t *testing.T, name string) (satelliteInfraConfig, bool) {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	infra, found, err := satelliteInfraFromConfig(cfg, name)
	if err != nil {
		t.Fatalf("satelliteInfraFromConfig: %v", err)
	}
	return infra, found
}

// TestSatelliteInfraConfigParse covers the [satellites.<name>.infra] block: it
// parses out of the same layered grove.toml the registry lives in, and an
// absent satellite/block yields the zero value.
func TestSatelliteInfraConfigParse(t *testing.T) {
	configDir := setupGroveHome(t)
	content := `[satellites.sat1]
ssh_addr = "203.0.113.7:22"
user = "solair"
host_key = "ssh-ed25519 AAAA"

[satellites.sat1.infra]
project = "my-proj"
zone = "us-west1-a"
ssh_user = "solair"
cidr = "203.0.113.7/32"
identity_file = "~/.ssh/id_ed25519"
`
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	want := satelliteInfraConfig{
		Project:      "my-proj",
		Zone:         "us-west1-a",
		SSHUser:      "solair",
		CIDR:         "203.0.113.7/32",
		IdentityFile: "~/.ssh/id_ed25519",
	}
	got, found := loadInfraViaConfig(t, "sat1")
	if got != want {
		t.Fatalf("infra = %+v, want %+v", got, want)
	}
	if !found {
		t.Fatal("infra block present but found=false — `up` would clobber a user-owned block")
	}

	// Unknown satellite / satellite without an infra block → zero value,
	// found=false (so `up` persists a fresh block).
	got, found = loadInfraViaConfig(t, "absent")
	if got != (satelliteInfraConfig{}) {
		t.Fatalf("absent satellite infra = %+v, want zero", got)
	}
	if found {
		t.Fatal("absent infra block reported found=true")
	}
}

// TestMergeInfraPrecedence pins flag > config for the infra inputs, including
// the explicit set-to-empty override (e.g. --cidr "" forces re-detection).
func TestMergeInfraPrecedence(t *testing.T) {
	cfg := satelliteInfraConfig{
		Project:      "cfg-proj",
		Zone:         "cfg-zone",
		SSHUser:      "cfg-user",
		CIDR:         "10.0.0.1/32",
		IdentityFile: "~/.ssh/cfg-key",
	}

	// No flags set → config passes through untouched.
	if got := mergeInfra(cfg, infraFlagOverrides{}); got != cfg {
		t.Fatalf("no-flags merge = %+v, want %+v", got, cfg)
	}

	// Every flag set → flags win, including set-to-empty.
	got := mergeInfra(cfg, infraFlagOverrides{
		Project: "flag-proj", ProjectSet: true,
		Zone: "", ZoneSet: true, // set-to-empty disables config value
		SSHUser: "flag-user", SSHUserSet: true,
		CIDR: "", CIDRSet: true, // set-to-empty re-enables auto-detection
		IdentityFile: "~/.ssh/flag-key", IdentitySet: true,
	})
	want := satelliteInfraConfig{
		Project:      "flag-proj",
		Zone:         "",
		SSHUser:      "flag-user",
		CIDR:         "",
		IdentityFile: "~/.ssh/flag-key",
	}
	if got != want {
		t.Fatalf("all-flags merge = %+v, want %+v", got, want)
	}

	// Unset flags never override, even with non-zero values lying around.
	got = mergeInfra(cfg, infraFlagOverrides{Project: "ignored", SSHUser: "ignored"})
	if got.Project != "cfg-proj" || got.SSHUser != "cfg-user" {
		t.Fatalf("unset flags leaked into merge: %+v", got)
	}
}

// TestWriteSatelliteInfraTOML covers the subtable-aware upsert: writing
// [satellites.<name>.infra] must leave the flat registry entry, the sibling
// provision subtable, and other satellites' blocks byte-for-byte, omit empty
// fields, replace (not duplicate) on re-write, and round-trip through the
// real loader.
func TestWriteSatelliteInfraTOML(t *testing.T) {
	configDir := setupGroveHome(t)
	tomlPath := filepath.Join(configDir, "grove.toml")
	original := `# hand-written config
[satellites.sat1]
ssh_addr = "1.1.1.1:22"
user = "u"
host_key = "ssh-ed25519 KEY"

[satellites.sat1.provision]
gh_token_cmd = "gh auth token"

[satellites.sat2]
ssh_addr = "2.2.2.2:22"
user = "keep"
host_key = "ssh-ed25519 KEEP"
`
	if err := os.WriteFile(tomlPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	infra := satelliteInfraConfig{Project: "my-proj", SSHUser: "solair", CIDR: "203.0.113.7/32"}
	if err := writeSatelliteInfra("sat1", infra); err != nil {
		t.Fatalf("writeSatelliteInfra: %v", err)
	}

	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, keep := range []string{
		"# hand-written config",
		"[satellites.sat1]",
		`ssh_addr = "1.1.1.1:22"`,
		"[satellites.sat1.provision]",
		`gh_token_cmd = "gh auth token"`,
		"[satellites.sat2]",
		`ssh_addr = "2.2.2.2:22"`,
	} {
		if !strings.Contains(text, keep) {
			t.Errorf("infra upsert destroyed %q:\n%s", keep, text)
		}
	}
	// Empty fields are omitted so they do not pin empty-string defaults.
	if strings.Contains(text, "zone =") || strings.Contains(text, "identity_file =") {
		t.Errorf("infra upsert wrote empty fields:\n%s", text)
	}

	// Round-trip through the real loader: infra block AND the untouched
	// registry entry + provision block all still load.
	if got, _ := loadInfraViaConfig(t, "sat1"); got != infra {
		t.Fatalf("loaded infra = %+v, want %+v", got, infra)
	}
	if got := loadSatellitesViaConfig(t)["sat1"]; got.SSHAddr != "1.1.1.1:22" {
		t.Fatalf("registry entry disturbed by infra write: %+v", got)
	}
	if prov := loadProvisionViaConfig(t, "sat1"); prov.GHTokenCmd != "gh auth token" {
		t.Fatalf("provision block disturbed by infra write: %+v", prov)
	}

	// Upsert: re-writing replaces the subtable, not duplicates it.
	infra.Zone = "us-west1-a"
	if err := writeSatelliteInfra("sat1", infra); err != nil {
		t.Fatalf("writeSatelliteInfra (upsert): %v", err)
	}
	data, _ = os.ReadFile(tomlPath)
	if n := strings.Count(string(data), "[satellites.sat1.infra]"); n != 1 {
		t.Fatalf("expected exactly 1 [satellites.sat1.infra] table after upsert, got %d:\n%s", n, data)
	}
	if got, _ := loadInfraViaConfig(t, "sat1"); got.Zone != "us-west1-a" {
		t.Fatalf("infra upsert not visible via loader: %+v", got)
	}
}

// TestWriteSatelliteInfraYAMLOnlyMachine pins the YAML fallback path: without
// a grove.toml the infra block lands in grove.yml, which the loader reads.
func TestWriteSatelliteInfraYAMLOnlyMachine(t *testing.T) {
	configDir := setupGroveHome(t)

	infra := satelliteInfraConfig{Project: "my-proj", SSHUser: "solair", CIDR: "203.0.113.7/32"}
	if err := writeSatelliteInfra("sat1", infra); err != nil {
		t.Fatalf("writeSatelliteInfra: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "grove.yml")); err != nil {
		t.Fatalf("expected grove.yml to be written: %v", err)
	}
	if got, _ := loadInfraViaConfig(t, "sat1"); got != infra {
		t.Fatalf("loaded infra = %+v, want %+v", got, infra)
	}
}

// TestDownPreservesInfraSubtable pins the whole point of the infra block: the
// registry removal `down` performs (removeSatelliteTOMLTable → the flat block
// only) must leave [satellites.<name>.infra] (and .provision) in place, so
// the next 0→1 `up <name>` needs zero flags.
func TestDownPreservesInfraSubtable(t *testing.T) {
	content := `[satellites.sat1]
ssh_addr = "1.2.3.4:22"
user = "u"
host_key = "ssh-ed25519 KEY"

[satellites.sat1.infra]
project = "my-proj"
ssh_user = "solair"
cidr = "203.0.113.7/32"

[satellites.sat1.provision]
gh_token_cmd = "gh auth token"

[satellites.sat2]
ssh_addr = "5.6.7.8:22"
user = "keep"
host_key = "ssh-ed25519 KEEP"
`
	// The splice itself: flat block gone, subtables and other satellites kept.
	out, removed := removeSatelliteTOMLTable(content, "sat1")
	if !removed {
		t.Fatal("expected sat1 flat block to be removed")
	}
	if strings.Contains(out, "1.2.3.4") || strings.Contains(out, "[satellites.sat1]\n") {
		t.Fatalf("sat1 flat block not fully removed:\n%s", out)
	}
	for _, keep := range []string{
		"[satellites.sat1.infra]",
		`project = "my-proj"`,
		`cidr = "203.0.113.7/32"`,
		"[satellites.sat1.provision]",
		"[satellites.sat2]",
	} {
		if !strings.Contains(out, keep) {
			t.Fatalf("removal dropped %q:\n%s", keep, out)
		}
	}

	// End-to-end through what `down` calls now: remove the state entry AND
	// the legacy flat config block. The infra block (and .provision) still
	// load afterwards, so the next `up sat1` merges them.
	configDir := setupGroveHome(t)
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := upsertSatelliteState("sat1", satelliteConfigEntry{
		SSHAddr: "9.9.9.9:22", User: "solair", HostKey: "ssh-ed25519 STATE",
	}); err != nil {
		t.Fatalf("upsertSatelliteState: %v", err)
	}

	removedState, err := removeSatelliteState("sat1")
	if err != nil {
		t.Fatalf("removeSatelliteState: %v", err)
	}
	if !removedState {
		t.Fatal("state entry not reported removed")
	}
	legacyRemoved, err := removeLegacySatelliteConfigEntry("sat1")
	if err != nil {
		t.Fatalf("removeLegacySatelliteConfigEntry: %v", err)
	}
	if !legacyRemoved {
		t.Fatal("legacy flat config entry not reported removed")
	}

	want := satelliteInfraConfig{Project: "my-proj", SSHUser: "solair", CIDR: "203.0.113.7/32"}
	if got, _ := loadInfraViaConfig(t, "sat1"); got != want {
		t.Fatalf("infra block after down = %+v, want %+v", got, want)
	}
	if prov := loadProvisionViaConfig(t, "sat1"); prov.GHTokenCmd != "gh auth token" {
		t.Fatalf("provision block after down = %+v", prov)
	}
	if got := loadSatellitesViaConfig(t)["sat1"]; got.SSHAddr != "" {
		t.Fatalf("flat registry entry survived down: %+v", got)
	}
	if state, err := loadSatelliteState(); err != nil || len(state) != 0 {
		t.Fatalf("state after down = (%v, %v), want empty", state, err)
	}
}

// TestSatelliteRegistryIgnoresInfraSubtable is the daemon-compat gate for the
// infra block (same stance as TestSatelliteRegistryIgnoresProvisionSubtable):
// the daemon's satellite.LoadRegistry decodes [satellites.*] through the exact
// same path used here — config.UnmarshalExtension, mapstructure with
// ErrorUnused left OFF — so an unknown infra subtable must be silently
// ignored, not an error.
func TestSatelliteRegistryIgnoresInfraSubtable(t *testing.T) {
	configDir := setupGroveHome(t)
	content := `[satellites.sat1]
ssh_addr = "203.0.113.7:22"
user = "solair"
host_key = "ssh-ed25519 AAAA"

[satellites.sat1.infra]
project = "my-proj"
zone = "us-west1-a"
ssh_user = "solair"
cidr = "203.0.113.7/32"
identity_file = "~/.ssh/id_ed25519"
`
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sats := loadSatellitesViaConfig(t)
	want := satelliteConfigEntry{
		SSHAddr: "203.0.113.7:22",
		User:    "solair",
		HostKey: "ssh-ed25519 AAAA",
	}
	if got := sats["sat1"]; got != want {
		t.Fatalf("registry entry with infra subtable = %+v, want %+v", got, want)
	}
}
