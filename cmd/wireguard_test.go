package cmd

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

func testWireGuardConfig() config.WireGuardConfig {
	on := true
	return config.WireGuardConfig{
		Enabled:      &on,
		Address:      "10.100.0.7/24",
		Endpoint:     "hub.example.com:51820",
		HubPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
}

func TestRenderWG0ConfGoldens(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.WireGuardConfig)
	}{
		{"wireguard_wg0_hostname.conf", func(*config.WireGuardConfig) {}},
		{"wireguard_wg0_hostname_dns.conf", func(c *config.WireGuardConfig) { c.DNS = "10.100.0.1" }},
		{"wireguard_wg0_ip.conf", func(c *config.WireGuardConfig) { c.Endpoint = "192.0.2.10:51820" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testWireGuardConfig()
			tc.mutate(&cfg)
			compareGolden(t, tc.name, renderWG0Conf(cfg, wireGuardPrivateKeyPlaceholder))
		})
	}
}

func TestParseWGShowRoundTrip(t *testing.T) {
	raw := "apt output is ignored\nPUBKEY=public-key=\nCONF_CHANGED=1\nHANDSHAKE_AGE_S=42\nENDPOINT=192.0.2.1:51820\nTRANSFER_RX_BYTES=12\nTRANSFER_TX_BYTES=34\nCONF_PRESENT=1\nSERVICE_ENABLED=1\nINTERFACE_ACTIVE=1\n"
	got, err := parseWGShow(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pubkey != "public-key=" || !got.ConfChanged || got.HandshakeAge != 42 || got.Endpoint != "192.0.2.1:51820" || got.TransferRXBytes != 12 || got.TransferTXBytes != 34 || !got.ConfPresent || !got.ServiceEnabled || !got.InterfaceActive {
		t.Fatalf("parsed = %+v", got)
	}
	never, err := parseWGShow("PUBKEY=k=\nCONF_CHANGED=0\nHANDSHAKE_AGE_S=never\n")
	if err != nil || never.HandshakeAge != -1 {
		t.Fatalf("never = %+v, %v", never, err)
	}
	if _, err := parseWGShow("PUBKEY=k\nCONF_CHANGED=0\n"); err == nil {
		t.Fatal("missing handshake trailer accepted")
	}
}

func TestWireGuardConvergeScriptKeepsPrivateKeyRemote(t *testing.T) {
	cfg := testWireGuardConfig()
	script := wireGuardConvergeScript(cfg, defaultWireGuardPaths("/var/lib/grove-forge"))
	for _, want := range []string{
		"wg genkey", "/etc/wireguard/wg0.key", "wg pubkey", "wg-quick@wg0", "grove-wg-reresolve.timer",
		"PUBKEY=%s", "CONF_CHANGED=%s", "HANDSHAKE_AGE_S=%s",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if strings.Contains(script, "PrivateKey = "+cfg.HubPublicKey) {
		t.Fatal("script confused the hub public key with private key material")
	}
	// The generated shell must carry only the marker; replacement reads the
	// root-only file inside awk on the VM (not through argv or laptop memory).
	if !strings.Contains(script, wireGuardPrivateKeyPlaceholder) || strings.Contains(script, "-v key=") {
		t.Fatal("script does not preserve VM-side-only key substitution")
	}
	path := filepath.Join(t.TempDir(), "wireguard.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}

	cfg.Endpoint = "192.0.2.10:51820"
	ipScript := wireGuardConvergeScript(cfg, defaultWireGuardPaths("/var/lib/grove-forge"))
	if strings.Contains(ipScript, "grove-wg-reresolve.timer") {
		t.Fatal("IP-literal endpoint installed a DNS re-resolution timer")
	}
}

// compareGolden diffs got against testdata/<name>, or rewrites it under
// UPDATE_GOLDEN=1.
//
// It used to live in forge_test.go, which left with the forge verbs; the
// WireGuard conf goldens above are its only remaining caller here.
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
