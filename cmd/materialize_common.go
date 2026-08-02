package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/registry"
)

// Shared ground for the four adoption verbs — `grove join`, `grove subscribe`,
// `grove ecosystem materialize`, `grove sync adopt`.
//
// Three properties are common to all of them and are enforced here rather than
// four times over:
//
//  1. NOTHING IS DESTRUCTIVE. Every write is create-or-converge: config edits
//     go through the append-only sync editor or the surgical machine.toml
//     writer, clones repair rather than replace, and no verb removes anything
//     from the laptop tree.
//  2. THE CONFIRMATION GATE IS REAL. Consuming registry data before device
//     principals land means trusting a document any token could have written,
//     so a human must see the remotes first. A non-TTY run without the explicit
//     flag REFUSES — an unattended materialize is precisely the thing that must
//     not exist yet.
//  3. CONFIG WRITES ARE READ BACK. These verbs mutate config and then resolve
//     against it in the same process, so every write is followed by a load
//     cache reset (config.ResetLoadCache) before anything reads.

// stdinIsTTY reports whether this process can actually ask the user something.
// It is the gate's precondition, not a cosmetic check.
func stdinIsTTY() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// errNotConfirmed is returned when a gate is refused, either by the user
// answering no or by there being no user to ask.
var errNotConfirmed = errors.New("not confirmed")

// confirm asks a yes/no question on the terminal. `assumeYes` is the caller's
// explicit --yes flag; it short-circuits BOTH the prompt and the TTY check,
// which is what makes scripted use possible without weakening the default.
func confirm(in io.Reader, out io.Writer, question string, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !stdinIsTTY() {
		return fmt.Errorf("%w: refusing to continue without confirmation in a non-interactive context; re-run with --yes if you have reviewed the plan above", errNotConfirmed)
	}
	fmt.Fprintf(out, "%s [y/N]: ", question)
	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && answer == "" {
		return fmt.Errorf("%w: %v", errNotConfirmed, err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("%w: declined", errNotConfirmed)
	}
}

// --- machine.toml subscriptions ---

// writeEcosystemSubscription upserts one [machine.ecosystems.<name>] entry and
// resets the config load cache so the caller's next read sees it.
//
// It reports whether the file changed, which is what makes `subscribe` and a
// re-run of `materialize` able to say "already declared" instead of implying
// they did something.
func writeEcosystemSubscription(name string, entry config.MachineEcosystem) (path string, changed bool, err error) {
	path = config.MachineConfigPath()
	if path == "" {
		return "", false, fmt.Errorf("cannot resolve the grove config directory")
	}
	changed, err = config.WriteMachineSubscriptions(path, config.MachineSubscriptions{
		Ecosystems: map[string]config.MachineEcosystem{name: entry},
		Header: []string{
			"# This machine's grove intent — name, ecosystem subscriptions, bare roots.",
			"# Dotfiles-portable on purpose: restoring it onto a new host declares the",
			"# same intent there, and `grove ecosystem materialize` fills in what is missing.",
		},
	})
	if changed {
		config.ResetLoadCache()
	}
	return path, changed, err
}

// existingEcosystemSubscription returns this machine's declared subscription
// for name, or nil when it declares none.
func existingEcosystemSubscription(name string) (*config.MachineEcosystem, error) {
	cfg, err := config.LoadMachineConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	if eco, ok := cfg.Machine.Ecosystems[name]; ok {
		return &eco, nil
	}
	return nil, nil
}

// declaredMissingEcosystems returns the subscriptions this machine declares
// but does not have on disk — the materialization verb's input, and what join
// offers immediately after a dotfiles restore.
func declaredMissingEcosystems() ([]config.MachineEcosystemState, error) {
	cfg, err := config.LoadMachineConfig()
	if err != nil {
		return nil, err
	}
	var out []config.MachineEcosystemState
	for _, state := range config.ReconcileMachineEcosystems(cfg) {
		if state.Missing() {
			out = append(out, state)
		}
	}
	return out, nil
}

// --- registry reads ---

// registryOffers reads the machine notes replicated into this machine's
// registry workspace and returns the ecosystems they advertise.
//
// A missing registry is not an error here: `materialize --url` and a
// dotfiles-restore materialize both work without one, and only the card
// resolution path needs to complain.
func registryOffers() (offers []registry.Offer, root string, err error) {
	_, root, err = registry.Locate()
	if err != nil {
		return nil, "", err
	}
	selfID := ""
	if id, lerr := machine.Load(); lerr == nil && id != nil {
		selfID = id.ID
	}
	machines, err := registry.ReadMachines(root, selfID)
	if err != nil {
		return nil, root, err
	}
	return registry.Offers(machines), root, nil
}

// renderOffer prints what materializing an offer would actually do — the
// contents of the confirmation gate. It names every remote, not just the
// primary, because "what will be cloned from where" is the whole question a
// user is being asked to answer.
func renderOffer(out io.Writer, offer registry.Offer, dest string) {
	fmt.Fprintf(out, "\nEcosystem:  %s\n", offer.Name)
	if offer.Card.ID != "" {
		fmt.Fprintf(out, "ID:         %s\n", offer.Card.ID)
	}
	fmt.Fprintf(out, "Layout:     %s\n", offer.Card.Layout)
	fmt.Fprintf(out, "Destination: %s\n", dest)
	if len(offer.Card.Remotes) == 0 {
		fmt.Fprintf(out, "Remotes:    <none declared>\n")
	} else {
		fmt.Fprintf(out, "Clone from:\n")
		primary, _ := offer.PrimaryRemote()
		for _, r := range offer.Card.Remotes {
			marker := " "
			if r == primary {
				marker = "*"
			}
			fmt.Fprintf(out, "  %s %-10s %s\n", marker, r.Name, r.URL)
		}
	}
	if len(offer.Publishers) > 0 {
		fmt.Fprintf(out, "Published by: %s\n", strings.Join(offer.Publishers, ", "))
	}
	fmt.Fprintf(out, "\n! This card comes from the machine registry, which is integrity-ADVISORY:\n")
	fmt.Fprintf(out, "  until device principals land, any token issued by the sync server can write\n")
	fmt.Fprintf(out, "  any machine's note. Read the URLs above before answering.\n")
	if offer.Conflicting {
		fmt.Fprintf(out, "\n!! Machines disagree about this ecosystem's identity or remotes. The card shown\n")
		fmt.Fprintf(out, "   is the first one read. Resolve the disagreement before materializing.\n")
	}
}

// --- daemon-facing helpers ---

// syncStatusSoft asks the daemon for sync status, returning nil when there is
// no daemon, no sync, or a groved predating the endpoint. Every adoption verb
// treats the daemon as optional: it is what makes replication happen, not what
// makes the config write valid.
func syncStatusSoft(ctx context.Context) *models.SyncStatus {
	status, err := daemon.New().GetSyncStatus(ctx)
	if err != nil {
		return nil
	}
	return status
}

// workspaceStatus finds one workspace's row in a sync status snapshot.
func workspaceStatus(status *models.SyncStatus, name string) *models.SyncWorkspaceStatus {
	if status == nil {
		return nil
	}
	for i := range status.Workspaces {
		if status.Workspaces[i].Name == name {
			return &status.Workspaces[i]
		}
	}
	return nil
}

// waitForWorkspacePickup polls until the daemon reports the named workspace,
// which is how a caller knows the config write actually reached the running
// sync handler (the ConfigWatcher on ~/.config/grove drives the reload).
//
// It returns the last status it saw, and whether the workspace appeared. A
// false return is NOT an error: the subscription is written either way, and the
// caller's job is to say "start/restart groved" rather than to fail.
func waitForWorkspacePickup(ctx context.Context, out io.Writer, name string, timeout time.Duration) (*models.SyncStatus, bool) {
	deadline := time.Now().Add(timeout)
	announced := false
	var last *models.SyncStatus
	for {
		status := syncStatusSoft(ctx)
		if status != nil {
			last = status
			if workspaceStatus(status, name) != nil {
				return status, true
			}
		}
		if time.Now().After(deadline) {
			return last, false
		}
		if !announced {
			fmt.Fprintf(out, "  waiting for the daemon to pick up the subscription…\n")
			announced = true
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// reportSyncAuthFailure prints the daemon's standing token rejection, if any.
// It is worth its own line everywhere sync is involved: every other counter
// stays plausible while a 401 loop replicates nothing at all.
func reportSyncAuthFailure(out io.Writer, status *models.SyncStatus) {
	if status == nil || status.AuthError == "" {
		return
	}
	fmt.Fprintf(out, "\n! the sync server is REJECTING this machine's token: %s\n", status.AuthError)
	if !status.AuthErrorSince.IsZero() {
		fmt.Fprintf(out, "  since %s — nothing is replicating until it is fixed.\n", status.AuthErrorSince.Format(time.RFC3339))
	}
}

// --- paths ---

// defaultEcosystemPath proposes where to put an ecosystem this machine has
// never had. Preference order, most-specific first:
//
//  1. an existing subscription's path (a re-run must land on the same tree);
//  2. the parent directory of an ecosystem this machine already has, so a
//     second one lands beside the first rather than somewhere new;
//  3. ~/code/<name>, the conventional default.
//
// It is only ever a PROPOSAL: --path overrides it and the confirmation gate
// shows what it picked.
func defaultEcosystemPath(name string) string {
	if existing, err := existingEcosystemSubscription(name); err == nil && existing != nil && existing.Path != "" {
		return expandUserPath(existing.Path)
	}
	if cfg, err := config.LoadMachineConfig(); err == nil && cfg != nil {
		var parents []string
		for _, other := range sortedEcosystemNames(cfg) {
			p := expandUserPath(cfg.Machine.Ecosystems[other].Path)
			if p == "" {
				continue
			}
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				parents = append(parents, filepath.Dir(p))
			}
		}
		if len(parents) > 0 {
			sort.Strings(parents)
			return filepath.Join(parents[0], name)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join("code", name)
	}
	return filepath.Join(home, "code", name)
}

func sortedEcosystemNames(cfg *config.MachineConfig) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Machine.Ecosystems))
	for name := range cfg.Machine.Ecosystems {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
