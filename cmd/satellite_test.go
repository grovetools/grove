package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/models"
	"github.com/pelletier/go-toml/v2"
)

// setupGroveHome points GROVE_HOME at a temp root and returns the config dir
// (paths.ConfigDir() = $GROVE_HOME/config/grove — the extra /grove segment
// matters).
func setupGroveHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	configDir := filepath.Join(home, "config", "grove")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return configDir
}

// loadSatellitesViaConfig loads the merged grove config from a fresh empty dir
// (so the global XDG config is the only layer) and decodes the [satellites.*]
// extension exactly the way daemon's satellite.LoadRegistry does
// (cfg.UnmarshalExtension keyed off the same yaml tags).
func loadSatellitesViaConfig(t *testing.T) map[string]satelliteConfigEntry {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	var raw map[string]satelliteConfigEntry
	if err := cfg.UnmarshalExtension("satellites", &raw); err != nil {
		t.Fatalf("UnmarshalExtension(satellites): %v", err)
	}
	return raw
}

// TestRemoveLegacySatelliteConfigEntryTOML covers `down`'s legacy cleanup: a
// flat [satellites.<name>] entry an OLDER `up` wrote into grove.toml is
// removed (that block is CLI-written state, not user config), while unrelated
// tables, comments, and other satellites survive byte-for-byte — and an
// absent entry is a clean no-op.
func TestRemoveLegacySatelliteConfigEntryTOML(t *testing.T) {
	configDir := setupGroveHome(t)
	tomlPath := filepath.Join(configDir, "grove.toml")
	original := `# my grove config — do not clobber
[satellites.other]
ssh_addr = "10.0.0.9:22"
user = "keep"
host_key = "ssh-ed25519 OTHER"

[satellites.sat1]
ssh_addr = "203.0.113.7:22"
user = "solair"
host_key = "ssh-ed25519 AAAATESTKEY"
`
	if err := os.WriteFile(tomlPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := removeLegacySatelliteConfigEntry("sat1")
	if err != nil {
		t.Fatalf("removeLegacySatelliteConfigEntry: %v", err)
	}
	if !removed {
		t.Fatal("expected the legacy sat1 entry to be reported removed")
	}

	sats := loadSatellitesViaConfig(t)
	if _, ok := sats["sat1"]; ok {
		t.Fatalf("satellites.sat1 still present after removal: %v", sats)
	}
	if _, ok := sats["other"]; !ok {
		t.Fatalf("removal dropped unrelated satellites.other: %v", sats)
	}
	data, _ := os.ReadFile(tomlPath)
	if !strings.Contains(string(data), "# my grove config — do not clobber") {
		t.Fatalf("comment was destroyed by legacy removal:\n%s", data)
	}

	// Removing a nonexistent entry is a no-op, not an error.
	removed, err = removeLegacySatelliteConfigEntry("sat1")
	if err != nil {
		t.Fatalf("removeLegacySatelliteConfigEntry (absent): %v", err)
	}
	if removed {
		t.Fatal("absent entry reported removed=true")
	}
}

// TestRemoveLegacySatelliteConfigEntryYAML pins the YAML fallback: a legacy
// entry in grove.yml (a machine that never had a grove.toml) is removed, and
// a machine with NO global config file at all is a clean no-op — `down` must
// never fail on the now-normal state-only layout.
func TestRemoveLegacySatelliteConfigEntryYAML(t *testing.T) {
	configDir := setupGroveHome(t)

	// No config file at all: clean no-op.
	removed, err := removeLegacySatelliteConfigEntry("sat1")
	if err != nil {
		t.Fatalf("removeLegacySatelliteConfigEntry (no config file): %v", err)
	}
	if removed {
		t.Fatal("no config file reported removed=true")
	}

	yamlPath := filepath.Join(configDir, "grove.yml")
	original := "satellites:\n  sat1:\n    ssh_addr: \"203.0.113.7:22\"\n    user: solair\n    host_key: ssh-ed25519 AAAATESTKEY\n"
	if err := os.WriteFile(yamlPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err = removeLegacySatelliteConfigEntry("sat1")
	if err != nil {
		t.Fatalf("removeLegacySatelliteConfigEntry (yaml): %v", err)
	}
	if !removed {
		t.Fatal("expected the legacy yaml sat1 entry to be reported removed")
	}
	if sats := loadSatellitesViaConfig(t); len(sats) != 0 {
		t.Fatalf("satellites still present after yaml removal: %v", sats)
	}
}

// TestRemoveSatelliteTOMLTable exercises the textual splice directly: only the
// named table's block is removed, up to the next table header.
func TestRemoveSatelliteTOMLTable(t *testing.T) {
	content := `[ui]
theme = "dark"

[satellites.sat1]
ssh_addr = "1.2.3.4:22"
# a comment inside the block
user = "u"

[satellites.sat2]
ssh_addr = "5.6.7.8:22"
`
	out, removed := removeSatelliteTOMLTable(content, "sat1")
	if !removed {
		t.Fatal("expected sat1 block to be removed")
	}
	if strings.Contains(out, "1.2.3.4") || strings.Contains(out, "[satellites.sat1]") {
		t.Fatalf("sat1 block not fully removed:\n%s", out)
	}
	for _, keep := range []string{"[ui]", `theme = "dark"`, "[satellites.sat2]", "5.6.7.8"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("removal dropped unrelated content %q:\n%s", keep, out)
		}
	}

	if _, removed := removeSatelliteTOMLTable(content, "nope"); removed {
		t.Fatal("removal of absent table reported removed=true")
	}
}

// TestWriteSatelliteTFVars covers B2: `up` persists the default-less terraform
// variables so `down` (terraform destroy -input=false) resolves them without
// prompting. Simple key = "value" tfvars lines are TOML-parseable, so the
// round-trip is asserted through a real parser.
func TestWriteSatelliteTFVars(t *testing.T) {
	dir := t.TempDir()
	if err := writeSatelliteTFVars(dir, "my-proj", "solair", "203.0.113.7/32", "sat1", "", ""); err != nil {
		t.Fatalf("writeSatelliteTFVars: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, satelliteTFVarsName))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	if err := toml.Unmarshal(data, &vars); err != nil {
		t.Fatalf("tfvars did not parse: %v\n%s", err, data)
	}
	want := map[string]string{
		"project_id":       "my-proj",
		"ssh_user":         "solair",
		"allowed_ssh_cidr": "203.0.113.7/32",
		"vm_name":          "sat1",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Fatalf("tfvars[%s] = %q, want %q\n%s", k, vars[k], v, data)
		}
	}
	if _, ok := vars["zone"]; ok {
		t.Fatalf("zone should be omitted when unset:\n%s", data)
	}
	if _, ok := vars["service_account_email"]; ok {
		t.Fatalf("service_account_email should be omitted when unset:\n%s", data)
	}

	// With zone + service account overrides they must be persisted too.
	if err := writeSatelliteTFVars(dir, "my-proj", "solair", "203.0.113.7/32", "sat1", "us-west1-a", "sa@my-proj.iam.gserviceaccount.com"); err != nil {
		t.Fatalf("writeSatelliteTFVars (zone+sa): %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, satelliteTFVarsName))
	vars = map[string]string{}
	if err := toml.Unmarshal(data, &vars); err != nil {
		t.Fatalf("tfvars (zone+sa) did not parse: %v\n%s", err, data)
	}
	if vars["zone"] != "us-west1-a" {
		t.Fatalf("tfvars[zone] = %q, want us-west1-a\n%s", vars["zone"], data)
	}
	if vars["service_account_email"] != "sa@my-proj.iam.gserviceaccount.com" {
		t.Fatalf("tfvars[service_account_email] = %q\n%s", vars["service_account_email"], data)
	}
}

// TestSatelliteTableRowsForward pins the Forward column contract for
// `satellite status`/`list`: a live status carrying the daemon's forward
// string (models.SatelliteStatus.Forward) renders it in the Forward cell,
// and a status without one shows the "-" placeholder.
func TestSatelliteTableRowsForward(t *testing.T) {
	forwardIdx := -1
	for i, h := range satelliteTableHeaders {
		if h == "Forward" {
			forwardIdx = i
		}
	}
	if forwardIdx == -1 {
		t.Fatalf("no Forward column in headers: %v", satelliteTableHeaders)
	}

	configured := map[string]satelliteConfigEntry{
		"sat-fwd":  {SSHAddr: "203.0.113.7:22"},
		"sat-none": {SSHAddr: "203.0.113.8:22"},
	}
	live := map[string]satelliteLiveStatus{
		"sat-fwd":  {state: "connected", addr: "203.0.113.7:22", forward: "active on 127.0.0.1:8789"},
		"sat-none": {state: "connected", addr: "203.0.113.8:22"},
	}

	rows := satelliteTableRows(configured, live)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
	byName := map[string][]string{}
	for _, r := range rows {
		if len(r) != len(satelliteTableHeaders) {
			t.Fatalf("row width %d != header width %d: %v", len(r), len(satelliteTableHeaders), r)
		}
		byName[r[0]] = r
	}

	if got := byName["sat-fwd"][forwardIdx]; got != "active on 127.0.0.1:8789" {
		t.Fatalf("Forward cell = %q, want %q", got, "active on 127.0.0.1:8789")
	}
	if got := byName["sat-none"][forwardIdx]; got != "-" {
		t.Fatalf("Forward cell without forward = %q, want %q", got, "-")
	}

	// A configured-only satellite (daemon not reporting it) also stays clean.
	rows = satelliteTableRows(map[string]satelliteConfigEntry{"sat-cfg": {SSHAddr: "203.0.113.9:22"}}, nil)
	if got := rows[0][forwardIdx]; got != "-" {
		t.Fatalf("Forward cell for configured-only satellite = %q, want %q", got, "-")
	}
}

// TestSatelliteTableRowsExecOnly pins the status/list rendering of exec-kind
// satellites: with no live daemon status the merged-registry kind alone
// renders State "exec-only" (never the "restart groved?" hint), agreeing
// with the daemon's own exec-only status when groved is running — while full
// satellites keep their existing states.
func TestSatelliteTableRowsExecOnly(t *testing.T) {
	stateIdx, forwardIdx := -1, -1
	for i, h := range satelliteTableHeaders {
		switch h {
		case "State":
			stateIdx = i
		case "Forward":
			forwardIdx = i
		}
	}
	if stateIdx == -1 || forwardIdx == -1 {
		t.Fatalf("missing State/Forward columns in headers: %v", satelliteTableHeaders)
	}

	configured := map[string]satelliteConfigEntry{
		"exec-sat": {SSHAddr: "203.0.113.7:22", Kind: satelliteKindExec},
		"full-sat": {SSHAddr: "203.0.113.8:22"},
	}

	// Daemon not running: no live statuses at all.
	rows := satelliteTableRows(configured, nil)
	byName := map[string][]string{}
	for _, r := range rows {
		byName[r[0]] = r
	}
	if got := byName["exec-sat"][stateIdx]; got != satelliteStateExecOnly {
		t.Errorf("exec-kind State without daemon = %q, want %q", got, satelliteStateExecOnly)
	}
	if got := byName["exec-sat"][forwardIdx]; got != "-" {
		t.Errorf("exec-kind Forward = %q, want -", got)
	}
	if got := byName["full-sat"][stateIdx]; got != "not connected (restart groved?)" {
		t.Errorf("full-kind State degraded: %q", got)
	}

	// Daemon running: the live exec-only status flows through unchanged.
	rows = satelliteTableRows(configured, map[string]satelliteLiveStatus{
		"exec-sat": {state: satelliteStateExecOnly, addr: "203.0.113.7:22"},
	})
	for _, r := range rows {
		byName[r[0]] = r
	}
	if got := byName["exec-sat"][stateIdx]; got != satelliteStateExecOnly {
		t.Errorf("exec-kind State with daemon = %q, want %q", got, satelliteStateExecOnly)
	}
}

// TestFormatReloadSummary pins the one-liner `up`/`down` print after the
// registry hot-reload POST: action groups only when non-empty, unchanged
// elided, and an all-empty summary reading "no changes".
func TestFormatReloadSummary(t *testing.T) {
	cases := []struct {
		name string
		in   models.SatelliteReloadSummary
		want string
	}{
		{"added only", models.SatelliteReloadSummary{Added: []string{"mysat"}}, "added: mysat"},
		{"removed only", models.SatelliteReloadSummary{Removed: []string{"mysat"}}, "removed: mysat"},
		{
			"mixed, unchanged elided",
			models.SatelliteReloadSummary{Changed: []string{"a", "b"}, Unchanged: []string{"c"}},
			"changed: a, b",
		},
		{"empty", models.SatelliteReloadSummary{}, "no changes"},
	}
	for _, tc := range cases {
		if got := formatReloadSummary(&tc.in); got != tc.want {
			t.Errorf("%s: formatReloadSummary = %q, want %q", tc.name, got, tc.want)
		}
	}
}
