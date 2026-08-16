package cmd

import (
	"strings"
	"testing"
)

func TestValidateSatelliteBareUp(t *testing.T) {
	bareEntry := satelliteConfigEntry{ProviderRef: "tart:grove-sat-x", Kind: satelliteKindExec, Bare: true}
	stockedEntry := satelliteConfigEntry{ProviderRef: "tart:grove-sat-x", Kind: satelliteKindExec}

	cases := []struct {
		name             string
		providerKind     string
		execKind         bool
		explicitPrebuilt bool
		existing         satelliteConfigEntry
		wantErr          string
	}{
		{"fresh tart exec ok", tartSatelliteTarget, true, false, satelliteConfigEntry{}, ""},
		{"re-up of bare entry ok", tartSatelliteTarget, true, false, bareEntry, ""},
		{"docker refused", dockerSatelliteTarget, true, false, satelliteConfigEntry{}, "--bare currently supports"},
		{"gcp refused", "gcp", true, false, satelliteConfigEntry{}, "--bare currently supports"},
		{"full kind refused", tartSatelliteTarget, false, false, satelliteConfigEntry{}, "exec-kind"},
		{"explicit prebuilt refused", tartSatelliteTarget, true, true, satelliteConfigEntry{}, "--prebuilt contradicts --bare"},
		{"existing stocked entry refused", tartSatelliteTarget, true, false, stockedEntry, "cannot remove"},
		// A config-only row (no provider_ref → no machine was ever created)
		// must not block a bare provision.
		{"config-only row ok", tartSatelliteTarget, true, false, satelliteConfigEntry{Kind: satelliteKindExec}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSatelliteBareUp(tc.providerKind, tc.execKind, tc.explicitPrebuilt, tc.existing, "x")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestTartLayer0ProbeBare(t *testing.T) {
	const sshdConf = "/etc/ssh/sshd_config.d/00-grove.conf"
	const profile = "/etc/profile.d/grove-satellite.sh"
	const sentinel = "/var/lib/grove-satellite/startup-done"

	bare := tartLayer0Probe(false, true)
	if !strings.Contains(bare, sshdConf) {
		t.Fatalf("bare probe misses the sshd drop-in: %q", bare)
	}
	if strings.Contains(bare, profile) || strings.Contains(bare, sentinel) {
		t.Fatalf("bare probe checks non-bare artifacts: %q", bare)
	}

	// The non-bare exec probe must include the profile fragment: that is what
	// makes a plain `up` on a bare guest re-run configure (promotion) instead
	// of skipping it because the sshd drop-in already exists.
	execProbe := tartLayer0Probe(false, false)
	if !strings.Contains(execProbe, profile) {
		t.Fatalf("exec probe misses the profile fragment (bare→stocked promotion would be skipped): %q", execProbe)
	}
	if strings.Contains(execProbe, sentinel) {
		t.Fatalf("exec probe checks the full-node sentinel: %q", execProbe)
	}

	if fullProbe := tartLayer0Probe(true, false); !strings.Contains(fullProbe, sentinel) {
		t.Fatalf("full probe misses the startup sentinel: %q", fullProbe)
	}
}

func TestTartGuestConfigScriptBare(t *testing.T) {
	const pub = "ssh-ed25519 AAAA test-key"

	bare := tartGuestConfigScript(pub, false, true)
	for _, want := range []string{pub, "00-grove.conf", "PasswordAuthentication no"} {
		if !strings.Contains(bare, want) {
			t.Fatalf("bare script misses transport prep %q:\n%s", want, bare)
		}
	}
	// The clean-room promise: no grove paths of any kind on a bare guest.
	for _, forbidden := range []string{"grove-satellite.sh", ".local/share/grove", "startup-done"} {
		if strings.Contains(bare, forbidden) {
			t.Fatalf("bare script writes grove state %q:\n%s", forbidden, bare)
		}
	}

	stocked := tartGuestConfigScript(pub, false, false)
	for _, want := range []string{"grove-satellite.sh", ".local/share/grove/bin"} {
		if !strings.Contains(stocked, want) {
			t.Fatalf("exec script misses guest prep %q:\n%s", want, stocked)
		}
	}

	if full := tartGuestConfigScript(pub, true, false); !strings.Contains(full, "startup-done") {
		t.Fatalf("full script misses the startup sentinel:\n%s", full)
	}
}

// TestMergeSatelliteEntriesBare pins the config∪state merge carrying the bare
// marker: yaml can never author Bare (tag "-"), so the state snapshot is its
// only source, and a config-side table for the same name (e.g. the
// marker-tagged infra block `up` itself writes) must not strip it — that
// stripping is exactly the live bug this test was written against: status
// showed bare:false and the idempotent bare re-`up` refused its own satellite.
func TestMergeSatelliteEntriesBare(t *testing.T) {
	state := map[string]satelliteConfigEntry{
		"clean": {ProviderRef: "tart:grove-sat-clean", Kind: satelliteKindExec, Bare: true},
	}
	config := map[string]satelliteConfigEntry{
		"clean": {}, // an all-zero row, as an infra-subtable-only config table decodes
	}
	merged, _ := mergeSatelliteEntries(config, state)
	if !merged["clean"].Bare {
		t.Fatalf("merge with a config-side table stripped Bare: %+v", merged["clean"])
	}
	// State-only passthrough keeps it too.
	merged, _ = mergeSatelliteEntries(nil, state)
	if !merged["clean"].Bare {
		t.Fatalf("state-only merge lost Bare: %+v", merged["clean"])
	}
}

func TestSatelliteBareStateAndViews(t *testing.T) {
	setupGroveHome(t)

	entry := satelliteConfigEntry{
		SSHAddr:     "192.168.64.5:22",
		User:        "admin",
		HostKey:     "ssh-ed25519 AAAA",
		Kind:        satelliteKindExec,
		ProviderRef: "tart:grove-sat-clean",
		Bare:        true,
	}
	if err := upsertSatelliteState("clean", entry); err != nil {
		t.Fatalf("upsertSatelliteState: %v", err)
	}
	got := mustLoadSatelliteState(t)["clean"]
	if !got.Bare {
		t.Fatalf("Bare not round-tripped through the state file: %+v", got)
	}

	if state := satelliteEntryState("clean", got, nil); state != "exec-only (bare)" {
		t.Fatalf("registry-derived state = %q, want %q", state, "exec-only (bare)")
	}
	if row := satelliteEntryJSON("clean", got, nil); !row.Bare {
		t.Fatalf("satelliteEntryJSON dropped bare: %+v", row)
	}

	// The promote path assembles a fresh entry with Bare unset; the marker
	// must not survive an upsert.
	promoted := entry
	promoted.Bare = false
	if err := upsertSatelliteState("clean", promoted); err != nil {
		t.Fatalf("upsertSatelliteState (promote): %v", err)
	}
	got = mustLoadSatelliteState(t)["clean"]
	if got.Bare {
		t.Fatalf("Bare survived promotion: %+v", got)
	}
	if state := satelliteEntryState("clean", got, nil); state != satelliteStateExecOnly {
		t.Fatalf("promoted state = %q, want %q", state, satelliteStateExecOnly)
	}
}
