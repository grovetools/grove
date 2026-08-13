package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/pkg/transition"
)

// `grove notespace move <ns> --to <notebook>` — sharing is transferring
// (P3 W3.4).
//
// This is the ONE mechanism. There is no "share this notespace" verb, because
// the notebook is the only sync knob: a notespace becomes shared by moving into
// a shared notebook and stops being shared by moving out of one. The three
// cases fall out of that single act:
//
//   - into a shared notebook: the notespace is registered and attached, or —
//     when the server already holds it under another notebook — re-parented;
//   - between shared notebooks: a server re-parent, same id, so documents,
//     events and every cursor keyed on that id are untouched. The response
//     carries the event-log head, which this verb prints rather than claiming
//     "history preserved" on its own authority;
//   - out of a shared notebook: a re-parent to NO notebook — the protocol's
//     detach. It is forward-only (D9): the server retains the notespace and its
//     full history, copies pulled elsewhere are not retracted, and nothing is
//     deleted anywhere. What it does end is MEMBERSHIP, and that is the point.
//     A local-only move-out would leave the notespace in the notebook's
//     server-side roll, so every join delta would keep offering to pull it back
//     onto the machine that deliberately moved it out.
//
// The id is preserved in all three, which is also why [primaries] needs no
// edit: it is keyed by notespace id, so primariness survives a move by
// construction. The local move is reversible — the inverse invocation is
// printed with the evidence.
//
// Failure has two halves, and they are not symmetric. Before the server has
// accepted anything, a failure rolls the local move back. AFTER it has, nothing
// is rolled back: undoing the tree while the server holds the new membership
// would produce the same disagreement in the other direction, so the error
// names what stands and what to repair instead.

func init() {
	rootCmd.AddCommand(newNotespaceCmd())
}

func newNotespaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notespace",
		Short: "Notespace-grained operations (new, primary, list, move)",
		Long: `Operate on a single notespace.

  new <subject> --in <nb>     create an additional notespace for a subject that
                              already has a primary — new id, [primaries] untouched
  primary <ns>                record which notespace this machine writes into
                              for that notespace's subject
  list                        list notespaces grouped by subject, primary first
  move <ns> --to <notebook>   move a notespace into another recorded notebook,
                              preserving its immutable id

The primary is this machine's default target notespace for a subject — a
machine-local routing pointer recorded in config, never a synced property of the
notespace. Additional notespaces for one subject are siblings; they are explicit
only, and creating one never changes where unqualified writes land.

Moving is how sharing changes: the notebook is the only sync knob, so a
notespace is shared because the notebook containing it is shared. Moving out of
a shared notebook stops sync forward-only (D9) — the server retains everything
and copies pulled elsewhere are not retracted.`,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newNotespaceNewCmd())
	cmd.AddCommand(newNotespacePrimaryCmd())
	cmd.AddCommand(newNotespaceListCmd())
	cmd.AddCommand(newNotespaceMoveCmd())
	return cmd
}

func newNotespaceMoveCmd() *cobra.Command {
	var (
		to              string
		asJSON          bool
		expectedVersion int64
	)
	cmd := &cobra.Command{
		Use:   "move <notespace> --to <notebook>",
		Short: "Move a notespace into another recorded notebook, id preserved",
		Long: `Move one notespace's directory into another recorded notebook.

<notespace> is matched against stamped ids first, then stamped display names,
then directory names, across every recorded notebook. An ambiguous match is
refused rather than resolved by sort order.

Both notebooks must be recorded and both roots must exist. The destination must
not already hold a directory of that name.

What moves and what does not:

  · the subtree moves, and its .notespace.toml id moves with it unchanged. The
    id is re-read after the move and a change is a failure, not a warning;
  · machine.toml [primaries] is NOT edited — it is keyed by notespace id, so
    primariness survives the move by construction. A [subjects] entry keyed by
    the old path is re-keyed, because that one records a location;
  · into a shared notebook, the server attaches or re-parents the notespace.
    A re-parent preserves the stream: same id, same documents, same cursors, and
    the response's event-log head is printed as the evidence for that;
  · out of a shared notebook, the server DETACHES it — a re-parent to no
    notebook. Membership ends so a join delta stops offering it back; the
    notespace and its whole history are retained, forward-only per D9, and the
    server's own retention sentence is printed. Nothing is deleted anywhere.

A move out of a notebook this machine records as shared therefore needs the
server: it is refused rather than performed half-way when the server cannot be
reached.

If the server refuses, the local move is rolled back. If the server ACCEPTS and
a later local step fails, nothing is rolled back — the error says what stands.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(to) == "" {
				return fmt.Errorf("--to <notebook> is required; a move names its destination")
			}
			opts := notespaceMoveOptions{
				notespace: args[0],
				to:        strings.TrimSpace(to),
				asJSON:    asJSON,
			}
			// Pinned only when the operator actually passed the flag. A
			// sentinel value would make 0 — the legitimate membership version
			// of an unparented notespace — indistinguishable from "unset", and
			// the zero value of this struct would silently mean "pin 0".
			if cmd.Flags().Changed("expected-membership-version") {
				opts.expectedVersion = &expectedVersion
			}
			return runNotespaceMove(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Destination notebook name (recorded in notebooks.toml)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the transition evidence as JSON")
	cmd.Flags().Int64Var(&expectedVersion, "expected-membership-version", 0, "Server membership version this decision was made against (unset: discover it from the server)")
	return cmd
}

type notespaceMoveOptions struct {
	notespace string
	to        string
	asJSON    bool
	// expectedVersion is nil when the caller did not pin one, which is how the
	// verb knows to discover it from the server's own precondition refusal.
	expectedVersion *int64
}

func runNotespaceMove(ctx context.Context, out io.Writer, opts notespaceMoveOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	table, scanned, err := loadRecordedNotebooks()
	if err != nil {
		return err
	}
	if err := refuseDuplicateNotebookIDs(scanned); err != nil {
		return err
	}
	source, sourceNotebook, err := locateRecordedNotespace(scanned, opts.notespace)
	if err != nil {
		return err
	}
	if source.Stamp == nil {
		return fmt.Errorf("%s carries no %s; a move preserves an immutable id and this notespace has none — `grove notebook share %s` mints one",
			source.Root, notespace.NotespaceStampName, sourceNotebook.Name)
	}
	destination, err := findRecordedNotebook(table, scanned, opts.to)
	if err != nil {
		return err
	}
	if destination.Name == sourceNotebook.Name {
		return fmt.Errorf("notespace %s is already in notebook %q", source.Stamp.ID, destination.Name)
	}
	if !destination.Exists {
		return fmt.Errorf("destination notebook %q records root %s, which does not exist; a move never creates a notebook root — create it, or fix [notebooks.%s].root in %s",
			destination.Name, destination.Root, destination.Name, displayRecordedPath(table.NotebooksFilePath, coderoot.NotebooksFileName))
	}
	target := filepath.Join(destination.Root, notespaceContainerDir, source.Dir)
	if _, statErr := os.Lstat(target); statErr == nil {
		return fmt.Errorf("%s already exists; move refuses to merge two notespaces into one path", target)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	// The server plan is decided BEFORE anything moves, from the server's own
	// inventory rather than from what this machine believes the server thinks.
	//
	// The inventory is read whenever EITHER notebook is recorded as shared,
	// because both directions have a server half: moving in attaches, and
	// moving out detaches. Reading it only for the destination is what let a
	// move-out change nothing on the server and stay on offer in every
	// subsequent join delta.
	plan := serverMovePlan{sourceShared: sourceNotebook.Shared, destinationShared: destination.Shared}
	var client *deviceSessionHTTP
	var deviceID string
	var inventory serverInventory
	if plan.destinationShared || plan.sourceShared {
		if plan.destinationShared && destination.ID() == "" {
			return fmt.Errorf("notebook %q is recorded as shared but its root carries no %s; `grove notebook share %s` registers it before anything can move into it",
				destination.Name, notespace.NotebookStampName, destination.Name)
		}
		if plan.sourceShared && sourceNotebook.ID() == "" {
			return fmt.Errorf("notebook %q is recorded as shared but its root carries no %s, so this machine cannot say what the server holds for it; run `grove notebook share %s`, or record `share = false` under [notebooks.%s.sync] if it is not shared after all",
				sourceNotebook.Name, notespace.NotebookStampName, sourceNotebook.Name, sourceNotebook.Name)
		}
		client, err = loadDeviceSessionHTTP(ctx)
		if err != nil {
			return fmt.Errorf("%w\n  %s", err, moveNeedsServerBecause(plan, sourceNotebook.Name, destination.Name))
		}
		key, keyErr := devicekey.Load()
		if keyErr != nil {
			return keyErr
		}
		deviceID = key.DeviceID()
		inventory, err = fetchServerInventory(ctx, client, deviceID)
		if err != nil {
			return fmt.Errorf("%w\n  %s", err, moveNeedsServerBecause(plan, sourceNotebook.Name, destination.Name))
		}
	}
	registered, isRegistered := inventory.notespaceByID(syncproto.NotespaceID(source.Stamp.ID))
	switch {
	case plan.destinationShared:
		serverDestination, ok := inventory.notebookByID(syncproto.NotebookID(destination.ID()))
		if !ok {
			return fmt.Errorf("notebook %q (%s) is recorded as shared here but this server does not hold it; run `grove notebook share %s` first",
				destination.Name, destination.ID(), destination.Name)
		}
		if serverDestination.ShareState == syncproto.NotebookShareStateUnshared {
			return fmt.Errorf("destination notebook %s is unshared on this server; re-share it before moving into it (%s)",
				serverDestination.ID, syncproto.UnshareRetentionStatement)
		}
		switch {
		case !isRegistered:
			plan.action = movePlanRegisterAndAttach
		case registered.NotebookID == syncproto.NotebookID(destination.ID()):
			plan.action = movePlanAlreadyMember
		case registered.NotebookID == "":
			plan.action = movePlanAttach
		default:
			plan.action = movePlanReparent
			plan.from = registered.NotebookID
		}
		plan.to = syncproto.NotebookID(destination.ID())
		// The attach leg re-states the destination notebook's share, so it
		// carries the version it saw — the same staleness guard share uses.
		plan.toVersion = serverDestination.Version

	case plan.sourceShared:
		// Out of a shared notebook and into one that is not: the server half is
		// a detach, and `plan.to` stays empty because "no notebook" is the
		// destination. Which notebook it leaves is read from the server, and a
		// server that parents it somewhere else is a disagreement this verb
		// refuses rather than resolves — detaching it from a notebook the
		// operator did not name would withdraw a membership they never asked
		// about.
		switch {
		case !isRegistered:
			plan.action = movePlanNothingToDetach
			plan.detachNote = "this server does not hold notespace " + source.Stamp.ID + ", so there is no membership to withdraw"
		case registered.NotebookID == "":
			plan.action = movePlanNothingToDetach
			plan.detachNote = "this server already holds notespace " + source.Stamp.ID + " outside every notebook"
		case registered.NotebookID == syncproto.NotebookID(sourceNotebook.ID()):
			plan.action = movePlanDetach
			plan.from = registered.NotebookID
		default:
			return fmt.Errorf("this machine is moving notespace %s out of notebook %q (%s), but the server holds it in notebook %s; re-run `grove sync join` to see the delta and move it out of the notebook the server actually records",
				source.Stamp.ID, sourceNotebook.Name, sourceNotebook.ID(), registered.NotebookID)
		}
	}

	// The old path has to be canonicalized while it still exists: once the tree
	// has moved, EvalSymlinks cannot resolve it, and a [subjects] key recorded
	// through a symlinked home would no longer match.
	sourceCanonical := canonicalPath(source.Root)

	staged, err := stageNotespaceMove(source.Root, target)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		if undoErr := staged.undo(); undoErr != nil {
			return fmt.Errorf("%w; AND the local move could not be rolled back (%v) — %s and %s both need inspection", cause, undoErr, source.Root, target)
		}
		return fmt.Errorf("%w; the local move was rolled back, %s is unchanged", cause, source.Root)
	}

	// The id survives the move or the move is a failure. A stamp is the only
	// durable identity in this system; a move that silently re-keyed one would
	// detach a notespace from its entire history.
	moved, err := notespace.LoadNotespace(target)
	if err != nil {
		return rollback(err)
	}
	if moved == nil || moved.ID != source.Stamp.ID {
		return rollback(fmt.Errorf("the moved tree at %s does not carry id %s", target, source.Stamp.ID))
	}

	var receipt *transition.ServerReceipt
	var cursor int64
	var retention string
	serverAction := "none (neither notebook is recorded as shared)"
	if plan.action != "" {
		result, serverErr := applyServerMove(ctx, client, deviceID, plan, source, destination, opts.expectedVersion)
		if serverErr != nil {
			// A server that never changed anything leaves the local move free to
			// undo. One that DID is past the point where undoing helps.
			if result.applied {
				return serverAppliedFailure(serverErr, result.action, target)
			}
			return rollback(serverErr)
		}
		receipt, cursor, retention, serverAction = result.receipt, result.cursor, result.retention, result.action
	}

	// Past this line nothing is rolled back. The destination tree is complete
	// and the server (when there was one) has agreed to it, so every remaining
	// failure is a repair note rather than a reason to undo a move both sides
	// already hold.
	if err := staged.commit(); err != nil {
		return fmt.Errorf("the move completed — %s holds notespace %s%s — but the source copy at %s could not be removed: %w; delete it by hand, nothing else is outstanding",
			target, source.Stamp.ID, serverStandsClause(receipt != nil, serverAction), source.Root, err)
	}
	if err := recordMovedSubjectPath(sourceCanonical, target); err != nil {
		return fmt.Errorf("the move completed — %s holds notespace %s%s — but machine.toml still records the old location under [subjects]: %w; re-key %q to %q by hand (`grove doctor` reports it)",
			target, source.Stamp.ID, serverStandsClause(receipt != nil, serverAction), err, sourceCanonical, canonicalPath(target))
	}
	config.ResetLoadCache()

	fmt.Fprintf(out, "  moved        %s\n", source.Stamp.ID)
	fmt.Fprintf(out, "    from       %s  [notebook %s]\n", source.Root, sourceNotebook.Name)
	fmt.Fprintf(out, "    to         %s  [notebook %s]\n", target, destination.Name)
	fmt.Fprintf(out, "  server       %s\n", serverAction)
	if receipt != nil && (plan.action == movePlanReparent || plan.action == movePlanDetach) {
		fmt.Fprintf(out, "  cursor       %d — the move did not touch the stream; documents, events and cursors are keyed on the id, which did not change\n", cursor)
	}
	switch plan.action {
	case movePlanDetach:
		fmt.Fprintf(out, "\n  This notespace has left a shared notebook, here AND on the server. %s\n", retention)
		fmt.Fprintf(out, "  It is no longer a member of %q there, so a join delta will not offer it back; nothing was deleted anywhere.\n", sourceNotebook.Name)
	case movePlanNothingToDetach:
		fmt.Fprintf(out, "\n  This notespace has left a notebook this machine records as shared. %s\n", syncproto.DetachRetentionStatement)
		fmt.Fprintf(out, "  No membership was withdrawn because %s.\n", plan.detachNote)
	}
	fmt.Fprintf(out, "\n  Reversible: grove notespace move %s --to %s\n", source.Stamp.ID, sourceNotebook.Name)

	evidence := transition.Evidence{
		Action: "notespace move",
		Counts: []transition.Count{{Name: "notespaces-moved", Value: 1}},
		ResolvedRoots: []transition.ResolvedRoot{
			{Name: "from/" + sourceNotebook.Name, Declared: sourceNotebook.Declared, Resolved: source.Root},
			{Name: "to/" + destination.Name, Declared: destination.Declared, Resolved: target},
		},
		ServerReceipt: receipt,
	}
	switch {
	case receipt != nil:
		// The receipt is the evidence; no reason is owed.
	case plan.action == "":
		evidence.Reason = transition.Reason("local move only: neither notebook is recorded as shared, so no server holds a membership to change")
	case plan.action == movePlanAlreadyMember:
		evidence.Reason = transition.Reason("the server already held this notespace in the destination notebook; only the local tree moved")
	case plan.action == movePlanNothingToDetach:
		evidence.Reason = transition.Reason("no membership was withdrawn: " + plan.detachNote)
	}
	fmt.Fprintln(out)
	if opts.asJSON {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

// ---- resolution -----------------------------------------------------------------

// locateRecordedNotespace matches a notespace across every recorded notebook:
// immutable id first, then stamped display name, then directory name. Each rung
// is tried in full before the next, so an id never loses to a name, and an
// ambiguous rung is a refusal rather than a choice.
func locateRecordedNotespace(scanned []recordedNotebook, want string) (recordedNotespace, recordedNotebook, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return recordedNotespace{}, recordedNotebook{}, fmt.Errorf("a notespace id, name or directory is required")
	}
	rungs := []struct {
		label string
		match func(recordedNotespace) bool
	}{
		{"id", func(ns recordedNotespace) bool { return ns.ID() == want }},
		{"name", func(ns recordedNotespace) bool { return ns.Stamp != nil && ns.Stamp.Name == want }},
		{"directory", func(ns recordedNotespace) bool { return ns.Dir == want }},
	}
	for _, rung := range rungs {
		var hits []recordedNotespace
		var owners []recordedNotebook
		for _, entry := range scanned {
			for _, ns := range entry.Notespaces {
				if rung.match(ns) {
					hits = append(hits, ns)
					owners = append(owners, entry)
				}
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], owners[0], nil
		default:
			var where []string
			for i := range hits {
				where = append(where, owners[i].Name+"/"+hits[i].Dir)
			}
			return recordedNotespace{}, recordedNotebook{}, fmt.Errorf("%q matches %d notespaces by %s (%s); name it by its immutable id",
				want, len(hits), rung.label, strings.Join(where, ", "))
		}
	}
	return recordedNotespace{}, recordedNotebook{}, fmt.Errorf("no recorded notebook contains a notespace matching %q by id, name or directory", want)
}

// ---- the server half --------------------------------------------------------------

const (
	movePlanRegisterAndAttach = "register-and-attach"
	movePlanAttach            = "attach"
	movePlanReparent          = "reparent"
	movePlanAlreadyMember     = "already-member"
	// movePlanDetach is the out-of-shared leg: a re-parent whose destination is
	// no notebook. movePlanNothingToDetach is the same leg when the server
	// holds no membership to withdraw — reported rather than silently skipped,
	// because "the server was asked and had nothing" and "the server was never
	// asked" are the two states this correction exists to distinguish.
	movePlanDetach          = "detach"
	movePlanNothingToDetach = "nothing-to-detach"
)

type serverMovePlan struct {
	sourceShared      bool
	destinationShared bool
	action            string
	from              syncproto.NotebookID
	to                syncproto.NotebookID
	toVersion         int64
	// detachNote explains a movePlanNothingToDetach in the server's terms.
	detachNote string
}

// detaching reports whether the planned server step takes the notespace out of
// every notebook rather than into one.
func (p serverMovePlan) detaching() bool { return p.action == movePlanDetach }

type serverMoveResult struct {
	receipt   *transition.ServerReceipt
	cursor    int64
	action    string
	retention string
	// applied is true once the server has CHANGED something. It is the flag the
	// caller reads to decide whether undoing the local move is still honest:
	// after an applied change it is not, and a failure past this point is
	// reported rather than reversed.
	applied bool
}

// moveNeedsServerBecause explains why a move that could not reach the server is
// refused rather than performed locally, in the terms of the leg that needed it.
func moveNeedsServerBecause(plan serverMovePlan, sourceName, destinationName string) string {
	if plan.destinationShared {
		return "notebook " + strconv.Quote(destinationName) + " is recorded as shared, so this move has to register the notespace there before the tree moves"
	}
	return "notebook " + strconv.Quote(sourceName) + " is recorded as shared, so this move has to withdraw the notespace's membership there; moving it locally alone would leave the server offering it back on every join"
}

// serverStandsClause names the accepted server change inside a later failure,
// so an operator reading the error knows which half of the move is done.
func serverStandsClause(applied bool, action string) string {
	if !applied {
		return ""
	}
	return ", and the server change stands (" + action + ")"
}

// serverAppliedFailure reports a failure that arrived AFTER the server accepted
// the membership change. Nothing is undone: rolling the tree back now would
// leave this machine and the server disagreeing in the other direction, which
// is the state the staged move exists to prevent, not to trade for.
func serverAppliedFailure(cause error, action, target string) error {
	return fmt.Errorf("%w; the server change was already applied (%s) and the tree now lives at %s, so nothing was rolled back — reconcile from `grove sync join` rather than by moving the directory back blindly",
		cause, action, target)
}

func applyServerMove(ctx context.Context, client *deviceSessionHTTP, deviceID string, plan serverMovePlan, source recordedNotespace, destination recordedNotebook, expectedVersion *int64) (serverMoveResult, error) {
	switch plan.action {
	case movePlanAlreadyMember:
		return serverMoveResult{action: "already a member of " + plan.to.String() + " (no change requested)"}, nil

	case movePlanNothingToDetach:
		return serverMoveResult{action: "none: " + plan.detachNote}, nil

	case movePlanRegisterAndAttach, movePlanAttach:
		registeredHere := false
		if plan.action == movePlanRegisterAndAttach {
			if err := registerNotespaceStamp(ctx, client, deviceID, *source.Stamp); err != nil {
				return serverMoveResult{}, err
			}
			registeredHere = true
		}
		// A registration that lands before a refused attach is not a membership
		// and not something to undo — the id is claimed, unparented, and a
		// retry reuses it. Saying so keeps the rollback message from reading as
		// "the server is untouched" when one harmless thing on it is not.
		attachFailed := func(cause error) (serverMoveResult, error) {
			if !registeredHere {
				return serverMoveResult{}, cause
			}
			return serverMoveResult{}, fmt.Errorf("%w (notespace %s is now registered on this server but belongs to no notebook; a retry of this move reuses that registration)",
				cause, source.Stamp.ID)
		}
		req := syncproto.NotebookShareRequest{
			RequestIdentity: syncproto.RequestIdentity{
				ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
				IdempotencyKey:  idempotencyKey("notespace-move-attach", plan.to.String(), source.Stamp.ID, fmt.Sprint(plan.toVersion)),
				DeviceID:        deviceID,
			},
			NotebookID:      plan.to,
			Name:            destination.Name,
			ExpectedVersion: plan.toVersion,
			Members:         []syncproto.NotespaceID{syncproto.NotespaceID(source.Stamp.ID)},
		}
		if wire := req.Validate(); wire != nil {
			return attachFailed(fmt.Errorf("invalid attach request: %s", wire.Message))
		}
		requestJSON, err := json.Marshal(req)
		if err != nil {
			return attachFailed(err)
		}
		var response syncproto.NotebookShareResponse
		status, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/notebooks/share", req, &response)
		if err != nil {
			return attachFailed(err)
		}
		if response.Error != nil {
			return attachFailed(fmt.Errorf("attach rejected: %s: %s", response.Error.Code, response.Error.Message))
		}
		if err := refuseUnexplainedStatus(status, response.Error, "POST /sync/notebooks/share"); err != nil {
			return attachFailed(err)
		}
		replyJSON, err := json.Marshal(response)
		if err != nil {
			return serverMoveResult{applied: true}, err
		}
		receipt, err := transition.NewServerReceipt(string(requestJSON), string(replyJSON), "POST /sync/notebooks/share")
		if err != nil {
			return serverMoveResult{applied: true}, err
		}
		return serverMoveResult{receipt: receipt, applied: true, action: "attached to notebook " + plan.to.String() + " at version " + fmt.Sprint(response.Version)}, nil

	case movePlanReparent, movePlanDetach:
		return reparentNotespace(ctx, client, deviceID, plan, source, expectedVersion)
	}
	return serverMoveResult{}, fmt.Errorf("no server action was planned for this move")
}

// registerNotespaceStamp gives the server a notespace's identity before its
// membership is moved.
//
// The intent is the same per-notespace decision `notebook share` makes: a
// notespace that is not this machine's recorded primary for its subject is a
// SIBLING, and asking the server to reconcile it against a subject that already
// names another notespace is refused — which, before P4, could not happen, and
// after P4 meant a sibling could not be moved into a shared notebook at all.
func registerNotespaceStamp(ctx context.Context, client *deviceSessionHTTP, deviceID string, stamp notespace.NotespaceStamp) error {
	intent := registrationIntentFor(stamp, recordedPrimaries())
	req := syncproto.RegisterRequest{
		RequestIdentity: syncproto.RequestIdentity{
			ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
			IdempotencyKey:  idempotencyKey("notespace-move-register", stamp.ID, stamp.Subject, stamp.Name, stamp.Kind, intent),
			DeviceID:        deviceID,
		},
		Intent:              intent,
		Subject:             stamp.Subject,
		NotespaceName:       syncproto.NotespaceName(stamp.Name),
		Kind:                stamp.Kind,
		ProposedNotespaceID: syncproto.NotespaceID(stamp.ID),
	}
	if wire := req.Validate(); wire != nil {
		return fmt.Errorf("notespace %s cannot be registered: %s", stamp.ID, wire.Message)
	}
	var response syncproto.RegisterResponse
	status, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/register", req, &response)
	if err != nil && response.Error == nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("register %s rejected: %s: %s", stamp.ID, response.Error.Code, response.Error.Message)
	}
	if err := refuseUnexplainedStatus(status, response.Error, "POST /sync/register"); err != nil {
		return err
	}
	if response.NotespaceID.String() != stamp.ID {
		return fmt.Errorf("register %s: the server bound id %s instead", stamp.ID, response.NotespaceID)
	}
	return nil
}

// reparentNotespace performs the membership move, discovering the expected
// version when the caller did not pin one.
//
// One request shape serves both legs: a destination notebook re-parents, and an
// EMPTY destination detaches — the notespace leaves every notebook, which is
// how a move out of a shared notebook stops being local-only. The server treats
// the two identically apart from the retention sentence it returns for a
// detach, and this function passes that sentence through rather than composing
// one of its own.
//
// The inventory does not carry a notespace's membership version, so a first
// attempt cannot know it. Rather than invent one, this asks with the version it
// has and lets the server correct it: a stale-resolution refusal carries the
// CURRENT version, and exactly one retry uses it. That keeps the optimistic
// check the server built (a genuinely concurrent third change makes the retry
// fail too) instead of routing around it.
func reparentNotespace(ctx context.Context, client *deviceSessionHTTP, deviceID string, plan serverMovePlan, source recordedNotespace, expectedVersion *int64) (serverMoveResult, error) {
	attempt := func(version int64) (syncproto.NotespaceReparentRequest, syncproto.NotespaceReparentResponse, error) {
		req := syncproto.NotespaceReparentRequest{
			RequestIdentity: syncproto.RequestIdentity{
				ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
				IdempotencyKey:  idempotencyKey("notespace-reparent", source.Stamp.ID, plan.from.String(), plan.to.String(), fmt.Sprint(version)),
				DeviceID:        deviceID,
			},
			NotespaceID:     syncproto.NotespaceID(source.Stamp.ID),
			FromNotebookID:  plan.from,
			ToNotebookID:    plan.to,
			ExpectedVersion: version,
		}
		if wire := req.Validate(); wire != nil {
			return req, syncproto.NotespaceReparentResponse{}, fmt.Errorf("invalid re-parent request: %s", wire.Message)
		}
		var response syncproto.NotespaceReparentResponse
		// The precondition refusal is the interesting answer here, so it is
		// decoded rather than collapsed into an HTTP status: it carries the
		// membership version the server actually holds.
		status, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/notespaces/reparent", req, &response)
		if response.Error != nil {
			err = nil
		} else if err == nil {
			err = refuseUnexplainedStatus(status, response.Error, "POST /sync/notespaces/reparent")
		}
		return req, response, err
	}

	var version int64
	pinned := expectedVersion != nil
	if pinned {
		version = *expectedVersion
	}
	discovered := false
	req, response, err := attempt(version)
	// A refused precondition answers with the version the server actually
	// holds; that is the one fact the inventory could not supply. A pinned
	// version is never second-guessed — that is what pinning is for.
	if response.Error != nil && response.Error.Code == syncproto.ErrorStaleResolution &&
		!pinned && response.Error.CurrentVersion != version {
		version = response.Error.CurrentVersion
		discovered = true
		req, response, err = attempt(version)
	}
	if err != nil {
		return serverMoveResult{}, err
	}
	if response.Error != nil {
		verb := "re-parent"
		if plan.detaching() {
			verb = "detach"
		}
		return serverMoveResult{}, fmt.Errorf("%s rejected: %s: %s", verb, response.Error.Code, response.Error.Message)
	}

	// From here the server has moved membership. Every remaining check reports
	// on an applied change rather than gating one, so each failure is marked
	// applied and the caller stops treating the local move as undoable.
	applied := serverMoveResult{applied: true, cursor: response.Cursor}
	if !response.HistoryPreserved || response.NotespaceID.String() != source.Stamp.ID {
		return applied, fmt.Errorf("the server answered a membership move this one did not ask for: %+v", response)
	}
	if plan.detaching() && (!response.Detached || response.ToNotebookID != "") {
		return applied, fmt.Errorf("this move asked the server to detach %s, and the server answered with a membership of %q instead: %+v",
			source.Stamp.ID, response.ToNotebookID, response)
	}
	retention := strings.TrimSpace(response.Retention)
	if plan.detaching() && retention == "" {
		// The retention sentence is the server's to make, so a server that
		// sends none gets the protocol's — labelled, never passed off as the
		// server's own words.
		retention = syncproto.DetachRetentionStatement + " (this server sent no retention statement; that is the protocol's)"
	}
	requestJSON, err := json.Marshal(req)
	if err != nil {
		return applied, err
	}
	replyJSON, err := json.Marshal(response)
	if err != nil {
		return applied, err
	}
	receipt, err := transition.NewServerReceipt(string(requestJSON), string(replyJSON), "POST /sync/notespaces/reparent")
	if err != nil {
		return applied, err
	}
	action := fmt.Sprintf("re-parented %s → %s at membership version %d", plan.from, plan.to, response.Version)
	if plan.detaching() {
		action = fmt.Sprintf("detached from notebook %s at membership version %d; the notespace now belongs to no notebook on this server", plan.from, response.Version)
	}
	if discovered {
		action += " (version read back from the server's own precondition refusal)"
	}
	applied.receipt, applied.action, applied.retention = receipt, action, retention
	return applied, nil
}

// ---- the local half ---------------------------------------------------------------

// recordMovedSubjectPath re-keys a [subjects] entry that recorded the old
// location. [primaries] is deliberately untouched: it is keyed by notespace id,
// which a move preserves, so primariness survives without an edit — and an edit
// here would be the one that could break it.
func recordMovedSubjectPath(oldKey, target string) error {
	machineCfg, err := config.LoadMachineConfig()
	if err != nil {
		return fmt.Errorf("read machine.toml to re-key [subjects]: %w", err)
	}
	if machineCfg == nil {
		// No machine.toml at all: there is no recorded path to re-key, which is
		// a legitimate state rather than a missing step.
		return nil
	}
	value, recorded := machineCfg.Subjects[oldKey]
	if !recorded {
		return nil
	}
	newKey := canonicalPath(target)
	if newKey == oldKey {
		return nil
	}
	_, _, err = config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{}, func(machine *config.MachineConfig) error {
		if machine.Subjects == nil {
			return nil
		}
		delete(machine.Subjects, oldKey)
		machine.Subjects[newKey] = value
		return nil
	})
	if err != nil {
		return fmt.Errorf("re-key [subjects] %s → %s: %w", oldKey, newKey, err)
	}
	return nil
}

// stagedMove is a move that can still be undone.
//
// A rename is atomic and its inverse is another rename. Across filesystems
// there is no rename, so the tree is COPIED and the source is left in place
// until commit: that keeps the undo trivial (delete the copy) through the whole
// window where a server call can still fail, instead of leaving a half-moved
// notespace as the recovery story.
type stagedMove struct {
	from   string
	to     string
	copied bool
}

func stageNotespaceMove(from, to string) (*stagedMove, error) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(to), err)
	}
	err := os.Rename(from, to)
	if err == nil {
		return &stagedMove{from: from, to: to}, nil
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
		return nil, fmt.Errorf("move %s to %s: %w", from, to, err)
	}
	if copyErr := copyTree(from, to); copyErr != nil {
		_ = os.RemoveAll(to)
		return nil, fmt.Errorf("copy %s to %s across filesystems: %w", from, to, copyErr)
	}
	return &stagedMove{from: from, to: to, copied: true}, nil
}

func (m *stagedMove) undo() error {
	if m.copied {
		return os.RemoveAll(m.to)
	}
	return os.Rename(m.to, m.from)
}

func (m *stagedMove) commit() error {
	if !m.copied {
		return nil
	}
	return os.RemoveAll(m.from)
}

// copyTree copies a notespace subtree across filesystems, preserving
// directories, regular files (with their mode and modification time) and
// symlinks. Anything else is refused by name rather than skipped: a device node
// or socket inside a notespace is a surprise the operator should hear about,
// not something a move silently drops.
//
// Modification times are preserved because a move is not an edit. Every
// document in the tree arriving with a fresh mtime would tell the daemon's
// watcher that the whole notespace had just been rewritten, and a move whose
// only visible difference from an edit is its intent is one the rest of the
// system cannot read correctly.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(to, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(destination, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			return os.Symlink(link, destination)
		case info.Mode().IsRegular():
			if err := copyRegularFile(path, destination, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(destination, info.ModTime(), info.ModTime())
		default:
			return fmt.Errorf("%s is neither a regular file, a directory nor a symlink (%s); move it by hand", path, info.Mode())
		}
	})
}

func copyRegularFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
