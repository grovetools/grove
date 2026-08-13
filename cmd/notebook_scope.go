package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/pkg/transition"
)

// The client half of the P3 notebook scope model, shared by `grove sync join`,
// `grove notebook share|pull` and `grove notespace move`.
//
// One rule runs through all of it: the three facts these verbs reason over —
// what notebooks.toml records, what the stamps on disk say, and what the server
// holds — are READ, never guessed. A notebook whose recorded root does not
// exist is a refusal rather than a MkdirAll; a notespace directory with no
// stamp is reported as unminted rather than silently dropped; a server that
// disagrees with a stamp is an error naming both ids. The verbs differ only in
// what they do after the reading.

// recordedNotespace is one notespace directory beneath a recorded notebook.
// Stamp is nil when the directory carries no .notespace.toml — a real state
// (created before P2, or by hand) that share mints and every other verb names.
type recordedNotespace struct {
	Dir   string
	Root  string
	Stamp *notespace.NotespaceStamp
}

// ID is the notespace's immutable id, or "" when it is unminted.
func (n recordedNotespace) ID() string {
	if n.Stamp == nil {
		return ""
	}
	return n.Stamp.ID
}

// recordedNotebook is one [notebooks.<name>] table as this machine holds it:
// the recorded declaration, the directory it resolves to, whether that
// directory exists, the recorded share state, and what is inside it.
type recordedNotebook struct {
	Name string
	// Declared is the notebooks.toml spelling; Root is the expanded path. Both
	// are kept because the transition printer reports both.
	Declared string
	Root     string
	Exists   bool
	// Shared is `share = true`; SyncRecorded distinguishes an explicit
	// `share = false` (D9's recorded-as-unshared) from "never recorded".
	Shared       bool
	SyncRecorded bool
	Stamp        *notespace.NotebookStamp
	Notespaces   []recordedNotespace
}

// ID is the notebook's immutable id, or "" when the root carries no stamp.
func (n recordedNotebook) ID() string {
	if n.Stamp == nil {
		return ""
	}
	return n.Stamp.ID
}

// loadRecordedNotebooks reads the recorded routing pair and scans every
// notebook it names. A machine with no notebooks.toml is refused by name: the
// notebook scope model has nothing to talk about before the config collapse
// has landed on this machine.
func loadRecordedNotebooks() (coderoot.Table, []recordedNotebook, error) {
	table, err := coderoot.Load()
	if err != nil {
		return coderoot.Table{}, nil, err
	}
	if table.NotebooksFilePath == "" {
		return coderoot.Table{}, nil, fmt.Errorf("this machine records no %s; the notebook scope model reads recorded notebooks only — run `grove migrate` first", coderoot.NotebooksFileName)
	}
	scanned, err := scanRecordedNotebooks(table)
	if err != nil {
		return coderoot.Table{}, nil, err
	}
	return table, scanned, nil
}

// scanRecordedNotebooks walks the recorded notebooks in deterministic order. A
// missing root is recorded as Exists=false rather than an error: only the verbs
// that need the directory decide that its absence is fatal, and `sync join`
// deliberately still reports it.
func scanRecordedNotebooks(table coderoot.Table) ([]recordedNotebook, error) {
	out := make([]recordedNotebook, 0, len(table.Notebooks))
	for _, name := range table.SortedNotebookNames() {
		definition := table.Notebooks[name]
		root := table.NotebookRoot(name)
		entry := recordedNotebook{
			Name:         name,
			Declared:     definition.Root,
			Root:         root,
			Shared:       definition.Shared(),
			SyncRecorded: definition.SyncRecorded(),
		}
		info, err := os.Stat(root)
		entry.Exists = err == nil && info.IsDir()
		if !entry.Exists {
			out = append(out, entry)
			continue
		}
		stamp, err := notespace.LoadNotebook(root)
		if err != nil {
			return nil, err
		}
		entry.Stamp = stamp
		contained, err := scanContainedNotespaces(root)
		if err != nil {
			return nil, err
		}
		entry.Notespaces = contained
		out = append(out, entry)
	}
	return out, nil
}

// scanContainedNotespaces lists the notespaces of one notebook root. The
// notespaces/ directory being absent is not an error — an empty notebook is a
// notebook.
func scanContainedNotespaces(notebookRoot string) ([]recordedNotespace, error) {
	dir := filepath.Join(notebookRoot, notespaceContainerDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read notespaces in %s: %w", notebookRoot, err)
	}
	out := make([]recordedNotespace, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(dir, entry.Name())
		stamp, err := notespace.LoadNotespace(root)
		if err != nil {
			return nil, err
		}
		out = append(out, recordedNotespace{Dir: entry.Name(), Root: root, Stamp: stamp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// notespaceContainerDir is the notebook-relative directory notespaces live in.
// It mirrors core/pkg/workspace.NotespaceDirectory, quoted here rather than
// imported so this file depends only on the identity and routing packages.
const notespaceContainerDir = "notespaces"

// findRecordedNotebook resolves a notebook NAME against the recorded table. An
// unknown name is a hard error naming the file and every name it does record —
// the alternative is a verb that invents a notebook the operator never wrote
// down.
func findRecordedNotebook(table coderoot.Table, scanned []recordedNotebook, name string) (recordedNotebook, error) {
	for _, entry := range scanned {
		if entry.Name == name {
			return entry, nil
		}
	}
	return recordedNotebook{}, fmt.Errorf("%s records no notebook %q (it records: %s)",
		displayRecordedPath(table.NotebooksFilePath, coderoot.NotebooksFileName), name,
		strings.Join(table.SortedNotebookNames(), ", "))
}

func displayRecordedPath(path, fallback string) string {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	return path
}

// deltaLocalNotebooks projects the scan into the pure delta function's input.
// Only stamped notebooks carry an id, and only an id compares against a server
// — unstamped notebooks are reported separately by the caller rather than
// folded in as "absent from the server", which they are not.
func deltaLocalNotebooks(scanned []recordedNotebook) []syncproto.LocalNotebook {
	out := make([]syncproto.LocalNotebook, 0, len(scanned))
	for _, entry := range scanned {
		if entry.ID() == "" {
			continue
		}
		local := syncproto.LocalNotebook{
			ID:   syncproto.NotebookID(entry.ID()),
			Name: entry.Name,
			// Both halves of the recorded tri-state travel: `share = false` is
			// D9's recorded-as-unshared and must not read as "never considered".
			Shared:   entry.Shared,
			Recorded: entry.SyncRecorded,
		}
		for _, ns := range entry.Notespaces {
			if ns.ID() != "" {
				local.Notespaces = append(local.Notespaces, syncproto.NotespaceID(ns.ID()))
			}
		}
		out = append(out, local)
	}
	return out
}

// refuseDuplicateNotebookIDs stops a verb that would ACT on a machine
// recording one notebook id twice — the copied-stamp state of D8.
//
// The check needs no server: duplication is entirely a local fact, and
// BuildInventoryDelta reports it against an empty inventory just as well as
// against a real one. `sync join` deliberately does not call this — the delta
// is where an operator meets the duplicate first, and refusing to render it
// would hide the thing that needs fixing.
func refuseDuplicateNotebookIDs(scanned []recordedNotebook) error {
	return syncproto.BuildInventoryDelta(deltaLocalNotebooks(scanned), syncproto.InventoryResponse{}).Conflicts()
}

// notebookStampedElsewhere reports another recorded notebook already carrying
// this notebook id.
//
// It exists for the one verb that can CREATE a duplicate rather than merely
// meet one: `notebook pull` installs a server-supplied id into a root that
// carried none, and doing that while a second recorded root already holds it
// would mint the exact D8 state every other verb here refuses to act on.
func notebookStampedElsewhere(scanned []recordedNotebook, exclude string, id syncproto.NotebookID) (recordedNotebook, bool) {
	for _, entry := range scanned {
		if entry.Name == exclude {
			continue
		}
		if entry.ID() != "" && syncproto.NotebookID(entry.ID()) == id {
			return entry, true
		}
	}
	return recordedNotebook{}, false
}

// ---- server inventory --------------------------------------------------------

// serverInventory is one answered GET /sync/inventory: the decoded response
// plus the two JSON documents the transition printer's receipt is built from.
// The request is carried because a receipt built from the response alone could
// be forged by echoing it back; transition.NewServerReceipt checks the pair.
type serverInventory struct {
	Response    syncproto.InventoryResponse
	RequestJSON string
	ReplyJSON   string
}

// fetchServerInventory asks the server what it holds.
//
// Inventory is the one v3 surface carried in query parameters rather than a
// body (sync/pkg/server/registration.go's handleInventory reads
// protocol_version, idempotency_key and device_id off the URL), so the request
// is assembled here rather than handed to doJSON as a body.
func fetchServerInventory(ctx context.Context, client *deviceSessionHTTP, deviceID string) (serverInventory, error) {
	req := syncproto.InventoryRequest{RequestIdentity: syncproto.RequestIdentity{
		ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
		IdempotencyKey:  idempotencyKey("inventory", deviceID),
		DeviceID:        deviceID,
	}}
	if wire := req.RequestIdentity.Validate(); wire != nil {
		return serverInventory{}, fmt.Errorf("invalid inventory request: %s", wire.Message)
	}
	query := url.Values{}
	query.Set("protocol_version", fmt.Sprintf("%d", req.ProtocolVersion))
	query.Set("idempotency_key", req.IdempotencyKey)
	query.Set("device_id", req.DeviceID)

	var response syncproto.InventoryResponse
	if err := client.doJSON(ctx, http.MethodGet, "/sync/inventory?"+query.Encode(), nil, &response); err != nil {
		return serverInventory{}, err
	}
	if response.Error != nil {
		return serverInventory{}, fmt.Errorf("inventory rejected: %s: %s", response.Error.Code, response.Error.Message)
	}
	requestJSON, err := json.Marshal(req)
	if err != nil {
		return serverInventory{}, err
	}
	replyJSON, err := json.Marshal(response)
	if err != nil {
		return serverInventory{}, err
	}
	return serverInventory{Response: response, RequestJSON: string(requestJSON), ReplyJSON: string(replyJSON)}, nil
}

// notebookByID indexes a server inventory by notebook id.
func (s serverInventory) notebookByID(id syncproto.NotebookID) (syncproto.InventoryNotebook, bool) {
	for _, nb := range s.Response.Notebooks {
		if nb.ID == id {
			return nb, true
		}
	}
	return syncproto.InventoryNotebook{}, false
}

// notebookByName resolves a display name, refusing ambiguity. Names do not
// route — that is what ids are for — so this exists only for the one verb that
// has nothing else to go on (`notebook pull` of a notebook this machine has
// never stamped), and it fails rather than pick.
func (s serverInventory) notebookByName(name string) (syncproto.InventoryNotebook, error) {
	var matches []syncproto.InventoryNotebook
	for _, nb := range s.Response.Notebooks {
		if nb.Name == name {
			matches = append(matches, nb)
		}
	}
	switch len(matches) {
	case 0:
		return syncproto.InventoryNotebook{}, fmt.Errorf("this server holds no notebook named %q", name)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, nb := range matches {
			ids = append(ids, nb.ID.String())
		}
		sort.Strings(ids)
		return syncproto.InventoryNotebook{}, fmt.Errorf("this server holds %d notebooks named %q (%s); a name does not route — pull it by the notebook this machine records",
			len(matches), name, strings.Join(ids, ", "))
	}
}

// notespaceByID finds a registered notespace in the inventory.
func (s serverInventory) notespaceByID(id syncproto.NotespaceID) (syncproto.InventoryNotespace, bool) {
	for _, ns := range s.Response.Notespaces {
		if ns.ID == id {
			return ns, true
		}
	}
	return syncproto.InventoryNotespace{}, false
}

// inventoryReceipt seals the inventory exchange as transition evidence.
func (s serverInventory) receipt(requestID string) (*transition.ServerReceipt, error) {
	return transition.NewServerReceipt(s.RequestJSON, s.ReplyJSON, requestID)
}

// refuseUnexplainedStatus turns a non-2xx the server did not explain into an
// error.
//
// doJSONStatus deliberately returns a nil error whenever the body decodes,
// because on these surfaces a typed ProtocolError IS the answer — a 409 naming
// the notebook a member already belongs to is evidence, not a transport
// failure. The gap that leaves is a non-2xx whose body decodes into a response
// carrying NO error at all: nothing in the v3 protocol produces one, but a
// proxy, a future server, or a route that changed shape can, and reading it as
// success would let a verb record a share the server never granted.
func refuseUnexplainedStatus(status int, wire *syncproto.ProtocolError, requestID string) error {
	if (status >= 200 && status < 300) || wire != nil {
		return nil
	}
	return fmt.Errorf("%s returned HTTP %d with a body that names no protocol error; refusing to read an unexplained refusal as success", requestID, status)
}

// ---- idempotency --------------------------------------------------------------

// idempotencyKey derives a stable key from the request's own content.
//
// The server replays a key it has already answered and REFUSES a key replayed
// with a different request digest, so a key must be stable across retries of
// the same request and different across different ones. Deriving it from the
// content gets both without a clock: a re-run of an unchanged share replays the
// first answer, and a share whose member list grew asks a new question.
func idempotencyKey(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%s-%x", prefix, sum[:8])
}
