package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/machine"
)

func init() {
	rootCmd.AddCommand(newMachineCmd())
}

func newMachineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machine",
		Short: "This machine's identity",
		Long: `Inspect and initialize this machine's grove identity.

Identity is split in two on purpose:

  ID    a ULID in ` + "`$XDG_STATE_HOME/grove/machine.json`" + ` — durable state, minted
        once on this host. It is never symlinked, never hand-edited, and never
        travels in a dotfiles repo.
  name  ` + "`[machine] name`" + ` in ~/.config/grove/machine.toml — intent, and
        dotfiles-portable on purpose. Restoring your dotfiles onto a new host
        gives it the same name and a brand-new ID, which is correct: it is a
        new machine with the same intent.

Because names collide after such a restore, every surface renders
"name (short id)" rather than the name alone.

Subcommands:
  init    — mint the identity (if absent) and record the display name
  status  — show id, name, subscriptions, config/state paths, sync origin
  retire  — remove a decommissioned machine's note from the registry

Legacy topology migration is the top-level ` + "`grove migrate`" + ` command.

To see the OTHER machines in this account, use ` + "`grove machines`" + `.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newMachineInitCmd())
	cmd.AddCommand(newMachineStatusCmd())
	cmd.AddCommand(newMachineRetireCmd())
	return cmd
}

// newMachineInitCmd implements `grove machine init`.
func newMachineInitCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Mint this machine's identity and record its display name",
		Long: `Mint this machine's ULID into $XDG_STATE_HOME/grove/machine.json (if it does
not already exist) and record its display name in ~/.config/grove/machine.toml.

Idempotent: re-running keeps the existing ID and leaves the name alone unless
--name asks for a different one. To deliberately become a NEW machine, delete
machine.json first — the next run mints a fresh ID.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMachineInit(cmd, name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Display name for this machine (default: existing name, else hostname)")
	return cmd
}

func runMachineInit(cmd *cobra.Command, name string) error {
	out := cmd.OutOrStdout()

	before, err := machine.Load()
	if err != nil {
		return fmt.Errorf("machine identity is unreadable — inspect %s before re-running: %w",
			machine.IdentityPath(), err)
	}

	id, err := machine.EnsureIdentity()
	if err != nil {
		return fmt.Errorf("failed to mint machine identity: %w", err)
	}
	if before == nil {
		fmt.Fprintf(out, "✓ minted machine id %s (%s)\n", id.ID, machine.IdentityPath())
	} else {
		fmt.Fprintf(out, "· machine id %s already minted %s (%s)\n",
			id.ID, id.MintedAt.Format(time.RFC3339), machine.IdentityPath())
	}

	cfgPath := config.MachineConfigPath()
	if cfgPath == "" {
		return fmt.Errorf("cannot resolve the grove config directory")
	}

	resolved := name
	if resolved == "" {
		// No explicit name: keep whatever machine.toml already declares, and
		// fall back to the hostname on a fresh machine.
		existing, lerr := config.LoadMachineConfigFrom(cfgPath)
		if lerr != nil {
			return lerr
		}
		if existing != nil && existing.Machine.Name != "" {
			resolved = existing.Machine.Name
		} else {
			resolved = config.DefaultMachineName()
		}
	}

	changed, err := config.WriteMachineName(cfgPath, resolved)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(out, "✓ machine name %q written to %s\n", resolved, cfgPath)
	} else {
		fmt.Fprintf(out, "· machine name %q already set in %s\n", resolved, cfgPath)
	}

	fmt.Fprintf(out, "\nThis machine is %s\n", machine.Describe(resolved, id.ID))
	return nil
}

// newMachineStatusCmd implements `grove machine status`.
func newMachineStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this machine's identity, name, and paths",
		Args:  cobra.NoArgs,
		RunE:  runMachineStatus,
	}
}

func runMachineStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	id, idErr := machine.Load()

	// Name resolution is reported with its source so "why is my machine
	// called that" is answerable without opening the file.
	cfgPath := config.MachineConfigPath()
	name, nameSource := config.DefaultMachineName(), "hostname (no [machine] name declared)"
	machineCfg, cfgErr := config.LoadMachineConfig()
	if cfgErr == nil && machineCfg != nil && machineCfg.Machine.Name != "" {
		name, nameSource = machineCfg.Machine.Name, cfgPath
	}

	switch {
	case idErr != nil:
		fmt.Fprintf(out, "Machine:  <unreadable identity: %v>\n", idErr)
	case id == nil:
		fmt.Fprintf(out, "Machine:  %s — no id yet; run `grove machine init`\n", name)
	default:
		fmt.Fprintf(out, "Machine:  %s\n", machine.Describe(name, id.ID))
		fmt.Fprintf(out, "ID:       %s (minted %s)\n", id.ID, id.MintedAt.Format(time.RFC3339))
	}

	fmt.Fprintf(out, "Name:     %s [from %s]\n", name, nameSource)
	if cfgErr != nil {
		fmt.Fprintf(out, "          warning: %v\n", cfgErr)
	}
	fmt.Fprintf(out, "Config:   %s\n", pathOrMissing(cfgPath))
	fmt.Fprintf(out, "State:    %s\n", pathOrMissing(machine.IdentityPath()))

	// Origin id lives in sync.db, which the global daemon owns — a soft
	// lookup, since identity does not depend on a running daemon.
	fmt.Fprintf(out, "Origin:   %s\n", softSyncOriginID())

	printMachineIntent(out)

	// The dead machines/ directory is a standing condition, reported here and
	// by `grove doctor` (legacy_machines_dir) — never by config load, which
	// every grove process performs and would turn one condition into one log
	// line per invocation.
	if legacy := config.LegacyMachinesDir(); legacy != "" {
		fmt.Fprintf(out, "\n! %s is ignored; migrate with `grove migrate`\n", legacy)
	}
	return nil
}

// printMachineIntent reports this machine's declared subscriptions reconciled
// against the disk. "declared but missing" is the whole reason intent is a
// first-class file: a subscription with nothing on disk is not an error, it is
// the input to `grove ecosystem materialize`.
//
// Computed locally rather than asked of the daemon: it is derived from config
// and the filesystem, and status has to work on a host where nothing is
// running yet.
func printMachineIntent(out io.Writer) {
	status, err := daemon.LocalMachineStatus()
	if err != nil {
		fmt.Fprintf(out, "\n! machine config is unreadable: %v\n", err)
		return
	}
	if len(status.Ecosystems) == 0 && len(status.Roots) == 0 {
		fmt.Fprintf(out, "\nNo recorded code roots declared. Run `grove migrate` to import legacy\n[groves.*], search_paths, and machine topology declarations.\n")
		return
	}

	if len(status.Ecosystems) > 0 {
		fmt.Fprintf(out, "\nEcosystems:\n")
		for _, eco := range status.Ecosystems {
			marker, note := "✓", ""
			switch eco.State {
			case "declared-missing":
				marker, note = "!", "  declared but missing — materialize it or drop the subscription"
			case "unmanifested":
				marker, note = "?", "  directory exists but carries no grove manifest"
			}
			suffix := ""
			if !eco.Enabled {
				suffix = " (disabled)"
			}
			if eco.Notebook != "" {
				suffix += " → " + eco.Notebook
			}
			fmt.Fprintf(out, "  %s %-18s %s%s\n", marker, eco.Name, eco.Path, suffix)
			if note != "" {
				fmt.Fprintf(out, "   %s\n", note)
			}
		}
	}

	if len(status.Roots) > 0 {
		fmt.Fprintf(out, "\nRoots:\n")
		for _, root := range status.Roots {
			marker := "✓"
			if !root.Exists {
				marker = "·" // absent roots are inert, not actionable
			}
			suffix := ""
			if !root.Enabled {
				suffix = " (disabled)"
			}
			if root.Notebook != "" {
				suffix += " → " + root.Notebook
			}
			fmt.Fprintf(out, "  %s %-18s %s%s\n", marker, root.Name, root.Path, suffix)
		}
	}
}

// pathOrMissing annotates a path that does not exist yet, so status never
// implies a file is there when it is not.
func pathOrMissing(path string) string {
	if path == "" {
		return "<unresolvable>"
	}
	if _, err := os.Stat(path); err != nil {
		return path + " (not present)"
	}
	return path
}

// softSyncOriginID asks the daemon for sync.db's origin id. Every failure
// mode — no daemon, sync not configured, a groved predating the endpoint —
// degrades to a dash rather than an error: the origin is a diagnostic, not
// part of this machine's identity.
func softSyncOriginID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// daemon.New connects to a running daemon and otherwise returns a local
	// client — it never auto-starts one just to answer a status question.
	status, err := daemon.New().GetSyncStatus(ctx)
	if err != nil || status == nil {
		return "- (daemon not reachable or sync not configured)"
	}
	if status.OriginID == "" {
		return "- (sync not configured)"
	}
	return status.OriginID
}
