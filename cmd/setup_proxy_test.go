package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestSetupProxyDryRun_MacOS verifies the macOS install branch prints the
// exact anchor path, launchd plist path, and pf rule without touching the
// filesystem. Dry-run is forced so the test is safe to run as non-root.
func TestSetupProxyDryRun_MacOS(t *testing.T) {
	var out bytes.Buffer
	// Explicitly exercise the macOS install path — tests can't reliably
	// flip runtime.GOOS, so instead we call the helper directly.
	if err := setupProxyMacOSInstall(&out, true); err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}

	got := out.String()
	wants := []string{
		"/etc/pf.anchors/com.grove.proxy",
		"/Library/LaunchDaemons/com.grove.pf.plist",
		"LaunchAgents/com.grove.daemon.plist",
		"pfctl -e -f /etc/pf.anchors/com.grove.proxy",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in output, got:\n%s", w, got)
		}
	}
}

// TestSetupProxyDryRun_Linux verifies the Linux install branch prints the
// iptables rule and the systemd user unit path without executing them.
func TestSetupProxyDryRun_Linux(t *testing.T) {
	var out bytes.Buffer
	if err := setupProxyLinuxInstall(&out, true); err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}
	got := out.String()
	wants := []string{
		"-t nat -A OUTPUT -p tcp -d 127.0.0.0/8 --dport 80 -j REDIRECT --to-ports 8443",
		"systemd/user/groved.service",
		"systemctl --user enable --now groved",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in output, got:\n%s", w, got)
		}
	}
}

// TestPfAnchorRuleContent is a regression guard on the pf redirect target.
// The Phase 2 design pins 80 -> 8443 (global groved binds 8443); if the
// port ever drifts, this catches it.
func TestPfAnchorRuleContent(t *testing.T) {
	want := "rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 80 -> 127.0.0.1 port 8443\n"
	if pfAnchorRule != want {
		t.Errorf("pfAnchorRule = %q, want %q", pfAnchorRule, want)
	}
}

// TestIptablesRuleContent checks the Linux rule is constructed against the
// OUTPUT chain (localhost-originated traffic) rather than PREROUTING, which
// would only match packets coming in from other hosts.
func TestIptablesRuleContent(t *testing.T) {
	if !strings.Contains(iptablesRule, "-A OUTPUT") {
		t.Errorf("iptablesRule must target OUTPUT chain, got %q", iptablesRule)
	}
	if !strings.Contains(iptablesRule, "--to-ports 8443") {
		t.Errorf("iptablesRule must redirect to 8443, got %q", iptablesRule)
	}
}

// TestMacOSUninstallDryRun verifies the uninstall path lists the unload +
// rm commands for the same plist paths install wrote.
func TestMacOSUninstallDryRun(t *testing.T) {
	var out bytes.Buffer
	if err := setupProxyMacOSUninstall(&out, true); err != nil {
		t.Fatalf("uninstall dry run: %v", err)
	}
	got := out.String()
	for _, w := range []string{
		"launchctl unload -w /Library/LaunchDaemons/com.grove.pf.plist",
		"rm /Library/LaunchDaemons/com.grove.pf.plist",
		"rm /etc/pf.anchors/com.grove.proxy",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in uninstall plan, got:\n%s", w, got)
		}
	}
}

// TestLinuxUninstallDryRun verifies the iptables -D command is emitted
// with the same match clause as the install -A command so the removal
// actually targets the same rule.
func TestLinuxUninstallDryRun(t *testing.T) {
	var out bytes.Buffer
	if err := setupProxyLinuxUninstall(&out, true); err != nil {
		t.Fatalf("uninstall dry run: %v", err)
	}
	got := out.String()
	for _, w := range []string{
		"systemctl --user disable --now groved",
		"-t nat -D OUTPUT -p tcp -d 127.0.0.0/8 --dport 80 -j REDIRECT --to-ports 8443",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in uninstall plan, got:\n%s", w, got)
		}
	}
}
