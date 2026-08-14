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
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/notespace"
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
	// Adoption probes the global sync daemon explicitly. Keep every sandboxed
	// test away from the developer's real daemon unless that test installs its
	// own socket fixture.
	t.Setenv(daemon.HostSocketEnv, filepath.Join(home, "no-such-daemon.sock"))
	t.Setenv("GROVE_SYNC_TOKEN", "") // never inherit the developer's token
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)

	configDir = filepath.Join(home, "config", "grove")
	notebookRoot = filepath.Join(home, "notebooks", "nb")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(notebookRoot, "notespaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"),
		[]byte("name = \"fixture\"\n\n[notebooks.definitions.nb]\nroot_dir = \""+notebookRoot+"\"\n[notebooks.rules]\ndefault = \"nb\"\n"), 0o644); err != nil {
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

func TestJoinDeviceOnlyApprovedWritesNoLegacyCredential(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	var publicKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sync/identity":
			_ = json.NewEncoder(w).Encode(syncproto.IdentityResponse{ServerEpoch: "epoch-device-only", ProtocolVersions: []int{syncproto.ProtocolVersionDeviceSession}})
		case "/sync/enroll":
			var req syncproto.EnrollRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Code != "one-use-code" {
				t.Errorf("enrollment code = %q", req.Code)
			}
			if err := syncproto.VerifyEnrollment(req); err != nil {
				t.Errorf("enrollment proof: %v", err)
			}
			publicKey = req.PublicKey
			fingerprint, _ := syncproto.DeviceFingerprint(publicKey)
			_ = json.NewEncoder(w).Encode(syncproto.EnrollResponse{DeviceID: req.DeviceID, Status: syncproto.DeviceStatusApproved, Fingerprint: fingerprint})
		case "/sync/capabilities":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("device handshake carried authorization %q", got)
			}
			var req syncproto.CapabilitiesRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			pub, err := syncproto.DecodeDevicePublicKey(publicKey)
			if err != nil || syncproto.VerifyCapabilities(req, pub) != nil {
				t.Errorf("invalid signed capabilities request: decode=%v verify=%v", err, syncproto.VerifyCapabilities(req, pub))
			}
			_ = json.NewEncoder(w).Encode(syncproto.CapabilitiesResponse{ProtocolVersion: syncproto.ProtocolVersionDeviceSession, SessionToken: "session-only", SessionExpiresAt: "tomorrow"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	opts := joinOptions{server: srv.URL, enrollCode: "one-use-code", registryWorkspace: defaultRegistryWorkspace, configDir: configDir, waitFor: 0}
	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("device-only join: %v\n%s", err, out.String())
	}
	cfg, err := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if err != nil || cfg == nil {
		t.Fatalf("load sync config: %v", err)
	}
	if cfg.Token != "" || cfg.TokenCommand != "" {
		t.Errorf("device-only config persisted legacy credential: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(configDir, syncTokenFileName)); !os.IsNotExist(err) {
		t.Errorf("device-only join created token file: %v", err)
	}
	if !strings.Contains(out.String(), "signed device handshake") {
		t.Errorf("missing device verification report:\n%s", out.String())
	}
}

func TestJoinEnrollsDeviceOnlyWhenAdvertised(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	var enrollments []syncproto.EnrollRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sync/capabilities":
			if r.Header.Get("Authorization") != "Bearer good-token" {
				http.Error(w, "denied", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(syncproto.CapabilitiesResponse{
				ServerName:      "grove-syncd",
				ProtocolVersion: syncproto.ProtocolVersion,
				Capabilities: syncproto.Capabilities{
					ProtocolVersions: []int{syncproto.ProtocolVersion},
					DeviceEnrollment: true,
				},
			})
		case "/sync/enroll":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("enrollment carried bearer authorization %q", got)
			}
			var req syncproto.EnrollRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode enrollment: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if err := syncproto.VerifyEnrollment(req); err != nil {
				t.Errorf("invalid enrollment proof: %v", err)
				http.Error(w, "bad proof", http.StatusBadRequest)
				return
			}
			if req.RequestedUser != "" {
				t.Errorf("join requested unauthenticated authority %q", req.RequestedUser)
			}
			enrollments = append(enrollments, req)
			fingerprint, _ := syncproto.DeviceFingerprint(req.PublicKey)
			_ = json.NewEncoder(w).Encode(syncproto.EnrollResponse{
				DeviceID: req.DeviceID, Status: syncproto.DeviceStatusPending, Fingerprint: fingerprint,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir)); err != nil {
			t.Fatalf("join run %d: %v\n%s", i+1, err, out.String())
		}
		if !strings.Contains(out.String(), "pending — fingerprint") {
			t.Errorf("join run %d did not truthfully report pending enrollment:\n%s", i+1, out.String())
		}
	}
	if len(enrollments) != 2 {
		t.Fatalf("enrollment count = %d, want 2", len(enrollments))
	}
	if enrollments[0].DeviceID != enrollments[1].DeviceID || enrollments[0].PublicKey != enrollments[1].PublicKey {
		t.Errorf("re-join changed device identity or key")
	}
	if info, err := os.Stat(devicekey.Path()); err != nil {
		t.Fatalf("device key missing: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("device key mode = %04o, want 0600", info.Mode().Perm())
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
	root := filepath.Join(notebookRoot, "notespaces", defaultRegistryWorkspace, "machines")
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Errorf("registry workspace not created at %s: %v", root, err)
	}
}

// TestJoinDoesNotStampTheNotebookItEnrollsInto: enrollment is a relationship,
// not a topology change. Join materializes the registry NOTESPACE and records
// which notebook holds it, but writing that notebook's own identity stamp is a
// claim only the notebook's own verbs may make.
//
// The concrete damage when it does: a machine joining a fleet to adopt a shared
// notebook has its default notebook root stamped during enrollment, and the
// `notebook pull` it joined in order to run then refuses that root as already
// claimed by a different notebook identity. Every check is behaving; the state
// they check was manufactured by the step before.
func TestJoinDoesNotStampTheNotebookItEnrollsInto(t *testing.T) {
	_, configDir, notebookRoot := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })

	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir)); err != nil {
		t.Fatalf("join: %v\n%s", err, out.String())
	}

	stampPath := filepath.Join(notebookRoot, notespace.NotebookStampName)
	if _, err := os.Stat(stampPath); err == nil {
		t.Errorf("join claimed the notebook root by writing %s; pull into this root is now refused", stampPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", stampPath, err)
	}

	// What join DOES own is still written: the registry notespace carries an
	// immutable id, and machine.toml records the notebook that holds it by the
	// name routing actually chose.
	spaceStamp, err := notespace.LoadNotespace(filepath.Join(notebookRoot, "notespaces", defaultRegistryWorkspace))
	if err != nil || spaceStamp == nil {
		t.Fatalf("registry notespace stamp not written: %v (%v)", spaceStamp, err)
	}
	machineCfg, err := config.LoadMachineConfig()
	if err != nil || machineCfg == nil || machineCfg.Sync.Registry == nil {
		t.Fatalf("[sync.registry] binding not recorded: %+v (%v)", machineCfg, err)
	}
	if machineCfg.Sync.Registry.Notebook != "nb" {
		t.Errorf("[sync.registry] notebook = %q, want %q", machineCfg.Sync.Registry.Notebook, "nb")
	}
	if machineCfg.Sync.Registry.NotespaceID != spaceStamp.ID {
		t.Errorf("[sync.registry] notespace_id = %q, want the stamped id %q", machineCfg.Sync.Registry.NotespaceID, spaceStamp.ID)
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
// restored roots.toml declares ecosystems this fresh host does not have, and
// join must end by naming them and the command that fixes each one.
func TestJoinDotfilesRestoreOffersMaterialize(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	nb := "nb"
	if _, err := config.WriteNotebooks(filepath.Join(configDir, coderoot.NotebooksFileName), config.NotebookEdits{
		Default: &nb, Upserts: map[string]coderoot.Notebook{"nb": {Root: filepath.Join(home, "notebooks", "nb")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteCodeRoots(filepath.Join(configDir, coderoot.RootsFileName), config.CodeRootEdits{
		Upserts: map[string]coderoot.Root{"grovetools": {Path: filepath.Join(home, "code", "grovetools"), Notebook: "nb"}},
	}); err != nil {
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

// THE JOB 52 REGRESSION, at the command level.
//
// Job 22 deliberately left this laptop a sync.toml with every key commented
// out — supported staging, not misuse. Joining against it wrote the
// subscription, declared no server, warned about nothing, and printed "the
// configuration above is complete". The daemon then had a workspace it could
// never replicate, and every status surface looked healthy.
func TestJoinConvergesAFullyCommentedSyncConfig(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	syncPath := filepath.Join(configDir, syncConfigFileName)
	staged := `# Notebook sync client config — staged, nothing enabled.
# server = "https://sync.example.com"
# token_command = "security find-generic-password -s grove-sync -w"
`
	if err := os.WriteFile(syncPath, []byte(staged), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })

	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir)); err != nil {
		t.Fatalf("join: %v\n%s", err, out.String())
	}

	// The post-condition is what the DAEMON reads back, not what join says.
	cfg, err := config.LoadSyncConfigFrom(syncPath)
	if err != nil || cfg == nil {
		t.Fatalf("sync.toml not usable: %v (%v)", cfg, err)
	}
	if cfg.Server != srv.URL {
		t.Errorf("server = %q, want the joined server — the merge path did not fill it", cfg.Server)
	}
	if strings.TrimSpace(cfg.TokenCommand) == "" && strings.TrimSpace(cfg.Token) == "" {
		t.Error("the converged config declares no credential")
	}
	if len(cfg.Workspaces) != 1 || cfg.Workspaces[0].Role != config.SyncRoleRegistry {
		t.Errorf("workspaces = %+v, want one registry entry", cfg.Workspaces)
	}
	if !strings.HasPrefix(readFileString(t, syncPath), staged) {
		t.Errorf("the staged comments were not preserved verbatim:\n%s", readFileString(t, syncPath))
	}

	// And it must never have claimed completeness in the old, unearned way.
	if strings.Contains(out.String(), "The configuration above is complete") {
		t.Errorf("join still prints an unearned completeness claim:\n%s", out.String())
	}
}

// A config join CANNOT complete must fail loudly and exit nonzero, rather than
// reporting the subscription it did manage to write.
func TestJoinFailsWhenTheConfigCannotBeCompleted(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	syncPath := filepath.Join(configDir, syncConfigFileName)
	// A declared-but-empty server: parses as absent, but IS declared, so
	// convergence refuses to add a second one.
	if err := os.WriteFile(syncPath, []byte("server = \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })

	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, joinOpts(srv.URL, configDir))
	if err == nil {
		t.Fatalf("join reported success for a config with no server:\n%s", out.String())
	}
	text := out.String()
	if !strings.Contains(text, "incomplete: no server") {
		t.Errorf("the failing step does not name the missing key:\n%s", text)
	}
	if !strings.Contains(text, "grove join --repair") {
		t.Errorf("the failure does not name the remedy:\n%s", text)
	}
	if !strings.Contains(text, "enrollment incomplete") {
		t.Errorf("the closing line still claims something:\n%s", text)
	}
}

// --repair converges without touching credentials — and refuses when there is
// no credential to converge around, rather than inventing or prompting for one.
func TestJoinRepairUsesTheExistingCredential(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	syncPath := filepath.Join(configDir, syncConfigFileName)
	tokenPath := filepath.Join(configDir, syncTokenFileName)

	// Nothing to repair yet.
	opts := joinOpts("", configDir)
	opts.token = ""
	opts.repair = true
	opts.deriveServerFn = func() (string, string, error) { return "http://127.0.0.1:1", "derived", nil }
	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err == nil {
		t.Fatalf("--repair invented a credential:\n%s", out.String())
	}

	// Now give the machine a credential the way join itself would, plus a
	// sync.toml that declares only the subscription.
	if err := os.WriteFile(tokenPath, []byte("good-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(syncPath, []byte("[[workspaces]]\nname = \"registry\"\nrole = \"registry\"\npull = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := capabilitiesServer(t, func(token string) int {
		if token != "good-token" {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	})
	opts.deriveServerFn = func() (string, string, error) { return srv.URL, "derived: forge.services domain + syncd.port", nil }
	out.Reset()
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("--repair: %v\n%s", err, out.String())
	}
	cfg, err := config.LoadSyncConfigFrom(syncPath)
	if err != nil || cfg == nil {
		t.Fatalf("sync.toml not usable: %v (%v)", cfg, err)
	}
	if cfg.Server != srv.URL {
		t.Errorf("--repair did not fill the absent server: %q", cfg.Server)
	}
	if cfg.TokenCommand != "cat "+tokenPath {
		t.Errorf("--repair did not record the existing credential: %q", cfg.TokenCommand)
	}
	if !strings.Contains(out.String(), "minted nothing") {
		t.Errorf("--repair did not say it minted nothing:\n%s", out.String())
	}
}

// The server is DERIVED when none is typed: the operator who ran `grove forge
// up` already told grove where the forge is.
func TestJoinDerivesTheServerWhenNoneIsGiven(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })

	opts := joinOpts("", configDir)
	opts.deriveServerFn = func() (string, string, error) {
		return srv.URL, "derived: forge.services domain + syncd.port", nil
	}
	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("join: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "derived: forge.services") {
		t.Errorf("join did not say where the server came from:\n%s", out.String())
	}
	cfg, _ := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if cfg == nil || cfg.Server != srv.URL {
		t.Errorf("the derived server was not written: %+v", cfg)
	}
}

// With no forge and no argument there is nothing to derive from, and the error
// must name the remedy rather than the missing internal.
func TestJoinWithoutAServerOrAForgeNamesTheRemedy(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	opts := joinOpts("", configDir)
	opts.deriveServerFn = func() (string, string, error) { return "", "", errNoForgeToDeriveFrom }
	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, opts)
	if err == nil || !strings.Contains(err.Error(), "grove join https://") {
		t.Fatalf("error does not name the remedy: %v", err)
	}
}

// --mint composes the forge recipe. grove never holds the secret at rest: the
// recipe returns a RECEIPT, and grove reads the credential back through the
// token_command the way the daemon later will.
func TestJoinMintWritesTheAccountPinnedTokenCommand(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(token string) int {
		if token != "minted-token" {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	})

	opts := joinOpts(srv.URL, configDir)
	opts.token = ""
	opts.mint = true
	minted := ""
	opts.mintFn = func(_ context.Context, machineName string) (mintReceipt, error) {
		minted = machineName
		return mintReceipt{
			Stored:       "keychain",
			Service:      "grove-sync",
			Account:      machineName,
			Description:  machineName,
			HashPrefix:   "a1b2c3d4e5f6",
			TokenCommand: "printf minted-token",
		}, nil
	}
	var out bytes.Buffer
	if err := runJoin(context.Background(), strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("join --mint: %v\n%s", err, out.String())
	}
	if minted == "" {
		t.Fatal("--mint did not pass this machine's name to the recipe")
	}

	cfg, err := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName))
	if err != nil || cfg == nil {
		t.Fatalf("sync.toml not usable: %v (%v)", cfg, err)
	}
	if cfg.TokenCommand != "printf minted-token" {
		t.Errorf("token_command = %q, want the recipe's read-back command", cfg.TokenCommand)
	}
	// The token file path is NOT used when a token_command is recorded.
	if _, statErr := os.Stat(filepath.Join(configDir, syncTokenFileName)); !os.IsNotExist(statErr) {
		t.Errorf("--mint wrote a token file beside the keychain item (%v)", statErr)
	}
	// The secret must not appear anywhere in what the user saw.
	if strings.Contains(out.String(), "minted-token") {
		t.Errorf("the minted token reached the terminal:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "a1b2c3d4e5f6") {
		t.Errorf("join did not report the server-side hash prefix:\n%s", out.String())
	}
}

// A mint whose stored value cannot be read back must fail loudly. This is the
// job 52 keychain bug's shape: the item exists, the command succeeds, the value
// is the empty string.
func TestJoinMintFailsWhenTheStoredValueIsUnreadable(t *testing.T) {
	_, configDir, _ := sandboxAdoption(t)
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })

	opts := joinOpts(srv.URL, configDir)
	opts.token = ""
	opts.mint = true
	opts.mintFn = func(_ context.Context, machineName string) (mintReceipt, error) {
		return mintReceipt{
			Stored:       "keychain",
			Service:      "grove-sync",
			Account:      machineName,
			TokenCommand: "printf ''", // succeeds, prints nothing
		}, nil
	}
	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, opts)
	if err == nil {
		t.Fatalf("join accepted a credential that reads back empty:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "keychain") {
		t.Errorf("the error does not name the store: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, syncConfigFileName)); !os.IsNotExist(statErr) {
		t.Errorf("config was written despite an unreadable credential (%v)", statErr)
	}
}

// Enrollment without replication is INCOMPLETE. Reporting the config write and
// exiting 0 with no daemon running is the exact claim job 52 was given.
func TestJoinWithoutADaemonIsIncomplete(t *testing.T) {
	home, configDir, _ := sandboxAdoption(t)
	// Point the daemon client at a socket that does not exist, so the wait
	// resolves quickly to "no daemon answered" rather than talking to the
	// developer's real groved.
	t.Setenv(daemon.HostSocketEnv, filepath.Join(home, "no-such-daemon.sock"))
	srv := capabilitiesServer(t, func(string) int { return http.StatusOK })

	opts := joinOpts(srv.URL, configDir)
	opts.waitFor = 10 * time.Millisecond
	var out bytes.Buffer
	err := runJoin(context.Background(), strings.NewReader(""), &out, opts)
	if err == nil {
		t.Fatalf("join exited 0 with nothing replicating:\n%s", out.String())
	}
	text := out.String()
	if !strings.Contains(text, "no running daemon answered") {
		t.Errorf("join did not say the daemon is missing:\n%s", text)
	}
	if strings.Contains(text, "enrolled and replicating") {
		t.Errorf("join claimed replication it never observed:\n%s", text)
	}
	// The config IS written — the failure is about replication, not the write.
	if cfg, cerr := config.LoadSyncConfigFrom(filepath.Join(configDir, syncConfigFileName)); cerr != nil || cfg == nil || cfg.Server == "" {
		t.Errorf("the config write was rolled back by a daemon failure: %+v (%v)", cfg, cerr)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
