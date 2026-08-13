package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
     directory that has none. Every stamped notespace's subject is then recorded
     in machine.toml [primaries]/[subjects], never left to be re-derived. Absent
     keys are filled and present ones are left alone, so an id whose stamp was
     written before a crash gets its record on the next run instead of never;
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

	identity, err := mintNotebookIdentity(out, &nb, scanned)
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
		status, registerErr := client.doJSONStatus(ctx, http.MethodPost, "/sync/register", req, &response)
		if registerErr != nil && response.Error == nil {
			return fmt.Errorf("register %s (%s): %w", ns.Dir, ns.Stamp.ID, registerErr)
		}
		if response.Error != nil {
			return fmt.Errorf("register %s (%s) rejected: %s: %s", ns.Dir, ns.Stamp.ID, response.Error.Code, response.Error.Message)
		}
		if err := refuseUnexplainedStatus(status, response.Error, "POST /sync/register"); err != nil {
			return fmt.Errorf("register %s (%s): %w", ns.Dir, ns.Stamp.ID, err)
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
	shareStatus, err := client.doJSONStatus(ctx, http.MethodPost, "/sync/notebooks/share", shareReq, &shareResp)
	if err != nil {
		renderMemberEvidence(out, shareResp.Members)
		return err
	}
	renderMemberEvidence(out, shareResp.Members)
	if shareResp.Error != nil {
		return fmt.Errorf("share rejected: %s: %s; %s", shareResp.Error.Code, shareResp.Error.Message, localStateAfterRefusal(identity))
	}
	if err := refuseUnexplainedStatus(shareStatus, shareResp.Error, "POST /sync/notebooks/share"); err != nil {
		return err
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
			{Name: "notespaces-minted", Value: identity.minted},
			{Name: "machine-identity-records-written", Value: identity.recorded},
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

// notebookIdentityResult reports what share had to write before it could talk
// to a server: how many ids it minted, and how many machine.toml records it
// filled in.
type notebookIdentityResult struct {
	minted   int64
	recorded int64
}

// wroteSomething reports whether this run has already changed local state, so a
// later refusal can say what stands instead of claiming nothing does.
func (r notebookIdentityResult) wroteSomething() bool { return r.minted > 0 || r.recorded > 0 }

// mintNotebookIdentity gives the notebook and every notespace under it a
// durable id, and records the machine-local half of every subject it finds.
//
// Minting belongs to share because share is the explicit materialization act:
// D4 says the verb that materializes the FIRST notespace for a subject records
// the primary, and there is no descriptive default to re-derive later. A
// notespace directory that already carries a stamp is left exactly as it is —
// stamps are authoritative forever.
//
// The machine.toml half covers EVERY stamped notespace in the notebook, not
// only the ones this run minted, and it fills absent keys without touching
// present ones. Recording only fresh mints looked equivalent and was not: a
// mint writes the stamp to disk first and the machine.toml transaction second,
// so a failure between them left an id that exists forever and a [primaries]
// entry that nothing would ever write, because the next run sees the stamp and
// mints nothing. Filling what is absent makes the pair converge on a re-run,
// which is what "recorded, never inferred" needs in order to survive a crash.
func mintNotebookIdentity(out io.Writer, nb *recordedNotebook, scanned []recordedNotebook) (notebookIdentityResult, error) {
	var result notebookIdentityResult
	if nb.Stamp == nil {
		stamp, err := notespace.MintNotebook(nb.Root, nb.Name)
		if err != nil {
			return result, fmt.Errorf("mint notebook identity for %s: %w", nb.Root, err)
		}
		nb.Stamp = stamp
		fmt.Fprintf(out, "  minted       %s  notebook %s\n", stamp.ID, nb.Name)
	}

	identities := make([]notespaceIdentity, 0, len(nb.Notespaces))
	for i := range nb.Notespaces {
		ns := &nb.Notespaces[i]
		if ns.Stamp == nil {
			local := subject.MintLocal().String()
			stamp, err := notespace.MintNotespace(ns.Root, notespace.NotespaceMutable{Name: ns.Dir, Subject: local, Kind: "notes"})
			if err != nil {
				return result, fmt.Errorf("mint notespace identity for %s: %w", ns.Root, err)
			}
			ns.Stamp = stamp
			result.minted++
			fmt.Fprintf(out, "  minted       %s  notespace %s (subject %s)\n", stamp.ID, ns.Dir, stamp.Subject)
			identities = append(identities, notespaceIdentity{id: stamp.ID, subject: stamp.Subject, root: ns.Root, minted: true})
			continue
		}
		if strings.TrimSpace(ns.Stamp.Subject) == "" {
			continue
		}
		identities = append(identities, notespaceIdentity{id: ns.Stamp.ID, subject: ns.Stamp.Subject, root: ns.Root})
	}
	recorded, err := recordNotespaceIdentities(identities, scanned)
	if err != nil {
		return result, err
	}
	result.recorded = recorded
	return result, nil
}

// notespaceIdentity is one (id, subject, root) triple on its way into
// machine.toml. minted distinguishes an id this run created from one it merely
// found — a subject collision means different things for the two.
type notespaceIdentity struct {
	id      string
	subject string
	root    string
	minted  bool
}

// recordsLocalSubject reports whether a subject belongs in machine [subjects].
//
// The test is the same one `MachineConfig.Validate` applies, deliberately: a
// writer that decided this differently from the validator would write files
// that cannot be read back.
func recordsLocalSubject(value string) bool {
	return strings.HasPrefix(value, subject.LocalPrefix)
}

// recordNotespaceIdentities fills the machine.toml half of P2 identity —
// [primaries] per subject, [subjects] per root — for identities that have one
// and do not have a record yet.
//
// It is shared by share (which mints local ids) and pull (which installs the
// server's), because the invariant is about the RECORD, not about where the id
// came from: a stamp on disk with no [primaries]/[subjects] entry is a
// notespace nothing can resolve by identity, and both verbs are capable of
// producing one.
//
// The two tables do NOT take the same subjects. [primaries] routes every
// subject family; [subjects] is the machine's local-subject registry and
// `MachineConfig.Validate` rejects any value there that is not `local:` —
// which is right, because a repository subject is derived from the repo's
// remote wherever it is cloned, so recording one against one machine's path
// would state as a machine fact something no machine owns. A notespace stamped
// for a repo therefore gets its [primaries] entry and no [subjects] entry, and
// that absence is its correct steady state rather than a gap to fill.
func recordNotespaceIdentities(identities []notespaceIdentity, scanned []recordedNotebook) (int64, error) {
	if len(identities) == 0 {
		return 0, nil
	}

	// Nothing is written unless something is missing, so a re-shared notebook
	// leaves machine.toml byte-identical.
	machineCfg, loadErr := config.LoadMachineConfig()
	recordedPrimaries := map[string]string{}
	recordedSubjects := map[string]string{}
	if loadErr == nil && machineCfg != nil {
		recordedPrimaries, recordedSubjects = machineCfg.Primaries, machineCfg.Subjects
	}
	var missing []notespaceIdentity
	for _, candidate := range identities {
		_, hasPrimary := recordedPrimaries[candidate.subject]
		_, hasSubject := recordedSubjects[canonicalPath(candidate.root)]
		// A remote subject is complete with its [primaries] entry alone, so it
		// must not be counted as missing on the strength of a [subjects] entry
		// it is never going to get — that would make every share of a repo
		// notebook open a transaction that then writes nothing.
		if !hasPrimary || (!hasSubject && recordsLocalSubject(candidate.subject)) {
			missing = append(missing, candidate)
		}
	}
	if len(missing) == 0 {
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
	for _, candidate := range identities {
		known[candidate.id] = struct{}{}
	}
	if machineCfg != nil && loadErr == nil {
		for _, id := range machineCfg.Primaries {
			known[id] = struct{}{}
		}
		if machineCfg.Sync.Registry != nil {
			known[machineCfg.Sync.Registry.NotespaceID] = struct{}{}
		}
	}

	var recorded int64
	if _, _, err := config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{KnownNotespaceIDs: known}, func(machine *config.MachineConfig) error {
		if machine.Primaries == nil {
			machine.Primaries = map[string]string{}
		}
		if machine.Subjects == nil {
			machine.Subjects = map[string]string{}
		}
		for _, candidate := range missing {
			switch existing, ok := machine.Primaries[candidate.subject]; {
			case ok && existing == candidate.id:
				// Already recorded, and recorded as this one.
			case ok && candidate.minted:
				// A freshly minted subject that something else already claims
				// is a collision in the id space, not a sibling.
				return fmt.Errorf("[primaries] already records %q as %s", candidate.subject, existing)
			case ok:
				// A pre-existing stamp whose subject already names another
				// notespace is a sibling (D2). Which one is primary is the
				// operator's call and never share's to change.
			default:
				machine.Primaries[candidate.subject] = candidate.id
				recorded++
			}
			// [subjects] maps a local path to a LOCAL subject and nothing else.
			// Writing a repository or ecosystem subject here would produce a
			// machine.toml that MachineConfig.Validate refuses to load — the
			// verb would appear to succeed and every later config read on this
			// machine would fail, which is a worse outcome than the missing
			// entry it was trying to avoid.
			if !recordsLocalSubject(candidate.subject) {
				continue
			}
			key := canonicalPath(candidate.root)
			if _, ok := machine.Subjects[key]; !ok {
				machine.Subjects[key] = candidate.subject
				recorded++
			}
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("record notespace identities in machine.toml: %w", err)
	}
	config.ResetLoadCache()
	return recorded, nil
}

// localStateAfterRefusal says what a refused share leaves behind.
//
// "nothing was recorded locally" is true of notebooks.toml and false of the
// disk: minting runs before the server is asked, and a stamp is immutable once
// written. A refusal that claimed otherwise would send an operator looking for
// a rollback that neither exists nor should.
func localStateAfterRefusal(identity notebookIdentityResult) string {
	if !identity.wroteSomething() {
		return "nothing was recorded locally"
	}
	return fmt.Sprintf("notebooks.toml records no share (that write comes last and did not happen), but this run had already minted %d notespace id(s) and written %d machine.toml identity record(s) — stamps are immutable, so a re-run reuses them rather than minting again",
		identity.minted, identity.recorded)
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
it carries a DIFFERENT id — a stamp is immutable, and refusing too when another
recorded root already carries that id, because binding it twice would record one
notebook id twice), binds every notespace the server holds in that notebook the
same way (an empty directory under notespaces/ carrying the SERVER's stamp, so
the daemon has something to replicate into and recognizes it by identity), and
records [notebooks.<name>.sync] share = true so the notebook is in scope for
sync in both directions.

A notespace whose id is already stamped elsewhere on this machine, or whose
name is taken by a directory stamped differently, is reported and skipped
rather than bound: binding it twice would create the duplicate-stamp condition
this verb's own preflight refuses to act on.

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
		// Binding is the one act here that can CREATE a duplicate id rather
		// than merely meet one. Every other verb refuses to act on a machine
		// that records a notebook id twice (D8); pull must therefore refuse to
		// produce that state, or it would be the source of the condition its
		// own preflight rejects.
		if other, clash := notebookStampedElsewhere(scanned, nb.Name, target.ID); clash {
			return fmt.Errorf("notebook %s is already stamped on this machine at %s (recorded as %q); binding it to %s as well would record one notebook id twice — pull into that root instead, or re-mint one of them first",
				target.ID, other.Root, other.Name, nb.Root)
		}
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
	bound, refused, err := bindServerNotespaces(out, nb, scanned, inventory, awaiting)
	if err != nil {
		return err
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
			{Name: "notespaces-bound", Value: bound},
			{Name: "notespaces-not-bound", Value: refused},
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

// bindServerNotespaces gives every notespace the server holds in this notebook
// a local identity: the directory under <root>/notespaces/, carrying the
// SERVER's stamp.
//
// This is the other half of "containment is consent". The daemon syncs a
// notespace because its notebook is shared, and it recognizes a notespace by
// the stamp at its root — so on the receiving machine the whole promise ("share
// a notebook on A and its notes appear on B") hangs on those stamps existing.
// Nothing else was going to write them: share mints local ids on the machine
// that publishes, and the daemon refuses to invent an identity for a tree it
// found, which is exactly the discipline that makes ids trustworthy.
//
// It still writes no documents. A bound notespace is an empty stamped
// directory; the daemon replicates into it, which is what pull has always said
// happens next.
//
// Every refusal here is per member and printed, never fatal, and never a
// re-key:
//
//   - an id already stamped somewhere else on this machine is skipped, because
//     binding it a second time would CREATE the duplicate-stamp condition (D8)
//     that pull's own notebook-level preflight refuses to act on;
//   - a directory already sitting at that name under a different id is left
//     alone — a stamp is immutable, and the operator has notes there;
//   - a server name that is not a single path component is refused rather than
//     joined, because a name is a directory here and the server's is not a
//     path this machine agreed to.
func bindServerNotespaces(out io.Writer, nb recordedNotebook, scanned []recordedNotebook, inventory serverInventory, awaiting []string) (int64, int64, error) {
	if len(awaiting) == 0 {
		return 0, 0, nil
	}
	// Where every id on this machine already lives, so a second binding of one
	// is refused rather than produced.
	stampedAt := map[string]string{}
	for _, entry := range scanned {
		for _, ns := range entry.Notespaces {
			if ns.ID() != "" {
				stampedAt[ns.ID()] = ns.Root
			}
		}
	}

	var bound, refused int64
	identities := make([]notespaceIdentity, 0, len(awaiting))
	for _, id := range awaiting {
		ns, ok := inventory.notespaceByID(syncproto.NotespaceID(id))
		if !ok {
			fmt.Fprintf(out, "  not bound    %s  the server lists it in this notebook but returned no notespace row\n", id)
			refused++
			continue
		}
		name := strings.TrimSpace(ns.Name.String())
		if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
			fmt.Fprintf(out, "  not bound    %s  the server calls it %q, which is not a directory name this machine can bind\n", id, ns.Name.String())
			refused++
			continue
		}
		if other, clash := stampedAt[id]; clash {
			fmt.Fprintf(out, "  not bound    %s  already stamped at %s; binding it twice would record one notespace id twice\n", id, other)
			refused++
			continue
		}
		root := filepath.Join(nb.Root, notespaceContainerDir, name)
		existing, err := notespace.LoadNotespace(root)
		if err != nil {
			return bound, refused, fmt.Errorf("read the notespace stamp at %s: %w", root, err)
		}
		if existing != nil {
			fmt.Fprintf(out, "  not bound    %s  %s is stamped %s; a stamp is immutable, so this is a different notespace\n", id, root, existing.ID)
			refused++
			continue
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return bound, refused, fmt.Errorf("create the notespace root %s: %w", root, err)
		}
		stamp, err := notespace.InstallNotespace(root, notespace.NotespaceStamp{
			ID: id, Name: name, Subject: ns.Subject, Kind: ns.Kind,
		})
		if err != nil {
			return bound, refused, fmt.Errorf("bind %s to notespace %s: %w", root, id, err)
		}
		stampedAt[id] = root
		bound++
		fmt.Fprintf(out, "  bound        %s  %s\n", id, root)
		if strings.TrimSpace(stamp.Subject) != "" {
			identities = append(identities, notespaceIdentity{id: stamp.ID, subject: stamp.Subject, root: root})
		}
	}
	if _, err := recordNotespaceIdentities(identities, scanned); err != nil {
		return bound, refused, err
	}
	return bound, refused, nil
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
