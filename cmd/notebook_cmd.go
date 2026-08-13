package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/subject"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/pkg/transition"
)

// `grove notebook share|pull` — the notebook-grained sync verbs (P3 W3.2).
//
// The notebook is the only sync knob. There is no per-notespace toggle here and
// there will not be one: a notespace is shared because the notebook containing
// it is shared, so the recorded fact is one boolean per notebook
// ([notebooks.<name>.sync] share) and containment does the rest. That is also
// why a notespace created later inside an already-shared notebook needs no
// verb — containment is consent, and the daemon registers it.
//
// Both directions are explicit and neither merges: share pushes this machine's
// notebook identity and membership to the server, pull binds a server notebook
// to a root this machine already recorded. Pull's refusal is the load-bearing
// one. A missing root is a REFUSAL, never a MkdirAll: recreating a root the
// operator deleted (or never had) is how a notebook gets resurrected into a
// directory no other grove surface reads, and there is no evidence afterwards
// that it was ever wrong.

func init() {
	rootCmd.AddCommand(newNotebookCmd())
}

func newNotebookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notebook",
		Short: "Notebook-grained sync scope (share, pull)",
		Long: `Share and pull whole notebooks.

The notebook is the only sync knob. A notespace is shared because the notebook
containing it is shared — containment is consent — so there is no per-notespace
toggle, and a notespace created inside an already-shared notebook is registered
by the daemon without a verb.

  share <name>  register this notebook and every notespace it contains, and
                record [notebooks.<name>.sync] share = true
  pull <name>   bind a notebook this server holds to a root this machine has
                already recorded, and record it as shared

Unsharing is forward-only (D9): the server retains the notebook's notespaces and
their full history, copies already pulled elsewhere are not retracted, and
re-sharing the same notebook id resumes the same history.`,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newNotebookShareCmd())
	cmd.AddCommand(newNotebookPullCmd())
	return cmd
}

// ---- share --------------------------------------------------------------------

func newNotebookShareCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "share <name>",
		Short: "Share a recorded notebook and every notespace it contains",
		Long: `Register a notebook with the sync server and record it as shared.

In order, and each step is reported per notespace rather than as a total:

  1. mint what is unminted — the notebook's .notebook.toml id, and a
     .notespace.toml id (with a machine-local subject) for every notespace
     directory that has none. Minting a subject is recorded in machine.toml
     [primaries]/[subjects] in the same transaction, never left to be re-derived;
  2. register every contained notespace with the server (v3 reconcile, proposing
     the id the stamp already holds);
  3. POST the notebook share. The server attaches unparented members and REFUSES
     the whole request if a member belongs to another notebook — acquiring
     someone else's notespace is a move, and ` + "`grove notespace move`" + ` is the verb
     that says so out loud;
  4. only then record [notebooks.<name>.sync] share = true in notebooks.toml.

The recorded root must exist. Share never creates a notebook root.

"shared 12 notespaces" is not evidence; this prints the list.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNotebookShare(cmd.Context(), cmd.OutOrStdout(), args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the transition evidence as JSON")
	return cmd
}

func runNotebookShare(ctx context.Context, out io.Writer, name string, asJSON bool) error {
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
	nb, err := findRecordedNotebook(table, scanned, name)
	if err != nil {
		return err
	}
	if !nb.Exists {
		return fmt.Errorf("notebook %q records root %s, which does not exist; share never creates a notebook root — create it, or fix [notebooks.%s].root in %s",
			nb.Name, nb.Root, nb.Name, displayRecordedPath(table.NotebooksFilePath, coderoot.NotebooksFileName))
	}

	minted, err := mintNotebookIdentity(out, &nb, scanned)
	if err != nil {
		return err
	}

	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return err
	}
	key, err := devicekey.Load()
	if err != nil {
		return err
	}

	// Share WRITES the notebook's name and share state, so it carries the
	// version it decided against: version 0 means "I believe this server does
	// not hold this notebook yet". Reading it from the inventory rather than
	// assuming it is what stops a stale machine from renaming a notebook for
	// everyone, or resurrecting one another machine has just unshared.
	inventory, err := fetchServerInventory(ctx, client, key.DeviceID())
	if err != nil {
		return err
	}
	var expectedVersion int64
	if held, ok := inventory.notebookByID(syncproto.NotebookID(nb.ID())); ok {
		expectedVersion = held.Version
	}

	// Members must already be registered: the server rejects an unregistered
	// notespace by name and fails the whole share, so registration happens here
	// first, one notespace at a time, with its own evidence line.
	registered := int64(0)
	members := make([]syncproto.NotespaceID, 0, len(nb.Notespaces))
	for _, ns := range nb.Notespaces {
		if ns.Stamp == nil {
			continue
		}
		req := syncproto.RegisterRequest{
			RequestIdentity: syncproto.RequestIdentity{
				ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
				IdempotencyKey:  idempotencyKey("notebook-share-register", ns.Stamp.ID, ns.Stamp.Subject, ns.Stamp.Name, ns.Stamp.Kind),
				DeviceID:        key.DeviceID(),
			},
			Intent:              syncproto.RegistrationIntentReconcile,
			Subject:             ns.Stamp.Subject,
			NotespaceName:       syncproto.NotespaceName(ns.Stamp.Name),
			Kind:                ns.Stamp.Kind,
			ProposedNotespaceID: syncproto.NotespaceID(ns.Stamp.ID),
		}
		if wire := req.Validate(); wire != nil {
			return fmt.Errorf("notespace %s cannot be registered: %s", ns.Dir, wire.Message)
		}
		var response syncproto.RegisterResponse
		if _, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/register", req, &response); err != nil && response.Error == nil {
			return fmt.Errorf("register %s (%s): %w", ns.Dir, ns.Stamp.ID, err)
		}
		if response.Error != nil {
			return fmt.Errorf("register %s (%s) rejected: %s: %s", ns.Dir, ns.Stamp.ID, response.Error.Code, response.Error.Message)
		}
		if response.NotespaceID.String() != ns.Stamp.ID {
			return fmt.Errorf("register %s: the server bound id %s, not the stamped %s", ns.Dir, response.NotespaceID, ns.Stamp.ID)
		}
		registered++
		fmt.Fprintf(out, "  registered   %s  %s\n", ns.Stamp.ID, ns.Dir)
		members = append(members, syncproto.NotespaceID(ns.Stamp.ID))
	}

	shareReq := syncproto.NotebookShareRequest{
		RequestIdentity: syncproto.RequestIdentity{
			ProtocolVersion: syncproto.ProtocolVersionNotespaceID,
			IdempotencyKey:  idempotencyKey("notebook-share", append([]string{nb.ID(), nb.Name, fmt.Sprint(expectedVersion)}, memberStrings(members)...)...),
			DeviceID:        key.DeviceID(),
		},
		NotebookID:      syncproto.NotebookID(nb.ID()),
		Name:            nb.Name,
		ExpectedVersion: expectedVersion,
		Members:         members,
	}
	if wire := shareReq.Validate(); wire != nil {
		return fmt.Errorf("invalid share request: %s", wire.Message)
	}
	var shareResp syncproto.NotebookShareResponse
	requestJSON, err := json.Marshal(shareReq)
	if err != nil {
		return err
	}
	if _, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/notebooks/share", shareReq, &shareResp); err != nil {
		renderMemberEvidence(out, shareResp.Members)
		return err
	}
	renderMemberEvidence(out, shareResp.Members)
	if shareResp.Error != nil {
		return fmt.Errorf("share rejected: %s: %s (nothing was recorded locally)", shareResp.Error.Code, shareResp.Error.Message)
	}
	replyJSON, err := json.Marshal(shareResp)
	if err != nil {
		return err
	}

	// The server accepted, so the file may now say so. This order is the whole
	// point: notebooks.toml recording share = true for a notebook the server
	// never accepted would be a lie the daemon would then act on.
	changed, err := config.WriteNotebooks(table.NotebooksFilePath, config.NotebookEdits{SyncShare: map[string]bool{nb.Name: true}})
	if err != nil {
		return fmt.Errorf("the server accepted the share but %s could not record it (`share = true` under [notebooks.%s.sync]); the notebook is shared on the server and this machine does not say so: %w",
			table.NotebooksFilePath, nb.Name, err)
	}
	config.ResetLoadCache()

	receipt, err := transition.NewServerReceipt(string(requestJSON), string(replyJSON), "POST /sync/notebooks/share")
	if err != nil {
		return err
	}
	evidence := transition.Evidence{
		Action: "notebook share",
		Counts: []transition.Count{
			{Name: "notespaces-in-notebook", Value: int64(len(nb.Notespaces))},
			{Name: "notespaces-minted", Value: minted},
			{Name: "notespaces-registered", Value: registered},
			{Name: "notespaces-attached", Value: countDisposition(shareResp.Members, syncproto.MemberDispositionAttached)},
			{Name: "notespaces-already-member", Value: countDisposition(shareResp.Members, syncproto.MemberDispositionAlreadyMember)},
		},
		ResolvedRoots: []transition.ResolvedRoot{{Name: nb.Name, Declared: nb.Declared, Resolved: nb.Root}},
		ServerReceipt: receipt,
	}
	if !changed {
		evidence.Reason = transition.Reason("notebooks.toml already recorded share = true for this notebook")
	}
	if shareResp.Resumed {
		fmt.Fprintf(out, "\n  resumed: this notebook id was unshared on the server; the same history resumes (D9).\n")
	}
	fmt.Fprintf(out, "\n  %s is shared at version %d. Unsharing later is forward-only:\n  %s\n", nb.Name, shareResp.Version, syncproto.UnshareRetentionStatement)
	fmt.Fprintln(out)
	if asJSON {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

// mintNotebookIdentity gives the notebook and every notespace under it a
// durable id, and records the machine-local half of any subject it had to mint.
//
// Minting belongs to share because share is the explicit materialization act:
// D4 says the verb that materializes the FIRST notespace for a subject records
// the primary, and there is no descriptive default to re-derive later. A
// notespace directory that already carries a stamp is left exactly as it is —
// stamps are authoritative forever.
func mintNotebookIdentity(out io.Writer, nb *recordedNotebook, scanned []recordedNotebook) (int64, error) {
	if nb.Stamp == nil {
		stamp, err := notespace.MintNotebook(nb.Root, nb.Name)
		if err != nil {
			return 0, fmt.Errorf("mint notebook identity for %s: %w", nb.Root, err)
		}
		nb.Stamp = stamp
		fmt.Fprintf(out, "  minted       %s  notebook %s\n", stamp.ID, nb.Name)
	}

	type mint struct {
		id      string
		subject string
		root    string
	}
	var mints []mint
	for i := range nb.Notespaces {
		ns := &nb.Notespaces[i]
		if ns.Stamp != nil {
			continue
		}
		local := subject.MintLocal().String()
		stamp, err := notespace.MintNotespace(ns.Root, notespace.NotespaceMutable{Name: ns.Dir, Subject: local, Kind: "notes"})
		if err != nil {
			return 0, fmt.Errorf("mint notespace identity for %s: %w", ns.Root, err)
		}
		ns.Stamp = stamp
		mints = append(mints, mint{id: stamp.ID, subject: stamp.Subject, root: ns.Root})
		fmt.Fprintf(out, "  minted       %s  notespace %s (subject %s)\n", stamp.ID, ns.Dir, stamp.Subject)
	}
	if len(mints) == 0 {
		return 0, nil
	}

	// The stamp index this transaction is checked against covers what is on
	// disk plus what [primaries] already references. Widening it that way keeps
	// the check on what THIS verb writes — a new primary must name a stamp that
	// exists — without failing a share over unrelated drift, which is doctor's
	// to report.
	known := map[string]struct{}{}
	for _, entry := range scanned {
		for _, ns := range entry.Notespaces {
			if ns.ID() != "" {
				known[ns.ID()] = struct{}{}
			}
		}
	}
	for _, m := range mints {
		known[m.id] = struct{}{}
	}
	if existing, err := config.LoadMachineConfig(); err == nil && existing != nil {
		for _, id := range existing.Primaries {
			known[id] = struct{}{}
		}
		if existing.Sync.Registry != nil {
			known[existing.Sync.Registry.NotespaceID] = struct{}{}
		}
	}

	if _, _, err := config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{KnownNotespaceIDs: known}, func(machine *config.MachineConfig) error {
		if machine.Primaries == nil {
			machine.Primaries = map[string]string{}
		}
		if machine.Subjects == nil {
			machine.Subjects = map[string]string{}
		}
		for _, m := range mints {
			if recorded, ok := machine.Primaries[m.subject]; ok && recorded != m.id {
				return fmt.Errorf("[primaries] already records %q as %s", m.subject, recorded)
			}
			machine.Primaries[m.subject] = m.id
			machine.Subjects[canonicalPath(m.root)] = m.subject
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("record minted identities in machine.toml: %w", err)
	}
	config.ResetLoadCache()
	return int64(len(mints)), nil
}

func renderMemberEvidence(out io.Writer, members []syncproto.NotebookMemberResult) {
	for _, member := range members {
		line := fmt.Sprintf("  %-12s %s", member.Disposition, member.NotespaceID)
		if member.FromNotebookID != "" {
			line += "  (belongs to notebook " + member.FromNotebookID.String() + ")"
		}
		if member.Error != nil {
			line += "  " + member.Error.Code + ": " + member.Error.Message
		}
		fmt.Fprintln(out, line)
	}
}

func countDisposition(members []syncproto.NotebookMemberResult, disposition string) int64 {
	var n int64
	for _, member := range members {
		if member.Disposition == disposition {
			n++
		}
	}
	return n
}

func memberStrings(members []syncproto.NotespaceID) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		out = append(out, member.String())
	}
	sort.Strings(out)
	return out
}

// ---- pull ---------------------------------------------------------------------

func newNotebookPullCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "pull <name>",
		Short: "Bind a notebook this server holds to a recorded, existing root",
		Long: `Materialize a server notebook at a root this machine has already recorded.

The root must be RECORDED and it must EXIST. Both are refusals, not defaults:

  · a notebook name with no [notebooks.<name>] table is refused, because a
    notebook root this machine never wrote down is a root nothing else reads;
  · a recorded root that is not there is refused and says which path is missing.
    Pull does not MkdirAll it back into existence — recreating a deleted root is
    how a notebook silently resurrects somewhere nobody is looking.

What pull does: reads the server's inventory, binds this root to the server's
notebook id (installing .notebook.toml when the root carries none, refusing when
it carries a DIFFERENT id — a stamp is immutable), and records
[notebooks.<name>.sync] share = true so the notebook is in scope for sync in
both directions.

What pull does NOT do: write documents. The daemon replicates into the recorded
root; this verb reports which notespaces the server holds and which of them are
already here, so what arrives later is what this evidence predicted.

A notebook the server has UNSHARED is retained, not offered: pulling it is
refused, because forward-only (D9) means the history survives an unshare, not
that the notebook is still on offer.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNotebookPull(cmd.Context(), cmd.OutOrStdout(), args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the transition evidence as JSON")
	return cmd
}

func runNotebookPull(ctx context.Context, out io.Writer, name string, asJSON bool) error {
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
	nb, err := findRecordedNotebook(table, scanned, name)
	if err != nil {
		return fmt.Errorf("%w\n  pull materializes into a RECORDED root; record one first:\n    [notebooks.%s]\n    root = \"~/notebooks/%s\"", err, name, name)
	}
	if !nb.Exists {
		return fmt.Errorf("notebook %q records root %s, which does not exist; pull refuses a missing root rather than creating one — restore or create that directory (or fix [notebooks.%s].root in %s) and re-run",
			nb.Name, nb.Root, nb.Name, displayRecordedPath(table.NotebooksFilePath, coderoot.NotebooksFileName))
	}

	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return err
	}
	key, err := devicekey.Load()
	if err != nil {
		return err
	}
	inventory, err := fetchServerInventory(ctx, client, key.DeviceID())
	if err != nil {
		return err
	}

	var target syncproto.InventoryNotebook
	switch {
	case nb.ID() != "":
		found, ok := inventory.notebookByID(syncproto.NotebookID(nb.ID()))
		if !ok {
			return fmt.Errorf("this root is stamped notebook %s, which this server does not hold; a stamp is immutable, so this is a different notebook rather than a rename", nb.ID())
		}
		target = found
	default:
		found, nameErr := inventory.notebookByName(name)
		if nameErr != nil {
			return nameErr
		}
		target = found
	}
	if target.ShareState == syncproto.NotebookShareStateUnshared {
		return fmt.Errorf("notebook %s (%s) is unshared on this server: %s — re-share it there before pulling it here",
			target.Name, target.ID, syncproto.UnshareRetentionStatement)
	}

	if nb.Stamp == nil {
		stamp, installErr := notespace.InstallNotebook(nb.Root, notespace.NotebookStamp{ID: target.ID.String(), Name: nb.Name})
		if installErr != nil {
			return fmt.Errorf("bind %s to notebook %s: %w", nb.Root, target.ID, installErr)
		}
		nb.Stamp = stamp
		fmt.Fprintf(out, "  bound        %s  %s\n", stamp.ID, nb.Root)
	} else if nb.Stamp.ID != target.ID.String() {
		return fmt.Errorf("%s is stamped notebook %s; the server offered %s. A notebook stamp is immutable — pull into a different recorded root instead of re-keying this one",
			nb.Root, nb.Stamp.ID, target.ID)
	}

	present := map[string]struct{}{}
	for _, ns := range nb.Notespaces {
		if ns.ID() != "" {
			present[ns.ID()] = struct{}{}
		}
	}
	var here, awaiting []string
	for _, id := range target.NotespaceIDs {
		if _, ok := present[id.String()]; ok {
			here = append(here, id.String())
			continue
		}
		awaiting = append(awaiting, id.String())
	}
	sort.Strings(here)
	sort.Strings(awaiting)
	for _, id := range here {
		fmt.Fprintf(out, "  here         %s  %s\n", id, describeInventoryNotespace(inventory, id))
	}
	for _, id := range awaiting {
		fmt.Fprintf(out, "  awaiting     %s  %s\n", id, describeInventoryNotespace(inventory, id))
	}

	changed, err := config.WriteNotebooks(table.NotebooksFilePath, config.NotebookEdits{SyncShare: map[string]bool{nb.Name: true}})
	if err != nil {
		return fmt.Errorf("record [notebooks.%s.sync] share = true in %s: %w", nb.Name, table.NotebooksFilePath, err)
	}
	config.ResetLoadCache()

	receipt, err := inventory.receipt("GET /sync/inventory")
	if err != nil {
		return err
	}
	evidence := transition.Evidence{
		Action: "notebook pull",
		Counts: []transition.Count{
			{Name: "notespaces-on-server", Value: int64(len(target.NotespaceIDs))},
			{Name: "notespaces-already-here", Value: int64(len(here))},
			{Name: "notespaces-awaiting-delivery", Value: int64(len(awaiting))},
		},
		ResolvedRoots: []transition.ResolvedRoot{{Name: nb.Name, Declared: nb.Declared, Resolved: nb.Root}},
		ServerReceipt: receipt,
	}
	if !changed {
		evidence.Reason = transition.Reason("notebooks.toml already recorded share = true for this notebook")
	}
	fmt.Fprintf(out, "\n  %s is bound to notebook %s at %s and recorded as shared.\n", nb.Name, target.ID, nb.Root)
	fmt.Fprintf(out, "  Documents arrive through the daemon, into that root and no other; this verb wrote none.\n")
	fmt.Fprintln(out)
	if asJSON {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

func describeInventoryNotespace(inventory serverInventory, id string) string {
	ns, ok := inventory.notespaceByID(syncproto.NotespaceID(id))
	if !ok {
		return "(the server lists it in this notebook but returned no notespace row)"
	}
	parts := []string{ns.Name.String()}
	if strings.TrimSpace(ns.Kind) != "" {
		parts = append(parts, ns.Kind)
	}
	if strings.TrimSpace(ns.Subject) != "" {
		parts = append(parts, ns.Subject)
	}
	return strings.Join(parts, "  ")
}
