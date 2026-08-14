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
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/registry"
)

// Shared ground for the four adoption verbs — `grove join`, `grove subscribe`,
// `grove ecosystem materialize`, `grove sync adopt`.
//
// Three properties are common to all of them and are enforced here rather than
// four times over:
//
//  1. NOTHING IS DESTRUCTIVE. Every write is create-or-converge: config edits
//     go through the append-only sync editor or the surgical roots.toml
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

// --- recorded code roots ---

// writeEcosystemSubscription upserts one specific root in roots.toml and
// resets the config load cache so the caller's next read sees it.
func writeEcosystemSubscription(name string, entry coderoot.Root) (path string, changed bool, err error) {
	if err := ensureRecordedRouting(&entry); err != nil {
		return "", false, err
	}
	path = coderoot.RootsPath()
	if path == "" {
		return "", false, fmt.Errorf("cannot resolve the grove config directory")
	}
	changed, err = config.WriteCodeRoots(path, config.CodeRootEdits{
		Upserts: map[string]coderoot.Root{name: entry},
		Header: []string{
			"# This machine's recorded code roots.",
			"# Specific roots are materializable ecosystems; scan roots discover repos beneath a directory.",
		},
	})
	if changed {
		config.ResetLoadCache()
	}
	return path, changed, err
}

// ensureRecordedRouting makes a newly-authored root independently valid. New
// user flows use the longstanding "nb" convention when no routing exists;
// migration never calls this helper and therefore never infers legacy paths.
func ensureRecordedRouting(root *coderoot.Root) error {
	table, err := coderoot.Load()
	if err != nil {
		return err
	}
	name := root.Notebook
	if name == "" {
		name = table.Default
	}
	if name == "" {
		name = "nb"
	}
	if root.Notebook == "" {
		root.Notebook = name
	}
	if _, ok := table.Notebooks[name]; ok && table.Default != "" {
		return nil
	}
	edits := config.NotebookEdits{Upserts: map[string]coderoot.Notebook{}}
	if _, ok := table.Notebooks[name]; !ok {
		edits.Upserts[name] = coderoot.Notebook{Root: "~/notebooks/" + name}
	}
	if table.Default == "" {
		value := name
		edits.Default = &value
	}
	_, err = config.WriteNotebooks(coderoot.NotebooksPath(), edits)
	return err
}

func existingEcosystemSubscription(name string) (*coderoot.Root, error) {
	table, err := coderoot.Load()
	if err != nil {
		return nil, err
	}
	root, ok := table.Roots[name]
	if !ok || root.Scan {
		return nil, nil
	}
	return &root, nil
}

func declaredMissingEcosystems() ([]config.CodeRootState, error) {
	table, err := coderoot.Load()
	if err != nil {
		return nil, err
	}
	var out []config.CodeRootState
	for _, state := range config.ReconcileCodeRoots(table) {
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
	// Notebook sync is owned by the global daemon, not the cwd-scoped daemon.
	// Using daemon.New() here made `grove join` report "no running daemon" from
	// inside a worktree even while the global daemon was actively replicating.
	socketPath := strings.TrimSpace(os.Getenv(daemon.HostSocketEnv))
	if socketPath == "" {
		socketPath = paths.SocketPath()
	}
	client, err := daemon.NewRemoteClient(socketPath)
	if err != nil {
		return nil
	}
	status, err := client.GetSyncStatus(ctx)
	if err != nil {
		return nil
	}
	return status
}

// notespaceStatus finds one notespace's row in a sync status snapshot.
//
// The daemon reports each row by its immutable stamp id with the display name
// beside it, so both are accepted: callers here pass whatever the user typed,
// which is a name, while anything derived from an earlier status row is an id.
func notespaceStatus(status *models.SyncStatus, nameOrID string) *models.SyncNotespaceStatus {
	if status == nil || nameOrID == "" {
		return nil
	}
	for i := range status.Notespaces {
		if status.Notespaces[i].NotespaceName == nameOrID || status.Notespaces[i].NotespaceID == nameOrID {
			return &status.Notespaces[i]
		}
	}
	return nil
}

// repushTarget resolves the name a user typed to the notespace id POST
// /api/sync/repush selects on.
//
// Never fall back to the empty string: empty means EVERY notespace there, and
// a repush is not a read — it voids server-confirmed state and clears outboxes
// before re-pushing. Sending the unresolved name instead scopes the blast
// radius to nothing, which is the right way to fail. (The daemon used to be
// handed a `workspace` key it stopped reading, so every adopt of one workspace
// quietly reset all of them.)
func repushTarget(status *models.SyncStatus, name string) string {
	if ws := notespaceStatus(status, name); ws != nil && ws.NotespaceID != "" {
		return ws.NotespaceID
	}
	return name
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
			if notespaceStatus(status, name) != nil {
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
	if table, err := coderoot.Load(); err == nil {
		var parents []string
		for _, other := range table.SortedRootNames() {
			root := table.Roots[other]
			if root.Scan {
				continue
			}
			p := expandUserPath(root.Path)
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
