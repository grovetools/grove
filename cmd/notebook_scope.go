package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/pkg/transition"
	"github.com/grovetools/grove/pkg/notescope"
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

// The scan and the pure comparison it feeds now live in grove/pkg/notescope,
// because the P3 config TUI reads exactly the same three facts and a second
// implementation of "what is recorded here" would be a second thing to keep
// true. The verbs keep their own vocabulary through these aliases: nothing
// below this line changed shape, only where it is defined.
type (
	recordedNotespace = notescope.Notespace
	recordedNotebook  = notescope.Notebook
)

// notespaceContainerDir is the notebook-relative directory notespaces live in.
const notespaceContainerDir = notescope.ContainerDir

func loadRecordedNotebooks() (coderoot.Table, []recordedNotebook, error) { return notescope.Load() }

func scanRecordedNotebooks(table coderoot.Table) ([]recordedNotebook, error) {
	return notescope.Scan(table)
}

func scanContainedNotespaces(notebookRoot string) ([]recordedNotespace, error) {
	return notescope.ScanContained(notebookRoot)
}

func findRecordedNotebook(table coderoot.Table, scanned []recordedNotebook, name string) (recordedNotebook, error) {
	return notescope.Find(table, scanned, name)
}

func displayRecordedPath(path, fallback string) string {
	return notescope.DisplayRecordedPath(path, fallback)
}

func deltaLocalNotebooks(scanned []recordedNotebook) []syncproto.LocalNotebook {
	return notescope.LocalNotebooks(scanned)
}

func refuseDuplicateNotebookIDs(scanned []recordedNotebook) error {
	return notescope.RefuseDuplicateNotebookIDs(scanned)
}

func notebookStampedElsewhere(scanned []recordedNotebook, exclude string, id syncproto.NotebookID) (recordedNotebook, bool) {
	return notescope.StampedElsewhere(scanned, exclude, id)
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

// ---- registration intent ------------------------------------------------------

// registrationIntentFor says which registration a stamped notespace is asking
// the server for.
//
// The two intents answer different questions, and the server enforces the
// difference: `reconcile` means "this notespace IS the subject's", so the
// server refuses it when its subject claim already names a different one
// (`registration_conflict: subject already has a different inherited
// notespace`). `create-sibling` means "this is one more notespace for a subject
// that already has one", which is exactly what P4 (W4.1) made legal.
//
// Before P4 every notespace was its subject's only one, so registering
// everything as `reconcile` was right by construction. It stopped being right
// the moment `grove notespace new` could put a second notespace for one subject
// inside a notebook: `grove notebook share` registers every member and would
// fail on the sibling — and fail the WHOLE share, leaving the notebook's
// membership unwritten — and `grove notespace move` would refuse to move a
// sibling into a shared notebook at all.
//
// The test is this machine's recorded [primaries] pointer, which is the same
// test the daemon's own registrationIntent applies, so the two writers cannot
// disagree about what a given notespace is: the recorded primary reconciles the
// subject, and anything else is a sibling of it. A machine with nothing
// recorded reconciles, which is the pre-P4 answer for a pre-P4 machine.
func registrationIntentFor(stamp notespace.NotespaceStamp, primaries map[string]string) string {
	if primary := primaries[stamp.Subject]; primary != "" && primary != stamp.ID {
		return syncproto.RegistrationIntentCreateSibling
	}
	return syncproto.RegistrationIntentReconcile
}

// recordedPrimaries reads machine.toml's [primaries] table for the intent
// decision above. It is best-effort on purpose: a machine whose machine.toml
// cannot be read still registers, with the pre-P4 answer, rather than being
// unable to share at all.
func recordedPrimaries() map[string]string {
	machineCfg, err := config.LoadMachineConfig()
	if err != nil || machineCfg == nil {
		return nil
	}
	return machineCfg.Primaries
}
