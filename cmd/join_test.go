package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/syncproto"
)

// sandboxAdoption builds a hermetic machine for the adoption verbs: GROVE_HOME
// redirects config AND state, so nothing in these tests can reach the
// developer's real ~/.config/grove or ~/.local/state/grove — not the sync.toml
// they write, not the machine.toml, and not the identity `join` mints.
//
// That is not belt-and-braces. `grove join` mints a machine identity as its
// first act, and a test that merely calls it against an unsandboxed state dir
// writes a real machine.json onto the developer's host (the plan's SI-1).
func sandboxAdoption(t *testing.T) (home, configDir, notebookRoot string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("GROVE_HOME", home)
	t.Setenv("GROVE_SYNC_TOKEN", "") // never inherit the developer's token
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)

	configDir = filepath.Join(home, "config", "grove")
	notebookRoot = filepath.Join(home, "notebooks", "nb")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(notebookRoot, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"),
		[]byte("name = \"fixture\"\n\n[notebooks.definitions.nb]\nroot_dir = \""+notebookRoot+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, configDir, notebookRoot
}

// capabilitiesServer stands in for grove-syncd's POST /sync/capabilities. It
// answers with the SHARED contract type (core/pkg/syncproto), which is the same
// struct the real server marshals, so the handshake this test exercises is the
// real one — only the transport is a httptest listener.
func capabilitiesServer(t *testing.T, accept func(token string) int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/capabilities" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if code := accept(token); code != http.StatusOK {
			http.Error(w, "denied", code)
			return
		}
		var req syncproto.CapabilitiesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(syncproto.CapabilitiesResponse{
			ServerName:      "grove-syncd",
			ServerEpoch:     "test-epoch",
			ProtocolVersion: syncproto.ProtocolVersion,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func joinOpts(server, configDir string) joinOptions {
	return joinOptions{
		server:            server,
		token:             "good-token",
		registryWorkspace: defaultRegistryWorkspace,
		configDir:         configDir,
		waitFor:           0, // no daemon in a unit test; the wait is not what is under test
	}
}

// TestJoinDoesNotPersistConfigOnRejectedToken is the command's whole reason for
// existing: a token the server refuses must leave nothing behind, because a
// rejected token in sync.toml means a daemon that 401-loops forever while every
// status surface still looks healthy.
func TestJoinDoesNotPersistConfigOnRejectedToken(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusUnauthorized })

	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir))
	if err == nil {
		t.Fatalf("join succeeded against a rejecting server:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "rejected this token") {
		t.Errorf("error does not name the rejection: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, syncConfigFileName)); !os.IsNotExist(statErr) {
		t.Errorf("sync.toml was written despite a rejected token (%v)", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, syncTokenFileName)); !os.IsNotExist(statErr) {
		t.Errorf("the rejected token file was left behind (%v)", statErr)
	}
}

// TestJoinUnreachableServerIsNotATokenVerdict: a transport failure must not be
// reported as a bad token, and must not persist config either.
func TestJoinUnreachableServerIsNotATokenVerdict(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })
	dead := srv.URL
	srv.Close()

	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(dead, configDir))
	if err == nil {
		t.Fatalf("join succeeded against a dead server:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "not a token verdict") {
		t.Errorf("a transport failure was reported as a token problem: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, syncConfigFileName)); !os.IsNotExist(statErr) {
		t.Errorf("sync.toml was written after a transport failure (%v)", statErr)
	}
}

// TestJoinWritesRegistrySubscription covers the happy path end to end: token
// file at 0600, a registry-ROLE (not registry-NAMED) subscription that pulls,
// and a registry workspace directory the daemon can resolve.
func TestJoinWritesRegistrySubscription(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(token string) int {
		if token != "good-token" {
			return http.StatusForbidden
		}
		return http.StatusOK
	})

	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir)); err != nil {
		t.Fatalf("join: %v\n%s", err, out.String())
	}

	tokenPath := filepath.Join(configDir, syncTokenFileName)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("token file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode is %v, want 0600", perm)
	}

	syncCfg, err := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if err != nil || syncCfg == nil {
		t.Fatalf("sync.toml not usable: %v (%v)", syncCfg, err)
	}
	if syncCfg.Server != srv.URL {
		t.Errorf("server = %q, want %q", syncCfg.Server, srv.URL)
	}
	if syncCfg.TokenCommand != "cat "+tokenPath {
		t.Errorf("token_command = %q, want a read of the token file", syncCfg.TokenCommand)
	}
	if len(syncCfg.Workspaces) != 1 {
		t.Fatalf("want exactly one workspace, got %d", len(syncCfg.Workspaces))
	}
	ws := syncCfg.Workspaces[0]
	if ws.Role != config.SyncRoleRegistry || !ws.Pull {
		t.Errorf("registry entry is role=%q pull=%t, want role=registry pull=true", ws.Role, ws.Pull)
	}

	// The registry directory must exist: syntheticNodeFor prefers a root that
	// is already on disk, so creating it is what pins the subscription to the
	// notebook join chose rather than one a later rule picks.
	root := filepath.Join(notebookRoot, "workspaces", defaultRegistryWorkspace, "machines")
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Errorf("registry workspace not created at %s: %v", root, err)
	}
}

// TestJoinIsIdempotentAndAppendOnly: a second join neither duplicates the
// subscription nor rewrites a single byte the user might have edited.
func TestJoinIsIdempotentAndAppendOnly(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })
	opts := joinOpts(srv.URL, configDir)

	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("first join: %v\n%s", err, out.String())
	}
	syncPath := filepath.Join(configDir, syncConfigFileName)
	first, err := os.ReadFile(syncPath)
	if err != nil {
		t.Fatal(err)
	}

	// A hand-edit the second run must preserve verbatim.
	edited := string(first) + "\n# a comment the user added by hand\n"
	if err := os.WriteFile(syncPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("second join: %v\n%s", err, out.String())
	}
	second, err := os.ReadFile(syncPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != edited {
		t.Errorf("second join rewrote sync.toml:\n--- want ---\n%s\n--- got ---\n%s", edited, second)
	}
	if !strings.Contains(out.String(), "already subscribes") {
		t.Errorf("second join did not report the subscription as already present:\n%s", out.String())
	}
}

// TestJoinRefusesToReplaceADifferentToken: silently replacing a working
// credential is how a machine goes offline without anyone noticing.
func TestJoinRefusesToReplaceADifferentToken(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })
	tokenPath := filepath.Join(configDir, syncTokenFileName)
	if err := os.WriteFile(tokenPath, []byte("an-existing-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir))
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("join replaced or failed to explain an existing token: %v\n%s", err, out.String())
	}
	data, _ := os.ReadFile(tokenPath)
	if strings.TrimSpace(string(data)) != "an-existing-token" {
		t.Errorf("the existing token was modified: %q", data)
	}

	// --force is the escape hatch, and it works.
	forced := joinOpts(srv.URL, configDir)
	forced.force = true
	out.Reset()
	if err := runJoin(context.Background(), strings.NewReader(""), &out, forced); err != nil {
		t.Fatalf("forced join: %v\n%s", err, out.String())
	}
	data, _ = os.ReadFile(tokenPath)
	if strings.TrimSpace(string(data)) != "good-token" {
		t.Errorf("--force did not replace the token: %q", data)
	}
}

// TestJoinDotfilesRestoreOffersMaterialize is the supported fast path: a
// restored machine.toml declares ecosystems this fresh host does not have, and
// join must end by naming them and the command that fixes each one.
func TestJoinDotfilesRestoreOffersMaterialize(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	restored := "[machine]\nname = \"restored\"\n\n[machine.ecosystems.grovetools]\npath = \"" +
		filepath.Join(home, "code", "grovetools") + "\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "machine.toml"), []byte(restored), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()

	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })
	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir)); err != nil {
		t.Fatalf("join: %v\n%s", err, out.String())
	}
	text := out.String()
	if !strings.Contains(text, "Declared but not present") {
		t.Errorf("join did not compute declared-but-missing:\n%s", text)
	}
	if !strings.Contains(text, "grove ecosystem materialize grovetools") {
		t.Errorf("join did not offer the materialize command:\n%s", text)
	}
}

// TestJoinRejectsNonHTTPServer keeps a plausible typo (a bare host, an ssh
// URL) from being written into config as if it were a server.
func TestJoinRejectsNonHTTPServer(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts("sync.example.com", configDir))
	if err == nil || !strings.Contains(err.Error(), "http://") {
		t.Fatalf("a non-URL server was accepted: %v", err)
	}
}
