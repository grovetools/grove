package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The --json contract is what an agent codes against, so these tests assert
// the KEYS and their derivation, not just that something encodes.

func TestSplitSatelliteAddr(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr string
		host string
		port int
	}{
		{"empty", "", "", 0},
		{"bare host defaults to 22", "203.0.113.7", "203.0.113.7", 22},
		{"host:port", "203.0.113.7:22", "203.0.113.7", 22},
		{"docker loopback high port", "127.0.0.1:49213", "127.0.0.1", 49213},
		{"non-numeric port falls back", "host:ssh", "host", 22},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, port := splitSatelliteAddr(tc.addr)
			if host != tc.host || port != tc.port {
				t.Fatalf("splitSatelliteAddr(%q) = (%q, %d), want (%q, %d)", tc.addr, host, port, tc.host, tc.port)
			}
		})
	}
}

func TestSatelliteSSHCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry satelliteConfigEntry
		want  string
	}{
		{
			name:  "no address yet",
			entry: satelliteConfigEntry{ProviderRef: "tart:grove-x"},
			want:  "",
		},
		{
			name:  "tart: identity file and the admin user",
			entry: satelliteConfigEntry{SSHAddr: "192.168.64.5:22", User: "admin", IdentityFile: "/state/sat/tart/id_ed25519"},
			want:  "ssh -i /state/sat/tart/id_ed25519 -o IdentitiesOnly=yes admin@192.168.64.5",
		},
		{
			name:  "docker: the published loopback port survives",
			entry: satelliteConfigEntry{SSHAddr: "127.0.0.1:49213", User: "admin", IdentityFile: "/k"},
			want:  "ssh -i /k -o IdentitiesOnly=yes -p 49213 admin@127.0.0.1",
		},
		{
			name:  "gcp: agent-only auth, bare host",
			entry: satelliteConfigEntry{SSHAddr: "203.0.113.7", User: "grovedev"},
			want:  "ssh grovedev@203.0.113.7",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := satelliteSSHCommand(tc.entry); got != tc.want {
				t.Fatalf("satelliteSSHCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSatelliteEntryJSON(t *testing.T) {
	since := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	t.Run("exec entry with no daemon status", func(t *testing.T) {
		got := satelliteEntryJSON("tartdemo", satelliteConfigEntry{
			SSHAddr:      "192.168.64.5:22",
			User:         "admin",
			HostKey:      "ssh-ed25519 AAAA",
			IdentityFile: "/state/id_ed25519",
			Kind:         satelliteKindExec,
			ProviderRef:  "tart:grove-tartdemo",
		}, nil)
		if got.Kind != satelliteKindExec {
			t.Errorf("kind = %q, want %q", got.Kind, satelliteKindExec)
		}
		if got.State != satelliteStateExecOnly {
			t.Errorf("state = %q, want %q", got.State, satelliteStateExecOnly)
		}
		if got.Provider != "tart" {
			t.Errorf("provider = %q, want tart (derived from provider_ref)", got.Provider)
		}
		if got.SSHHost != "192.168.64.5" || got.SSHPort != 22 {
			t.Errorf("ssh host/port = %q/%d", got.SSHHost, got.SSHPort)
		}
		if !got.HostKeyPinned {
			t.Error("host_key_pinned = false for an entry carrying a host key")
		}
		if got.Live || got.PartialUp {
			t.Errorf("live=%v partial_up=%v, want both false", got.Live, got.PartialUp)
		}
		if !strings.Contains(got.SSHCommand, "admin@192.168.64.5") {
			t.Errorf("ssh_command = %q", got.SSHCommand)
		}
		if got.GroveSSHCommand != "grove satellite ssh tartdemo" {
			t.Errorf("grove_ssh_command = %q", got.GroveSSHCommand)
		}
	})

	t.Run("no entry at all leaves kind at the zero value", func(t *testing.T) {
		got := satelliteEntryJSON("phantom", satelliteConfigEntry{}, &satelliteLiveStatus{state: "disconnected"})
		if got.Kind != "" {
			t.Fatalf("kind = %q for a row with no registry entry, want \"\" — a consumer cannot tell that from a genuinely full satellite", got.Kind)
		}
	})

	t.Run("empty kind normalizes to full", func(t *testing.T) {
		got := satelliteEntryJSON("sat", satelliteConfigEntry{SSHAddr: "203.0.113.7"}, nil)
		if got.Kind != satelliteKindFull {
			t.Fatalf("kind = %q, want %q — the registry's empty default must never leak", got.Kind, satelliteKindFull)
		}
	})

	t.Run("partial up is a boolean, not a string match", func(t *testing.T) {
		got := satelliteEntryJSON("half", satelliteConfigEntry{ProviderRef: "tart:grove-half"}, nil)
		if !got.PartialUp {
			t.Fatal("partial_up = false for a provider_ref with no pinned endpoint")
		}
		if got.State != satelliteStatePartialUp {
			t.Errorf("state = %q, want %q", got.State, satelliteStatePartialUp)
		}
		if !strings.Contains(got.LastError, "grove satellite down half") {
			t.Errorf("last_error carries no remediation: %q", got.LastError)
		}
	})

	t.Run("live status wins for addr and health", func(t *testing.T) {
		got := satelliteEntryJSON("sat", satelliteConfigEntry{SSHAddr: "203.0.113.7:22"}, &satelliteLiveStatus{
			state: "connected", addr: "203.0.113.9:22", since: since, forward: "127.0.0.1:8789", lastError: "",
		})
		if !got.Live {
			t.Error("live = false for a daemon-reported satellite")
		}
		if got.SSHAddr != "203.0.113.9:22" || got.SSHHost != "203.0.113.9" {
			t.Errorf("live addr did not win: %q / %q", got.SSHAddr, got.SSHHost)
		}
		if got.State != "connected" || got.Forward != "127.0.0.1:8789" {
			t.Errorf("state/forward = %q/%q", got.State, got.Forward)
		}
		if got.Since != "2026-07-19T12:00:00Z" {
			t.Errorf("since = %q, want RFC3339", got.Since)
		}
	})
}

// TestSatelliteStatusJSONKeysAreAlwaysPresent locks the promise the contract
// comment makes: a consumer indexes a field rather than probing for it, so no
// documented key may be elided by omitempty when its value is zero.
func TestSatelliteStatusJSONKeysAreAlwaysPresent(t *testing.T) {
	payload := satelliteStatusJSON{
		Schema:     satelliteStatusSchema,
		Satellites: satelliteStatusPayload(map[string]satelliteConfigEntry{"bare": {}}, nil),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema          string           `json:"schema"`
		DaemonReachable *bool            `json:"daemon_reachable"`
		Satellites      []map[string]any `json:"satellites"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != satelliteStatusSchema {
		t.Fatalf("schema = %q, want %q", decoded.Schema, satelliteStatusSchema)
	}
	if decoded.DaemonReachable == nil {
		t.Fatal("daemon_reachable absent")
	}
	if len(decoded.Satellites) != 1 {
		t.Fatalf("satellites = %d, want 1", len(decoded.Satellites))
	}
	for _, key := range []string{
		"name", "kind", "state", "partial_up", "live", "provider", "provider_ref",
		"ssh_addr", "ssh_host", "ssh_port", "user", "identity_file", "host_key_pinned",
		"ssh_command", "grove_ssh_command", "socket_path", "sync_local_port",
		"sync_remote_addr", "forward", "since", "last_error",
	} {
		if _, ok := decoded.Satellites[0][key]; !ok {
			t.Errorf("key %q missing from an all-zero satellite object", key)
		}
	}
}

func TestSatelliteStatusPayloadOrdering(t *testing.T) {
	got := satelliteStatusPayload(
		map[string]satelliteConfigEntry{"zeta": {}, "alpha": {}},
		map[string]satelliteLiveStatus{"mid": {state: "connected"}},
	)
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	want := []string{"alpha", "mid", "zeta"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v (sorted union of configured and live)", names, want)
	}
}

func TestSatelliteVerbReportPayload(t *testing.T) {
	r := &satelliteVerbReport{schema: satelliteUpSchema, action: "up", name: "sat", timings: newSatelliteTimings()}
	r.phase("provider_up", time.Now())
	if _, ok := r.timings.phases["provider_up_ms"]; !ok {
		t.Fatalf("phase key not suffixed _ms: %v", r.timings.phases)
	}
	if got := r.timings.payload(); got.Phases == nil {
		t.Fatal("timings payload has a nil phases map")
	}
}

func TestExitOnSatellitePartialPassesThroughOtherErrors(t *testing.T) {
	plain := errors.New("transport blew up")
	if got := exitOnSatellitePartial(plain); !errors.Is(got, plain) {
		t.Fatalf("exitOnSatellitePartial rewrote a plain error: %v", got)
	}
	if got := exitOnSatellitePartial(nil); got != nil {
		t.Fatalf("exitOnSatellitePartial(nil) = %v", got)
	}
	var partial *satellitePartialError
	if !errors.As(satellitePartialf("PARTIAL: %d held", 3), &partial) {
		t.Fatal("satellitePartialf did not produce a recognizable partial error")
	}
	if !strings.Contains(partial.Error(), "3 held") {
		t.Fatalf("partial error text = %q", partial.Error())
	}
}

// TestConfirmOrAbortNonTTYNamesTheFlag: `go test` runs with stdin off a
// terminal, which is exactly the case P10 reported as a bare "Error: aborted".
func TestConfirmOrAbortNonTTYNamesTheFlag(t *testing.T) {
	err := confirmOrAbort("Destroy satellite \"x\"?")
	if err == nil {
		t.Fatal("confirmOrAbort succeeded with stdin off a terminal")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error does not name the flag to pass: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "not a terminal") {
		t.Fatalf("error does not explain why the prompt failed: %q", err.Error())
	}
}
