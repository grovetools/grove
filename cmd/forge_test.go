package cmd

// Tests for the `grove forge` noun.
//
// Nothing here contacts GCP, applies anything, or writes outside t.TempDir():
// GROVE_HOME is redirected per the plan's D7 rule, and the one test that runs
// terraform runs it against a module-only configuration with no providers and
// no backend — a pure template render.

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"

	"github.com/grovetools/grove/cmd/forgeassets"
)

// sandboxForgeState redirects the grove state dir into the test's temp dir, so
// nothing here can touch the operator's real ~/.local/state/grove.
func sandboxForgeState(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir, err := forgeStateDir()
	if err != nil {
		t.Fatalf("forgeStateDir with GROVE_HOME=%s: %v", home, err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("forge state dir %q escaped the sandbox %q", dir, home)
	}
	return dir
}

// completeForgeConfig is the fixture every render test starts from: the
// smallest config that is actually provisionable.
func completeForgeConfig() *config.ForgeConfig {
	return &config.ForgeConfig{
		URL: "https://forge.example.com",
		Infra: &config.ForgeInfraConfig{
			Project: "example-project",
			SSHUser: "grovedev",
			CIDR:    "203.0.113.7/32",
		},
		Services: &config.ForgeServicesConfig{
			Forgejo: &config.ForgejoServiceConfig{
				Version: "16.0.2",
				SHA256:  "9f2c1b7d4e6a8c0f3b5d7e9a1c3e5f7092b4d6f8a0c2e4f68a9b1d3f5e7a9c1b",
			},
		},
	}
}

// ---- tfvars rendering ------------------------------------------------------

// TestForgeTFVarsGolden pins the whole terraform input surface. The tfvars file
// IS the plan — everything terraform will create is decided by this text — so a
// silent change to a default, a dropped key, or a flipped boolean has to show
// up as a diff here rather than as a surprise in a provisioned VM.
func TestForgeTFVarsGolden(t *testing.T) {
	got, err := forgeTFVars(completeForgeConfig())
	if err != nil {
		t.Fatalf("render tfvars: %v", err)
	}
	compareGolden(t, "forge_tfvars_defaults.tfvars", got)
}

// TestForgeTFVarsFullyConfiguredGolden pins the other end: every knob set,
// including the ACME branch.
func TestForgeTFVarsFullyConfiguredGolden(t *testing.T) {
	enabled := true
	disabled := false
	cfg := &config.ForgeConfig{
		Infra: &config.ForgeInfraConfig{
			Project:               "example-project",
			Zone:                  "us-central1-a",
			VMName:                "grove-forge-prod",
			MachineType:           "e2-standard-2",
			DiskSizeGB:            200,
			ImageFamily:           "debian-12",
			ImageProject:          "debian-cloud",
			SSHUser:               "grovedev",
			SSHPubkeyFile:         "~/.ssh/forge.pub",
			CIDR:                  "203.0.113.7/32",
			ServiceAccountEmail:   "forge@example-project.iam.gserviceaccount.com",
			EnableIAPSSH:          &enabled,
			SyncdIngressEnabled:   &disabled,
			ForgejoIngressEnabled: &disabled,
			SSHIngress:            config.ForgeSSHIngressIAP,
		},
		Services: &config.ForgeServicesConfig{
			Domain:          "forge.example.com",
			TLSMode:         config.ForgeTLSACME,
			ACMEEmail:       "ops@example.com",
			ACMEDNSProvider: "cloudflare",
			// A delegated subdomain's resolvers, so the golden pins the list
			// rendering too — this is the knob whose absence cost job 47 a
			// three-minute DNS-01 pre-check timeout per attempt.
			ACMEDNSResolvers: []string{"ns-cloud-e1.googledomains.com:53", "ns-cloud-e2.googledomains.com:53"},
			Forgejo: &config.ForgejoServiceConfig{
				Version:  "16.0.2",
				SHA256:   "9F2C1B7D4E6A8C0F3B5D7E9A1C3E5F7092B4D6F8A0C2E4F68A9B1D3F5E7A9C1B",
				HTTPPort: 3080,
				SiteName: "solar forge",
			},
			Syncd: &config.ForgeSyncdServiceConfig{Enabled: &enabled, Port: 9788},
		},
		// Backups on, so the golden pins the ENABLED shape too: this is the
		// one block that widens the module's no-IAM-roles/no-scopes posture,
		// and a silent change to what it renders is exactly the class of
		// surprise this golden exists to catch.
		Backup: &config.ForgeBackupConfig{
			Enabled:          &enabled,
			Bucket:           "example-project-grove-forge-backups",
			Location:         "us-central1",
			RetentionDays:    365,
			NearlineDays:     14,
			NoncurrentDays:   45,
			NtfyTopicCommand: "true",
		},
	}
	got, err := forgeTFVars(cfg)
	if err != nil {
		t.Fatalf("render tfvars: %v", err)
	}
	compareGolden(t, "forge_tfvars_full.tfvars", got)
}

// TestForgeTFVarsRefusesIncompleteConfig: the render is where a provision that
// cannot succeed is stopped, because every later gate costs money or time.
func TestForgeTFVarsRefusesIncompleteConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.ForgeConfig)
		wantSub string
	}{
		{"no infra block", func(c *config.ForgeConfig) { c.Infra = nil }, "[forge.infra]"},
		{"no project", func(c *config.ForgeConfig) { c.Infra.Project = "" }, "project"},
		{"no ssh_user", func(c *config.ForgeConfig) { c.Infra.SSHUser = "" }, "ssh_user"},
		{"no cidr", func(c *config.ForgeConfig) { c.Infra.CIDR = "" }, "cidr"},
		{"open cidr", func(c *config.ForgeConfig) { c.Infra.CIDR = "0.0.0.0/0" }, "whole internet"},
		{"no forgejo version", func(c *config.ForgeConfig) { c.Services.Forgejo.Version = "" }, "version and sha256"},
		{"version without checksum", func(c *config.ForgeConfig) { c.Services.Forgejo.SHA256 = "" }, "not a pin"},
		{"malformed checksum", func(c *config.ForgeConfig) { c.Services.Forgejo.SHA256 = "abc" }, "64 hex"},
		{"acme without dns provider", func(c *config.ForgeConfig) {
			c.Services.Domain = "forge.example.com"
			c.Services.TLSMode = config.ForgeTLSACME
		}, "acme_dns_provider"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := completeForgeConfig()
			tc.mutate(cfg)
			_, err := forgeTFVars(cfg)
			if err == nil {
				t.Fatal("render succeeded on a config that cannot provision")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

// TestForgeTFVarsNeverEmitsOpenIngress is a standing guard rather than a case:
// no rendering of any config may put the open internet into terraform's inputs.
func TestForgeTFVarsNeverEmitsOpenIngress(t *testing.T) {
	got, err := forgeTFVars(completeForgeConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"0.0.0.0/0", "::/0"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("rendered tfvars contains %q:\n%s", forbidden, got)
		}
	}
}

// ---- extraction ------------------------------------------------------------

// TestExtractForgeTerraformIsCompleteAndVersioned pins the embedded tree and
// the two properties extraction must have: module files are refreshed on every
// run (they version with the binary), and local terraform artifacts are never
// touched.
func TestExtractForgeTerraformIsCompleteAndVersioned(t *testing.T) {
	sandboxForgeState(t)
	dir, err := resolveForgeTerraformDir("")
	if err != nil {
		t.Fatalf("resolve terraform dir: %v", err)
	}

	files, err := forgeExtractedFiles(dir)
	if err != nil {
		t.Fatalf("list extracted files: %v", err)
	}
	compareGolden(t, "forge_terraform_files.txt", strings.Join(files, "\n")+"\n")

	// Local state must survive a re-extraction, and a stale module file must
	// not.
	statePath := filepath.Join(dir, "terraform.tfstate")
	if err := os.WriteFile(statePath, []byte(`{"version":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveForgeTerraformDir(""); err != nil {
		t.Fatalf("re-extract: %v", err)
	}
	if data, err := os.ReadFile(statePath); err != nil || string(data) != `{"version":4}` {
		t.Errorf("terraform.tfstate was clobbered by extraction (%q, %v)", string(data), err)
	}
	main, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil || strings.Contains(string(main), "# stale") {
		t.Error("main.tf was NOT refreshed — the module would drift from the binary")
	}
}

// TestResolveForgeTerraformDirRejectsANonModule: --tf-dir is the BYO escape
// hatch, and pointing it somewhere wrong must fail before terraform runs.
func TestResolveForgeTerraformDirRejectsANonModule(t *testing.T) {
	sandboxForgeState(t)
	_, err := resolveForgeTerraformDir(t.TempDir())
	if err == nil {
		t.Fatal("an empty --tf-dir was accepted as a forge module")
	}
	if !strings.Contains(err.Error(), "CONTRACT.md") {
		t.Errorf("error %q does not point at the contract", err)
	}
}

func TestForgeTerraformIgnoresOnlyRollingImageDrift(t *testing.T) {
	tfFS, err := forgeassets.TerraformFS()
	if err != nil {
		t.Fatal(err)
	}
	mainTF, err := fs.ReadFile(tfFS, "main.tf")
	if err != nil {
		t.Fatal(err)
	}
	main := string(mainTF)
	const lifecycle = `  lifecycle {
    ignore_changes = [
      boot_disk[0].initialize_params[0].image,
    ]
  }`
	if got := strings.Count(main, lifecycle); got != 1 {
		t.Fatalf("exact image-only lifecycle block count = %d, want 1", got)
	}
	if got := strings.Count(main, "ignore_changes"); got != 1 {
		t.Fatalf("ignore_changes declaration count = %d, want exactly 1", got)
	}

	// The exact block above is deliberately narrow. These neighboring or
	// broader fields must remain Terraform-managed rather than being folded
	// into the pet-image exception.
	for _, forbidden := range []string{
		"boot_disk",
		"boot_disk[0].initialize_params",
		"boot_disk[0].initialize_params[0].size",
		"boot_disk[0].initialize_params[0].type",
		"machine_type",
		"metadata",
		"network_interface",
		"service_account",
	} {
		if strings.Contains(lifecycle, "      "+forbidden+",\n") {
			t.Errorf("lifecycle broadly ignores %q", forbidden)
		}
	}
}

func TestForgeTerraformIngressControlsPreserveDefaultsAndGuardOutputs(t *testing.T) {
	tfFS, err := forgeassets.TerraformFS()
	if err != nil {
		t.Fatal(err)
	}
	variables, err := fs.ReadFile(tfFS, "variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	mainTF, err := fs.ReadFile(tfFS, "main.tf")
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := fs.ReadFile(tfFS, "outputs.tf")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`variable "syncd_ingress_enabled"`, `variable "forgejo_ingress_enabled"`,
		`default     = true`, `variable "ssh_ingress"`, `default     = "cidr+iap"`,
		`contains(["cidr+iap", "iap", "cidr"], var.ssh_ingress)`,
	} {
		if !strings.Contains(string(variables), want) {
			t.Errorf("variables.tf missing %q", want)
		}
	}
	for _, want := range []string{
		`count = var.forgejo_ingress_enabled ? 1 : 0`,
		`count = var.syncd_enabled && var.syncd_ingress_enabled ? 1 : 0`,
		`var.ssh_ingress == "iap" ? [local.iap_cidr]`,
	} {
		if !strings.Contains(string(mainTF), want) {
			t.Errorf("main.tf missing %q", want)
		}
	}
	for _, want := range []string{"one(google_compute_firewall.forgejo[*].name)", "one(google_compute_firewall.syncd[*].name)"} {
		if !strings.Contains(string(outputs), want) {
			t.Errorf("outputs.tf missing count-zero guard %q", want)
		}
	}
}

// ---- down gates ------------------------------------------------------------

// TestForgeDownRefusesWithoutBothGates is the pet's guard rail. A satellite is
// cattle and `satellite down` is routine; this is not that verb, and the test
// exists so it never quietly becomes it.
func TestForgeDownRefusesWithoutBothGates(t *testing.T) {
	const name = "grove-forge"
	tests := []struct {
		desc    string
		force   bool
		confirm string
		wantErr string
	}{
		{"no flags at all", false, "", "--force"},
		{"confirm without force", false, name, "--force"},
		{"force without the typed name", true, "", "--confirm"},
		{"force with the wrong name", true, "grove-forge-staging", "does not match"},
		{"force with a near-miss", true, "grove_forge", "does not match"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			err := checkForgeDownGates(name, tc.force, tc.confirm)
			if err == nil {
				t.Fatal("destroy was allowed")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}

	if err := checkForgeDownGates(name, true, name); err != nil {
		t.Errorf("both gates satisfied but destroy still refused: %v", err)
	}
	// Whitespace around the typed name is a shell artifact, not a mistake.
	if err := checkForgeDownGates(name, true, "  "+name+"  "); err != nil {
		t.Errorf("a padded --confirm was rejected: %v", err)
	}
}

// TestForgeDownRefusalNamesTheRemediation: the refusal is only useful if it
// says what is at stake and what to run.
func TestForgeDownRefusalNamesTheRemediation(t *testing.T) {
	err := checkForgeDownGates("grove-forge", false, "")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"grove forge backup", "--confirm grove-forge", "ever pushed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
}

// ---- terraform outputs -----------------------------------------------------

func TestDecodeForgeTerraformOutputs(t *testing.T) {
	raw := []byte(`{
	  "external_ip": {"value": "203.0.113.10", "type": "string"},
	  "forge_url":   {"value": "http://203.0.113.10:3000", "type": "string"},
	  "syncd_addr":  {"value": "203.0.113.10:8788", "type": "string"},
	  "tls_mode":    {"value": "self-signed", "type": "string"},
	  "firewall_rules": {"value": ["grove-forge-allow-ssh: tcp/22 from 203.0.113.7/32"], "type": ["list","string"]}
	}`)
	got, err := decodeForgeTerraformOutputs(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ExternalIP != "203.0.113.10" || got.TLSMode != "self-signed" {
		t.Errorf("decoded = %+v", got)
	}
	if len(got.FirewallRules) != 1 {
		t.Errorf("firewall rules = %v, want one entry", got.FirewallRules)
	}
}

func TestForgeOutputsCacheRoundTrip(t *testing.T) {
	sandboxForgeState(t)
	if _, ok, err := loadCachedForgeOutputs(); err != nil || ok {
		t.Fatalf("a machine that never provisioned reported ok=%v err=%v", ok, err)
	}
	want := forgeOutputs{ExternalIP: "203.0.113.9", VMName: "grove-forge", TLSMode: "self-signed"}
	if err := cacheForgeOutputs(want); err != nil {
		t.Fatalf("cache: %v", err)
	}
	got, ok, err := loadCachedForgeOutputs()
	if err != nil || !ok {
		t.Fatalf("reload: ok=%v err=%v", ok, err)
	}
	if got.ExternalIP != want.ExternalIP || got.VMName != want.VMName || got.TLSMode != want.TLSMode {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// ---- W2 mesh cutover controls ---------------------------------------------

func forgeCutoverTestConfig() *config.ForgeConfig {
	enabled := true
	return &config.ForgeConfig{
		Infra:    &config.ForgeInfraConfig{Project: "test-project", SSHUser: "grove", SSHIngress: config.ForgeSSHIngressIAP},
		Services: &config.ForgeServicesConfig{Forgejo: &config.ForgejoServiceConfig{HTTPPort: 3000}},
		Wireguard: &config.WireGuardConfig{
			Enabled:      &enabled,
			Address:      "10.100.0.7/24",
			Endpoint:     "hub.example:51820",
			HubPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		},
	}
}

func TestForgeDialAddrMeshFirstThenExternalFallback(t *testing.T) {
	oldProbe := forgeTCPProbe
	t.Cleanup(func() { forgeTCPProbe = oldProbe })
	cfg := forgeCutoverTestConfig()
	outputs := forgeOutputs{ExternalIP: "203.0.113.9", VMName: "grove-forge", Zone: "us-east1-b"}

	var probes []string
	forgeTCPProbe = func(addr string, _ time.Duration) error {
		probes = append(probes, addr)
		if strings.HasPrefix(addr, "10.100.0.7:") {
			return nil
		}
		return fmt.Errorf("closed")
	}
	addr, source, err := forgeDialAddr(cfg, outputs)
	if err != nil || addr != "10.100.0.7:22" || source != forgeDialSourceMesh {
		t.Fatalf("mesh selection = %q %q %v", addr, source, err)
	}
	if len(probes) != 1 {
		t.Fatalf("mesh success still probed fallback: %v", probes)
	}

	probes = nil
	forgeTCPProbe = func(addr string, _ time.Duration) error {
		probes = append(probes, addr)
		if strings.HasPrefix(addr, "203.0.113.9:") {
			return nil
		}
		return fmt.Errorf("closed")
	}
	addr, source, err = forgeDialAddr(cfg, outputs)
	if err != nil || addr != "203.0.113.9:22" || source != forgeDialSourceExternal {
		t.Fatalf("fallback selection = %q %q %v", addr, source, err)
	}
	if len(probes) != 2 {
		t.Fatalf("fallback probe order = %v", probes)
	}
}

func TestForgeDialAddrTotalFailureNamesBothAndIAP(t *testing.T) {
	oldProbe := forgeTCPProbe
	t.Cleanup(func() { forgeTCPProbe = oldProbe })
	forgeTCPProbe = func(string, time.Duration) error { return fmt.Errorf("closed") }
	_, _, err := forgeDialAddr(forgeCutoverTestConfig(), forgeOutputs{ExternalIP: "203.0.113.9", VMName: "grove-forge", Zone: "us-east1-b"})
	if err == nil {
		t.Fatal("total route failure succeeded")
	}
	for _, want := range []string{"10.100.0.7:22", "203.0.113.9:22", "--tunnel-through-iap", "--project=test-project"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestForgeMeshAndExternalHostKeyMismatchIsHard(t *testing.T) {
	oldProbe, oldScan := forgeTCPProbe, forgeHostKeyscan
	t.Cleanup(func() { forgeTCPProbe, forgeHostKeyscan = oldProbe, oldScan })
	forgeTCPProbe = func(string, time.Duration) error { return nil }
	forgeHostKeyscan = func(addr string) (string, error) {
		if strings.HasPrefix(addr, "10.100.0.7:") {
			return "ssh-ed25519 MESH", nil
		}
		return "ssh-ed25519 EXTERNAL", nil
	}
	_, err := scanForgeSelectedHostKey("10.100.0.7:22", forgeDialSourceMesh, forgeOutputs{ExternalIP: "203.0.113.9"})
	if err == nil || !strings.Contains(err.Error(), "mismatch") || !strings.Contains(err.Error(), "refusing to re-pin") {
		t.Fatalf("mismatch error = %v", err)
	}

	forgeHostKeyscan = func(string) (string, error) { return "ssh-ed25519 SAME", nil }
	if key, err := scanForgeSelectedHostKey("10.100.0.7:22", forgeDialSourceMesh, forgeOutputs{ExternalIP: "203.0.113.9"}); err != nil || key != "ssh-ed25519 SAME" {
		t.Fatalf("matching keys = %q, %v", key, err)
	}
}

func TestForgeRootURLReconcileShapeAndTrailer(t *testing.T) {
	cfg := forgeCutoverTestConfig()
	if got := forgeMeshRootURL(cfg); got != "http://10.100.0.7:3000/" {
		t.Fatalf("mesh ROOT_URL = %q", got)
	}
	script := forgeRootURLReconcileScript(forgeMeshRootURL(cfg))
	for _, want := range []string{"/etc/forgejo/app.ini", "ROOT_URL = ", "systemctl restart forgejo.service", "CHANGED=1", `install -o "$OWNER" -g "$GROUP" -m "$MODE"`} {
		if !strings.Contains(script, want) {
			t.Errorf("reconcile script missing %q", want)
		}
	}
	path := filepath.Join(t.TempDir(), "reconcile.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v: %s", err, out)
	}
	changed, oldURL, newURL, err := parseForgeRootURLResult("CHANGED=1\nOLD=http://203.0.113.9:3000/\nNEW=http://10.100.0.7:3000/\n")
	if err != nil || !changed || oldURL != "http://203.0.113.9:3000/" || newURL != "http://10.100.0.7:3000/" {
		t.Fatalf("parsed trailer = %v %q %q %v", changed, oldURL, newURL, err)
	}
}

func TestForgeStatusRendersIAPBreakGlass(t *testing.T) {
	var out strings.Builder
	renderForgeStatus(&out, forgeStatusReport{
		Provisioned:      true,
		Outputs:          forgeOutputs{VMName: "grove-forge", Zone: "us-east1-b"},
		IAPSSHBreakGlass: "gcloud compute ssh grove-forge --tunnel-through-iap --zone=us-east1-b",
	})
	for _, want := range []string{"break-glass SSH (IAP)", "--tunnel-through-iap"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output %q missing %q", out.String(), want)
		}
	}
}

// ---- TLS fingerprint probe -------------------------------------------------

func TestForgeTLSProbeConfigPinsCompactClassicalCurves(t *testing.T) {
	cfg := forgeTLSProbeConfig()
	wantCurves := []tls.CurveID{tls.X25519, tls.CurveP256}
	if !slices.Equal(cfg.CurvePreferences, wantCurves) {
		t.Fatalf("curve preferences = %v, want %v", cfg.CurvePreferences, wantCurves)
	}
	for _, curve := range cfg.CurvePreferences {
		if curve == tls.X25519MLKEM768 {
			t.Fatalf("hybrid ML-KEM curve %v was offered", curve)
		}
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("minimum TLS version = %#x, want TLS 1.2 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("fingerprint probe unexpectedly verifies the unpinned certificate")
	}
}

// TestProbeTLSFingerprintMatchesTheCertificate: the fingerprint `grove forge
// status` prints is the one an operator would pin, so it must equal the
// SHA-256 of the leaf DER — computed here from the server's own certificate.
func TestProbeTLSFingerprintMatchesTheCertificate(t *testing.T) {
	offeredCurves := make(chan []tls.CurveID, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			offeredCurves <- slices.Clone(hello.SupportedCurves)
			return nil, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "https://")
	got, err := probeTLSFingerprint(addr)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	leaf := srv.TLS.Certificates[0].Certificate[0]
	sum := sha256.Sum256(leaf)
	want := formatFingerprint(sum[:])
	if got != want {
		t.Errorf("fingerprint = %s, want %s", got, want)
	}
	wantCurves := []tls.CurveID{tls.X25519, tls.CurveP256}
	if offered := <-offeredCurves; !slices.Equal(offered, wantCurves) {
		t.Errorf("ClientHello curves = %v, want compact classical offer %v", offered, wantCurves)
	}
	// openssl's rendering, so the value read off the wire and the one written
	// on the VM compare by eye.
	if strings.Count(got, ":") != 31 || got != strings.ToUpper(got) {
		t.Errorf("fingerprint %q is not openssl-shaped (uppercase, colon-separated)", got)
	}
	// Sanity: this is an unverifiable certificate, which is exactly the case
	// the probe exists for.
	if _, err := tls.Dial("tcp", addr, &tls.Config{MinVersion: tls.VersionTLS12}); err == nil {
		t.Error("the fixture certificate verified — the test is not exercising the pinning case")
	}
}

func TestProbeTLSFingerprintRejectsABadAddress(t *testing.T) {
	if _, err := probeTLSFingerprint("not-a-host-port"); err == nil {
		t.Fatal("a malformed address was accepted")
	}
}

// ---- terraform render (offline) --------------------------------------------

// TestForgeTerraformModulesRenderCoherently runs terraform against the embedded
// service modules.
//
// The modules create no cloud resources — they render shell — so this needs no
// provider, no backend, no credentials and no network: `terraform apply` on a
// module-only configuration is a pure template evaluation. What it proves is
// what a golden file cannot: that the HCL parses, the templatefile calls
// resolve, and the shell that would run on the VM is valid shell.
func TestForgeTerraformModulesRenderCoherently(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH")
	}
	sandboxForgeState(t)
	moduleDir, err := resolveForgeTerraformDir("")
	if err != nil {
		t.Fatalf("extract module: %v", err)
	}

	harness := t.TempDir()
	cfg := fmt.Sprintf(`
module "forgejo" {
  source    = %q
  release   = "16.0.2"
  sha256    = "9f2c1b7d4e6a8c0f3b5d7e9a1c3e5f7092b4d6f8a0c2e4f68a9b1d3f5e7a9c1b"
  http_port = 3000
  domain    = ""
  tls_mode  = "self-signed"
}

module "syncd" {
  source   = %q
  port     = 8788
  domain   = ""
  tls_mode = "self-signed"
}

output "forgejo_setup" { value = module.forgejo.setup_script }
output "syncd_setup"   { value = module.syncd.setup_script }
`, filepath.Join(moduleDir, "modules", "forgejo"), filepath.Join(moduleDir, "modules", "syncd"))
	if err := os.WriteFile(filepath.Join(harness, "main.tf"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	runTF := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("terraform", append([]string{"-chdir=" + harness}, args...)...)
		cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("terraform %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runTF("init", "-input=false", "-backend=false")
	runTF("apply", "-auto-approve", "-input=false")

	for _, tc := range []struct {
		output string
		want   []string
	}{
		{"forgejo_setup", []string{
			"sha256sum -c -",                 // the download is verified
			"forgejo-16.0.2-linux-amd64",     // pinned version
			"INSTALL_LOCK = true",            // headless install, no web installer
			"DISABLE_REGISTRATION = true",    // closed forge
			"systemctl enable --now forgejo", // the service actually starts
		}},
		{"syncd_setup", []string{
			"--tls-cert /etc/grove-forge/tls/cert.pem", // TLS is not optional
			"ConditionPathExists=/usr/local/bin/grove-syncd",
			"systemctl enable grove-syncd.service",
		}},
	} {
		script := runTF("output", "-raw", tc.output)
		for _, want := range tc.want {
			if !strings.Contains(script, want) {
				t.Errorf("%s does not contain %q", tc.output, want)
			}
		}
		if strings.Contains(script, "--insecure") {
			t.Errorf("%s passes --insecure — the TLS posture is the deployment", tc.output)
		}
		// The fragment is injected into a `set -euo pipefail` script on the
		// VM; a syntax error there is a VM that never finishes booting.
		if err := shellSyntaxCheck(t, script); err != nil {
			t.Errorf("%s is not valid shell: %v", tc.output, err)
		}
	}
}

func shellSyntaxCheck(t *testing.T, script string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fragment.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("sh", "-n", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// ---- golden helper ---------------------------------------------------------

// compareGolden compares got against testdata/<name>, rewriting it when
// -update is passed to `go test`.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with UPDATE_GOLDEN=1): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s does not match the golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
