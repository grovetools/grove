package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
)

// Hermetic fixtures for the P3 notebook scope verbs.
//
// Every test here runs against a temp GROVE_HOME (which redirects config AND
// state, so neither the developer's ~/.config/grove nor the device key under
// ~/.local/state/grove is reachable) and an httptest server that speaks the
// SHARED wire contract from core/pkg/syncproto. The fake server enforces the
// real server's rules — share never re-parents, a re-parent is gated on the
// membership version it actually holds — because a fake that says yes to
// everything would let the client's own preconditions rot unnoticed.

// ---- sandbox -------------------------------------------------------------------

type scopeSandbox struct {
	home      string
	configDir string
	notebooks string
}

func sandboxNotebookScope(t *testing.T) scopeSandbox {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	t.Setenv(daemon.HostSocketEnv, filepath.Join(home, "no-such-daemon.sock"))
	t.Setenv("GROVE_SYNC_TOKEN", "")
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)

	box := scopeSandbox{
		home:      home,
		configDir: filepath.Join(home, "config", "grove"),
		notebooks: filepath.Join(home, "notebooks"),
	}
	if err := os.MkdirAll(box.configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := devicekey.Ensure(); err != nil {
		t.Fatal(err)
	}
	return box
}

// notebookFixture is one recorded notebook plus what is on disk under it.
type notebookFixture struct {
	// Missing declares the notebook in notebooks.toml without creating its
	// root — the state pull and share must refuse.
	Missing bool
	Share   *bool
	// Stamp, when non-empty, installs a .notebook.toml with this id.
	Stamp      string
	Notespaces []notespaceFixture
}

type notespaceFixture struct {
	Dir string
	// ID empty means an unstamped notespace directory.
	ID      string
	Name    string
	Subject string
	Kind    string
}

// recordNotebooks writes notebooks.toml and materializes the fixture trees.
func (s scopeSandbox) recordNotebooks(t *testing.T, defaultName string, fixtures map[string]notebookFixture) {
	t.Helper()
	var b strings.Builder
	if defaultName != "" {
		fmt.Fprintf(&b, "default = %q\n\n", defaultName)
	}
	names := make([]string, 0, len(fixtures))
	for name := range fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fixture := fixtures[name]
		root := filepath.Join(s.notebooks, name)
		fmt.Fprintf(&b, "[notebooks.%s]\nroot = %q\n", name, root)
		if fixture.Share != nil {
			fmt.Fprintf(&b, "\n[notebooks.%s.sync]\nshare = %t\n", name, *fixture.Share)
		}
		b.WriteString("\n")
		if fixture.Missing {
			continue
		}
		if err := os.MkdirAll(filepath.Join(root, "notespaces"), 0o755); err != nil {
			t.Fatal(err)
		}
		if fixture.Stamp != "" {
			if _, err := notespace.InstallNotebook(root, notespace.NotebookStamp{ID: fixture.Stamp, Name: name}); err != nil {
				t.Fatal(err)
			}
		}
		for _, ns := range fixture.Notespaces {
			nsRoot := filepath.Join(root, "notespaces", ns.Dir)
			if err := os.MkdirAll(nsRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nsRoot, "note.md"), []byte("# "+ns.Dir+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if ns.ID == "" {
				continue
			}
			stamp := notespace.NotespaceStamp{ID: ns.ID, Name: valueOr(ns.Name, ns.Dir), Subject: valueOr(ns.Subject, "local:"+ns.ID), Kind: valueOr(ns.Kind, "notes")}
			if _, err := notespace.InstallNotespace(nsRoot, stamp); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(s.configDir, "notebooks.toml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// recordSyncServer writes the sync.toml a verb needs to find a server without
// running `grove sync join` first.
func (s scopeSandbox) recordSyncServer(t *testing.T, server string) {
	t.Helper()
	content := fmt.Sprintf("server = %q\ntoken = \"fixture-token\"\n", server)
	if err := os.WriteFile(filepath.Join(s.configDir, "sync.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
}

func (s scopeSandbox) notespaceRoot(notebook, dir string) string {
	return filepath.Join(s.notebooks, notebook, "notespaces", dir)
}

func (s scopeSandbox) readNotebooksTOML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.configDir, "notebooks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ---- the fake grove-syncd -------------------------------------------------------

type fakeNotebook struct {
	Name       string
	ShareState string
	Version    int64
}

type fakeNotespace struct {
	Name              string
	Subject           string
	Kind              string
	NotebookID        string
	MembershipVersion int64
	Cursor            int64
}

type fakeSync struct {
	t  *testing.T
	mu sync.Mutex

	Notebooks  map[string]*fakeNotebook
	Notespaces map[string]*fakeNotespace

	Registers []syncproto.RegisterRequest
	Shares    []syncproto.NotebookShareRequest
	Reparents []syncproto.NotespaceReparentRequest

	// ShareError, when set, makes every share fail with this code, which is how
	// the rollback path is exercised.
	ShareError string
	// SuppressDetachRetention drops the retention sentence from a detach
	// response, so the client's fallback can be pinned: it must label the
	// protocol's sentence as the protocol's rather than pass it off as this
	// server's words.
	SuppressDetachRetention bool
	// UnexplainedRefusalPath answers this path with a non-2xx whose body
	// decodes but names no protocol error — the one shape doJSONStatus cannot
	// tell from success on its own.
	UnexplainedRefusalPath string

	URL string
}

func newFakeSync(t *testing.T) *fakeSync {
	t.Helper()
	f := &fakeSync{t: t, Notebooks: map[string]*fakeNotebook{}, Notespaces: map[string]*fakeNotespace{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

func (f *fakeSync) addNotebook(id, name, state string, version int64) {
	f.Notebooks[id] = &fakeNotebook{Name: name, ShareState: state, Version: version}
}

func (f *fakeSync) addNotespace(id, name, notebookID string, membershipVersion, cursor int64) {
	f.Notespaces[id] = &fakeNotespace{Name: name, Subject: "local:" + id, Kind: "notes", NotebookID: notebookID, MembershipVersion: membershipVersion, Cursor: cursor}
}

func (f *fakeSync) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case f.UnexplainedRefusalPath != "" && r.URL.Path == f.UnexplainedRefusalPath:
		writeFakeJSON(w, http.StatusConflict, map[string]string{"detail": "a refusal this protocol cannot read"})
	case r.URL.Path == "/sync/identity":
		writeFakeJSON(w, http.StatusOK, syncproto.IdentityResponse{ServerEpoch: "epoch", ProtocolVersions: []int{syncproto.ProtocolVersionDeviceSession, syncproto.ProtocolVersionNotespaceID}})
	case r.URL.Path == "/sync/capabilities":
		writeFakeJSON(w, http.StatusOK, syncproto.CapabilitiesResponse{
			ServerName:       "fake-syncd",
			ServerEpoch:      "epoch",
			ProtocolVersion:  syncproto.ProtocolVersionDeviceSession,
			SessionToken:     "session-token",
			SessionExpiresAt: "2099-01-01T00:00:00Z",
			Capabilities:     syncproto.Capabilities{NotebookScope: true},
		})
	case r.URL.Path == "/sync/inventory":
		f.requireSession(w, r)
		f.handleInventory(w, r)
	case r.URL.Path == "/sync/register":
		f.requireSession(w, r)
		f.handleRegister(w, r)
	case r.URL.Path == "/sync/notebooks/share":
		f.requireSession(w, r)
		f.handleShare(w, r)
	case r.URL.Path == "/sync/notespaces/reparent":
		f.requireSession(w, r)
		f.handleReparent(w, r)
	default:
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
	}
}

func (f *fakeSync) requireSession(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
		f.t.Errorf("%s carried authorization %q, want the device session bearer", r.URL.Path, got)
	}
}

func (f *fakeSync) handleInventory(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := r.URL.Query().Get("protocol_version"); got != fmt.Sprint(syncproto.ProtocolVersionNotespaceID) {
		f.t.Errorf("inventory protocol_version = %q", got)
	}
	if r.URL.Query().Get("device_id") == "" || r.URL.Query().Get("idempotency_key") == "" {
		f.t.Errorf("inventory is missing its request identity query parameters: %s", r.URL.RawQuery)
	}
	response := syncproto.InventoryResponse{}
	for _, id := range sortedFakeKeys(f.Notebooks) {
		nb := f.Notebooks[id]
		entry := syncproto.InventoryNotebook{ID: syncproto.NotebookID(id), Name: nb.Name, ShareState: nb.ShareState, Version: nb.Version}
		for _, nsID := range sortedFakeKeys(f.Notespaces) {
			if f.Notespaces[nsID].NotebookID == id {
				entry.NotespaceIDs = append(entry.NotespaceIDs, syncproto.NotespaceID(nsID))
			}
		}
		response.Notebooks = append(response.Notebooks, entry)
	}
	for _, id := range sortedFakeKeys(f.Notespaces) {
		ns := f.Notespaces[id]
		response.Notespaces = append(response.Notespaces, syncproto.InventoryNotespace{
			ID: syncproto.NotespaceID(id), Name: syncproto.NotespaceName(ns.Name), Subject: ns.Subject, Kind: ns.Kind,
			NotebookID: syncproto.NotebookID(ns.NotebookID),
		})
	}
	writeFakeJSON(w, http.StatusOK, response)
}

func (f *fakeSync) handleRegister(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req syncproto.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Fatal(err)
	}
	if wire := req.Validate(); wire != nil {
		writeFakeJSON(w, http.StatusBadRequest, syncproto.RegisterResponse{Error: wire})
		return
	}
	f.Registers = append(f.Registers, req)
	id := req.ProposedNotespaceID.String()
	if _, ok := f.Notespaces[id]; !ok {
		f.Notespaces[id] = &fakeNotespace{Name: req.NotespaceName.String(), Subject: req.Subject, Kind: req.Kind}
	}
	writeFakeJSON(w, http.StatusOK, syncproto.RegisterResponse{NotespaceID: req.ProposedNotespaceID, ClaimVersion: 1, ServerEcho: "registered"})
}

func (f *fakeSync) handleShare(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req syncproto.NotebookShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Fatal(err)
	}
	if wire := req.Validate(); wire != nil {
		writeFakeJSON(w, http.StatusBadRequest, syncproto.NotebookShareResponse{Error: wire})
		return
	}
	f.Shares = append(f.Shares, req)
	if f.ShareError != "" {
		writeFakeJSON(w, http.StatusConflict, syncproto.NotebookShareResponse{
			NotebookID: req.NotebookID, Name: req.Name,
			Error: &syncproto.ProtocolError{Code: f.ShareError, Message: "the fixture refuses this share"},
		})
		return
	}

	// Share writes the notebook's name and share state, so the fixture holds
	// the client to the same precondition the server does: decide against the
	// version you read, or be told to re-read.
	if held, ok := f.Notebooks[req.NotebookID.String()]; ok && req.ExpectedVersion != held.Version {
		writeFakeJSON(w, http.StatusPreconditionFailed, syncproto.NotebookShareResponse{
			NotebookID: req.NotebookID, Name: held.Name, ShareState: held.ShareState, Version: held.Version,
			Error: &syncproto.ProtocolError{Code: syncproto.ErrorStaleResolution, Message: "notebook has moved on since this decision was made", CurrentVersion: held.Version},
		})
		return
	}

	var members []syncproto.NotebookMemberResult
	rejected := false
	for _, member := range req.Members {
		ns, ok := f.Notespaces[member.String()]
		switch {
		case !ok:
			rejected = true
			members = append(members, syncproto.NotebookMemberResult{
				NotespaceID: member, Disposition: syncproto.MemberDispositionRejected,
				Error: &syncproto.ProtocolError{Code: syncproto.ErrorUnregisteredNotespace, Message: "not registered"},
			})
		case ns.NotebookID == "":
			members = append(members, syncproto.NotebookMemberResult{NotespaceID: member, Disposition: syncproto.MemberDispositionAttached})
		case ns.NotebookID == req.NotebookID.String():
			members = append(members, syncproto.NotebookMemberResult{NotespaceID: member, Disposition: syncproto.MemberDispositionAlreadyMember})
		default:
			rejected = true
			members = append(members, syncproto.NotebookMemberResult{
				NotespaceID: member, Disposition: syncproto.MemberDispositionRejected,
				FromNotebookID: syncproto.NotebookID(ns.NotebookID),
				Error:          &syncproto.ProtocolError{Code: syncproto.ErrorMembershipConflict, Message: "belongs to another notebook"},
			})
		}
	}
	if rejected {
		writeFakeJSON(w, http.StatusConflict, syncproto.NotebookShareResponse{
			NotebookID: req.NotebookID, Name: req.Name, ShareState: syncproto.NotebookShareStateShared, Members: members,
			Error: &syncproto.ProtocolError{Code: syncproto.ErrorMembershipConflict, Message: "share rejected: no membership was changed"},
		})
		return
	}
	for _, member := range req.Members {
		ns := f.Notespaces[member.String()]
		if ns.NotebookID == "" {
			ns.NotebookID = req.NotebookID.String()
			ns.MembershipVersion++
		}
	}
	existing, found := f.Notebooks[req.NotebookID.String()]
	resumed := found && existing.ShareState == syncproto.NotebookShareStateUnshared
	if !found {
		existing = &fakeNotebook{}
		f.Notebooks[req.NotebookID.String()] = existing
	}
	existing.Name, existing.ShareState = req.Name, syncproto.NotebookShareStateShared
	existing.Version++
	writeFakeJSON(w, http.StatusOK, syncproto.NotebookShareResponse{
		NotebookID: req.NotebookID, Name: req.Name, ShareState: syncproto.NotebookShareStateShared,
		Version: existing.Version, Resumed: resumed, Members: members,
	})
}

func (f *fakeSync) handleReparent(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req syncproto.NotespaceReparentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Fatal(err)
	}
	if wire := req.Validate(); wire != nil {
		writeFakeJSON(w, http.StatusBadRequest, syncproto.NotespaceReparentResponse{Error: wire})
		return
	}
	f.Reparents = append(f.Reparents, req)
	ns, ok := f.Notespaces[req.NotespaceID.String()]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, syncproto.NotespaceReparentResponse{
			NotespaceID: req.NotespaceID,
			Error:       &syncproto.ProtocolError{Code: syncproto.ErrorUnregisteredNotespace, Message: "not registered"},
		})
		return
	}
	// A detach has no destination to qualify — leaving is always allowed — so
	// the destination is only judged for a move INTO a notebook. The fixture
	// mirrors the real store here because the client's whole out-of-shared leg
	// depends on that asymmetry.
	if !req.Detaching() {
		target, ok := f.Notebooks[req.ToNotebookID.String()]
		if !ok || target.ShareState == syncproto.NotebookShareStateUnshared {
			writeFakeJSON(w, http.StatusConflict, syncproto.NotespaceReparentResponse{
				NotespaceID: req.NotespaceID,
				Error:       &syncproto.ProtocolError{Code: syncproto.ErrorNotebookUnshared, Message: "destination is not a shared notebook"},
			})
			return
		}
	}
	if ns.NotebookID != req.FromNotebookID.String() || ns.MembershipVersion != req.ExpectedVersion {
		writeFakeJSON(w, http.StatusPreconditionFailed, syncproto.NotespaceReparentResponse{
			NotespaceID: req.NotespaceID,
			Error:       &syncproto.ProtocolError{Code: syncproto.ErrorStaleResolution, Message: "membership has moved on", CurrentVersion: ns.MembershipVersion},
		})
		return
	}
	ns.NotebookID = req.ToNotebookID.String()
	ns.MembershipVersion++
	response := syncproto.NotespaceReparentResponse{
		NotespaceID: req.NotespaceID, FromNotebookID: req.FromNotebookID, ToNotebookID: req.ToNotebookID,
		Version: ns.MembershipVersion, Cursor: ns.Cursor, HistoryPreserved: true,
	}
	if req.Detaching() {
		response.Detached = true
		response.Retention = syncproto.DetachRetentionStatement
		if f.SuppressDetachRetention {
			response.Retention = ""
		}
	}
	writeFakeJSON(w, http.StatusOK, response)
}

func writeFakeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func sortedFakeKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// ---- shared assertions ----------------------------------------------------------

func requireContains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: output does not contain %q\n---\n%s", what, needle, haystack)
	}
}

func requireNotContains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("%s: output unexpectedly contains %q\n---\n%s", what, needle, haystack)
	}
}

// Fixture ULIDs. They are literals rather than minted values so a failing test
// names the same id every run.
const (
	fixtureNotebookA  = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fixtureNotebookB  = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	fixtureNotebookC  = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	fixtureNotespace1 = "01ARZ3NDEKTSV4RRFFQ69G5F01"
	fixtureNotespace2 = "01ARZ3NDEKTSV4RRFFQ69G5F02"
	fixtureNotespace3 = "01ARZ3NDEKTSV4RRFFQ69G5F03"
)

func boolPtr(v bool) *bool { return &v }
