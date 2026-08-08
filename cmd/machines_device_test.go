package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/syncproto"
)

func TestResolveServerDeviceRejectsAmbiguousNameAndPrefix(t *testing.T) {
	devices := []syncproto.DeviceInfo{
		{DeviceID: "01ABCDEFGHJKMNPQRSTVWXYZ01", Name: "laptop", Status: syncproto.DeviceStatusPending},
		{DeviceID: "01ABCDEFGHJKMNPQRSTVWXYZ02", Name: "laptop", Status: syncproto.DeviceStatusPending},
	}
	if _, err := resolveServerDevice(devices, "laptop", syncproto.DeviceStatusPending); err == nil {
		t.Fatal("ambiguous display name resolved")
	}
	if _, err := resolveServerDevice(devices, "01ABC", syncproto.DeviceStatusPending); err == nil {
		t.Fatal("ambiguous id prefix resolved")
	}
	got, err := resolveServerDevice(devices, devices[0].DeviceID, syncproto.DeviceStatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != devices[0].DeviceID {
		t.Fatalf("resolved %q, want %q", got.DeviceID, devices[0].DeviceID)
	}
}

func TestMachinesApprovePrintsFingerprintAndUsesSession(t *testing.T) {
	device := syncproto.DeviceInfo{
		DeviceID:    "01ABCDEFGHJKMNPQRSTVWXYZ01",
		Name:        "laptop",
		Status:      syncproto.DeviceStatusPending,
		Fingerprint: strings.Repeat("a", 64),
	}
	var approved bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Errorf("authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sync/devices":
			_ = json.NewEncoder(w).Encode(syncproto.DeviceListResponse{Devices: []syncproto.DeviceInfo{device}})
		case r.Method == http.MethodPost && r.URL.Path == "/sync/devices/"+device.DeviceID+"/approve":
			approved = true
			device.Status = syncproto.DeviceStatusApproved
			_ = json.NewEncoder(w).Encode(syncproto.DeviceApprovalResponse{Device: device})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client := &deviceSessionHTTP{baseURL: srv.URL, client: srv.Client(), bearer: "session-token"}
	var out bytes.Buffer
	if err := runMachinesApproveWithClient(context.Background(), strings.NewReader("yes\n"), &out, "laptop", false, client); err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("approve endpoint was not called")
	}
	if !strings.Contains(out.String(), device.Fingerprint) || !strings.Contains(out.String(), "Approve this exact fingerprint?") {
		t.Fatalf("approval did not expose and confirm full fingerprint:\n%s", out.String())
	}
}
