package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/grove/pkg/satellitecontract"
)

type recordMaintenanceStatus struct {
	Draining          bool `json:"draining"`
	OutboxPending     int  `json:"outbox_pending"`
	OutboxParked      int  `json:"outbox_parked"`
	DocumentsDiverged int  `json:"documents_diverged"`
}
type recordIncomingStatus struct {
	Manifest struct {
		Generation string            `json:"generation"`
		Operations []json.RawMessage `json:"operations"`
	} `json:"manifest"`
	Clean          bool   `json:"clean"`
	EscrowPath     string `json:"escrow_path"`
	EscrowVerified bool   `json:"escrow_verified"`
}

type recordReturnTransport interface {
	Maintenance(context.Context, string, string) (recordMaintenanceStatus, error)
	Incoming(context.Context, string, []string) (recordIncomingStatus, error)
}

type satelliteRecordTransport struct{ ssh *satelliteSSH }

func localRecordRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	client := &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", paths.SocketPath(""))
	}}}
	req, err := http.NewRequestWithContext(ctx, method, "http://groved"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}
func (t *satelliteRecordTransport) Maintenance(ctx context.Context, target, action string) (recordMaintenanceStatus, error) {
	body, _ := json.Marshal(map[string]string{"target": target, "action": action})
	var b []byte
	var err error
	if target != "" {
		b, err = localRecordRequest(ctx, http.MethodPost, "/api/sync/maintenance", body)
	} else {
		if t.ssh == nil {
			return recordMaintenanceStatus{}, fmt.Errorf("guest transport unavailable")
		}
		out, e := t.ssh.outputCommand("curl -fsS --unix-socket \"${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/grove/groved.sock\" -H 'Content-Type: application/json' --data-binary @- http://localhost/api/sync/maintenance", string(body))
		b, err = []byte(out), e
	}
	if err != nil {
		return recordMaintenanceStatus{}, err
	}
	var st recordMaintenanceStatus
	if err = json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("decode maintenance status: %w", err)
	}
	return st, nil
}
func (t *satelliteRecordTransport) Incoming(ctx context.Context, satellite string, workspaces []string) (recordIncomingStatus, error) {
	q := url.Values{}
	q.Set("satellite", satellite)
	q.Set("workspaces", strings.Join(workspaces, ","))
	b, err := localRecordRequest(ctx, http.MethodGet, "/api/sync/incoming?"+q.Encode(), nil)
	if err != nil {
		return recordIncomingStatus{}, err
	}
	var st recordIncomingStatus
	if err = json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	if st.Manifest.Generation == "" {
		return st, fmt.Errorf("incoming response has no generation")
	}
	return st, nil
}

func dirtyMaintenance(st recordMaintenanceStatus) error {
	if !st.Draining {
		return fmt.Errorf("maintenance barrier is not active")
	}
	if st.OutboxPending > 0 || st.OutboxParked > 0 || st.DocumentsDiverged > 0 {
		return fmt.Errorf("record state is not drained: pending=%d parked=%d diverged=%d", st.OutboxPending, st.OutboxParked, st.DocumentsDiverged)
	}
	return nil
}

// prepareRecordSafeDown establishes both dispatch barriers and returns the
// reviewed generation. The returned finalCheck MUST run from provider.Down's
// PostConfirm immediately before the provider delete call.
func prepareRecordSafeDown(ctx context.Context, tr recordReturnTransport, name string, workspaces []string, force bool) (func() error, func(), error) {
	if len(workspaces) == 0 {
		return nil, nil, fmt.Errorf("full Tart has no explicit sync workspace selection; destructive down is unsafe")
	}
	laptop, err := tr.Maintenance(ctx, name, "enter")
	if err != nil {
		return nil, nil, fmt.Errorf("enter laptop dispatch maintenance: %w", err)
	}
	if err := dirtyMaintenance(laptop); err != nil {
		_, _ = tr.Maintenance(context.Background(), name, "leave")
		return nil, nil, fmt.Errorf("laptop %w", err)
	}
	cleanup := func() { _, _ = tr.Maintenance(context.Background(), name, "leave") }
	abortCleanup := func() {
		_, _ = tr.Maintenance(context.Background(), "", "leave")
		cleanup()
	}
	guest, err := tr.Maintenance(ctx, "", "enter")
	if err != nil {
		abortCleanup()
		return nil, nil, fmt.Errorf("enter guest maintenance (disconnect/unknown blocks deletion): %w", err)
	}
	if err = dirtyMaintenance(guest); err != nil {
		abortCleanup()
		return nil, nil, err
	}
	initial, err := tr.Incoming(ctx, name, workspaces)
	if err != nil {
		abortCleanup()
		return nil, nil, fmt.Errorf("query initial record generation: %w", err)
	}
	if !initial.Clean && !initial.EscrowVerified && !force {
		abortCleanup()
		return nil, nil, fmt.Errorf("unreturned record changes block deletion (exit %d); review with `nb sync incoming --satellite %s --workspace ...` and create `--escrow`, or explicitly use --force-discard-record-changes", satellitecontract.ExitReturnDirty, name)
	}
	finalCheck := func() (checkErr error) {
		defer func() {
			if checkErr != nil {
				abortCleanup()
			}
		}()
		laptop, err := tr.Maintenance(ctx, name, "enter")
		if err != nil {
			return fmt.Errorf("final laptop drain unknown: %w", err)
		}
		if err := dirtyMaintenance(laptop); err != nil {
			return fmt.Errorf("final laptop %w", err)
		}
		guest, err := tr.Maintenance(ctx, "", "enter")
		if err != nil {
			return fmt.Errorf("final guest maintenance status unknown: %w", err)
		}
		if err = dirtyMaintenance(guest); err != nil {
			return err
		}
		final, err := tr.Incoming(ctx, name, workspaces)
		if err != nil {
			return fmt.Errorf("final generation unknown: %w", err)
		}
		initialStatus := satellitecontract.RecordReturnStatus{Generation: initial.Manifest.Generation, Cleanliness: satellitecontract.ReturnDirty}
		if initial.EscrowVerified {
			initialStatus.EscrowVerified = true
			initialStatus.EscrowOperationID = "verified"
			initialStatus.EscrowSHA256 = initial.Manifest.Generation
		}
		if initial.Clean {
			initialStatus.Cleanliness = satellitecontract.ReturnClean
		}
		finalStatus := satellitecontract.RecordReturnStatus{Generation: final.Manifest.Generation, Cleanliness: satellitecontract.ReturnDirty}
		if final.EscrowVerified {
			finalStatus.EscrowVerified = true
			finalStatus.EscrowOperationID = "verified"
			finalStatus.EscrowSHA256 = final.Manifest.Generation
		}
		if final.Clean {
			finalStatus.Cleanliness = satellitecontract.ReturnClean
		}
		if err := satellitecontract.DeletionAllowed(initialStatus, finalStatus, force); err != nil {
			return err
		}
		if !final.Clean && !final.EscrowVerified && !force {
			return fmt.Errorf("final record generation is dirty")
		}
		return nil
	}
	return finalCheck, cleanup, nil
}
