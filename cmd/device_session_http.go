package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/syncproto"
)

const maxDeviceHTTPResponse = 256 << 10

type deviceSessionHTTP struct {
	baseURL string
	client  *http.Client
	bearer  string
}

func loadDeviceSessionHTTP(ctx context.Context) (*deviceSessionHTTP, error) {
	cfg, err := config.LoadSyncConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || strings.TrimSpace(cfg.Server) == "" {
		return nil, fmt.Errorf("sync is not configured; run `grove join <server-url>` first")
	}
	key, err := devicekey.Load()
	if err != nil {
		return nil, fmt.Errorf("load device key: %w", err)
	}
	tlsConfig, err := cfg.TLSClientConfig()
	if err != nil {
		return nil, err
	}
	client := syncHTTPClient(tlsConfig)
	session, err := establishDeviceSession(ctx, client, cfg.Server, key)
	if err != nil {
		return nil, err
	}
	return &deviceSessionHTTP{baseURL: strings.TrimRight(cfg.Server, "/"), client: client, bearer: session.SessionToken}, nil
}

func syncHTTPClient(tlsConfig *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig.Clone()
	}
	return &http.Client{Transport: transport, Timeout: syncCapabilitiesTimeout}
}

func discoverSyncIdentity(ctx context.Context, client *http.Client, serverURL string) (*syncproto.IdentityResponse, error) {
	if client == nil {
		client = &http.Client{Timeout: syncCapabilitiesTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/sync/identity", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync identity discovery failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDeviceHTTPResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync identity discovery returned HTTP %d", resp.StatusCode)
	}
	var identity syncproto.IdentityResponse
	if err := json.Unmarshal(body, &identity); err != nil {
		return nil, fmt.Errorf("sync identity response is invalid: %w", err)
	}
	if identity.ServerEpoch == "" || !containsProtocol(identity.ProtocolVersions, syncproto.ProtocolVersionDeviceSession) {
		return nil, fmt.Errorf("sync server does not advertise device-session protocol v%d", syncproto.ProtocolVersionDeviceSession)
	}
	return &identity, nil
}

func establishDeviceSession(ctx context.Context, client *http.Client, serverURL string, key *devicekey.Key) (*syncproto.CapabilitiesResponse, error) {
	if client == nil {
		client = &http.Client{Timeout: syncCapabilitiesTimeout}
	}
	if key == nil {
		return nil, fmt.Errorf("device key is required")
	}
	identity, err := discoverSyncIdentity(ctx, client, serverURL)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate handshake nonce: %w", err)
	}
	body := syncproto.CapabilitiesRequest{
		ClientName:       "grove",
		ProtocolVersions: syncproto.SupportedProtocolVersions(),
		DeviceID:         key.DeviceID(),
		ServerEpoch:      identity.ServerEpoch,
		Timestamp:        syncproto.CanonicalTimestamp(time.Now()),
		Nonce:            base64.StdEncoding.EncodeToString(nonce),
	}
	payload, err := syncproto.CanonicalCapabilities(body)
	if err != nil {
		return nil, err
	}
	if err := syncproto.SetCapabilitiesSignature(&body, key.Sign(payload)); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(serverURL, "/")+"/sync/capabilities", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device session handshake failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDeviceHTTPResponse))
	if err != nil {
		return nil, err
	}
	var result syncproto.CapabilitiesResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("device session handshake returned HTTP %d with an unreadable response", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("device session handshake rejected (HTTP %d): %s", resp.StatusCode, message)
	}
	if result.ProtocolVersion != syncproto.ProtocolVersionDeviceSession || result.SessionToken == "" {
		return nil, fmt.Errorf("sync server did not establish a device session")
	}
	return &result, nil
}

func containsProtocol(versions []int, want int) bool {
	for _, version := range versions {
		if version == want {
			return true
		}
	}
	return false
}

func (c *deviceSessionHTTP) doJSON(ctx context.Context, method, path string, requestBody, responseBody any) error {
	status, data, err := c.do(ctx, method, path, requestBody)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return statusError(status, data)
	}
	if responseBody != nil && len(data) > 0 {
		if err := json.Unmarshal(data, responseBody); err != nil {
			return fmt.Errorf("decode sync server response: %w", err)
		}
	}
	return nil
}

// doJSONStatus is doJSON for the surfaces whose REFUSALS are structured: the
// v3 protocol answers a rejected share, a stale re-parent or an unregistered
// notespace with a typed ProtocolError body and a 4xx, and that body is the
// evidence a verb has to print (or, for a precondition, the current version it
// has to retry with). Collapsing it into a bare "HTTP 409" the way doJSON does
// would throw away the only per-member detail the server sends.
//
// It returns the status and decodes the body whatever the status is; a non-2xx
// with no decodable body still becomes an error, because a caller must never
// read an unpopulated response as success.
func (c *deviceSessionHTTP) doJSONStatus(ctx context.Context, method, path string, requestBody, responseBody any) (int, error) {
	status, data, err := c.do(ctx, method, path, requestBody)
	if err != nil {
		return 0, err
	}
	if responseBody != nil && len(data) > 0 {
		if uerr := json.Unmarshal(data, responseBody); uerr != nil {
			if status < 200 || status >= 300 {
				return status, statusError(status, data)
			}
			return status, fmt.Errorf("decode sync server response: %w", uerr)
		}
		return status, nil
	}
	if status < 200 || status >= 300 {
		return status, statusError(status, data)
	}
	return status, nil
}

func (c *deviceSessionHTTP) do(ctx context.Context, method, path string, requestBody any) (int, []byte, error) {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDeviceHTTPResponse))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

func statusError(status int, data []byte) error {
	var wireError struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &wireError)
	message := strings.TrimSpace(wireError.Error)
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return fmt.Errorf("sync server returned HTTP %d: %s", status, message)
}

func devicePath(deviceID, action string) string {
	return "/sync/devices/" + url.PathEscape(deviceID) + "/" + action
}
