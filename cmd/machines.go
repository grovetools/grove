package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/registry"
)

func init() {
	rootCmd.AddCommand(newMachinesCmd())
}

// newMachinesCmd implements `grove machines`: the fleet view.
//
// v1 reads the replicated notes straight off disk rather than going through a
// daemon endpoint. That is not a shortcut — the notes ARE the interchange
// format, so a machine whose daemon is not running can still answer "what do I
// know about my other machines", which is exactly when you tend to ask.
func newMachinesCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "machines",
		Short: "List the machines in this account's registry",
		Long: `List every machine that has published a presence note to the registry.

The registry is a reserved sync workspace holding one document per machine, at
machines/<machine-id>.md, written only by the machine it describes. This
command reads those documents directly from the local replica — no daemon
required, though a daemon is what keeps the replica current.

Staleness is ADVISORY. It compares each note's last_seen day against this
machine's clock; a peer that is simply powered off looks stale, and a peer with
a skewed clock looks wrong. It is a hint about who to go look at, not a health
check.

Trust: until device principals land, any token issued by the sync server can
write any machine's note. Rows are therefore checked, not trusted — a note
whose machine_id disagrees with its path, or whose revision counter went
backwards, is flagged rather than believed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMachines(cmd, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include this machine's own note in the listing")
	return cmd
}

func runMachines(cmd *cobra.Command, all bool) error {
	out := cmd.OutOrStdout()

	name, root, err := registry.Locate()
	if err != nil {
		if errors.Is(err, registry.ErrNoRegistry) {
			fmt.Fprintf(out, "No registry workspace is configured on this machine.\n\n")
			fmt.Fprintf(out, "A registry subscription is a sync.toml workspace entry with role = \"registry\".\n")
			fmt.Fprintf(out, "`grove join <server-url>` writes one.\n")
			return nil
		}
		return err
	}

	// Load, never EnsureIdentity: a listing must not mint state as a side
	// effect of being run.
	selfID := ""
	if id, lerr := machine.Load(); lerr == nil && id != nil {
		selfID = id.ID
	}

	machines, err := registry.ReadMachines(root, selfID)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Registry: %s\n", name)
	fmt.Fprintf(out, "Replica:  %s\n\n", filepath.Join(root, registry.MachinesDir))

	if len(machines) == 0 {
		fmt.Fprintf(out, "No machine notes yet.\n\n")
		fmt.Fprintf(out, "Notes appear once this machine's daemon has published its own and pulled\nits peers'. Check `grove sync status` if this persists.\n")
		return nil
	}

	shown := 0
	for _, m := range machines {
		if m.Self && !all {
			continue
		}
		shown++
		printMachineRow(out, m, time.Now())
	}
	if shown == 0 {
		fmt.Fprintf(out, "Only this machine has published a note. Run with --all to see it.\n")
	}
	return nil
}

// printMachineRow renders one machine: who it is, how long since it last said
// so, what it has, and what it says it wants but does not have.
func printMachineRow(out io.Writer, m registry.Machine, now time.Time) {
	marker := "✓"
	switch {
	case m.Suspicious():
		marker = "✗"
	case len(m.DeclaredMissing()) > 0:
		marker = "!"
	}

	self := ""
	if m.Self {
		self = " (this machine)"
	}
	fmt.Fprintf(out, "%s %s%s\n", marker, m.Label(), self)

	if m.Note == nil {
		for _, reason := range m.Suspect {
			fmt.Fprintf(out, "    %s\n", reason)
		}
		fmt.Fprintln(out)
		return
	}

	fmt.Fprintf(out, "    last seen  %s\n", humanizeStaleness(m, now))
	if m.Note.OriginID != "" {
		fmt.Fprintf(out, "    origin     %s\n", m.Note.OriginID)
	}
	if m.Note.GrovedVersion != "" {
		fmt.Fprintf(out, "    groved     %s\n", m.Note.GrovedVersion)
	}

	present, missing := 0, m.DeclaredMissing()
	for _, e := range m.Note.Ecosystems {
		if e.State == registry.StatePresent {
			present++
		}
	}
	fmt.Fprintf(out, "    ecosystems %d present, %d declared but missing\n", present, len(missing))
	for _, e := range missing {
		// Name the command, not the problem: a declared-but-missing ecosystem
		// is not an error, it is the materialization verb's input.
		fmt.Fprintf(out, "      ! %-18s %s\n", e.Name, e.Path)
		fmt.Fprintf(out, "        materialize with: grove ecosystem materialize %s\n", e.Name)
	}

	for _, reason := range m.Suspect {
		fmt.Fprintf(out, "    ! %s\n", reason)
	}
	if m.Suspicious() {
		fmt.Fprintf(out, "      (advisory: any sync token can write any machine's note today)\n")
	}
	fmt.Fprintln(out)
}

// humanizeStaleness renders last_seen at the resolution the note carries. The
// note stores a DAY, so "3 hours ago" would be a fiction; days are the honest
// unit.
func humanizeStaleness(m registry.Machine, now time.Time) string {
	d, ok := m.StaleFor(now)
	if !ok {
		return "unknown"
	}
	days := int(d.Hours() / 24)
	switch {
	case days <= 0:
		return "today (" + m.Note.LastSeen + ")"
	case days == 1:
		return "yesterday (" + m.Note.LastSeen + ")"
	default:
		return fmt.Sprintf("%d days ago (%s)", days, m.Note.LastSeen)
	}
}

// newMachineRetireCmd implements `grove machine retire <id>`.
func newMachineRetireCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "retire <machine-id>",
		Short: "Remove a decommissioned machine's note from the registry",
		Long: `Delete a machine's presence note from the registry.

Use this when a machine is gone for good — a rebuilt laptop, a destroyed VM —
and its note would otherwise sit in the listing forever, ageing.

The delete replicates like any other document change: this machine's daemon
pushes a document_deleted event and the note disappears from every peer. That
IS the tombstone; there is no separate marker.

ADVISORY, not authoritative. Under the interim trust model every token the sync
server issues is the owner, so any machine can retire any other machine's note
— and a machine that is still running will simply publish its note again on its
next write. Retiring is housekeeping for machines that are actually gone, not a
way to evict a machine you do not control. Real enforcement arrives with device
principals, which bind a note's path to the token that may write it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMachineRetire(cmd, args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func runMachineRetire(cmd *cobra.Command, id string, yes bool) error {
	out := cmd.OutOrStdout()

	_, root, err := registry.Locate()
	if err != nil {
		return err
	}

	if self, lerr := machine.Load(); lerr == nil && self != nil && self.ID == id {
		// Retiring your own note is self-erasure that undoes itself: the local
		// daemon rewrites it on its next pass. Refuse rather than perform a
		// no-op that looks like it worked.
		return fmt.Errorf("%s is THIS machine; its note is rewritten by the local daemon on the next pass. "+
			"To stop publishing, remove the role = \"registry\" subscription from sync.toml", machine.Describe("", id))
	}

	notePath := filepath.Join(root, filepath.FromSlash(registry.NotePath(id)))
	info, err := os.Stat(notePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no registry note for machine %q (looked in %s)", id, filepath.Dir(notePath))
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a machine note", notePath)
	}

	// Report who is about to be retired, from the note itself: an operator
	// pasting a ULID deserves to see the name attached to it before deleting.
	label := id
	if data, rerr := os.ReadFile(notePath); rerr == nil { //nolint:gosec // path derived from configured notebook root
		if note, perr := registry.ParseNote(data); perr == nil {
			label = machine.Describe(note.Name, id)
		}
	}

	if !yes {
		if err := confirmOrAbort(fmt.Sprintf("Retire %s from the registry?", label)); err != nil {
			return err
		}
	}
	if err := os.Remove(notePath); err != nil {
		return fmt.Errorf("failed to remove %s: %w", notePath, err)
	}

	fmt.Fprintf(out, "✓ retired %s\n", label)
	fmt.Fprintf(out, "  %s deleted; the daemon replicates the deletion to every peer.\n", notePath)
	fmt.Fprintf(out, "  If that machine is still running, it will publish a new note on its next write.\n")
	return nil
}
