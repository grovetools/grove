package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTartVMNaming pins the local VM naming and provider_ref scheme the
// restart-vs-collision check depends on.
func TestTartVMNaming(t *testing.T) {
	if got := tartVMName("mysat"); got != "grove-sat-mysat" {
		t.Errorf("tartVMName = %q, want grove-sat-mysat", got)
	}
	if got := tartProviderRef(tartVMName("mysat")); got != "tart:grove-sat-mysat" {
		t.Errorf("tartProviderRef = %q, want tart:grove-sat-mysat", got)
	}
}

// stubTartOnPath puts a fake `tart` binary (answering `list --format json`
// with an empty array) at the front of PATH so preflight tests never need the
// real tool.
func stubTartOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(dir, "tart"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestTartPrepareUpPreflight pins the preflight behavior: a missing tart
// binary yields the actionable brew-install error, and with a (stub) tart
// present the guest image resolves to the default or the infra override.
// Host-arch gated: the arch check itself makes the test meaningless
// elsewhere.
func TestTartPrepareUpPreflight(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skipf("tart preflight requires darwin/arm64 (this is %s/%s)", runtime.GOOS, runtime.GOARCH)
	}

	// Missing binary → actionable error.
	t.Setenv("PATH", t.TempDir()) // empty dir: no tart
	p := &tartSatelliteProvider{target: tartSatelliteTarget}
	err := p.PrepareUp(&satelliteUpOptions{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "brew install cirruslabs/cli/tart") {
		t.Fatalf("missing-tart preflight error = %v, want brew install suggestion", err)
	}

	// Stub tart present → image defaults.
	stubTartOnPath(t)
	p = &tartSatelliteProvider{target: tartSatelliteTarget}
	if err := p.PrepareUp(&satelliteUpOptions{Name: "x"}); err != nil {
		t.Fatalf("PrepareUp with stub tart: %v", err)
	}
	if p.image != defaultTartImage {
		t.Errorf("default image = %q, want %q", p.image, defaultTartImage)
	}

	// Infra image override wins.
	p = &tartSatelliteProvider{target: tartSatelliteTarget}
	if err := p.PrepareUp(&satelliteUpOptions{Name: "x", Infra: satelliteInfraConfig{Image: "ghcr.io/cirruslabs/debian:latest"}}); err != nil {
		t.Fatalf("PrepareUp with image override: %v", err)
	}
	if p.image != "ghcr.io/cirruslabs/debian:latest" {
		t.Errorf("image override = %q, want the infra value", p.image)
	}

	// Up without PrepareUp is a hard error.
	if _, err := (&tartSatelliteProvider{target: tartSatelliteTarget}).Up(t.Context(), &satelliteUpOptions{Name: "x"}); err == nil || !strings.Contains(err.Error(), "without PrepareUp") {
		t.Errorf("Up without PrepareUp = %v, want the guard error", err)
	}
}

// TestMergeInfraImage pins the --image flag-over-config precedence (same
// stance as every other infra field); tart_home's own precedence is
// TestMergeInfraTartHome's.
func TestMergeInfraImage(t *testing.T) {
	cfg := satelliteInfraConfig{Image: "cfg-image", TartHome: "/tank/tart"}
	out := mergeInfra(cfg, infraFlagOverrides{})
	if out.Image != "cfg-image" || out.TartHome != "/tank/tart" {
		t.Errorf("unset flags must keep config values: %+v", out)
	}
	out = mergeInfra(cfg, infraFlagOverrides{Image: "flag-image", ImageSet: true})
	if out.Image != "flag-image" {
		t.Errorf("set --image must win: %q", out.Image)
	}
	out = mergeInfra(cfg, infraFlagOverrides{Image: "", ImageSet: true})
	if out.Image != "" {
		t.Errorf("--image set-to-empty must disable the config value: %q", out.Image)
	}
}

// TestMergeSatelliteEntriesProviderRef pins provider_ref as machine-derived
// state: the state value wins over (and fills into) the config view, and a
// state-only entry passes through complete.
func TestMergeSatelliteEntriesProviderRef(t *testing.T) {
	fromConfig := map[string]satelliteConfigEntry{
		"sat": {User: "admin", ProviderRef: "tart:stale-vm"},
	}
	fromState := map[string]satelliteConfigEntry{
		"sat":       {SSHAddr: "192.168.64.2:22", ProviderRef: "tart:grove-sat-sat"},
		"stateonly": {SSHAddr: "192.168.64.3:22", ProviderRef: "tart:grove-sat-stateonly"},
	}
	merged, _ := mergeSatelliteEntries(fromConfig, fromState)
	if got := merged["sat"].ProviderRef; got != "tart:grove-sat-sat" {
		t.Errorf("state provider_ref must win: %q", got)
	}
	if got := merged["stateonly"].ProviderRef; got != "tart:grove-sat-stateonly" {
		t.Errorf("state-only provider_ref must pass through: %q", got)
	}

	// Empty state ref leaves a config-authored one alone.
	merged, _ = mergeSatelliteEntries(fromConfig, map[string]satelliteConfigEntry{"sat": {SSHAddr: "192.168.64.2:22"}})
	if got := merged["sat"].ProviderRef; got != "tart:stale-vm" {
		t.Errorf("empty state ref must not clear the config value: %q", got)
	}
}

// TestRenderSatelliteInfraTOMLTartFields pins the write-back/drift rendering
// of the tart-only infra fields (omitted when empty, quoted when set).
func TestRenderSatelliteInfraTOMLTartFields(t *testing.T) {
	got := renderSatelliteInfraTOML("mysat", satelliteInfraConfig{Target: "tart", Image: "img:latest", TartHome: "/tank/tart"})
	for _, want := range []string{"[satellites.mysat.infra]", `target = "tart"`, `image = "img:latest"`, `tart_home = "/tank/tart"`} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered block missing %q:\n%s", want, got)
		}
	}
	got = renderSatelliteInfraTOML("mysat", satelliteInfraConfig{Target: "gcp", Project: "p", SSHUser: "u"})
	if strings.Contains(got, "image") || strings.Contains(got, "tart_home") {
		t.Errorf("empty tart fields must be omitted:\n%s", got)
	}
}

// TestResolveTartHome pins the effective-TART_HOME resolution `up` persists
// and `down` reuses: --tart-home / the infra block first, then the process
// environment, then tart's own default. Never empty — an unrecorded home is
// exactly how a `down` shell with a different TART_HOME finds no VM, reports
// "nothing to delete", and orphans it.
func TestResolveTartHome(t *testing.T) {
	t.Setenv("TART_HOME", "/env/tart")
	if got := resolveTartHome("/cfg/tart"); got != "/cfg/tart" {
		t.Errorf("configured value must win: %q", got)
	}
	if got := resolveTartHome(""); got != "/env/tart" {
		t.Errorf("environment must fill an unconfigured home: %q", got)
	}
	t.Setenv("TART_HOME", "")
	if got := resolveTartHome(""); got != defaultTartHomeDir {
		t.Errorf("unset everywhere must resolve to %q, got %q", defaultTartHomeDir, got)
	}
}

// TestTartPrepareUpRecordsEffectiveHome pins that PrepareUp writes the
// resolved home back into the options the shared verb persists into
// [satellites.<name>.infra].
func TestTartPrepareUpRecordsEffectiveHome(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skipf("tart preflight requires darwin/arm64 (this is %s/%s)", runtime.GOOS, runtime.GOARCH)
	}
	stubTartOnPath(t)
	t.Setenv("TART_HOME", "/env/tart")

	opts := &satelliteUpOptions{Name: "x"}
	if err := (&tartSatelliteProvider{target: tartSatelliteTarget}).PrepareUp(opts); err != nil {
		t.Fatalf("PrepareUp: %v", err)
	}
	if opts.Infra.TartHome != "/env/tart" {
		t.Errorf("PrepareUp did not record the effective TART_HOME: %q", opts.Infra.TartHome)
	}
}

// TestMergeInfraTartHome pins --tart-home's flag-over-config precedence (R7:
// the field used to be config-only, which is what made the orphaned-VM
// mismatch unreachable by flag).
func TestMergeInfraTartHome(t *testing.T) {
	cfg := satelliteInfraConfig{TartHome: "/tank/tart"}
	if out := mergeInfra(cfg, infraFlagOverrides{}); out.TartHome != "/tank/tart" {
		t.Errorf("unset flag must keep the config value: %q", out.TartHome)
	}
	if out := mergeInfra(cfg, infraFlagOverrides{TartHome: "/flag/tart", TartHomeSet: true}); out.TartHome != "/flag/tart" {
		t.Errorf("set --tart-home must win: %q", out.TartHome)
	}
	if out := mergeInfra(cfg, infraFlagOverrides{TartHome: "", TartHomeSet: true}); out.TartHome != "" {
		t.Errorf("--tart-home set-to-empty must disable the config value: %q", out.TartHome)
	}
}
