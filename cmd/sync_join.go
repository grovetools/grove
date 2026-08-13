package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/core/pkg/transition"
)

// `grove sync join <url>` — the relationship verb (P3 W3.1).
//
// Join was one command doing seven things: machine identity, device
// enrollment, credential resolution, a hard-coded registry subscription, the
// registry root's creation and minting, a daemon-pickup wait, and a
// replication count. P3 splits the sentence in two. THIS verb records the
// relationship (which server, and how this machine authenticates to it) and
// then answers one question about it — what does that server hold that this
// machine does not, and the other way round. It mutates nothing else: no
// subscription, no machine name, no registry root, no mkdir, no mint.
//
// The compatibility path is deliberate and is not a shim. `grove join` remains
// exactly what it was — the ENROLLMENT verb, and the only thing that enrolls a
// device, writes the registry subscription and materializes the registry root —
// because a machine still has to be enrolled before it has a device session to
// ask for an inventory with. The two are ordered, not alternative: `grove join`
// once per machine, `grove sync join` whenever you want to see the delta. Each
// command's help says so, so neither reads as the other's replacement.
//
// The delta is a description, never an instruction: nothing here pulls, shares
// or moves anything. That is `grove notebook pull|share` and
// `grove notespace move`, each on an explicit keypress.

func newSyncJoinCmd() *cobra.Command {
	var (
		tokenCommand string
		asJSON       bool
		expand       bool
		deltaOnly    bool
	)
	cmd := &cobra.Command{
		Use:   "join [server-url]",
		Short: "Record the relationship with a sync server and show the notebook delta",
		Long: `Record which grove-syncd server this machine talks to, then show what it holds
that this machine does not — and the other way round.

This verb does TWO things and nothing else:

  1. records the relationship in sync.toml (server, and a --token-command when
     you pass one). Convergent, like every other writer here: absent keys are
     filled, keys you already wrote are left exactly as you wrote them. It adds
     NO [[workspaces]] subscription, pins no machine name, and creates no
     directory;
  2. fetches GET /sync/inventory and renders the delta, grouped by notebook and
     expandable to notespace grain with --notespaces, in both directions.

Enrollment is a different verb. ` + "`grove join`" + ` enrolls this device, waits for
its fingerprint to be approved, writes the registry subscription and
materializes the registry root; run it once per machine. This command needs an
approved device session to ask for an inventory at all, so it comes after.

The delta describes; it never acts. A notebook the server holds and this machine
does not is reported as pullable, not pulled — ` + "`grove notebook pull <name>`" + ` is
what pulls it, into a root you recorded, and it refuses when that root does not
exist. Sharing is ` + "`grove notebook share <name>`" + `; moving one notespace between
notebooks is ` + "`grove notespace move <ns> --to <notebook>`" + `.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := syncJoinOptions{tokenCommand: tokenCommand, asJSON: asJSON, expand: expand, deltaOnly: deltaOnly}
			if len(args) == 1 {
				opts.server = args[0]
			}
			return runSyncJoin(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&tokenCommand, "token-command", "", "Shell command that prints a bearer token; recorded in sync.toml verbatim when it declares none")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render the transition evidence as JSON")
	cmd.Flags().BoolVar(&expand, "notespaces", false, "Expand the delta to notespace grain")
	cmd.Flags().BoolVar(&deltaOnly, "delta-only", false, "Do not record anything; only fetch and render the delta")
	return cmd
}

type syncJoinOptions struct {
	server       string
	tokenCommand string
	asJSON       bool
	expand       bool
	deltaOnly    bool
}

func runSyncJoin(ctx context.Context, out io.Writer, opts syncJoinOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	server, provenance, err := resolveJoinServer(joinOptions{server: opts.server})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Server %s [%s]\n", server, provenance)

	configDir := paths.ConfigDir()
	if configDir == "" {
		return fmt.Errorf("cannot resolve the grove config directory")
	}
	syncPath := filepath.Join(configDir, syncConfigFileName)

	if opts.deltaOnly {
		fmt.Fprintf(out, "  relationship  not recorded (--delta-only)\n")
	} else if err := recordSyncRelationship(out, syncPath, server, opts.tokenCommand); err != nil {
		return err
	}
	reportRecordedRegistryBinding(out)

	// The delta needs an approved device session, which is enrollment's
	// product. Say which verb produces it rather than surfacing a bare
	// "device key not found".
	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return fmt.Errorf("%w\n  this verb reads the server through an approved device session; `grove join %s` enrolls this machine and waits for approval", err, server)
	}
	key, err := devicekey.Load()
	if err != nil {
		return err
	}
	inventory, err := fetchServerInventory(ctx, client, key.DeviceID())
	if err != nil {
		return err
	}

	_, scanned, err := loadRecordedNotebooks()
	if err != nil {
		return err
	}
	delta := syncproto.BuildInventoryDelta(deltaLocalNotebooks(scanned), inventory.Response)
	renderJoinDelta(out, delta, scanned, opts.expand)

	receipt, err := inventory.receipt("GET /sync/inventory")
	if err != nil {
		return err
	}
	evidence := transition.Evidence{
		Action:        "sync join",
		Counts:        joinDeltaCounts(delta),
		ResolvedRoots: recordedNotebookRoots(scanned),
		ServerReceipt: receipt,
	}
	fmt.Fprintln(out)
	if opts.asJSON {
		return transition.RenderJSON(out, evidence)
	}
	return transition.RenderHuman(out, evidence)
}

// recordSyncRelationship writes the relationship half and reads it back. The
// read-back is `grove join`'s discipline, kept here for the same reason: a
// write that returned no error is not evidence that the daemon will resolve a
// server out of the file.
func recordSyncRelationship(out io.Writer, syncPath, server, tokenCommand string) error {
	res, err := config.ApplySyncEdit(syncPath, config.SyncEdit{
		Server:       server,
		TokenCommand: strings.TrimSpace(tokenCommand),
		Header: []string{
			"# Notebook sync client config — the relationship recorded by `grove sync join`.",
			"# Subscriptions are NOT written here by that verb: under the P3 scope model the",
			"# notebook is the sync knob ([notebooks.<name>.sync] in notebooks.toml), and",
			"# device enrollment plus the registry subscription belong to `grove join`.",
		},
		Note: "Recorded by `grove sync join` (relationship only).",
	})
	if err != nil {
		return err
	}
	for _, warning := range res.Warnings {
		fmt.Fprintf(out, "  %-13s ! %s\n", "", warning)
	}
	written, err := config.LoadSyncConfigFrom(syncPath)
	if err != nil {
		return fmt.Errorf("recorded %s but it does not read back: %w", syncPath, err)
	}
	if written == nil || strings.TrimSpace(written.Server) == "" {
		return fmt.Errorf("%s declares no server after the write; it declares one already, or declares it empty, and convergence never overwrites what you wrote — edit it by hand", syncPath)
	}
	config.ResetLoadCache()

	var did []string
	switch {
	case res.Created:
		did = append(did, "created")
	case len(res.Filled) > 0:
		did = append(did, "filled "+strings.Join(res.Filled, ", "))
	default:
		did = append(did, "already recorded")
	}
	fmt.Fprintf(out, "  %-13s %s … ✓ %s — reads back with server %s\n", "relationship", syncPath, strings.Join(did, "; "), written.Server)
	return nil
}

// reportRecordedRegistryBinding prints D10's binding when this machine has one.
// It is read-only on purpose: minting a registry notespace is `grove join`'s
// act, and a relationship verb that quietly created one would be the seventh
// thing this split exists to remove.
func reportRecordedRegistryBinding(out io.Writer) {
	machineCfg, err := config.LoadMachineConfig()
	if err != nil || machineCfg == nil || machineCfg.Sync.Registry == nil {
		fmt.Fprintf(out, "  %-13s none recorded — `grove join` binds one when it enrolls this machine\n", "registry")
		return
	}
	fmt.Fprintf(out, "  %-13s notebook %q, notespace %s [sync.registry]\n", "registry",
		machineCfg.Sync.Registry.Notebook, machineCfg.Sync.Registry.NotespaceID)
}

// renderJoinDelta prints the comparison, grouped by notebook and expandable to
// notespace grain. Every row states a DIRECTION, never an action.
func renderJoinDelta(out io.Writer, delta syncproto.InventoryDelta, scanned []recordedNotebook, expand bool) {
	names := map[syncproto.NotebookID]string{}
	for _, entry := range scanned {
		if entry.ID() != "" {
			names[syncproto.NotebookID(entry.ID())] = entry.Name
		}
	}

	fmt.Fprintf(out, "\nJoin delta — %d notebook%s compared\n\n", len(delta.Notebooks), plural(len(delta.Notebooks)))
	if len(delta.Notebooks) == 0 {
		fmt.Fprintf(out, "  neither side records a notebook this comparison can name.\n")
	}
	w := tabwriter.NewWriter(out, 2, 2, 2, ' ', 0)
	if len(delta.Notebooks) > 0 {
		fmt.Fprintf(w, "  NOTEBOOK\tID\tHERE\tSERVER\tDELTA\n")
	}
	for _, nb := range delta.Notebooks {
		name := nb.Name
		if local, ok := names[nb.ID]; ok {
			name = local
		}
		if strings.TrimSpace(name) == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", name, nb.ID, joinLocalState(nb), joinServerState(nb), joinDeltaSummary(nb))
	}
	w.Flush()

	if expand {
		for _, nb := range delta.Notebooks {
			if len(nb.LocalOnlyNotespaces) == 0 && len(nb.ServerOnlyNotespaces) == 0 {
				continue
			}
			fmt.Fprintf(out, "\n  %s %s\n", nb.Name, nb.ID)
			for _, id := range nb.LocalOnlyNotespaces {
				fmt.Fprintf(out, "    here only    %s\n", id)
			}
			for _, id := range nb.ServerOnlyNotespaces {
				fmt.Fprintf(out, "    server only  %s\n", id)
			}
		}
	}

	if n := len(delta.UnparentedServerNotespaces); n > 0 {
		fmt.Fprintf(out, "\n  unparented on the server: %d notespace%s", n, plural(n))
		if expand {
			fmt.Fprintln(out)
			for _, id := range delta.UnparentedServerNotespaces {
				fmt.Fprintf(out, "    %s\n", id)
			}
		} else {
			fmt.Fprintf(out, " (--notespaces lists them)\n")
		}
	}

	// A duplicate id is rendered, not hidden: this is where an operator meets
	// it, and the acting verbs refuse until it is resolved.
	if conflict := delta.Conflicts(); conflict != nil {
		fmt.Fprintf(out, "\n  ! %v\n", conflict)
	}

	var unstamped []string
	for _, entry := range scanned {
		if entry.Exists && entry.ID() == "" {
			unstamped = append(unstamped, entry.Name)
		}
	}
	if len(unstamped) > 0 {
		fmt.Fprintf(out, "\n  recorded here but unstamped, so absent from this comparison: %s\n", strings.Join(unstamped, ", "))
		fmt.Fprintf(out, "  `grove notebook share <name>` mints a notebook id as part of sharing it.\n")
	}
	for _, entry := range scanned {
		if !entry.Exists {
			fmt.Fprintf(out, "\n  ! notebook %q records root %s, which does not exist — pull refuses into a missing root\n", entry.Name, entry.Root)
		}
	}

	fmt.Fprintf(out, "\nNothing above moved. `grove notebook pull <name>` / `grove notebook share <name>` act.\n")
}

// joinLocalState renders the recorded tri-state symmetrically with the
// server's. "unshared" is not the same as "—": the first is D9's recorded
// decision to stop, the second is a notebook nobody ever answered for.
func joinLocalState(nb syncproto.NotebookDelta) string {
	state := nb.LocalShareState
	if nb.Direction == syncproto.DeltaDirectionPull {
		return "—"
	}
	if state == "" {
		state = "recorded"
	}
	if nb.LocalDuplicate {
		return state + " (duplicate id)"
	}
	return state
}

func joinServerState(nb syncproto.NotebookDelta) string {
	if nb.ServerShareState == "" {
		return "—"
	}
	return nb.ServerShareState
}

func joinDeltaSummary(nb syncproto.NotebookDelta) string {
	switch nb.Direction {
	case syncproto.DeltaDirectionShare:
		return "share: this server does not hold it"
	case syncproto.DeltaDirectionPull:
		if !nb.PullEligible {
			return "retained after an unshare; not on offer (D9)"
		}
		return "pull: this machine does not record it"
	}
	switch {
	case len(nb.LocalOnlyNotespaces) == 0 && len(nb.ServerOnlyNotespaces) == 0:
		return "same membership on both sides"
	default:
		return fmt.Sprintf("%d notespace%s here only, %d there only",
			len(nb.LocalOnlyNotespaces), plural(len(nb.LocalOnlyNotespaces)), len(nb.ServerOnlyNotespaces))
	}
}

// joinDeltaCounts is the evidence half of the same comparison. Zero-valued
// counts are kept: "0 notebooks only on the server" is the answer to a question
// the operator asked, and dropping it would leave the reader unsure whether it
// was zero or unexamined.
func joinDeltaCounts(delta syncproto.InventoryDelta) []transition.Count {
	var shareable, pullable, both int64
	var localOnly, serverOnly int64
	for _, nb := range delta.Notebooks {
		switch nb.Direction {
		case syncproto.DeltaDirectionShare:
			shareable++
		case syncproto.DeltaDirectionPull:
			pullable++
		default:
			both++
		}
		localOnly += int64(len(nb.LocalOnlyNotespaces))
		serverOnly += int64(len(nb.ServerOnlyNotespaces))
	}
	return []transition.Count{
		{Name: "notebooks-compared", Value: int64(len(delta.Notebooks))},
		{Name: "notebooks-here-only", Value: shareable},
		{Name: "notebooks-server-only", Value: pullable},
		{Name: "notebooks-both-sides", Value: both},
		{Name: "notespaces-here-only", Value: localOnly},
		{Name: "notespaces-server-only", Value: serverOnly},
		{Name: "notespaces-unparented-on-server", Value: int64(len(delta.UnparentedServerNotespaces))},
		// The duplicate count travels in the evidence, not only in the rendered
		// table: `--json` is what a script reads, and the D8 condition that
		// makes every acting verb refuse must not be visible in one rendering
		// and absent from the other.
		{Name: "notebooks-with-duplicate-ids", Value: int64(len(delta.DuplicateLocalNotebooks))},
	}
}

// recordedNotebookRoots reports the roots this comparison resolved, declared
// spelling and all, which is the transition contract's ResolvedRoots half.
func recordedNotebookRoots(scanned []recordedNotebook) []transition.ResolvedRoot {
	out := make([]transition.ResolvedRoot, 0, len(scanned))
	for _, entry := range scanned {
		out = append(out, transition.ResolvedRoot{Name: entry.Name, Declared: entry.Declared, Resolved: entry.Root})
	}
	return out
}
