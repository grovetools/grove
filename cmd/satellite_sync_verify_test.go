package cmd

import (
	"fmt"
	"strings"
	"testing"
)

// TestVerifySatelliteSyncToken pins the live token probe's decision logic
// (2xx → ok, 401/403 → stale-token error carrying the remediation, transport
// error → distinct network message, other status → not a token verdict) and
// the secrecy invariant: the token reaches the remote command only via stdin,
// never argv.
func TestVerifySatelliteSyncToken(t *testing.T) {
	const (
		token     = "sst_secret_token_123"
		sshDest   = "grovedev@203.0.113.7"
		tokenPath = "/home/u/.config/grove/sync.token"
	)
	probeCmd := syncTokenProbeCmd("")

	runReturning := func(status string, err error) func(command, stdin string) (string, error) {
		return func(command, stdin string) (string, error) {
			if strings.Contains(command, token) {
				t.Fatalf("token leaked into the remote command line: %q", command)
			}
			if command != probeCmd {
				t.Fatalf("command = %q, want the capabilities probe %q", command, probeCmd)
			}
			if want := syncTokenProbeStdinHeader + token + "\n"; stdin != want {
				t.Fatalf("stdin = %q, want %q", stdin, want)
			}
			return status, err
		}
	}

	// 200 (and any other 2xx) → token verified.
	for _, code := range []string{"200", "204", "200\n"} {
		if err := verifySatelliteSyncToken(runReturning(code, nil), probeCmd, token, sshDest, tokenPath); err != nil {
			t.Fatalf("%q should verify: %v", code, err)
		}
	}

	// 401/403 → stale-token error with the remediation (ssh dest, token path,
	// the VM-side token file) — and the token itself never in the message.
	for _, code := range []string{"401", "403"} {
		err := verifySatelliteSyncToken(runReturning(code, nil), probeCmd, token, sshDest, tokenPath)
		if err == nil {
			t.Fatalf("%s must be a stale-token error", code)
		}
		for _, want := range []string{"stale", sshDest, tokenPath, "/root/laptop-sync.token", "chmod 600"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s error missing %q: %v", code, want, err)
			}
		}
		if strings.Contains(err.Error(), token) {
			t.Fatalf("%s error leaked the token: %v", code, err)
		}
	}

	// Transport/probe failure → distinct network message, never a token
	// verdict (no remediation telling the operator to replace a fine token).
	err := verifySatelliteSyncToken(runReturning("", fmt.Errorf("connection refused")), probeCmd, token, sshDest, tokenPath)
	if err == nil {
		t.Fatal("transport failure must be an error")
	}
	if strings.Contains(err.Error(), "stale") {
		t.Fatalf("transport failure misclassified as a token problem: %v", err)
	}
	if !strings.Contains(err.Error(), "not a token verdict") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("transport failure error not distinct/causal: %v", err)
	}

	// Any other status (e.g. 500) → unexpected-status error, also not stale.
	err = verifySatelliteSyncToken(runReturning("500", nil), probeCmd, token, sshDest, tokenPath)
	if err == nil {
		t.Fatal("500 must be an error")
	}
	if strings.Contains(err.Error(), "stale") {
		t.Fatalf("500 misclassified as a token problem: %v", err)
	}
	if !strings.Contains(err.Error(), `"500"`) {
		t.Fatalf("500 error should carry the status: %v", err)
	}
}

// TestSyncTokenProbeCmd pins the probe command's shape: bootstrap's own
// capabilities probe (POST /sync/capabilities), status-code-only output, the
// Authorization header read from stdin (-H @- — never argv, on either side),
// and the registry's sync_remote_addr (defaulted) as the target.
func TestSyncTokenProbeCmd(t *testing.T) {
	def := syncTokenProbeCmd("")
	for _, want := range []string{"-H @-", "-X POST", "%{http_code}", "http://127.0.0.1:8788/sync/capabilities"} {
		if !strings.Contains(def, want) {
			t.Fatalf("default probe cmd missing %q: %s", want, def)
		}
	}
	if got := syncTokenProbeCmd("127.0.0.1:9999"); !strings.Contains(got, "http://127.0.0.1:9999/sync/capabilities") {
		t.Fatalf("probe cmd ignores the registry sync_remote_addr: %s", got)
	}
}
