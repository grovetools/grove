package cmd

// Tests for the forge's ACME (Let's Encrypt DNS-01) machinery: config gating,
// the on-VM scripts' shell validity, state parsing, the credentials helper,
// and — most load-bearing — the guards that keep an acme cutover from being
// clobbered by the self-signed reconcile or from half-applying.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/config"
)

func acmeForgeConfig() *config.ForgeConfig {
	cfg := forgeCutoverTestConfig()
	cfg.Services.Domain = "forge.example.test"
	cfg.Services.TLSMode = config.ForgeTLSACME
	cfg.Services.ACMEEmail = "ops@example.test"
	cfg.Services.ACMEDNSProvider = "gcloud"
	return cfg
}

func TestForgeACMEEnabledGating(t *testing.T) {
	if forgeACMEEnabled(nil) {
		t.Error("nil config must not enable acme")
	}
	cfg := forgeCutoverTestConfig()
	if forgeACMEEnabled(cfg) {
		t.Error("self-signed config must not enable acme")
	}
	cfg.Services.TLSMode = config.ForgeTLSACME
	if forgeACMEEnabled(cfg) {
		t.Error("acme without a domain must degrade to self-signed (EffectiveTLSMode contract)")
	}
	if !forgeACMEEnabled(acmeForgeConfig()) {
		t.Error("acme with a domain must be enabled")
	}
}

func TestForgeMeshRootURLGrowsTheDomainBranch(t *testing.T) {
	// Self-signed + mesh: today's behavior, unchanged.
	cfg := forgeCutoverTestConfig()
	if got := forgeMeshRootURL(cfg); !strings.HasPrefix(got, "http://") || !strings.Contains(got, forgeMeshIP(cfg)) {
		t.Errorf("self-signed mesh ROOT_URL should be http://<mesh-ip>:<port>/, got %q", got)
	}
	// ACME: the domain is the origin, https, no port.
	acme := acmeForgeConfig()
	if got := forgeMeshRootURL(acme); got != "https://forge.example.test/" {
		t.Errorf("acme ROOT_URL should be https://<domain>/, got %q", got)
	}
	// ACME wins even without WireGuard: the domain resolves wherever the
	// operator pointed it; the mesh is not a precondition of the name.
	acme.Wireguard = nil
	if got := forgeMeshRootURL(acme); got != "https://forge.example.test/" {
		t.Errorf("acme ROOT_URL should not require wireguard, got %q", got)
	}
}

// TestReconcileForgeTLSSkipsACME pins the guard that keeps the self-signed SAN
// repair from regenerating a self-signed certificate OVER a Let's Encrypt one.
// The cached outputs still say self-signed on a terraform-free pet, so without
// this guard the repair would fire (an LE cert never carries the IP SANs it
// checks for) and break every default-trust client.
func TestReconcileForgeTLSSkipsACME(t *testing.T) {
	outputs := forgeOutputs{TLSMode: "self-signed", ExternalIP: "203.0.113.10"}
	var buf bytes.Buffer
	// No SSH server exists; reaching the dial path would error. Returning nil
	// without output IS the assertion.
	if err := reconcileForgeTLS(&buf, outputs, acmeForgeConfig()); err != nil {
		t.Fatalf("acme config must skip the self-signed TLS reconcile entirely, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("acme skip should be silent, got %q", buf.String())
	}
}

func TestReconcileForgeACMENoopWithoutACME(t *testing.T) {
	var buf bytes.Buffer
	if err := reconcileForgeACME(&buf, forgeOutputs{}, forgeCutoverTestConfig()); err != nil {
		t.Fatalf("self-signed config must be a no-op, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("no-op should print nothing, got %q", buf.String())
	}
}

// TestReconcileForgeACMEPreflightRequiresResolvableDomain uses an RFC 6761
// .test name that cannot resolve: the preflight must fail BEFORE any SSH, and
// its error must carry the remediation (the record to create).
func TestReconcileForgeACMEPreflightRequiresResolvableDomain(t *testing.T) {
	var buf bytes.Buffer
	err := reconcileForgeACME(&buf, forgeOutputs{}, acmeForgeConfig())
	if err == nil {
		t.Fatal("an unresolvable domain must fail the preflight")
	}
	for _, want := range []string{"does not resolve", "gcloud dns record-sets create", "runbooks"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error should contain %q, got:\n%s", want, err)
		}
	}
}

func TestParseForgeACMEState(t *testing.T) {
	out := strings.Join([]string{
		"ENV_PRESENT=1",
		"LEGO_PRESENT=0",
		"CERT_PRESENT=1",
		"CERT_COVERS_DOMAIN=1",
		"CERT_SELF_SIGNED=0",
		"FORGEJO_PROTOCOL=https",
		"FORGEJO_ROOT_URL=https://forge.example.test/",
		"TIMER_ENABLED=1",
	}, "\n")
	state, err := parseForgeACMEState(out)
	if err != nil {
		t.Fatal(err)
	}
	if !state.EnvPresent || state.LegoPresent || !state.CertCoversDomain || state.CertSelfSigned {
		t.Errorf("parsed state is wrong: %+v", state)
	}
	if state.ForgejoProtocol != "https" || state.ForgejoRootURL != "https://forge.example.test/" {
		t.Errorf("forgejo fields wrong: %+v", state)
	}
	if _, err := parseForgeACMEState("garbage\n"); err == nil {
		t.Error("a missing trailer must be an error, not a zero state that looks converged")
	}
}

// TestForgeACMEScriptsAreValidShell runs every on-VM script through the same
// syntax check the terraform fragments get: a syntax error here is a converge
// that dies halfway on the pet.
func TestForgeACMEScriptsAreValidShell(t *testing.T) {
	cfg := acmeForgeConfig()
	for name, script := range map[string]string{
		"state":     forgeACMEStateScript(cfg.Services.EffectiveDomain()),
		"lego":      forgeLegoInstallScript(),
		"assets":    forgeACMEAssetsScript(cfg),
		"renew":     forgeACMERenewScript(),
		"flip":      forgeForgejoTLSFlipScript(cfg.Services.EffectiveDomain()),
		"timerstat": forgeACMETimerStatusScript(),
	} {
		if err := shellSyntaxCheck(t, script); err != nil {
			t.Errorf("%s script is not valid shell: %v\n%s", name, err, script)
		}
	}
}

func TestForgeACMERenewScriptRestartsBothServices(t *testing.T) {
	script := forgeACMERenewScript()
	for _, want := range []string{
		"reload-or-restart grove-syncd.service",
		"reload-or-restart forgejo.service",
		"tls-fingerprint.txt",
		forgeACMEDefaultsPath,
		forgeACMEEnvPath,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("renew script must contain %q:\n%s", want, script)
		}
	}
	// The refusal shape: missing credentials name the installing command.
	if !strings.Contains(script, "grove forge acme install-credentials") {
		t.Error("the missing-credentials refusal must name the command that fixes it")
	}
}

func TestForgeForgejoTLSFlipRollsBack(t *testing.T) {
	script := forgeForgejoTLSFlipScript("forge.example.test")
	for _, want := range []string{
		"pre-acme",             // a backup exists before anything changes
		"rolling back",         // the failure path restores it
		"CAP_NET_BIND_SERVICE", // :443 needs exactly this capability
		"SupplementaryGroups=" + forgeTLSGroup,
		"HTTP_PORT = 443",
		"ROOT_URL = https://",
		"--resolve", // the verify curl hits the real name
	} {
		if !strings.Contains(script, want) {
			t.Errorf("flip script must contain %q", want)
		}
	}
}

func TestRenderForgeACMEEnv(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(keyPath, []byte(`{
		"type": "service_account",
		"project_id": "example-project",
		"client_email": "dns01@example-project.iam.gserviceaccount.com",
		"private_key": "-----BEGIN PRIVATE KEY-----\nxxx\n-----END PRIVATE KEY-----\n"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := acmeForgeConfig()
	env, err := renderForgeACMEEnv(cfg, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if env.Project != "example-project" || !strings.Contains(env.EnvFile, "GCE_PROJECT=example-project") {
		t.Errorf("env render wrong: %+v", env)
	}
	if !strings.Contains(env.EnvFile, "GCE_SERVICE_ACCOUNT_FILE="+forgeACMESAKeyPath) {
		t.Errorf("env must point lego at the shipped key path:\n%s", env.EnvFile)
	}

	// A non-service-account JSON is refused: shipping a wrong file 0600 root
	// to the VM and failing at 3am in lego is the bad version of this error.
	if err := os.WriteFile(keyPath, []byte(`{"type":"authorized_user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := renderForgeACMEEnv(cfg, keyPath); err == nil || !strings.Contains(err.Error(), "service-account key") {
		t.Errorf("a non-SA key must be refused with a naming error, got: %v", err)
	}

	// Only the gcloud provider is automated; others get told to write
	// acme.env by hand rather than getting GCE variables that cannot work.
	other := acmeForgeConfig()
	other.Services.ACMEDNSProvider = "cloudflare"
	if _, err := renderForgeACMEEnv(other, keyPath); err == nil || !strings.Contains(err.Error(), "gcloud") {
		t.Errorf("a non-gcloud provider must be named in the refusal, got: %v", err)
	}
}

func TestForgeCertDaysLeft(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if got := forgeCertDaysLeft(now.Add(89*24*time.Hour+time.Hour), now); got != 89 {
		t.Errorf("days left = %d, want 89", got)
	}
	if got := forgeCertDaysLeft(now.Add(-24*time.Hour), now); got != -1 {
		t.Errorf("expired cert days left = %d, want -1", got)
	}
}

func TestPrintForgeClientMigration(t *testing.T) {
	var buf bytes.Buffer
	printForgeClientMigration(&buf, acmeForgeConfig())
	out := buf.String()
	for _, want := range []string{
		`server = "https://forge.example.test:8788"`,
		"delete ca_cert",
		`url = "https://forge.example.test/"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("client migration epilogue must contain %q, got:\n%s", want, out)
		}
	}
}

// TestForgeTerraformModulesRenderCoherentlyACME is the acme sibling of
// TestForgeTerraformModulesRenderCoherently: the fresh-provision templates
// must encode the same posture the terraform-free pet converge applies —
// Forgejo native https on 443 with the shared certificate, a renew path that
// restarts BOTH services, and no start before the first certificate exists.
func TestForgeTerraformModulesRenderCoherentlyACME(t *testing.T) {
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
  domain    = "forge.example.test"
  tls_mode  = "acme"
}

module "syncd" {
  source            = %q
  port              = 8788
  domain            = "forge.example.test"
  tls_mode          = "acme"
  acme_email        = "ops@example.test"
  acme_dns_provider = "gcloud"
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

	forgejoSetup := runTF("output", "-raw", "forgejo_setup")
	for _, want := range []string{
		"PROTOCOL = https",
		"CERT_FILE = /etc/grove-forge/tls/cert.pem",
		"KEY_FILE = /etc/grove-forge/tls/key.pem",
		"HTTP_PORT = 443",
		"ROOT_URL = https://forge.example.test/",
		"SupplementaryGroups=grove-tls",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
		"getent group grove-tls",
		// No start before the first certificate: enable-only branch present.
		"waiting for the first ACME certificate",
	} {
		if !strings.Contains(forgejoSetup, want) {
			t.Errorf("acme forgejo_setup does not contain %q", want)
		}
	}
	if strings.Contains(forgejoSetup, "PROTOCOL = http\n") {
		t.Error("acme forgejo_setup still renders plain http")
	}

	syncdSetup := runTF("output", "-raw", "syncd_setup")
	for _, want := range []string{
		"lego_" + forgeLegoVersion,
		". /etc/grove-forge/acme.defaults",
		"reload-or-restart grove-syncd.service",
		"reload-or-restart forgejo.service",
		"grove forge acme install-credentials",
		"tls-fingerprint.txt",
		"systemctl enable grove-forge-acme.timer",
	} {
		if !strings.Contains(syncdSetup, want) {
			t.Errorf("acme syncd_setup does not contain %q", want)
		}
	}
	for name, script := range map[string]string{"forgejo_setup": forgejoSetup, "syncd_setup": syncdSetup} {
		if err := shellSyntaxCheck(t, script); err != nil {
			t.Errorf("%s is not valid shell: %v", name, err)
		}
	}
}

// TestRenderForgeStatusACMEPosture pins the loud failure renders: a failing or
// dead renewal timer, and an acme config still serving a self-issued cert.
func TestRenderForgeStatusACMEPosture(t *testing.T) {
	base := forgeStatusReport{
		Provisioned:       true,
		Outputs:           forgeOutputs{VMName: "forge-1", Zone: "us-east1-b", TLSMode: "self-signed"},
		TLSFingerprint:    "AA:BB",
		TLSModeConfigured: config.ForgeTLSACME,
		TLSNotAfter:       "2026-11-05",
		TLSDaysLeft:       89,
		TLSSANs:           []string{"DNS:forge.example.test"},
	}

	t.Run("timer dead is loud", func(t *testing.T) {
		var buf bytes.Buffer
		report := base
		report.ACMETimer = &forgeACMETimerStatus{Enabled: false, Active: false}
		renderForgeStatus(&buf, report)
		if !strings.Contains(buf.String(), "NOT running") {
			t.Errorf("a dead renewal timer must be loud:\n%s", buf.String())
		}
	})
	t.Run("failed run is loud", func(t *testing.T) {
		var buf bytes.Buffer
		report := base
		report.ACMETimer = &forgeACMETimerStatus{Enabled: true, Active: true, LastResult: "exit-code"}
		renderForgeStatus(&buf, report)
		if !strings.Contains(buf.String(), "FAILED") {
			t.Errorf("a failed renewal run must be loud:\n%s", buf.String())
		}
	})
	t.Run("self-issued under acme is loud", func(t *testing.T) {
		var buf bytes.Buffer
		report := base
		report.TLSSelfSigned = true
		renderForgeStatus(&buf, report)
		if !strings.Contains(buf.String(), "SELF-ISSUED") {
			t.Errorf("acme-configured but self-issued must be loud:\n%s", buf.String())
		}
	})
	t.Run("healthy is quiet and informative", func(t *testing.T) {
		var buf bytes.Buffer
		report := base
		report.ACMETimer = &forgeACMETimerStatus{Enabled: true, Active: true, LastResult: "success", NextRun: "Sat 2026-08-08 00:00:00 UTC"}
		renderForgeStatus(&buf, report)
		out := buf.String()
		if strings.Contains(out, "!!") {
			t.Errorf("healthy posture must not warn:\n%s", out)
		}
		for _, want := range []string{"acme", "89 days", "DNS:forge.example.test", "renewal timer: active"} {
			if !strings.Contains(out, want) {
				t.Errorf("healthy posture should render %q:\n%s", want, out)
			}
		}
	})
}
