package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/syncproto"
)

// TestClassifySyncTokenStatus pins the decision table both probes share. The
// distinction that carries weight is REJECTED vs UNHEALTHY: only the first is a
// verdict on the token, and only it should tell a user to re-mint one.
func TestClassifySyncTokenStatus(t *testing.T) {
	cases := map[string]syncTokenVerdict{
		"200":     syncTokenAccepted,
		"204":     syncTokenAccepted,
		" 200 \n": syncTokenAccepted,
		"401":     syncTokenRejected,
		"403":     syncTokenRejected,
		"500":     syncTokenUnhealthy,
		"404":     syncTokenUnhealthy,
		"":        syncTokenUnhealthy,
		"nope":    syncTokenUnhealthy,
	}
	for status, want := range cases {
		if got := classifySyncTokenStatus(status); got != want {
			t.Errorf("classifySyncTokenStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestVerifySyncTokenOverHTTPRejectsAFailedHandshake: a syncd that answers 200
// while reporting a protocol mismatch IN the body has not accepted anything.
// Treating that as success is how join would "verify" a server it cannot talk
// to.
func TestVerifySyncTokenOverHTTPRejectsAFailedHandshake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(syncproto.CapabilitiesResponse{
			ServerName: "grove-syncd",
			Error:      "no common protocol version: server speaks [2], client offered [1]",
		})
	}))
	defer srv.Close()

	err := verifySyncTokenOverHTTP(context.Background(), nil, srv.URL, "token", "01DEVICE")
	if err == nil || !strings.Contains(err.Error(), "no common protocol version") {
		t.Fatalf("a failed handshake was accepted: %v", err)
	}
}

// TestVerifySyncTokenOverHTTPSendsTheDeviceID: the machine's durable identity
// rides the handshake from first contact, so a server that ever starts
// recording device principals sees the real one.
func TestVerifySyncTokenOverHTTPSendsTheDeviceID(t *testing.T) {
	var got syncproto.CapabilitiesRequest
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(syncproto.CapabilitiesResponse{
			ServerName:      "grove-syncd",
			ProtocolVersion: syncproto.ProtocolVersion,
		})
	}))
	defer srv.Close()

	if err := verifySyncTokenOverHTTP(context.Background(), nil, srv.URL, "the-token", "01DEVICE"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if auth != "Bearer the-token" {
		t.Errorf("Authorization = %q", auth)
	}
	if got.DeviceID != "01DEVICE" {
		t.Errorf("device_id = %q, want 01DEVICE", got.DeviceID)
	}
	if len(got.ProtocolVersions) != 1 || got.ProtocolVersions[0] != syncproto.ProtocolVersion {
		t.Errorf("protocol_versions = %v", got.ProtocolVersions)
	}
}

// TestVerifySyncTokenOverHTTPNamesTheRejection.
func TestVerifySyncTokenOverHTTPNamesTheRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := verifySyncTokenOverHTTP(context.Background(), nil, srv.URL, "bad", "")
	if err == nil || !strings.Contains(err.Error(), "rejected this token") {
		t.Fatalf("rejection not surfaced: %v", err)
	}
	if !strings.Contains(err.Error(), "nothing has been written") {
		t.Errorf("the error does not reassure about config state: %v", err)
	}
}

// TestVerifySyncTokenOverHTTPSeparatesUnhealthyFromRejected.
func TestVerifySyncTokenOverHTTPSeparatesUnhealthyFromRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := verifySyncTokenOverHTTP(context.Background(), nil, srv.URL, "token", "")
	if err == nil || !strings.Contains(err.Error(), "not a token verdict") {
		t.Fatalf("a 500 was reported as a token problem: %v", err)
	}
}

// TestVerifySatelliteSyncTokenStillUsesTheSharedTable proves the extraction did
// not change the satellite path's behavior: the same three outcomes, with the
// VM-specific remediation intact.
func TestVerifySatelliteSyncTokenStillUsesTheSharedTable(t *testing.T) {
	probe := func(status string) func(string, string) (string, error) {
		return func(string, string) (string, error) { return status, nil }
	}
	if err := verifySatelliteSyncToken(probe("200"), "cmd", "tok", "vm", "/tmp/t"); err != nil {
		t.Errorf("2xx rejected: %v", err)
	}
	err := verifySatelliteSyncToken(probe("401"), "cmd", "tok", "vm", "/tmp/t")
	if err == nil || !strings.Contains(err.Error(), "stale for this VM") {
		t.Errorf("401 did not produce the stale-token remediation: %v", err)
	}
	err = verifySatelliteSyncToken(probe("500"), "cmd", "tok", "vm", "/tmp/t")
	if err == nil || !strings.Contains(err.Error(), "not a token verdict") {
		t.Errorf("500 was treated as a token verdict: %v", err)
	}
}
