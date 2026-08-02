package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/registry"
)

func init() {
	rootCmd.AddCommand(newJoinCmd())
}

// defaultRegistryWorkspace is the working name of the reserved registry
// workspace. It is only a NAME: what makes a workspace the registry is
// role = "registry" (registry.Subscription), so an operator who prefers a
// different name still gets registry semantics.
const defaultRegistryWorkspace = "registry"

// joinOptions is what `grove join` was asked to do.
type joinOptions struct {
	server            string
	token             string
	tokenFile         string
	tokenCommand      string
	registryWorkspace string
	force             bool
	waitFor           time.Duration
	httpClient        *http.Client
	// configDir overrides the resolved grove config directory. Tests set it;
	// nothing on the command line does, because a user who wants a different
	// config directory sets XDG_CONFIG_HOME.
	configDir string
}

func newJoinCmd() *cobra.Command {
	opts := joinOptions{registryWorkspace: defaultRegistryWorkspace, waitFor: 30 * time.Second}
	cmd := &cobra.Command{
		Use:   "join <server-url>",
		Short: "Join this machine to a grove-syncd server",
		Long: `Point this machine at a grove-syncd server and subscribe to the machine
registry.

What it does, in order:

  1. mints this machine's identity if it has none (a ULID in XDG state);
  2. reads the bearer token (--token, --token-file, GROVE_SYNC_TOKEN, or a
     prompt) and writes it to <config>/sync.token with mode 0600;
  3. VERIFIES the token live against POST <server>/sync/capabilities;
  4. only then writes sync.toml — server, token_command, and a
     role = "registry" workspace entry;
  5. creates the registry workspace directory so the daemon can replicate into
     it, and reports the ecosystems other machines have published.

Step 3 is the point of the command. A token that is not accepted never reaches
your config, because a rejected token in sync.toml leaves the daemon retrying a
401 forever while every status surface still looks healthy.

Restoring your dotfiles onto a new host is the SUPPORTED FAST PATH, not an edge
case: machine.toml arrives with your subscriptions already declared, this
command mints a fresh identity for the new host, and it finishes by listing
what you have declared but not yet materialized.

Nothing here is destructive. sync.toml is append-merged (existing content stays
byte-for-byte), and an existing sync.token with different content is refused
rather than overwritten unless you pass --force.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.server = args[0]
			return runJoin(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.token, "token", "", "Bearer token for the sync server (else $GROVE_SYNC_TOKEN, else prompt)")
	cmd.Flags().StringVar(&opts.tokenFile, "token-file", "", "Read the bearer token from this file instead of prompting")
	cmd.Flags().StringVar(&opts.tokenCommand, "token-command", "", "Shell command that prints the token; written to sync.toml verbatim instead of a token file")
	cmd.Flags().StringVar(&opts.registryWorkspace, "registry-workspace", defaultRegistryWorkspace, "Name of the reserved registry workspace")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Overwrite an existing sync.token whose contents differ")
	cmd.Flags().DurationVar(&opts.waitFor, "wait", 30*time.Second, "How long to wait for the daemon to pick up the new subscription (0 = do not wait)")
	return cmd
}

func runJoin(ctx context.Context, in io.Reader, out io.Writer, opts joinOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	server := strings.TrimRight(strings.TrimSpace(opts.server), "/")
	if server == "" {
		return fmt.Errorf("a sync server URL is required")
	}
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		return fmt.Errorf("sync server URL %q must start with http:// or https://", opts.server)
	}
	workspaceName := strings.TrimSpace(opts.registryWorkspace)
	if workspaceName == "" {
		workspaceName = defaultRegistryWorkspace
	}

	configDir := opts.configDir
	if configDir == "" {
		configDir = paths.ConfigDir()
	}
	if configDir == "" {
		return fmt.Errorf("cannot resolve the grove config directory")
	}

	// 1. Identity. A machine writes its own registry note and stamps its
	// DeviceID onto every handshake, so joining without an id would produce a
	// machine that syncs but never appears in the fleet.
	id, err := machine.EnsureIdentity()
	if err != nil {
		return fmt.Errorf("failed to mint machine identity: %w", err)
	}
	name := config.ResolveMachineName()
	fmt.Fprintf(out, "Joining as %s\n", machine.Describe(name, id.ID))

	// 2. Token.
	token, tokenCommand, err := resolveJoinToken(in, out, opts)
	if err != nil {
		return err
	}

	tokenPath := filepath.Join(configDir, syncTokenFileName)
	wroteToken := false
	if tokenCommand == "" {
		wroteToken, err = writeSyncTokenFile(tokenPath, token, opts.force)
		if err != nil {
			return err
		}
		tokenCommand = "cat " + tokenPath
	}

	// 3. Verify BEFORE persisting config. On rejection, a token file this run
	// created is removed again — leaving a rejected credential behind would be
	// the same trap one step earlier.
	fmt.Fprintf(out, "Verifying the token against %s/sync/capabilities…\n", server)
	if verr := verifySyncTokenOverHTTP(ctx, opts.httpClient, server, token, id.ID); verr != nil {
		if wroteToken {
			_ = os.Remove(tokenPath)
		}
		return verr
	}
	fmt.Fprintf(out, "✓ token accepted by %s\n", server)
	if wroteToken {
		fmt.Fprintf(out, "✓ token written to %s (0600)\n", tokenPath)
	}

	// 4. Config. The role-aware editor is append-only: an existing sync.toml
	// keeps every byte it had, and a workspace it already declares is left
	// exactly as the user wrote it.
	syncPath := filepath.Join(configDir, syncConfigFileName)
	res, err := config.ApplySyncEdit(syncPath, config.SyncEdit{
		Server:       server,
		TokenCommand: tokenCommand,
		Workspaces: []config.SyncWorkspace{{
			Name: workspaceName,
			Role: config.SyncRoleRegistry,
			Pull: true,
		}},
		Header: []string{
			"# Notebook sync client config — generated by `grove join`.",
			"# The registry-role entry below pulls: it is this account's machine",
			"# inventory, replicated to every machine that joins.",
		},
		Note: "Added by `grove join` (registry-role entry).",
	})
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	switch {
	case res.Created:
		fmt.Fprintf(out, "✓ wrote %s (server + registry subscription %q)\n", res.Path, workspaceName)
	case len(res.Added) > 0:
		fmt.Fprintf(out, "✓ appended registry subscription %q to %s\n", workspaceName, res.Path)
	default:
		fmt.Fprintf(out, "· %s already subscribes to %q — left untouched\n", res.Path, workspaceName)
	}
	config.ResetLoadCache()

	// 5. Local registry root. The daemon's syntheticNodeFor prefers a workspace
	// root that already EXISTS, so creating the directory now is what pins this
	// subscription to the notebook chosen here rather than to whatever a later
	// rule picks. PlannedRoot — not WorkspaceRoot — because only it names a
	// notebook explicitly instead of falling through to the locator's
	// home-anchored default, which would put the registry somewhere nothing
	// else reads.
	if err := ensureRegistryRoot(out, workspaceName); err != nil {
		return err
	}

	// 6. Let the daemon pick it up, then read what replicated.
	if opts.waitFor > 0 {
		status, picked := waitForWorkspacePickup(ctx, out, workspaceName, opts.waitFor)
		reportSyncAuthFailure(out, status)
		switch {
		case picked:
			fmt.Fprintf(out, "✓ the daemon is syncing %q\n", workspaceName)
		case status == nil:
			fmt.Fprintf(out, "\n! no running daemon answered — start one with `groved` (or `grove daemon start`)\n")
			fmt.Fprintf(out, "  to begin replicating. The configuration above is complete.\n")
		default:
			fmt.Fprintf(out, "\n! the daemon has not picked up %q yet; it reloads on config change and at startup.\n", workspaceName)
		}
	}

	reportJoinNextSteps(out)
	return nil
}

// resolveJoinToken determines the bearer token and the token_command to record.
//
// A --token-command is the one mode where grove never sees the secret: the
// command is written into sync.toml and run by the daemon. It still has to be
// run ONCE here, because a token_command that does not yield a working token
// is the same silent-401 trap as a stale token file.
func resolveJoinToken(in io.Reader, out io.Writer, opts joinOptions) (token, tokenCommand string, err error) {
	switch {
	case strings.TrimSpace(opts.tokenCommand) != "":
		value, rerr := runSyncTokenCommand(opts.tokenCommand)
		if rerr != nil {
			return "", "", fmt.Errorf("--token-command failed; it must print the token on stdout: %w", rerr)
		}
		return value, strings.TrimSpace(opts.tokenCommand), nil
	case strings.TrimSpace(opts.token) != "":
		return strings.TrimSpace(opts.token), "", nil
	case strings.TrimSpace(opts.tokenFile) != "":
		data, rerr := os.ReadFile(expandUserPath(strings.TrimSpace(opts.tokenFile)))
		if rerr != nil {
			return "", "", fmt.Errorf("failed to read --token-file: %w", rerr)
		}
		if strings.TrimSpace(string(data)) == "" {
			return "", "", fmt.Errorf("--token-file %s is empty", opts.tokenFile)
		}
		return strings.TrimSpace(string(data)), "", nil
	case strings.TrimSpace(os.Getenv("GROVE_SYNC_TOKEN")) != "":
		return strings.TrimSpace(os.Getenv("GROVE_SYNC_TOKEN")), "", nil
	}

	if !stdinIsTTY() {
		return "", "", fmt.Errorf("no sync token supplied: pass --token, --token-file, --token-command, or set GROVE_SYNC_TOKEN (there is no terminal to prompt on)")
	}
	fmt.Fprintf(out, "Sync token for the server (input hidden): ")
	value, rerr := readSecret(in)
	fmt.Fprintln(out)
	if rerr != nil {
		return "", "", rerr
	}
	if strings.TrimSpace(value) == "" {
		return "", "", fmt.Errorf("no sync token entered")
	}
	return strings.TrimSpace(value), "", nil
}

// ensureRegistryRoot creates the registry workspace directory so the daemon
// and every read surface resolve it to the same place.
//
// It REFUSES when this machine declares no notebooks, rather than accepting the
// locator's `~/.grove/notebooks/nb` fallback. Creating a notebook tree at a
// path the user never configured is not a helpful default: the registry would
// replicate into a directory no other grove surface reads, and on a machine
// whose real notebooks live elsewhere it would look like the feature silently
// did nothing.
func ensureRegistryRoot(out io.Writer, workspaceName string) error {
	cfg, err := config.LoadDefault()
	if err != nil {
		return fmt.Errorf("failed to load grove config: %w", err)
	}
	root := registry.PlannedRoot(cfg, workspaceName)
	if root == "" {
		fmt.Fprintf(out, "\n! this machine declares no notebooks, so there is nowhere to put the registry.\n")
		fmt.Fprintf(out, "  The sync configuration above is written and valid. Declare a notebook —\n")
		fmt.Fprintf(out, "    [notebooks.definitions.<name>]\n    root_dir = \"~/notebooks/<name>\"\n")
		fmt.Fprintf(out, "  — then re-run `grove join %s` to create the registry workspace.\n", strings.TrimRight(cfgServerHint(), "/"))
		return nil
	}
	if mkErr := os.MkdirAll(filepath.Join(root, "machines"), 0o755); mkErr != nil {
		return fmt.Errorf("failed to create the registry workspace at %s: %w", root, mkErr)
	}
	fmt.Fprintf(out, "✓ registry workspace %q ready at %s\n", workspaceName, root)
	return nil
}

// cfgServerHint reads back the server just persisted, so the remediation above
// can quote the exact command to re-run.
func cfgServerHint() string {
	if syncCfg, err := config.LoadSyncConfig(); err == nil && syncCfg != nil {
		return syncCfg.Server
	}
	return "<server-url>"
}

// runSyncTokenCommand executes a sync token_command exactly the way the daemon
// will, so what join verifies is what groved will later present.
//
// Two deliberate differences from the satellite runTokenCommand next door:
// this one uses `sh -c`, matching config.SyncConfig.ResolveToken (the satellite
// one uses $SHELL, matching its bootstrap), and it does not consult
// GROVE_SYNC_TOKEN. ResolveToken prefers that variable, but a token that
// happens to be exported in this shell says nothing about whether the command
// works — and the command, not the variable, is what gets persisted.
func runSyncTokenCommand(command string) (string, error) {
	out, err := exec.Command("sh", "-c", command).Output() //nolint:gosec // G204: the command is the operator's own --token-command
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("command produced no output")
	}
	return token, nil
}

// readSecret reads one line without echoing it when stdin is a real terminal,
// and falls back to a plain read otherwise (a piped stdin has no echo to
// suppress).
func readSecret(in io.Reader) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		data, err := term.ReadPassword(int(f.Fd()))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// writeSyncTokenFile persists the bearer token at 0600. It reports whether it
// wrote anything.
//
// An existing file with the SAME token is left alone (idempotent re-join); one
// with different content is refused unless --force, because silently replacing
// a working credential is how you take a machine offline without noticing.
func writeSyncTokenFile(path, token string, force bool) (bool, error) {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && strings.TrimSpace(string(existing)) == strings.TrimSpace(token):
		// Already exactly this token; still make sure the mode is right.
		_ = os.Chmod(path, 0o600)
		return false, nil
	case err == nil && !force:
		return false, fmt.Errorf("%s already holds a different token; re-run with --force to replace it (nothing else has been written)", path)
	case err != nil && !os.IsNotExist(err):
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return false, fmt.Errorf("failed to create %s: %w", filepath.Dir(path), mkErr)
	}
	if wErr := os.WriteFile(path, []byte(strings.TrimSpace(token)+"\n"), 0o600); wErr != nil {
		return false, fmt.Errorf("failed to write %s: %w", path, wErr)
	}
	return true, nil
}

// reportJoinNextSteps closes the run with what this machine can now do: the
// ecosystems the registry advertises, and — the dotfiles-restore fast path —
// what it already declared but does not have.
func reportJoinNextSteps(out io.Writer) {
	missing, err := declaredMissingEcosystems()
	if err != nil {
		fmt.Fprintf(out, "\n! machine config is unreadable: %v\n", err)
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "\nDeclared but not present on this machine:\n")
		for _, m := range missing {
			suffix := ""
			if !m.Enabled {
				suffix = " (disabled)"
			}
			fmt.Fprintf(out, "  ! %-20s %s%s\n", m.Name, m.Path, suffix)
		}
		fmt.Fprintf(out, "\nMaterialize them with:\n")
		for _, m := range missing {
			fmt.Fprintf(out, "  grove ecosystem materialize %s\n", m.Name)
		}
		return
	}

	offers, _, err := registryOffers()
	if err != nil {
		if !errors.Is(err, registry.ErrNoRegistry) {
			fmt.Fprintf(out, "\n· registry not readable yet: %v\n", err)
		}
		return
	}
	if len(offers) == 0 {
		fmt.Fprintf(out, "\nNo ecosystems are published in the registry yet. They appear as soon as\nanother machine's presence note replicates here (or as soon as this machine\nwrites its own).\n")
		return
	}
	fmt.Fprintf(out, "\nEcosystems published in the registry:\n")
	for _, offer := range offers {
		remote := ""
		if r, ok := offer.PrimaryRemote(); ok {
			remote = r.URL
		}
		flag := ""
		if offer.Conflicting {
			flag = "  !! machines disagree about this card"
		}
		fmt.Fprintf(out, "  %-20s %-10s %s%s\n", offer.Name, offer.Card.Layout, remote, flag)
	}
	fmt.Fprintf(out, "\nMaterialize one with:\n  grove ecosystem materialize <name>\n")
}
