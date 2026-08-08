package doctorchecks

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/syncproto"
)

func setupDeviceDoctor(t *testing.T, staticFallback bool) {
	t.Helper()
	groveDir := setupScratchConfig(t)
	scratch := filepath.Dir(filepath.Dir(groveDir))
	t.Setenv("XDG_STATE_HOME", filepath.Join(scratch, "state"))
	fallback := ""
	if staticFallback {
		fallback = "token_command = \"printf super-secret-value\"\n"
	}
	write(t, filepath.Join(groveDir, "sync.toml"), "server = \"https://sync.example.com\"\n"+fallback+
		"\n[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n")
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
}

func runDeviceDoctor(t *testing.T) doctor.CheckResult {
	t.Helper()
	return (&deviceEnrolledCheck{}).Run(context.Background(), doctor.RunOptions{})
}

func withDeviceProbe(t *testing.T, status string, err error) {
	t.Helper()
	oldCapability := deviceEnrollmentCapability
	oldStatus := deviceEnrollmentStatus
	deviceEnrollmentCapability = func(context.Context, *config.SyncConfig) (bool, error) { return true, nil }
	deviceEnrollmentStatus = func(context.Context, *config.SyncConfig, *devicekey.Key) (string, error) { return status, err }
	t.Cleanup(func() {
		deviceEnrollmentCapability = oldCapability
		deviceEnrollmentStatus = oldStatus
	})
}

func withDeviceCapability(t *testing.T, supported bool, err error) {
	t.Helper()
	old := deviceEnrollmentCapability
	deviceEnrollmentCapability = func(context.Context, *config.SyncConfig) (bool, error) { return supported, err }
	t.Cleanup(func() { deviceEnrollmentCapability = old })
}

func TestDeviceEnrolledMissingKeyFails(t *testing.T) {
	setupDeviceDoctor(t, false)
	withDeviceCapability(t, true, nil)
	res := runDeviceDoctor(t)
	if res.Status != doctor.StatusFail || !strings.Contains(res.Message, "no machine identity") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDeviceEnrolledStatesAndNoSecretLeak(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		fallback     bool
		want         doctor.Status
		contains     string
	}{
		{"approved device-only", syncproto.DeviceStatusApproved, false, doctor.StatusOK, "approved"},
		{"pending", syncproto.DeviceStatusPending, false, doctor.StatusFail, "pending"},
		{"revoked", syncproto.DeviceStatusRevoked, false, doctor.StatusFail, "revoked"},
		{"approved static fallback", syncproto.DeviceStatusApproved, true, doctor.StatusWarn, "service-credential fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDeviceDoctor(t, tc.fallback)
			if _, err := devicekey.Ensure(); err != nil {
				t.Fatal(err)
			}
			withDeviceProbe(t, tc.status, nil)
			res := runDeviceDoctor(t)
			if res.Status != tc.want || !strings.Contains(res.Message, tc.contains) {
				t.Fatalf("result = %+v", res)
			}
			whole := res.Message + res.Error + res.Resolution
			if strings.Contains(whole, "super-secret-value") || strings.Contains(whole, "printf") {
				t.Fatalf("secret or command leaked: %q", whole)
			}
		})
	}
}

func TestDeviceEnrolledRejectedProofFails(t *testing.T) {
	setupDeviceDoctor(t, false)
	if _, err := devicekey.Ensure(); err != nil {
		t.Fatal(err)
	}
	withDeviceProbe(t, "", deviceProbeRejected{status: 401})
	res := runDeviceDoctor(t)
	if res.Status != doctor.StatusFail || !strings.Contains(res.Message, "rejected") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDeviceEnrolledOldServerWarnsWhenV1CredentialWorks(t *testing.T) {
	setupDeviceDoctor(t, true)
	withDeviceCapability(t, false, nil)
	old := legacySyncCompatibility
	legacySyncCompatibility = func(context.Context, *config.SyncConfig) error { return nil }
	t.Cleanup(func() { legacySyncCompatibility = old })
	res := runDeviceDoctor(t)
	if res.Status != doctor.StatusWarn || !strings.Contains(res.Message, "legacy v1 compatibility") {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Resolution, "join --repair") {
		t.Fatalf("old server received impossible enrollment remediation: %+v", res)
	}
}

func TestDeviceEnrolledUnreachableWarns(t *testing.T) {
	setupDeviceDoctor(t, false)
	if _, err := devicekey.Ensure(); err != nil {
		t.Fatal(err)
	}
	withDeviceProbe(t, "", errors.New("dial failed with secret payload that must not appear"))
	res := runDeviceDoctor(t)
	if res.Status != doctor.StatusWarn || !strings.Contains(res.Message, "unreachable") {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Error, "secret payload") {
		t.Fatalf("network error leaked details: %+v", res)
	}
}
