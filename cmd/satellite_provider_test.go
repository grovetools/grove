package cmd

import (
	"strings"
	"testing"
)

// TestSatelliteProviderFor pins the provider registry resolution: empty and
// "gcp" both resolve to the gcp provider (whose satellite-kind default is
// full — the pre-kind behavior), "tart" resolves to the tart provider (exec
// default, no bootstrap script), and an unknown target errors listing the
// known providers.
func TestSatelliteProviderFor(t *testing.T) {
	for _, target := range []string{"", "gcp"} {
		p, err := satelliteProviderFor(target)
		if err != nil {
			t.Fatalf("satelliteProviderFor(%q): %v", target, err)
		}
		if p.Kind() != "gcp" {
			t.Errorf("satelliteProviderFor(%q).Kind() = %q, want gcp", target, p.Kind())
		}
		if p.DefaultSatelliteKind() != satelliteKindFull {
			t.Errorf("gcp DefaultSatelliteKind() = %q, want %q", p.DefaultSatelliteKind(), satelliteKindFull)
		}
		if !p.UsesBootstrapScript() {
			t.Error("gcp UsesBootstrapScript() = false, want true")
		}
	}

	tp, err := satelliteProviderFor("tart")
	if err != nil {
		t.Fatalf("satelliteProviderFor(tart): %v", err)
	}
	if tp.Kind() != "tart" {
		t.Errorf("tart Kind() = %q, want tart", tp.Kind())
	}
	if tp.DefaultSatelliteKind() != satelliteKindExec {
		t.Errorf("tart DefaultSatelliteKind() = %q, want %q", tp.DefaultSatelliteKind(), satelliteKindExec)
	}
	if tp.UsesBootstrapScript() {
		t.Error("tart UsesBootstrapScript() = true, want false")
	}

	_, err = satelliteProviderFor("vsphere")
	if err == nil {
		t.Fatal("unknown target resolved to a provider")
	}
	if !strings.Contains(err.Error(), `"vsphere"`) || !strings.Contains(err.Error(), "known providers: docker, gcp, tart") {
		t.Errorf("unknown-target error does not name the target and list known providers: %v", err)
	}
}

// TestResolveSatelliteKind pins the --kind flag semantics: empty resolves to
// the provider default (gcp: full), full/exec pass through, anything else is
// rejected.
func TestResolveSatelliteKind(t *testing.T) {
	p, err := satelliteProviderFor("gcp")
	if err != nil {
		t.Fatalf("satelliteProviderFor(gcp): %v", err)
	}

	for flagValue, want := range map[string]string{
		"":                satelliteKindFull,
		satelliteKindFull: satelliteKindFull,
		satelliteKindExec: satelliteKindExec,
	} {
		got, err := resolveSatelliteKind(flagValue, p)
		if err != nil {
			t.Errorf("resolveSatelliteKind(%q): %v", flagValue, err)
			continue
		}
		if got != want {
			t.Errorf("resolveSatelliteKind(%q) = %q, want %q", flagValue, got, want)
		}
	}

	if _, err := resolveSatelliteKind("both", p); err == nil || !strings.Contains(err.Error(), "--kind") {
		t.Errorf("invalid --kind value not rejected: %v", err)
	}
}

// TestSatelliteEndpointHost pins the host extraction the shared `up` verb
// performs on a provider endpoint (keyscan/bootstrap/print target).
func TestSatelliteEndpointHost(t *testing.T) {
	host, err := satelliteEndpointHost(satelliteEndpoint{SSHAddr: "203.0.113.7:22"})
	if err != nil {
		t.Fatalf("satelliteEndpointHost: %v", err)
	}
	if host != "203.0.113.7" {
		t.Errorf("host = %q, want 203.0.113.7", host)
	}
	if _, err := satelliteEndpointHost(satelliteEndpoint{SSHAddr: "no-port"}); err == nil {
		t.Error("malformed endpoint address not rejected")
	}
}
