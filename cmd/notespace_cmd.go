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
//   - out of a shared notebook: forward-only (D9). The server retains the
//     notespace and its full history, copies pulled elsewhere are not
//     retracted, and nothing is deleted anywhere. This verb says that out loud
//     rather than implying a withdrawal it cannot perform.
//
// The id is preserved in all three, which is also why [primaries] needs no
// edit: it is keyed by notespace id, so primariness survives a move by
// construction. The local move is reversible — the inverse invocation is
// printed with the evidence — and a server step that fails rolls the local move
// back rather than leaving the tree and the server disagreeing.

func init() {
	rootCmd.AddCommand(newNotespaceCmd())
}

func newNotespaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notespace",
		Short: "Notespace-grained operations (move)",
		Long: `Operate on a single notespace.

  move <ns> --to <notebook>   move a notespace into another recorded notebook,
                              preserving its immutable id

Moving is how sharing changes: the notebook is the only sync knob, so a
notespace is shared because the notebook containing it is shared. Moving out of
a shared notebook stops sync forward-only (D9) — the server retains everything
and copies pulled elsewhere are not retracted.`,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
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
  · out of a shared notebook, nothing is withdrawn from the server. Unshare is
    forward-only.

If the server step fails, the local move is rolled back.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(to) == "" {
				return fmt.Errorf("--to <notebook> is required; a move names its destination")
			}
			return runNotespaceMove(cmd.Context(), cmd.OutOrStdout(), notespaceMoveOptions{
				notespace:       args[0],
				to:              strings.TrimSpace(to),
				asJSON:          asJSON,
				expectedVersion: expectedVersion,
			})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Destination notebook name (recorded in notebooks.toml)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the transition evidence as JSON")
	cmd.Flags().Int64Var(&expectedVersion, "expected-membership-version", -1, "Server membership version this decision was made against (default: discover it from the server)")
	return cmd
}

type notespaceMoveOptions struct {
	notespace       string
	to              string
	asJSON          bool
	expectedVersion int64
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
	plan := serverMovePlan{sourceShared: sourceNotebook.Shared, destinationShared: destination.Shared}
	var client *deviceSessionHTTP
	var deviceID string
	var inventory serverInventory
	if plan.destinationShared {
		if destination.ID() == "" {
			return fmt.Errorf("notebook %q is recorded as shared but its root carries no %s; `grove notebook share %s` registers it before anything can move into it",
				destination.Name, notespace.NotebookStampName, destination.Name)
		}
		client, err = loadDeviceSessionHTTP(ctx)
		if err != nil {
			return err
		}
		key, keyErr := devicekey.Load()
		if keyErr != nil {
			return keyErr
		}
		deviceID = key.DeviceID()
		inventory, err = fetchServerInventory(ctx, client, deviceID)
		if err != nil {
			return err
		}
		serverDestination, ok := inventory.notebookByID(syncproto.NotebookID(destination.ID()))
		if !ok {
			return fmt.Errorf("notebook %q (%s) is recorded as shared here but this server does not hold it; run `grove notebook share %s` first",
				destination.Name, destination.ID(), destination.Name)
		}
		if serverDestination.ShareState == syncproto.NotebookShareStateUnshared {
			return fmt.Errorf("destination notebook %s is unshared on this server; re-share it before moving into it (%s)",
				serverDestination.ID, syncproto.UnshareRetentionStatement)
		}
		registered, isRegistered := inventory.notespaceByID(syncproto.NotespaceID(source.Stamp.ID))
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
	serverAction := "none (the destination notebook is not shared)"
	if plan.destinationShared {
		result, serverErr := applyServerMove(ctx, client, deviceID, plan, source, destination, opts.expectedVersion)
		if serverErr != nil {
			return rollback(serverErr)
		}
		receipt, cursor, serverAction = result.receipt, result.cursor, result.action
	}

	if err := recordMovedSubjectPath(sourceCanonical, target); err != nil {
		return rollback(err)
	}
	if err := staged.commit(); err != nil {
		return rollback(err)
	}
	config.ResetLoadCache()

	fmt.Fprintf(out, "  moved        %s\n", source.Stamp.ID)
	fmt.Fprintf(out, "    from       %s  [notebook %s]\n", source.Root, sourceNotebook.Name)
	fmt.Fprintf(out, "    to         %s  [notebook %s]\n", target, destination.Name)
	fmt.Fprintf(out, "  server       %s\n", serverAction)
	if receipt != nil && plan.action == movePlanReparent {
		fmt.Fprintf(out, "  cursor       %d — the re-parent did not touch the stream; documents, events and cursors are keyed on the id, which did not change\n", cursor)
	}
	if plan.sourceShared && !plan.destinationShared {
		fmt.Fprintf(out, "\n  This notespace has left a shared notebook. %s\n", syncproto.UnshareRetentionStatement)
		fmt.Fprintf(out, "  Its server-side membership of %q is not withdrawn by this move; nothing was deleted anywhere.\n", sourceNotebook.Name)
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
	case !plan.destinationShared:
		evidence.Reason = transition.Reason("local move only: the destination notebook is not shared, and leaving a shared notebook is forward-only (D9)")
	case receipt == nil:
		evidence.Reason = transition.Reason("the server already held this notespace in the destination notebook; only the local tree moved")
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
)

type serverMovePlan struct {
	sourceShared      bool
	destinationShared bool
	action            string
	from              syncproto.NotebookID
	to                syncproto.NotebookID
	toVersion         int64
}

type serverMoveResult struct {
	receipt *transition.ServerReceipt
	cursor  int64
	action  string
}

func applyServerMove(ctx context.Context, client *deviceSessionHTTP, deviceID string, plan serverMovePlan, source recordedNotespace, destination recordedNotebook, expectedVersion int64) (serverMoveResult, error) {
	switch plan.action {
	case movePlanAlreadyMember:
		return serverMoveResult{action: "already a member of " + plan.to.String() + " (no change requested)"}, nil

	case movePlanRegisterAndAttach, movePlanAttach:
		if plan.action == movePlanRegisterAndAttach {
			if err := registerNotespaceStamp(ctx, client, deviceID, *source.Stamp); err != nil {
				return serverMoveResult{}, err
			}
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
			return serverMoveResult{}, fmt.Errorf("invalid attach request: %s", wire.Message)
		}
		requestJSON, err := json.Marshal(req)
		if err != nil {
			return serverMoveResult{}, err
		}
		var response syncproto.NotebookShareResponse
		if _, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/notebooks/share", req, &response); err != nil {
			return serverMoveResult{}, err
		}
		if response.Error != nil {
			return serverMoveResult{}, fmt.Errorf("attach rejected: %s: %s", response.Error.Code, response.Error.Message)
		}
		replyJSON, err := json.Marshal(response)
		if err != nil {
			return serverMoveResult{}, err
		}
		receipt, err := transition.NewServerReceipt(string(requestJSON), string(replyJSON), "POST /sync/notebooks/share")
		if err != nil {
			return serverMoveResult{}, err
		}
		return serverMoveResult{receipt: receipt, action: "attached to notebook " + plan.to.String() + " at version " + fmt.Sprint(response.Version)}, nil

	case movePlanReparent:
		return reparentNotespace(ctx, client, deviceID, plan, source, expectedVersion)
	}
	return serverMoveResult{}, fmt.Errorf("no server action was planned for this move")
}

func registerNotespaceStamp(ctx context.Context, client *deviceSessionHTTP, deviceID string, stamp notespace.NotespaceStamp) error {
	req := syncproto.RegisterRequest{
		RequestIdentity: syncproto.RequestIdentity{
			ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
			IdempotencyKey:  idempotencyKey("notespace-move-register", stamp.ID, stamp.Subject, stamp.Name, stamp.Kind),
			DeviceID:        deviceID,
		},
		Intent:              syncproto.RegistrationIntentReconcile,
		Subject:             stamp.Subject,
		NotespaceName:       syncproto.NotespaceName(stamp.Name),
		Kind:                stamp.Kind,
		ProposedNotespaceID: syncproto.NotespaceID(stamp.ID),
	}
	if wire := req.Validate(); wire != nil {
		return fmt.Errorf("notespace %s cannot be registered: %s", stamp.ID, wire.Message)
	}
	var response syncproto.RegisterResponse
	if _, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/register", req, &response); err != nil && response.Error == nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("register %s rejected: %s: %s", stamp.ID, response.Error.Code, response.Error.Message)
	}
	if response.NotespaceID.String() != stamp.ID {
		return fmt.Errorf("register %s: the server bound id %s instead", stamp.ID, response.NotespaceID)
	}
	return nil
}

// reparentNotespace performs the membership move, discovering the expected
// version when the caller did not pin one.
//
// The inventory does not carry a notespace's membership version, so a first
// attempt cannot know it. Rather than invent one, this asks with the version it
// has and lets the server correct it: a stale-resolution refusal carries the
// CURRENT version, and exactly one retry uses it. That keeps the optimistic
// check the server built (a genuinely concurrent third change makes the retry
// fail too) instead of routing around it.
func reparentNotespace(ctx context.Context, client *deviceSessionHTTP, deviceID string, plan serverMovePlan, source recordedNotespace, expectedVersion int64) (serverMoveResult, error) {
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
		_, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/notespaces/reparent", req, &response)
		if response.Error != nil {
			err = nil
		}
		return req, response, err
	}

	version := expectedVersion
	discovered := false
	if version < 0 {
		version = 0
	}
	req, response, err := attempt(version)
	// A refused precondition answers with the version the server actually
	// holds; that is the one fact the inventory could not supply.
	if response.Error != nil && response.Error.Code == syncproto.ErrorStaleResolution &&
		expectedVersion < 0 && response.Error.CurrentVersion != version {
		version = response.Error.CurrentVersion
		discovered = true
		req, response, err = attempt(version)
	}
	if err != nil {
		return serverMoveResult{}, err
	}
	if response.Error != nil {
		return serverMoveResult{}, fmt.Errorf("re-parent rejected: %s: %s", response.Error.Code, response.Error.Message)
	}
	if !response.HistoryPreserved || response.NotespaceID.String() != source.Stamp.ID {
		return serverMoveResult{}, fmt.Errorf("the server answered a re-parent this move did not ask for: %+v", response)
	}
	requestJSON, err := json.Marshal(req)
	if err != nil {
		return serverMoveResult{}, err
	}
	replyJSON, err := json.Marshal(response)
	if err != nil {
		return serverMoveResult{}, err
	}
	receipt, err := transition.NewServerReceipt(string(requestJSON), string(replyJSON), "POST /sync/notespaces/reparent")
	if err != nil {
		return serverMoveResult{}, err
	}
	action := fmt.Sprintf("re-parented %s → %s at membership version %d", plan.from, plan.to, response.Version)
	if discovered {
		action += " (version read back from the server's own precondition refusal)"
	}
	return serverMoveResult{receipt: receipt, cursor: response.Cursor, action: action}, nil
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
// directories, regular files (with their mode) and symlinks. Anything else is
// refused by name rather than skipped: a device node or socket inside a
// notespace is a surprise the operator should hear about, not something a move
// silently drops.
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
			return copyRegularFile(path, destination, info.Mode().Perm())
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
