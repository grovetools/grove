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

	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/version"
)

// Live sync-token verification, in the two transports that need it.
//
// The DECISION TABLE is shared and lives here; only the transport differs:
//
//   - `grove satellite up` probes the VM's loopback syncd over the pinned SSH
//     connection, because that syncd is not reachable from the laptop at all
//     (satellite_sync.go: syncTokenProbeCmd + verifySatelliteSyncToken).
//   - `grove join` talks to the server directly over HTTP, because a server you
//     are joining is by definition one this machine can reach.
//
// Both ask the same question — POST /sync/capabilities with a bearer token —
// and both must answer it the same way, which is why classifySyncTokenStatus
// is one function and not two. The distinction that matters is
// "rejected" vs "could not ask": a transport failure is NOT a token verdict,
// and treating it as one would tell a user to re-mint a perfectly good token
// because their network was down.

// syncTokenVerdict is the outcome of a capabilities probe.
type syncTokenVerdict int

const (
	// syncTokenAccepted: the server answered 2xx with this bearer token.
	syncTokenAccepted syncTokenVerdict = iota
	// syncTokenRejected: 401/403 — the token is not valid for this server.
	syncTokenRejected
	// syncTokenUnhealthy: the server answered, but not usably. Not a token
	// verdict.
	syncTokenUnhealthy
)

// classifySyncTokenStatus maps an HTTP status code onto the verdict. Status is
// taken as a string because the SSH probe reads it off curl's `-w '%{http_code}'`
// output, and re-parsing it to an int here would only add a failure mode.
func classifySyncTokenStatus(status string) syncTokenVerdict {
	switch status = strings.TrimSpace(status); {
	case strings.HasPrefix(status, "2"):
		return syncTokenAccepted
	case status == "401" || status == "403":
		return syncTokenRejected
	default:
		return syncTokenUnhealthy
	}
}

// syncCapabilitiesTimeout bounds the join-time probe. It is deliberately short:
// this call stands between the user and every later step, and a server that
// cannot complete a handshake in ten seconds is one they need told about now,
// not in a minute.
const syncCapabilitiesTimeout = 10 * time.Second

// verifySyncTokenOverHTTP probes serverURL's /sync/capabilities with token and
// reports whether the server accepts it.
//
// It is `grove join`'s gate: nothing is persisted to the user's config until
// this returns nil. A rejected token that got written into sync.toml anyway
// would leave the daemon 401-looping silently, which is exactly the trap the
// satellite path already learned to close.
//
// deviceID rides along in the handshake (the server may ignore it — rendezvous
// stays dumb) so a server that DOES record device identity sees the real one
// from the first contact.
func verifySyncTokenOverHTTP(ctx context.Context, client *http.Client, serverURL, token, deviceID string, capsOut ...*syncproto.CapabilitiesResponse) error {
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if base == "" {
		return fmt.Errorf("sync server URL is empty")
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("sync token is empty")
	}
	if client == nil {
		client = &http.Client{Timeout: syncCapabilitiesTimeout}
	}

	body, err := json.Marshal(syncproto.CapabilitiesRequest{
		ClientName:       "grove",
		ClientVersion:    version.Version,
		ProtocolVersions: []int{syncproto.ProtocolVersion},
		DeviceID:         deviceID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/sync/capabilities", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))

	resp, err := client.Do(req)
	if err != nil {
		// Transport failure: the server was never asked, so this says nothing
		// about the token. Phrased so the user checks the URL and the service
		// rather than re-minting credentials.
		return fmt.Errorf("could not reach the sync server at %s (network/TLS/service failure, not a token verdict — check the URL and that grove-syncd is running, then re-run): %w", base, err)
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection can be reused and a chatty
	// error body cannot be used to make this call hang.
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	switch classifySyncTokenStatus(fmt.Sprint(resp.StatusCode)) {
	case syncTokenAccepted:
		// 2xx does not yet mean usable: the handshake reports a negotiation
		// failure IN the body, with status 200. Checking it here is what makes
		// join's "verified" claim mean something.
		var caps syncproto.CapabilitiesResponse
		if err := json.Unmarshal(payload, &caps); err != nil {
			return fmt.Errorf("the sync server at %s accepted the token but its capabilities response is not readable (is this a grove-syncd?): %w", base, err)
		}
		if caps.Error != "" {
			return fmt.Errorf("the sync server at %s rejected the handshake: %s", base, caps.Error)
		}
		if caps.ProtocolVersion != syncproto.ProtocolVersion {
			return fmt.Errorf("the sync server at %s negotiated protocol version %d, but this grove speaks %d — upgrade whichever is older",
				base, caps.ProtocolVersion, syncproto.ProtocolVersion)
		}
		if len(capsOut) > 0 && capsOut[0] != nil {
			*capsOut[0] = caps
		}
		return nil
	case syncTokenRejected:
		return fmt.Errorf("the sync server at %s rejected this token (HTTP %d)\n"+
			"remediation — mint a token on the server and pass it again:\n"+
			"  grove-syncd --data-dir <dir> token create <name>\n"+
			"nothing has been written to your config", base, resp.StatusCode)
	default:
		return fmt.Errorf("unexpected HTTP status %d from %s/sync/capabilities (server reachable but unhealthy?) — not a token verdict; check grove-syncd and re-run",
			resp.StatusCode, base)
	}
}
