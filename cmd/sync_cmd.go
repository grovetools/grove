package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/registry"
)

func init() {
	rootCmd.AddCommand(newSyncCmd())
}

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Notebook synchronization tools",
		Long: `Manage this machine's notebook synchronization with a grove-syncd server.

Subcommands:
  doctor — diagnose sync configuration and notebook health
  adopt  — bring an existing notebook workspace under replication

These are CLIENT-side verbs. Anything else after ` + "`grove sync`" + ` is passed
through to the grove-syncd SERVER binary, so ` + "`grove sync serve`" + ` and
` + "`grove sync token create`" + ` still reach the server they always did.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newSyncDoctorCmd())
	cmd.AddCommand(newSyncAdoptCmd())
	cmd.AddCommand(newSyncConflictsCmd())
	cmd.AddCommand(newSyncAdoptIDCmd())
	return cmd
}

// newSyncDoctorCmd implements `grove sync doctor`
func newSyncDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose sync configuration and notebook health",
		Long:  `Examine sync configuration and stamped notespace roots for local health issues.`,
		RunE:  runSyncDoctor,
	}
	return cmd
}

func runSyncDoctor(cmd *cobra.Command, args []string) error {
	issues := []string{}

	// 1. Check sync.toml existence and validity
	_, err := config.LoadSyncConfig()
	if err != nil {
		issues = append(issues, fmt.Sprintf("invalid sync.toml: %v", err))
	}
	// Note: Missing sync.toml is not an issue; sync is dark by default

	// 2. Load ecosystem config to get notebook definitions
	ecosystemCfg, err := config.LoadDefault()
	if err != nil {
		return fmt.Errorf("failed to load ecosystem config: %w", err)
	}

	// 3. Check each notebook for TCC blocks and .stfolder markers
	if ecosystemCfg.Notebooks != nil && ecosystemCfg.Notebooks.Definitions != nil {
		for notebookName, nb := range ecosystemCfg.Notebooks.Definitions {
			if nb.RootDir == "" {
				continue
			}
			// A declared spelling reaching ReadDir is not a loud failure, it is
			// a silent all-clear: "~/notebooks/x" does not exist, ReadDir
			// returns ENOENT rather than EPERM, and doctor reports no issues
			// for a notebook it never actually looked at.
			rootDir := coderoot.ExpandPath(nb.RootDir)

			// TCC detection: try to read the directory
			entries, err := os.ReadDir(rootDir)
			if err != nil {
				var pathErr *os.PathError
				if errors.As(err, &pathErr) && pathErr.Err == syscall.EPERM {
					issues = append(issues, fmt.Sprintf(
						"notebook %q: TCC-protected (macOS privacy control blocks access). "+
							"Move to a non-protected location or grant Terminal full disk access in System Settings > Privacy & Security > Full Disk Access",
						notebookName,
					))
				}
				continue
			}

			// Check for machine-suffixed orphan vaults (e.g., vault-machinename)
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				// Check for patterns like ".stfolder-<machine>" or backup dirs
				name := entry.Name()
				if len(name) > 10 && (name[:1] == "." || name[len(name)-10:] == "-XXXXXX") {
					issues = append(issues, fmt.Sprintf(
						"notebook %q: possible orphan vault directory: %s. "+
							"Safe to remove if no longer in use",
						notebookName, name,
					))
				}
			}

			// Check for dangling notebooks.toml entries in the workspace directory
			workspacesDir := filepath.Join(rootDir, "notespaces")
			if wsEntries, err := os.ReadDir(workspacesDir); err == nil {
				for _, wsEntry := range wsEntries {
					if !wsEntry.IsDir() {
						continue
					}
					notebooksTOML := filepath.Join(workspacesDir, wsEntry.Name(), "notebooks.toml")
					if _, err := os.Stat(notebooksTOML); os.IsNotExist(err) {
						// Not necessarily an issue; notebooks.toml is optional
						continue
					}
				}
			}
		}
	}

	// 4. Emit findings
	if len(issues) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "✓ no sync issues detected")
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Found %d issue(s):\n\n", len(issues))
	for i, issue := range issues {
		fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n\n", i+1, issue)
	}
	return nil
}

// newSyncAdoptCmd implements `grove sync adopt <workspace>`.
//
// Adoption is three things, and the daemon already owns all three: subscribe
// the workspace, make the daemon act on it now, and report what actually
// happened. It deliberately does no hashing of its own — the anti-entropy pass
// walks the tree, and a second walk here would only produce a number that
// disagrees with the one sync.db keeps.
func newSyncAdoptCmd() *cobra.Command {
	var (
		mode      string
		role      string
		pull      bool
		waitFor   time.Duration
		noKick    bool
		excludes  []string
		maxSizeMB int
	)
	cmd := &cobra.Command{
		Use:   "adopt <workspace>",
		Short: "Adopt a notebook workspace into notebook sync",
		Long: `Bring an existing notebook workspace under grove-syncd replication.

Three steps, in order:

  1. write the [[workspaces]] subscription into ~/.config/grove/sync.toml
     (append-only: an existing entry for this workspace is left exactly as you
     wrote it, and every other byte of the file is preserved);
  2. wait for the running daemon to reload and kick an immediate anti-entropy
     pass, so the workspace is reconciled now instead of at the next hourly one;
  3. poll the daemon's sync status and report the REAL counts — documents
     tracked, outbox pending, hydration progress.

Adoption never writes to the notebook tree. Hash-equal files are registered
against the server's document ids by the daemon's own reconcile; divergent
files surface as conflicts on the conflicts feed, where they can be inspected.

Identity routing is stamp/id based. Display names are accepted only to locate an
explicit configured tree; they never become server routing keys.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncAdopt(cmd.Context(), cmd.OutOrStdout(), syncAdoptOptions{
				workspace: args[0],
				mode:      mode,
				role:      role,
				pull:      pull,
				excludes:  excludes,
				maxSize:   int64(maxSizeMB) << 20,
				waitFor:   waitFor,
				noKick:    noKick,
			})
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "Subscription mode (full, plans-only, search-only); empty = the schema default")
	cmd.Flags().StringVar(&role, "role", config.SyncRolePeer, "Relationship with the peer holding this workspace (peer, registry, satellite)")
	cmd.Flags().BoolVar(&pull, "pull", true, "Also apply the server's changes locally (peer/registry roles only)")
	cmd.Flags().StringSliceVar(&excludes, "exclude", nil, "Glob to exclude from this workspace (repeatable)")
	cmd.Flags().IntVar(&maxSizeMB, "max-file-size-mb", 0, "Skip files larger than this (0 = the schema default)")
	cmd.Flags().DurationVar(&waitFor, "wait", 60*time.Second, "How long to wait for the daemon to pick the workspace up (0 = do not wait)")
	cmd.Flags().BoolVar(&noKick, "no-kick", false, "Do not ask the daemon for an immediate pass; wait for the scheduled one")
	return cmd
}

// syncAdoptOptions is one adoption request.
type syncAdoptOptions struct {
	workspace string
	mode      string
	role      string
	pull      bool
	excludes  []string
	maxSize   int64
	waitFor   time.Duration
	noKick    bool
}

func runSyncAdopt(ctx context.Context, out io.Writer, opts syncAdoptOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	workspaceName := strings.TrimSpace(opts.workspace)
	if workspaceName == "" {
		return fmt.Errorf("a workspace name is required")
	}

	syncCfg, err := config.LoadSyncConfig()
	if err != nil {
		return fmt.Errorf("failed to load sync configuration: %w", err)
	}
	if syncCfg == nil || syncCfg.Server == "" {
		return fmt.Errorf("no sync server is configured; run `grove join <server-url>` first")
	}

	// Locate the tree so a typo fails here rather than as a workspace that
	// silently syncs nothing. Resolution mirrors what the daemon does
	// (registry.WorkspaceRoot ~ syntheticNodeFor/nodeWorkspaceRoot), so the
	// path reported is the path that will be watched.
	ecosystemCfg, err := config.LoadDefault()
	if err != nil {
		return fmt.Errorf("failed to load ecosystem config: %w", err)
	}
	workspaceRoot := registry.WorkspaceRoot(ecosystemCfg, workspaceName)
	if workspaceRoot == "" {
		return fmt.Errorf("cannot resolve a local directory for workspace %q — check your notebook configuration", workspaceName)
	}
	if info, statErr := os.Stat(workspaceRoot); statErr != nil || !info.IsDir() {
		return fmt.Errorf("workspace %q resolves to %s, which does not exist; create it (or fix the name) before adopting", workspaceName, workspaceRoot)
	}
	fmt.Fprintf(out, "Workspace %q → %s\n", workspaceName, workspaceRoot)
	fmt.Fprintf(out, "Server:    %s\n", syncCfg.Server)

	// 1. Subscribe. The role-aware editor is the only writer, so a pull-enabled
	// entry under a push-only role is refused here rather than discovered later.
	entry := config.SyncWorkspace{
		Name:        workspaceName,
		Role:        strings.TrimSpace(opts.role),
		Mode:        strings.TrimSpace(opts.mode),
		Pull:        opts.pull,
		Excludes:    opts.excludes,
		MaxFileSize: opts.maxSize,
	}
	res, err := config.ApplySyncEdit(config.SyncConfigPath(), config.SyncEdit{
		Workspaces: []config.SyncWorkspace{entry},
		Note:       "Added by `grove sync adopt`.",
	})
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	added := len(res.Added) > 0
	if added {
		fmt.Fprintf(out, "✓ subscribed %q (role=%s, pull=%t) in %s\n", workspaceName, displayRole(entry.Role), entry.Pull, res.Path)
		config.ResetLoadCache()
	} else {
		fmt.Fprintf(out, "· %q is already subscribed in %s — left exactly as it is\n", workspaceName, res.Path)
	}

	// 2. Let the daemon see it, then kick.
	if opts.waitFor <= 0 {
		fmt.Fprintf(out, "\nNot waiting for the daemon (--wait 0). Run `grove status` to watch progress.\n")
		return nil
	}
	status, picked := waitForWorkspacePickup(ctx, out, workspaceName, opts.waitFor)
	reportSyncAuthFailure(out, status)
	if !picked {
		if status == nil {
			return fmt.Errorf("no running daemon answered; the subscription is written — start groved to begin replicating %q", workspaceName)
		}
		fmt.Fprintf(out, "\n! the daemon has not picked up %q within %s. The subscription is written;\n", workspaceName, opts.waitFor)
		fmt.Fprintf(out, "  it reloads on config change and at startup.\n")
		return nil
	}

	if !opts.noKick {
		if _, err := daemon.New().SyncRepush(ctx, workspaceName); err != nil {
			fmt.Fprintf(out, "· could not kick an immediate pass (%v); the scheduled one will cover it.\n", err)
		} else {
			fmt.Fprintf(out, "✓ anti-entropy pass kicked\n")
		}
	}

	// 3. Report what actually happened. These are the daemon's own counters,
	// which is the entire difference between this and what it replaced.
	final := pollAdoptionProgress(ctx, out, workspaceName, opts.waitFor)
	reportAdoptionResult(out, workspaceName, final)
	return nil
}

// pollAdoptionProgress follows the workspace until its hydration pass finishes
// or the budget runs out, printing progress as it goes. It returns the last
// status it managed to read.
func pollAdoptionProgress(ctx context.Context, out io.Writer, workspace string, budget time.Duration) *models.SyncStatus {
	deadline := time.Now().Add(budget)
	var last *models.SyncStatus
	sawRunning := false
	for {
		status := syncStatusSoft(ctx)
		if status != nil {
			last = status
			ws := workspaceStatus(status, workspace)
			switch {
			case ws == nil:
				// The daemon dropped it again — nothing more to poll.
				return last
			case ws.Hydration != nil && ws.Hydration.Running:
				sawRunning = true
				fmt.Fprintf(out, "  hydrating: %d scanned, %d enqueued, %d quarantined\n",
					ws.Hydration.Scanned, ws.Hydration.Enqueued, ws.Hydration.Quarantined)
			case sawRunning && ws.Hydration != nil && !ws.Hydration.Running:
				return last
			case status.OutboxPending == 0 && ws.Cursor > 0:
				// Nothing queued and the workspace has a cursor: converged.
				return last
			}
		}
		if time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(time.Second):
		}
	}
}

// reportAdoptionResult prints the real counters. Every number here comes from
// the daemon's sync.db; none is computed by this process.
func reportAdoptionResult(out io.Writer, workspace string, status *models.SyncStatus) {
	fmt.Fprintf(out, "\nAdoption status for %q:\n", workspace)
	if status == nil {
		fmt.Fprintf(out, "  <the daemon stopped answering; run `grove status` to check>\n")
		return
	}
	ws := workspaceStatus(status, workspace)
	if ws == nil {
		fmt.Fprintf(out, "  <the daemon is no longer tracking this workspace>\n")
		return
	}
	fmt.Fprintf(out, "  Documents tracked (all workspaces): %d\n", status.Documents)
	if status.DocumentsDiverged > 0 {
		fmt.Fprintf(out, "  Diverged:                          %d — inspect with `grove status`\n", status.DocumentsDiverged)
	}
	fmt.Fprintf(out, "  Outbox pending:                    %d (parked %d)\n", status.OutboxPending, status.OutboxParked)
	fmt.Fprintf(out, "  Cursor:                            %d\n", ws.Cursor)
	if !ws.LastSyncedAt.IsZero() {
		fmt.Fprintf(out, "  Last synced:                       %s\n", ws.LastSyncedAt.Format(time.RFC3339))
	}
	if h := ws.Hydration; h != nil {
		state := "finished"
		if h.Running {
			state = "running"
		}
		fmt.Fprintf(out, "  Hydration (%s):                 %d scanned, %d enqueued, %d quarantined\n",
			state, h.Scanned, h.Enqueued, h.Quarantined)
	}
	if status.OutboxPending > 0 {
		fmt.Fprintf(out, "\nStill draining. Watch it with `grove status` or the Notebook Sync panel.\n")
	} else {
		fmt.Fprintf(out, "\n✓ %q is adopted and converged.\n", workspace)
	}
}

// displayRole renders an empty role as what it means rather than as a blank.
func displayRole(role string) string {
	if role == "" {
		return "legacy/push-only"
	}
	return role
}
