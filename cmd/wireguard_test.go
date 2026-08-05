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

func TestForgeTLSTargetAndRegenSANs(t *testing.T) {
	on := true
	for _, tc := range []struct {
		name       string
		wg         bool
		domain     string
		wantCN     string
		wantSANSet string
	}{
		{"external only", false, "", "203.0.113.9", "subjectAltName=IP:203.0.113.9"},
		{"mesh", true, "", "203.0.113.9", "subjectAltName=IP:203.0.113.9,IP:10.100.0.7"},
		{"domain", false, "forge.example.com", "forge.example.com", "subjectAltName=IP:203.0.113.9,DNS:forge.example.com"},
		{"mesh and domain", true, "forge.example.com", "forge.example.com", "subjectAltName=IP:203.0.113.9,IP:10.100.0.7,DNS:forge.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.ForgeConfig{Services: &config.ForgeServicesConfig{Domain: tc.domain}}
			if tc.wg {
				cfg.Wireguard = &config.WireGuardConfig{Enabled: &on, Address: "10.100.0.7/24", Endpoint: "hub:51820", HubPublicKey: "public"}
			}
			target := forgeTLSTarget(cfg, "203.0.113.9")
			if target.CN != tc.wantCN {
				t.Fatalf("CN = %q, want %q", target.CN, tc.wantCN)
			}
			script := forgeTLSRegenScript(target)
			if !strings.Contains(script, tc.wantSANSet) {
				t.Fatalf("script missing %q:\n%s", tc.wantSANSet, script)
			}
			for _, needle := range target.Needles {
				if strings.HasPrefix(needle, "IP Address:") && !strings.Contains(script, "IP:"+strings.TrimPrefix(needle, "IP Address:")) {
					t.Errorf("script dropped %q", needle)
				}
				if strings.HasPrefix(needle, "DNS:") && !strings.Contains(script, needle) {
					t.Errorf("script dropped %q", needle)
				}
			}
		})
	}
}
