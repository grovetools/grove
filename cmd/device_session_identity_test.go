package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/syncproto"
)

func identityHandler(t *testing.T, refusals int32, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/identity" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) <= refusals {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":               "identity discovery rate limit exceeded; retry in 1s",
				"retry_after_seconds": 1,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(syncproto.IdentityResponse{
			ServerEpoch:      "epoch",
			ProtocolVersions: syncproto.SupportedProtocolVersions(),
			ServerName:       "grove-syncd",
		})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// A rate limit is a request to come back, not a no. Identity discovery opens
// every device-session verb, so a bare 429 there failed a verb that had not
// yet asked the server for anything — with no retry and no advice.
func TestIdentityDiscoveryWaitsOutARateLimit(t *testing.T) {
	server, calls := identityHandler(t, 1, "1")
	identity, err := discoverSyncIdentity(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("discovery gave up on a refusal that asked it to come back: %v", err)
	}
	if identity.ServerEpoch != "epoch" {
		t.Fatalf("identity = %+v", identity)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("identity calls = %d, want the refusal plus one retry", got)
	}
}

// The retry is BOUNDED: a server that refuses forever gets a fixed number of
// attempts, and the error an operator reads carries the server's own sentence
// and the wait rather than a status code.
func TestIdentityDiscoveryGivesUpWithAdvice(t *testing.T) {
	server, calls := identityHandler(t, 99, "1")
	_, err := discoverSyncIdentity(context.Background(), server.Client(), server.URL)
	if err == nil {
		t.Fatal("a server refusing forever was read as success")
	}
	for _, want := range []string{"429", "rate limit exceeded", "wait 1s"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if got := calls.Load(); got != identityRetryAttempts {
		t.Fatalf("identity calls = %d, want exactly %d attempts", got, identityRetryAttempts)
	}
}

// A cancelled context stops the wait; a verb the operator interrupted must not
// sit through a Retry-After it never agreed to.
func TestIdentityDiscoveryStopsWaitingOnCancel(t *testing.T) {
	server, _ := identityHandler(t, 99, "30")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		_, err := discoverSyncIdentity(ctx, server.Client(), server.URL)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled discovery reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery sat through the Retry-After after its context was cancelled")
	}
}

// The header is advice, and advice can be missing, junk, or absurd.
func TestRetryAfterWaitIsClampedAndDefaulted(t *testing.T) {
	for header, want := range map[string]time.Duration{
		"":             time.Second,
		"not-a-number": time.Second,
		"0":            time.Second,
		"-5":           time.Second,
		"2":            2 * time.Second,
		" 3 ":          3 * time.Second,
		"86400":        identityRetryCap,
	} {
		if got := retryAfterWait(header); got != want {
			t.Fatalf("retryAfterWait(%q) = %s, want %s", header, got, want)
		}
	}
}
