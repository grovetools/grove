package doctorchecks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/syncproto"
)

func init() {
	doctor.Register(&deviceEnrolledCheck{})
}

type deviceEnrolledCheck struct{}

func (c *deviceEnrolledCheck) ID() string   { return "device_enrolled" }
func (c *deviceEnrolledCheck) Name() string { return "sync device is enrolled" }

const deviceDoctorTimeout = 8 * time.Second

// deviceEnrollmentStatus is replaceable only by package tests. The production
// implementation sends public key material and a signature; it never sends a
// private key, service credential, token command, or session bearer.
var (
	deviceEnrollmentCapability = probeDeviceEnrollmentCapability
	deviceEnrollmentStatus     = probeDeviceEnrollmentStatus
	legacySyncCompatibility    = probeLegacySyncCompatibility
)

type deviceProbeRejected struct{ status int }

func (e deviceProbeRejected) Error() string {
	return fmt.Sprintf("server rejected device proof (HTTP %d)", e.status)
}

func (c *deviceEnrolledCheck) Run(ctx context.Context, _ doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}
	cfg, err := config.LoadSyncConfig()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "sync configuration is unreadable"
		res.Error = compactError(err)
		return res
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		res.Status = doctor.StatusOK
		res.Message = "no sync subscriptions; device enrollment is not required"
		return res
	}
	if strings.TrimSpace(cfg.Server) == "" {
		res.Status = doctor.StatusFail
		res.Message = "sync subscriptions exist but sync.toml has no server"
		return res
	}

	probeCtx, cancel := context.WithTimeout(ctx, deviceDoctorTimeout)
	defer cancel()
	v2Capable, err := deviceEnrollmentCapability(probeCtx, cfg)
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = "device enrollment capability could not be checked because the sync server is unavailable or unreachable"
		res.Error = "network capability discovery failed"
		res.Resolution = "retry when the sync server is reachable"
		return res
	}
	if !v2Capable {
		if err := legacySyncCompatibility(probeCtx, cfg); err != nil {
			res.Status = doctor.StatusFail
			res.Message = "sync server supports only legacy v1, but the configured legacy credential was not accepted"
			res.Error = "legacy compatibility authentication failed"
			res.Resolution = "configure and verify a v1 service credential for this server, or upgrade the server to device-session v2"
			return res
		}
		res.Status = doctor.StatusWarn
		res.Message = "sync server does not support device enrollment; verified legacy v1 compatibility mode is active"
		res.Resolution = "upgrade the sync server to device-session v2 when available; device enrollment cannot be repaired on this server"
		return res
	}

	identity, err := machine.Load()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "machine identity is corrupt"
		res.Error = compactError(err)
		res.Resolution = "repair machine identity deliberately; do not silently mint a replacement for an enrolled machine"
		return res
	}
	if identity == nil {
		res.Status = doctor.StatusFail
		res.Message = "sync subscriptions exist but this machine has no machine identity"
		res.Resolution = "run `grove join --repair` to establish and enroll this machine"
		return res
	}
	key, err := devicekey.Load()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "device identity or key is corrupt"
		res.Error = compactError(err)
		res.Resolution = "repair the machine identity/key mismatch deliberately; do not delete a corrupt key and silently re-enroll"
		return res
	}
	if key == nil {
		res.Status = doctor.StatusFail
		res.Message = "sync subscriptions exist but this machine has no device key"
		res.Resolution = "run `grove join --repair` to enroll this machine"
		return res
	}
	status, err := deviceEnrollmentStatus(probeCtx, cfg, key)
	if err != nil {
		var rejected deviceProbeRejected
		if errors.As(err, &rejected) {
			res.Status = doctor.StatusFail
			res.Message = "sync server rejected this device's signed enrollment proof"
			res.Error = fmt.Sprintf("verification rejected (HTTP %d)", rejected.status)
			res.Resolution = "repair the local machine identity/key or explicitly re-enroll; static credentials are not device identity"
			return res
		}
		res.Status = doctor.StatusWarn
		res.Message = "device key is valid, but enrollment could not be verified because the server is unavailable or unreachable"
		res.Error = "network verification failed"
		res.Resolution = "retry when the sync server is reachable"
		return res
	}

	hasFallback := strings.TrimSpace(cfg.TokenCommand) != "" || strings.TrimSpace(cfg.Token) != ""
	switch status {
	case syncproto.DeviceStatusApproved:
		if hasFallback {
			res.Status = doctor.StatusWarn
			res.Message = "device is enrolled and approved, but this machine still configures a legacy service-credential fallback"
			res.Resolution = "remove token/token_command after confirming device-session sync; no credential value was inspected or printed"
			return res
		}
		res.Status = doctor.StatusOK
		res.Message = "device is enrolled and approved; device-only sync is ready"
	case syncproto.DeviceStatusPending:
		res.Status = doctor.StatusFail
		res.Message = "device enrollment is pending approval"
		res.Resolution = "approve this device by fingerprint from an enrolled owner device or with `grove-syncd device approve`"
	case syncproto.DeviceStatusRevoked:
		res.Status = doctor.StatusFail
		res.Message = "device enrollment is revoked"
		res.Resolution = "re-enroll through an explicit administrator-approved recovery; static fallback does not restore device identity"
	default:
		res.Status = doctor.StatusFail
		res.Message = "device is not enrolled on the configured server"
		res.Resolution = "run `grove join --repair` to enroll this machine"
	}
	return res
}

func deviceDoctorHTTPClient(cfg *config.SyncConfig) (*http.Client, error) {
	tlsConfig, err := cfg.TLSClientConfig()
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig.Clone()
	}
	return &http.Client{Transport: transport, Timeout: deviceDoctorTimeout}, nil
}

func probeDeviceEnrollmentCapability(ctx context.Context, cfg *config.SyncConfig) (bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Server, "/")+"/sync/identity", nil)
	if err != nil {
		return false, err
	}
	client, err := deviceDoctorHTTPClient(cfg)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("identity discovery unavailable (HTTP %d)", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	var identity syncproto.IdentityResponse
	if err := decoder.Decode(&identity); err != nil {
		return false, err
	}
	for _, version := range identity.ProtocolVersions {
		if version == syncproto.ProtocolVersionDeviceSession {
			return true, nil
		}
	}
	return false, nil
}

func probeLegacySyncCompatibility(ctx context.Context, cfg *config.SyncConfig) error {
	token, err := cfg.ResolveToken()
	if err != nil {
		return fmt.Errorf("legacy credential resolution failed: %w", err)
	}
	if token == "" {
		return fmt.Errorf("legacy credential is not configured")
	}
	encoded, err := json.Marshal(syncproto.CapabilitiesRequest{ProtocolVersions: []int{syncproto.ProtocolVersionLegacy}, ClientName: "grove-doctor"})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Server, "/")+"/sync/capabilities", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	client, err := deviceDoctorHTTPClient(cfg)
	if err != nil {
		return err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("legacy capabilities rejected (HTTP %d)", resp.StatusCode)
	}
	var caps syncproto.CapabilitiesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&caps); err != nil {
		return err
	}
	if caps.Error != "" || caps.ProtocolVersion != syncproto.ProtocolVersionLegacy || !caps.Capabilities.SupportsVersion(syncproto.ProtocolVersionLegacy) {
		return fmt.Errorf("server did not negotiate legacy v1")
	}
	return nil
}

func probeDeviceEnrollmentStatus(ctx context.Context, cfg *config.SyncConfig, key *devicekey.Key) (string, error) {
	reqBody := syncproto.EnrollRequest{
		DeviceID: key.DeviceID(), PublicKey: key.PublicKeyString(), Timestamp: syncproto.CanonicalTimestamp(time.Now()),
	}
	payload, err := syncproto.CanonicalEnrollment(reqBody)
	if err != nil {
		return "", err
	}
	if err := syncproto.SetEnrollmentSignature(&reqBody, key.Sign(payload)); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Server, "/")+"/sync/enroll/status", bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client, err := deviceDoctorHTTPClient(cfg)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "missing", nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return "", deviceProbeRejected{status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status verification unavailable (HTTP %d)", resp.StatusCode)
	}
	var enrolled syncproto.EnrollResponse
	if err := json.Unmarshal(body, &enrolled); err != nil {
		return "", err
	}
	return enrolled.Status, nil
}

func (c *deviceEnrolledCheck) AutoFix(context.Context) error {
	return fmt.Errorf("%w: device enrollment requires explicit fingerprint approval", doctor.ErrNotFixable)
}
