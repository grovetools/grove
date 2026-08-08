package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/syncproto"
)

const maxEnrollmentResponse = 64 << 10

// enrollDeviceOverHTTP submits public material plus a proof of possession.
// The configured static token is deliberately not sent: /sync/enroll is the
// unauthenticated bootstrap endpoint, and requested_user is left empty so the
// request cannot choose its own authority.
func enrollDeviceOverHTTP(ctx context.Context, client *http.Client, serverURL, name string, key *devicekey.Key) (*syncproto.EnrollResponse, error) {
	return enrollDeviceWithCodeOverHTTP(ctx, client, serverURL, name, "", key)
}

func enrollDeviceWithCodeOverHTTP(ctx context.Context, client *http.Client, serverURL, name, code string, key *devicekey.Key) (*syncproto.EnrollResponse, error) {
	if key == nil {
		return nil, fmt.Errorf("device key is required")
	}
	if client == nil {
		client = &http.Client{Timeout: syncCapabilitiesTimeout}
	}
	reqBody := syncproto.EnrollRequest{
		DeviceID:  key.DeviceID(),
		Name:      strings.TrimSpace(name),
		PublicKey: key.PublicKeyString(),
		Code:      strings.TrimSpace(code),
		Timestamp: syncproto.CanonicalTimestamp(time.Now()),
	}
	payload, err := syncproto.EnrollmentSigningBytes(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize device enrollment: %w", err)
	}
	if err := syncproto.SetEnrollmentSignature(&reqBody, key.Sign(payload)); err != nil {
		return nil, fmt.Errorf("failed to sign device enrollment: %w", err)
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/sync/enroll", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device enrollment request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxEnrollmentResponse))
	if readErr != nil {
		return nil, fmt.Errorf("failed to read device enrollment response: %w", readErr)
	}
	var result syncproto.EnrollResponse
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return nil, fmt.Errorf("device enrollment returned HTTP %d with an unreadable response", resp.StatusCode)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = strings.TrimSpace(string(responseBody))
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("device enrollment rejected (HTTP %d): %s", resp.StatusCode, message)
	}
	if result.DeviceID != "" && result.DeviceID != key.DeviceID() {
		return nil, fmt.Errorf("device enrollment response named device %q, want %q", result.DeviceID, key.DeviceID())
	}
	if result.Fingerprint != key.Fingerprint() {
		return nil, fmt.Errorf("device enrollment fingerprint %q does not match local key %q", result.Fingerprint, key.Fingerprint())
	}
	if result.Status != syncproto.DeviceStatusPending && result.Status != syncproto.DeviceStatusApproved {
		return nil, fmt.Errorf("device enrollment returned unexpected status %q", result.Status)
	}
	return &result, nil
}
