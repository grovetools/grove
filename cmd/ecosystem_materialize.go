package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/registry"
)

// `grove ecosystem materialize` converges this machine toward a subscription.
//
// It is REPAIR-SHAPED, not install-shaped. Every step asks "is this already
// true?" and does nothing when it is, which makes a half-finished run safe to
// re-run and makes a complete one a no-op. That property is the whole reason
// materialization is a standing verb rather than a one-shot bootstrap: the
// interesting case is not the empty machine, it is the machine that has three
// of four repos, a subscription written last week, and a notebook that never
// got subscribed.
//
// The card is the input, and there are exactly two places to get one:
//
//   - --url: clone first, read the card off disk. Authoritative, because the
//     card ships inside the repo it describes.
//   - the registry: another machine's presence note embedded a copy. Convenient
//     and the reason `join` can offer a menu — but under the interim trust
//     model any token can write any note, so this path is CONFIRMATION-GATED
//     and refuses outright without a terminal. That gate comes out when device
//     principals land, and not before.
//
// A third case needs no card resolution at all: the ecosystem is already on
// disk with its own manifest. That is the repair path, and it reads the local
// card because a local card beats a replicated copy of one.

// materializeOptions is one materialization request.
type materializeOptions struct {
	name string
	url  string
	path string
	// notebookWorkspace is the notebook workspace to subscribe with
	// role = "peer". Defaults to the ecosystem name, which is the convention
	// (<notebook-root>/workspaces/<ecosystem>).
	notebookWorkspace string
	noNotebookSync    bool
	assumeYes         bool
	jobs              int
	waitFor           time.Duration
}

// newEcosystemMaterializeCmd is registered on the `ecosystem` noun in
// newEcosystemCmd, not on the root — materialization is an ecosystem verb.
func newEcosystemMaterializeCmd() *cobra.Command {
	opts := materializeOptions{waitFor: 30 * time.Second}
	cmd := &cobra.Command{
		Use:   "materialize <ecosystem>",
		Short: "Make this machine's disk agree with an ecosystem subscription",
		Long: `Clone and wire up an ecosystem this machine has declared but does not have.

Resolution order for the ecosystem's identity card:

  1. --url <git-url>   clone it, then read the card from the clone. The card
                       lives in the ecosystem's own grove.toml, so this is the
                       authoritative source.
  2. an existing checkout at the subscription's path — its own card is used and
     the run becomes a repair.
  3. the machine registry — another machine published a copy of the card in its
     presence note. This path is confirmation-gated: it prints every remote it
     would clone from and requires an explicit yes. In a non-interactive
     context it REFUSES rather than proceeding, because until device principals
     land any token issued by the sync server can write any machine's note.

What it does:

  - writes the [machine.ecosystems.<name>] subscription (if absent);
  - clones per the card's declared layout — superrepo (clone + submodule init +
    aborted-checkout self-heal) or flat (the remotes ARE the member repos);
  - subscribes this machine's notebook workspace with role = "peer" so notes
    replicate both ways;
  - kicks the sync daemon so the new workspace is scanned now rather than at
    the next hourly pass.

Every step is idempotent. Re-running a completed materialization reports what
is already true and writes nothing.

Nothing is ever removed: an existing checkout is converged, an existing remote
is never re-pointed, and sync.toml and machine.toml are edited surgically.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.name = args[0]
			return runEcosystemMaterialize(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.url, "url", "", "Clone from this git URL and read the card from the clone (skips the registry)")
	cmd.Flags().StringVar(&opts.path, "path", "", "Where to materialize it (default: the subscription's path, else ~/code/<name>)")
	cmd.Flags().StringVar(&opts.notebookWorkspace, "notebook-workspace", "", "Notebook workspace to subscribe with role=peer (default: the ecosystem name)")
	cmd.Flags().BoolVar(&opts.noNotebookSync, "no-notebook-sync", false, "Do not write a notebook sync subscription")
	cmd.Flags().BoolVar(&opts.assumeYes, "yes", false, "Skip the registry confirmation gate (required for non-interactive use)")
	cmd.Flags().IntVar(&opts.jobs, "jobs", ecosystemCloneJobsDefault, "Submodule clone parallelism")
	cmd.Flags().DurationVar(&opts.waitFor, "wait", 30*time.Second, "How long to wait for the daemon to pick up the notebook subscription (0 = do not wait)")
	return cmd
}

func runEcosystemMaterialize(ctx context.Context, in io.Reader, out io.Writer, opts materializeOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	name := strings.TrimSpace(opts.name)
	if name == "" {
		return fmt.Errorf("an ecosystem name is required")
	}

	existing, err := existingEcosystemSubscription(name)
	if err != nil {
		return err
	}
	pathIntent := opts.path
	if strings.TrimSpace(pathIntent) == "" && existing != nil {
		pathIntent = existing.Path
	}
	dest, err := resolveMaterializeDest(name, pathIntent)
	if err != nil {
		return err
	}

	card, source, err := resolveMaterializeCard(ctx, in, out, name, dest, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nCard for %q resolved from %s (layout %s).\n", name, source, card.Layout)

	// 1. Intent, before the expensive part. If the clone is interrupted the
	// subscription is still declared, which is exactly the state
	// `grove machine status` calls declared-missing and this command repairs.
	subscription := config.MachineEcosystem{Path: dest}
	if existing != nil {
		subscription = *existing
		subscription.Path = dest
	}
	cfgPath, changed, err := writeEcosystemSubscription(name, subscription)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(out, "✓ subscription %q → %s written to %s\n", name, dest, cfgPath)
	} else {
		fmt.Fprintf(out, "· subscription %q already declared in %s\n", name, cfgPath)
	}

	// 2. The tree.
	if len(subscription.Repos) > 0 || len(subscription.Exclude) > 0 {
		fmt.Fprintf(out, "Partial subscription: repos=%v exclude=%v. The full notebook still syncs.\n", subscription.Repos, subscription.Exclude)
		if card.Layout == config.LayoutSuperrepo {
			fmt.Fprintln(out, "Note: root-level builds and cross-repo plans may require all submodules; widen the subscription and re-run materialize to upgrade.")
		}
	}
	if err := cloneEcosystem(ctx, card, dest, ecosystemCloneOptions{Jobs: opts.jobs, Out: out, Subscription: subscription}); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ %s is materialized at %s\n", name, dest)

	// 3. The notebook. role = "peer" is what makes pull legal here: this
	// machine is mirroring its OWN notebook, not receiving a disposable VM's
	// writes, and the push-only invariant that protects the latter does not
	// apply to the former.
	if !opts.noNotebookSync {
		if err := subscribeNotebookWorkspace(ctx, out, name, opts); err != nil {
			return err
		}
	}

	// 4. The machine note. No explicit trigger is needed and none is invented:
	// the daemon's registry writer re-evaluates on every config reload
	// (SyncHandler.HandleStoreUpdate → kickRegistry), and the ConfigWatcher
	// broadcasts one for any write under ~/.config/grove — which both edits
	// above are. The note is rewritten only if its rendered bytes changed.
	fmt.Fprintf(out, "\nThis machine's registry note refreshes on the next daemon config reload.\n")
	fmt.Fprintf(out, "Check the result with:\n  grove machine status\n  grove machines\n")
	return nil
}

// resolveMaterializeDest picks the destination path: the flag, else this
// machine's existing subscription, else the conventional default.
func resolveMaterializeDest(name, flag string) (string, error) {
	dest := strings.TrimSpace(flag)
	if dest == "" {
		dest = defaultEcosystemPath(name)
	}
	dest = expandUserPath(dest)
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %q: %w", dest, err)
	}
	return abs, nil
}

// resolveMaterializeCard implements the three-way resolution and returns the
// card plus a human label for where it came from.
func resolveMaterializeCard(ctx context.Context, in io.Reader, out io.Writer, name, dest string, opts materializeOptions) (config.EcosystemCard, string, error) {
	// (1) --url: clone, then read. The clone is minimal — just enough to get a
	// manifest on disk — because cloneEcosystem converges the rest and must be
	// the only thing that knows what a layout means.
	if url := strings.TrimSpace(opts.url); url != "" {
		card, err := cardFromURL(ctx, out, url, dest)
		return card, "the clone at " + dest, err
	}

	// (2) an existing checkout. A local card beats a replicated copy of one,
	// and this is what makes a repair run need no registry at all.
	if manifest := config.FindEcosystemManifest(dest); manifest != "" {
		card, err := config.LoadEcosystemCard(manifest)
		if err != nil {
			return config.EcosystemCard{}, "", err
		}
		if card != nil {
			return *card, manifest, nil
		}
		return config.EcosystemCard{}, "", fmt.Errorf("%s exists but its manifest %s carries no [ecosystem] card; run `grove ecosystem adopt` there (or pass --url) so materialize knows how to converge it",
			dest, manifest)
	}

	// (3) the registry, behind the gate.
	offers, _, err := registryOffers()
	if err != nil {
		if errors.Is(err, registry.ErrNoRegistry) {
			return config.EcosystemCard{}, "", fmt.Errorf("no card for %q: nothing is at %s, this machine has no registry (run `grove join <server-url>`), and no --url was given",
				name, dest)
		}
		return config.EcosystemCard{}, "", err
	}
	offer, ok := findOfferByName(offers, name)
	if !ok {
		return config.EcosystemCard{}, "", fmt.Errorf("no machine in the registry publishes an ecosystem named %q (known: %s); pass --url to clone it directly",
			name, offerNames(offers))
	}
	if err := offer.Card.Validate(); err != nil {
		return config.EcosystemCard{}, "", fmt.Errorf("the registry's card for %q is not usable: %w", name, err)
	}

	renderOffer(out, offer, dest)
	if err := confirm(in, out, fmt.Sprintf("Clone %s into %s?", name, dest), opts.assumeYes); err != nil {
		if errors.Is(err, errNotConfirmed) {
			return config.EcosystemCard{}, "", fmt.Errorf("materialize refused: %w", err)
		}
		return config.EcosystemCard{}, "", err
	}
	return offer.Card, "the machine registry", nil
}

// cardFromURL clones url into dest (when dest is not already a checkout) and
// reads the card the clone carries.
func cardFromURL(ctx context.Context, out io.Writer, url, dest string) (config.EcosystemCard, error) {
	isRepo, err := gitRepoRoot(dest)
	if err != nil {
		return config.EcosystemCard{}, err
	}
	if !isRepo {
		if err := ensureCloneTarget(dest); err != nil {
			return config.EcosystemCard{}, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return config.EcosystemCard{}, fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		fmt.Fprintf(out, "Cloning %s into %s to read its ecosystem card...\n", url, dest)
		if err := runGit(ctx, "", out, "clone", url, dest); err != nil {
			return config.EcosystemCard{}, fmt.Errorf("clone %s: %w", url, err)
		}
	}

	manifest := config.FindEcosystemManifest(dest)
	if manifest == "" {
		return config.EcosystemCard{}, fmt.Errorf("%s has no grove manifest — %s does not look like a grove ecosystem", dest, url)
	}
	card, err := config.LoadEcosystemCard(manifest)
	if err != nil {
		return config.EcosystemCard{}, err
	}
	if card == nil {
		return config.EcosystemCard{}, fmt.Errorf("%s carries no [ecosystem] card; run `grove ecosystem adopt` in the source ecosystem so peers know its layout and remotes", manifest)
	}
	return *card, nil
}

// subscribeNotebookWorkspace writes the role = "peer" sync entry for this
// ecosystem's notebook workspace and, when it actually added one, asks the
// daemon to scan it now.
//
// The kick is conditional on purpose. POST /api/sync/repush both voids synced
// state and kicks anti-entropy; on a workspace just subscribed there is no
// synced state to void, so it is purely the kick. Firing it on a re-run would
// turn an idempotent verb into a full re-push of an already-healthy workspace.
func subscribeNotebookWorkspace(ctx context.Context, out io.Writer, name string, opts materializeOptions) error {
	workspace := strings.TrimSpace(opts.notebookWorkspace)
	if workspace == "" {
		workspace = name
	}
	syncPath := config.SyncConfigPath()
	if syncPath == "" {
		return fmt.Errorf("cannot resolve the sync config path")
	}

	res, err := config.ApplySyncEdit(syncPath, config.SyncEdit{
		Workspaces: []config.SyncWorkspace{{
			Name: workspace,
			Role: config.SyncRolePeer,
			Pull: true,
		}},
		Header: []string{
			"# Notebook sync client config — generated by `grove ecosystem materialize`.",
			"# Peer-role entries pull: this machine mirrors its own notebook.",
		},
		Note: "Added by `grove ecosystem materialize` (peer-role entry).",
	})
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	if len(res.Added) == 0 {
		fmt.Fprintf(out, "· notebook workspace %q is already subscribed in %s\n", workspace, res.Path)
		return nil
	}
	fmt.Fprintf(out, "✓ notebook workspace %q subscribed (role=peer) in %s\n", workspace, res.Path)
	config.ResetLoadCache()

	if opts.waitFor <= 0 {
		return nil
	}
	status, picked := waitForWorkspacePickup(ctx, out, workspace, opts.waitFor)
	reportSyncAuthFailure(out, status)
	if !picked {
		if status == nil {
			fmt.Fprintf(out, "! no running daemon answered — start one to begin replicating %q.\n", workspace)
		} else {
			fmt.Fprintf(out, "! the daemon has not picked up %q yet; it reloads on config change and at startup.\n", workspace)
		}
		return nil
	}
	if _, err := daemon.New().SyncRepush(ctx, workspace); err != nil {
		// A failed kick costs a delay, never correctness: the hourly
		// anti-entropy pass covers the same ground.
		fmt.Fprintf(out, "· could not kick an immediate sync pass (%v); the next scheduled pass will pick it up.\n", err)
		return nil
	}
	fmt.Fprintf(out, "✓ sync pass kicked for %q\n", workspace)
	return nil
}

func findOfferByName(offers []registry.Offer, name string) (registry.Offer, bool) {
	for _, o := range offers {
		if o.Name == name {
			return o, true
		}
	}
	return registry.Offer{}, false
}

func offerNames(offers []registry.Offer) string {
	if len(offers) == 0 {
		return "none"
	}
	names := make([]string, 0, len(offers))
	for _, o := range offers {
		names = append(names, o.Name)
	}
	return strings.Join(names, ", ")
}
