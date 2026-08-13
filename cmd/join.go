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
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/registry"
	"github.com/grovetools/core/pkg/subject"
	"github.com/grovetools/core/pkg/syncproto"
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
	enrollCode        string
	registryWorkspace string
	// machineName pins this machine's one spelling. Empty means "whatever
	// config.ResolveMachineName resolves"; either way the resolved value is
	// used for the identity line, the token description and the keychain
	// account, and is written into machine.toml so the three cannot drift.
	machineName string
	// mint delegates token creation to `grove forge token create`.
	mint bool
	// repair converges an existing config without touching credentials.
	repair     bool
	force      bool
	waitFor    time.Duration
	httpClient *http.Client
	// configDir overrides the resolved grove config directory. Tests set it;
	// nothing on the command line does, because a user who wants a different
	// config directory sets XDG_CONFIG_HOME.
	configDir string
	// Test seams for the two things that reach outside this process.
	mintFn         func(ctx context.Context, machineName string) (mintReceipt, error)
	deriveServerFn func() (server, provenance string, err error)
}

func newJoinCmd() *cobra.Command {
	opts := joinOptions{registryWorkspace: defaultRegistryWorkspace, waitFor: 30 * time.Second}
	cmd := &cobra.Command{
		Use:   "join [server-url]",
		Short: "Enroll this machine with a grove-syncd server and start replicating",
		Long: `Enroll this machine: point it at a grove-syncd server, approve its device
fingerprint, subscribe it to the machine registry, and confirm it is actually
replicating.

New servers use the locally-held device key to mint a short-lived session; no
static token is required. Join prints the full fingerprint and waits for peer
approval. --code can carry a short-lived single-use enrollment voucher. The
legacy --token, --token-file, --token-command, and --mint paths remain explicit
fallbacks for old servers and service credentials.

With a forge configured, the server URL is DERIVED (from [forge.services]
domain and [forge.services.syncd] port). An explicit URL argument stays
supported for any other grove-syncd.

Every line this command prints is a POST-CONDITION read back from the artifact
it names — the config re-parsed after writing, the credential resolved the way
the daemon will resolve it, the daemon queried, the registry counted. The
closing claim is the conjunction of those facts, and a false conjunct exits
NONZERO. A command that reports what it attempted rather than what it achieved
is how a machine ends up configured, silent, and believed to be working.

Common shapes:

  · 'grove join' — derive the server, enroll this device, and await approval;
  · 'grove join --code <code>' — consume a bounded voucher for immediate approval;
  · 'grove join --token-file <path>' — explicitly use legacy token fallback;
  · 'grove join --repair' — converge an existing configuration without minting;
  · 'grove join https://host:8788' — use a grove-syncd that is not a grove forge.

Nothing here is destructive. sync.toml is converged, never rewritten: absent
keys are filled, declared keys are left exactly as you wrote them, and an
existing sync.token whose contents differ is refused rather than replaced
unless you pass --force.

This is the ENROLLMENT verb and it stays that: device approval, the credential,
the registry subscription and the registry root. Its P3 sibling
` + "`grove sync join`" + ` is the RELATIONSHIP verb — it records which server this
machine talks to and reports the notebook delta, and mutates nothing else. Run
this one once per machine; run that one whenever you want to see what the server
holds that this machine does not.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.server = args[0]
			}
			return runJoin(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.token, "token", "", "Explicit legacy bearer token fallback for the sync server")
	cmd.Flags().StringVar(&opts.tokenFile, "token-file", "", "Read the bearer token from this file instead of prompting")
	cmd.Flags().StringVar(&opts.tokenCommand, "token-command", "", "Shell command that prints a legacy token; written to sync.toml verbatim")
	cmd.Flags().StringVar(&opts.enrollCode, "code", "", "Single-use device enrollment code minted by `grove machines enroll-code`")
	cmd.Flags().BoolVar(&opts.mint, "mint", false, "Mint this machine's token via `grove forge token create` and store it in the keychain")
	cmd.Flags().BoolVar(&opts.repair, "repair", false, "Converge an existing config (fill absent keys) using the credential it already declares; mint and prompt for nothing")
	cmd.Flags().StringVar(&opts.machineName, "machine-name", "", "This machine's name — used for the registry, the token description and the keychain account (default: [machine] name, else hostname)")
	cmd.Flags().StringVar(&opts.registryWorkspace, "registry-workspace", defaultRegistryWorkspace, "Name of the reserved registry workspace")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Overwrite an existing sync.token whose contents differ")
	cmd.Flags().DurationVar(&opts.waitFor, "wait", 30*time.Second, "How long to wait for the daemon to pick up the new subscription (0 = do not wait, and do not claim replication)")
	return cmd
}

// errEnrollmentIncomplete is the nonzero exit for a run whose steps did not all
// hold. The per-step lines carry the diagnosis; this only has to be an error.
var errEnrollmentIncomplete = errors.New("enrollment incomplete — this machine is not replicating")

func runJoin(ctx context.Context, in io.Reader, out io.Writer, opts joinOptions) error {
	if ctx == nil {
		ctx = context.Background()
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
	syncPath := filepath.Join(configDir, syncConfigFileName)
	rep := &joinReporter{out: out}

	// 1. Identity, in ONE spelling. A machine writes its own registry note and
	// stamps its DeviceID onto every handshake, so joining without an id would
	// produce a machine that syncs but never appears in the fleet. The NAME is
	// resolved once here and then threaded through the registry note, the token
	// description and the keychain account — three surfaces that, left to
	// derive it separately, showed three different names for one laptop.
	id, err := machine.EnsureIdentity()
	if err != nil {
		return fmt.Errorf("failed to mint machine identity: %w", err)
	}
	name, pinned, err := pinMachineName(opts.machineName)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Joining as %s\n", machine.Describe(name, id.ID))
	if pinned {
		fmt.Fprintf(out, "  %-*s [machine] name = %q — pinned so the registry, the token description\n", joinLabelWidth, "name", name)
		fmt.Fprintf(out, "  %-*s and the keychain account cannot drift apart\n", joinLabelWidth, "")
	}
	deviceKey, err := devicekey.Ensure()
	if err != nil {
		return fmt.Errorf("failed to ensure device signing key: %w", err)
	}

	// 2. Server: the argument if given, otherwise derived from the forge.
	server, provenance, err := resolveJoinServer(opts)
	if err != nil {
		return err
	}
	rep.info("server", server, provenance)

	// 3. Device first. A v2 server needs no couriered bearer: enroll this
	// machine, wait for an owner to approve the displayed fingerprint, and
	// prove the approved key can mint a session before writing configuration.
	deviceOnly, deviceErr := completeDeviceJoin(ctx, opts.httpClient, server, name, opts.enrollCode, deviceKey, opts.waitFor, rep)
	if deviceErr != nil && !legacyJoinRequested(opts, syncPath) {
		return deviceErr
	}

	// Explicit legacy inputs remain a migration/service fallback. They are
	// resolved only when the device path was unavailable or rejected; a
	// successful device join never prompts for or persists a static token.
	tokenCommand := ""
	if !deviceOnly {
		rep.info("device", "device-session join unavailable", "using explicit legacy credential fallback")
		token, resolvedCommand, err := resolveJoinToken(ctx, in, rep, opts, syncPath, name)
		if err != nil {
			return err
		}
		tokenCommand = resolvedCommand
		tokenPath := filepath.Join(configDir, syncTokenFileName)
		wroteToken := false
		if tokenCommand == "" {
			wroteToken, err = writeSyncTokenFile(tokenPath, token, opts.force)
			if err != nil {
				return err
			}
			tokenCommand = "cat " + tokenPath
			if wroteToken {
				rep.ok("token", "written to "+tokenPath, "0600")
			} else {
				rep.ok("token", tokenPath, "already held this token")
			}
		}
		var capabilities syncproto.CapabilitiesResponse
		if verr := verifySyncTokenOverHTTP(ctx, opts.httpClient, server, token, id.ID, &capabilities); verr != nil {
			if wroteToken {
				_ = os.Remove(tokenPath)
			}
			rep.fail("verify", "POST "+server+"/sync/capabilities", "not accepted")
			return verr
		}
		rep.ok("verify", "POST "+server+"/sync/capabilities", "accepted (legacy token)")
		if capabilities.Capabilities.DeviceEnrollment {
			enrolled, enrollErr := enrollDeviceWithCodeOverHTTP(ctx, opts.httpClient, server, name, opts.enrollCode, deviceKey)
			if enrollErr != nil {
				rep.fail("device", machine.Describe(name, id.ID), "not enrolled")
				return enrollErr
			}
			rep.ok("device", machine.Describe(name, id.ID), enrolled.Status+" — fingerprint "+enrolled.Fingerprint)
		}
	}

	// 4. Config. The editor is additive: absent keys are filled, declared keys
	// keep the values the user wrote, and existing entries survive verbatim.
	if cerr := applyAndVerifyJoinConfig(rep, syncPath, server, tokenCommand, workspaceName, deviceOnly); cerr != nil {
		if errors.Is(cerr, errEnrollmentIncomplete) {
			// The step line above carries the diagnosis and the remedy. Fall
			// through to the closing claim rather than returning bare, so the
			// run always ends with an explicit verdict.
			return rep.conclude(false, opts.waitFor > 0)
		}
		return cerr
	}
	config.ResetLoadCache()

	// 6. Local registry root. The daemon's syntheticNodeFor prefers a workspace
	// root that already EXISTS, so creating the directory now is what pins this
	// subscription to the notebook chosen here rather than to whatever a later
	// rule picks.
	registryRoot := ensureRegistryRoot(rep, workspaceName)

	// 7. Replication. This is what enrollment is FOR, so it is checked, not
	// assumed — and when it is not checked, the closing claim says so instead
	// of quietly widening.
	replicating := false
	switch {
	case opts.waitFor <= 0:
		rep.skip("daemon", "not waited for (--wait 0)")
	default:
		replicating = reportDaemonPickup(ctx, rep, workspaceName, opts.waitFor)
	}
	if replicating {
		reportRegistryReplication(rep, registryRoot)
	}

	reportJoinNextSteps(out, registryRoot)
	return rep.conclude(replicating, opts.waitFor > 0)
}

// ---- step reporting ---------------------------------------------------------

// joinReporter renders one line per step and remembers whether every one of
// them held.
//
// The format is deliberately uniform — `label  subject … ✓ outcome` — because
// the outcome half is the part that must be read back from the artifact. A
// step that cannot say what it achieved gets `skip`, which is neither a pass
// nor a failure but does forbid the closing "and replicating".
type joinReporter struct {
	out     io.Writer
	failed  bool
	skipped bool
}

const joinLabelWidth = 9

func (r *joinReporter) info(label, subject, annotation string) {
	if annotation == "" {
		fmt.Fprintf(r.out, "  %-*s %s\n", joinLabelWidth, label, subject)
		return
	}
	fmt.Fprintf(r.out, "  %-*s %-38s [%s]\n", joinLabelWidth, label, subject, annotation)
}

func (r *joinReporter) ok(label, subject, outcome string) {
	fmt.Fprintf(r.out, "  %-*s %s … ✓ %s\n", joinLabelWidth, label, subject, outcome)
}

func (r *joinReporter) skip(label, reason string) {
	r.skipped = true
	fmt.Fprintf(r.out, "  %-*s %s\n", joinLabelWidth, label, reason)
}

func (r *joinReporter) fail(label, subject, outcome string, remedy ...string) {
	r.failed = true
	fmt.Fprintf(r.out, "  %-*s %s … ✗ %s\n", joinLabelWidth, label, subject, outcome)
	for _, line := range remedy {
		fmt.Fprintf(r.out, "  %-*s %s\n", joinLabelWidth, "", line)
	}
}

func (r *joinReporter) warn(text string) {
	fmt.Fprintf(r.out, "  %-*s ! %s\n", joinLabelWidth, "", text)
}

// conclude prints the closing claim and decides the exit status. The claim is
// a CONJUNCTION of the facts above it: "enrolled and replicating" requires
// every step to have held AND replication to have been observed, not merely
// configured.
func (r *joinReporter) conclude(replicating, waited bool) error {
	switch {
	case r.failed:
		fmt.Fprintf(r.out, "\n✗ %v\n", errEnrollmentIncomplete)
		return errEnrollmentIncomplete
	case replicating:
		fmt.Fprintf(r.out, "\n✓ enrolled and replicating.\n")
		return nil
	case !waited:
		// Not a failure: the caller asked not to wait, so nothing here has any
		// evidence about replication and the line must not imply otherwise.
		fmt.Fprintf(r.out, "\n✓ enrolled. Replication was not checked (--wait 0).\n")
		return nil
	default:
		fmt.Fprintf(r.out, "\n✗ %v\n", errEnrollmentIncomplete)
		return errEnrollmentIncomplete
	}
}

// ---- server ----------------------------------------------------------------

// resolveJoinServer returns the syncd base URL and where it came from.
func resolveJoinServer(opts joinOptions) (server, provenance string, err error) {
	if raw := strings.TrimSpace(opts.server); raw != "" {
		server = strings.TrimRight(raw, "/")
		if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
			return "", "", fmt.Errorf("sync server URL %q must start with http:// or https://", opts.server)
		}
		return server, "given", nil
	}
	derive := opts.deriveServerFn
	if derive == nil {
		derive = deriveForgeSyncServer
	}
	server, provenance, err = derive()
	if err != nil {
		if errors.Is(err, errNoForgeToDeriveFrom) {
			return "", "", fmt.Errorf("no server to join: this machine configures no [forge], so there is nothing to derive from — pass the grove-syncd URL as an argument (`grove join https://host:8788`)")
		}
		return "", "", err
	}
	return strings.TrimRight(server, "/"), provenance, nil
}

// ---- identity ---------------------------------------------------------------

// pinMachineName resolves this machine's one name and writes it into
// machine.toml when the file declares none.
//
// PINNING is the point. With no [machine] name declared, every surface falls
// back to the hostname independently, so the registry showed
// "Matthews-MacBook-Air.local" while the token was described "solair" and the
// keychain account was "solair" — three spellings of one machine, and the
// distinction is load-bearing for revocation. Writing it down once makes them
// the same string by construction.
func pinMachineName(requested string) (name string, pinned bool, err error) {
	name = strings.TrimSpace(requested)
	if name == "" {
		name = config.ResolveMachineName()
	}
	if name == "" {
		return "", false, fmt.Errorf("cannot resolve a name for this machine — pass --machine-name")
	}
	path := config.MachineConfigPath()
	if path == "" {
		return name, false, nil
	}
	if cfg, lerr := config.LoadMachineConfig(); lerr == nil && cfg != nil &&
		strings.TrimSpace(cfg.Machine.Name) != "" && requested == "" {
		// Already pinned and the caller did not ask for a different one.
		return name, false, nil
	}
	changed, werr := config.WriteMachineName(path, name)
	if werr != nil {
		// Not fatal. A machine that cannot record its name still enrolls; it
		// just keeps re-deriving one, which is the drift this prevents rather
		// than a precondition for anything below.
		return name, false, nil
	}
	if changed {
		config.ResetLoadCache()
	}
	return name, changed, nil
}

func completeDeviceJoin(ctx context.Context, client *http.Client, server, name, code string, key *devicekey.Key, wait time.Duration, rep *joinReporter) (bool, error) {
	if _, err := discoverSyncIdentity(ctx, client, server); err != nil {
		return false, err
	}
	enrolled, err := enrollDeviceWithCodeOverHTTP(ctx, client, server, name, code, key)
	if err != nil {
		rep.info("device", machine.Describe(name, key.DeviceID()), "device enrollment unavailable")
		return false, err
	}
	rep.info("device", machine.Describe(name, key.DeviceID()), enrolled.Status+" — fingerprint "+enrolled.Fingerprint)
	deadline := time.Now().Add(wait)
	for enrolled.Status == syncproto.DeviceStatusPending && wait > 0 && time.Now().Before(deadline) {
		delay := 250 * time.Millisecond
		if remaining := time.Until(deadline); remaining < delay {
			delay = remaining
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(delay):
			}
		}
		enrolled, err = enrollDeviceWithCodeOverHTTP(ctx, client, server, name, code, key)
		if err != nil {
			return false, err
		}
	}
	if enrolled.Status != syncproto.DeviceStatusApproved {
		rep.info("approval", machine.Describe(name, key.DeviceID()), "still "+enrolled.Status+"; compare fingerprint "+enrolled.Fingerprint)
		fmt.Fprintf(rep.out, "  %-*s run `grove machines approve %s` on an enrolled owner machine\n", joinLabelWidth, "", key.DeviceID())
		return false, fmt.Errorf("device enrollment is %s; approval is required before this machine can join", enrolled.Status)
	}
	rep.ok("approval", machine.Describe(name, key.DeviceID()), "approved")
	session, err := establishDeviceSession(ctx, client, server, key)
	if err != nil {
		rep.info("verify", "signed device handshake", "not accepted")
		return false, err
	}
	rep.ok("verify", "signed device handshake", "session expires "+session.SessionExpiresAt)
	return true, nil
}

func legacyJoinRequested(opts joinOptions, syncPath string) bool {
	if opts.repair || opts.mint || strings.TrimSpace(opts.token) != "" || strings.TrimSpace(opts.tokenFile) != "" || strings.TrimSpace(opts.tokenCommand) != "" || strings.TrimSpace(os.Getenv(config.SyncTokenEnvVar)) != "" {
		return true
	}
	_, _, ok := existingJoinCredential(syncPath)
	return ok
}

// ---- credential -------------------------------------------------------------

// resolveJoinToken determines the bearer token and the token_command to record.
//
// The modes, in precedence order: --repair (whatever the config already
// declares), --mint (delegate to the forge recipe), --token-command, --token,
// --token-file, $GROVE_SYNC_TOKEN, prompt.
//
// A --token-command is the mode where grove never holds the secret at rest:
// the command is written into sync.toml and run by the daemon. It still has to
// be run ONCE here, because a token_command that does not yield a working token
// is the same silent-401 trap as a stale token file. --mint is that same mode
// with the command's other end created for you.
func resolveJoinToken(ctx context.Context, in io.Reader, rep *joinReporter, opts joinOptions, syncPath, machineName string) (token, tokenCommand string, err error) {
	switch {
	case opts.repair:
		return resolveRepairToken(rep, opts, syncPath)

	case opts.mint:
		mint := opts.mintFn
		if mint == nil {
			mint = mintForgeToken
		}
		receipt, merr := mint(ctx, machineName)
		if merr != nil {
			rep.fail("token", "minting for "+machineName, "not minted")
			return "", "", merr
		}
		value, rerr := runSyncTokenCommand(receipt.TokenCommand)
		if rerr != nil {
			// The token exists on the server but this machine cannot read it
			// back, which is the failure the keychain write-then-verify guard
			// exists to catch. Say which store, never the value.
			rep.fail("token", "reading back from "+receipt.Stored, "stored value is not readable")
			return "", "", fmt.Errorf("`forge token create` reported the token stored in %s (%s/%s), but reading it back produced nothing usable — the credential is minted; re-run `grove join --repair` once the store is fixed: %w",
				receipt.Stored, receipt.Service, receipt.Account, rerr)
		}
		rep.ok("token", "minted for "+machineName,
			fmt.Sprintf("stored (%s %s/%s), server hash %s", receipt.Stored, receipt.Service, receipt.Account, receipt.HashPrefix))
		return value, strings.TrimSpace(receipt.TokenCommand), nil

	case strings.TrimSpace(opts.tokenCommand) != "":
		value, rerr := runSyncTokenCommand(opts.tokenCommand)
		if rerr != nil {
			return "", "", fmt.Errorf("--token-command failed; it must print the token on stdout: %w", rerr)
		}
		rep.ok("token", "--token-command", "resolved")
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

	// Nothing was supplied. Before prompting, see whether the machine is
	// already holding a credential: re-running `grove join` on an enrolled
	// machine must be a no-op that still verifies, not a password prompt.
	if value, command, ok := existingJoinCredential(syncPath); ok {
		rep.ok("token", describeTokenSource(command), "resolved from the existing config")
		return value, command, nil
	}

	if !stdinIsTTY() {
		return "", "", fmt.Errorf("no sync token supplied: pass --mint, --token, --token-file, --token-command, or set GROVE_SYNC_TOKEN (there is no terminal to prompt on)")
	}
	fmt.Fprintf(rep.out, "Sync token for the server (input hidden): ")
	value, rerr := readSecret(in)
	fmt.Fprintln(rep.out)
	if rerr != nil {
		return "", "", rerr
	}
	if strings.TrimSpace(value) == "" {
		return "", "", fmt.Errorf("no sync token entered")
	}
	return strings.TrimSpace(value), "", nil
}

// resolveRepairToken is --repair's whole credential story: use what the config
// already declares, and refuse rather than invent one.
//
// This is why --repair needed no separate write path. Convergence fills absent
// keys; repair is convergence with the credential fixed, so the only thing it
// adds is a mode that will not prompt, will not mint, and will not write a new
// token file.
func resolveRepairToken(rep *joinReporter, opts joinOptions, syncPath string) (token, tokenCommand string, err error) {
	if value, command, ok := existingJoinCredential(syncPath); ok {
		rep.ok("token", describeTokenSource(command), "resolved (--repair minted nothing)")
		return value, command, nil
	}
	rep.fail("token", "--repair", "this machine declares no usable credential")
	return "", "", fmt.Errorf("--repair converges a configuration that already has a credential, and this one has none: %s declares no token_command and %s does not exist — run `grove join --mint` (or pass --token-command) to establish one",
		syncPath, filepath.Join(filepath.Dir(syncPath), syncTokenFileName))
}

// existingJoinCredential resolves the credential this machine already holds,
// the way the daemon would: the config's token_command, then its literal
// token, then the sync.token file join itself writes.
func existingJoinCredential(syncPath string) (token, tokenCommand string, ok bool) {
	if cfg, err := config.LoadSyncConfigFrom(syncPath); err == nil && cfg != nil {
		if command := strings.TrimSpace(cfg.TokenCommand); command != "" {
			if value, rerr := runSyncTokenCommand(command); rerr == nil {
				return value, command, true
			}
			return "", "", false
		}
		if literal := strings.TrimSpace(cfg.Token); literal != "" {
			// A declared literal is left declared: returning an empty
			// token_command keeps the editor from adding a second source.
			return literal, "", true
		}
	}
	tokenPath := filepath.Join(filepath.Dir(syncPath), syncTokenFileName)
	if data, err := os.ReadFile(tokenPath); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data)), "cat " + tokenPath, true
	}
	return "", "", false
}

// describeTokenSource labels a token_command without repeating a command that
// may itself carry a secret in its text (`echo hunter2`). Only the shapes grove
// writes are named; anything else is described by its config key.
func describeTokenSource(command string) string {
	switch {
	case command == "":
		return "the token declared in sync.toml"
	case strings.HasPrefix(command, "security find-generic-password"):
		return "the keychain item named by token_command"
	case strings.HasPrefix(command, "cat "):
		return strings.TrimSpace(strings.TrimPrefix(command, "cat "))
	default:
		return "sync.toml token_command"
	}
}

// ---- config -----------------------------------------------------------------

// applyAndVerifyJoinConfig writes the sync config and then READS IT BACK,
// resolving it the way the daemon will.
//
// The read-back is the entire discipline this command exists to demonstrate.
// `grove join` used to print "the configuration above is complete" from the
// fact that the write returned no error — which stayed true for a config with
// a subscription and no server, because the merge path never wrote one.
func applyAndVerifyJoinConfig(rep *joinReporter, syncPath, server, tokenCommand, workspaceName string, deviceOnly ...bool) error {
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
		rep.fail("config", syncPath, "not written")
		return err
	}
	for _, w := range res.Warnings {
		rep.warn(w)
	}

	// Post-condition: what the DAEMON will read, not what we believe we wrote.
	written, err := config.LoadSyncConfigFrom(syncPath)
	if err != nil {
		rep.fail("config", syncPath, "written but not re-readable")
		return err
	}
	if written == nil {
		rep.fail("config", syncPath, "does not exist after the write")
		return errEnrollmentIncomplete
	}

	var missing []string
	if strings.TrimSpace(written.Server) == "" {
		missing = append(missing, "server")
	}
	usesDeviceSession := len(deviceOnly) > 0 && deviceOnly[0]
	if !usesDeviceSession && strings.TrimSpace(written.TokenCommand) == "" && strings.TrimSpace(written.Token) == "" {
		missing = append(missing, "a credential (token_command or token)")
	}
	if !declaresWorkspace(written, workspaceName) {
		missing = append(missing, "the "+workspaceName+" subscription")
	}
	if len(missing) > 0 {
		rep.fail("config", syncPath, "incomplete: no "+strings.Join(missing, ", "),
			"the existing file declares these keys, or declares them empty, and",
			"convergence never overwrites what you wrote — edit them by hand,",
			"then re-run: grove join --repair")
		return errEnrollmentIncomplete
	}

	var did []string
	if res.Created {
		did = append(did, "created")
	}
	if len(res.Filled) > 0 {
		did = append(did, "filled "+strings.Join(res.Filled, ", "))
	}
	if len(res.Added) > 0 {
		did = append(did, "added subscription "+strings.Join(res.Added, ", "))
	}
	if len(res.Present) > 0 && len(res.Added) == 0 {
		did = append(did, "already subscribes to "+strings.Join(res.Present, ", "))
	}
	if len(did) == 0 {
		did = append(did, "already complete")
	}
	rep.ok("config", syncPath, strings.Join(did, "; ")+" — reads back with server, credential and subscription")
	return nil
}

func declaresWorkspace(cfg *config.SyncConfig, name string) bool {
	for _, ws := range cfg.Workspaces {
		if ws.Name == name {
			return true
		}
	}
	return false
}

// ---- registry root ----------------------------------------------------------

// ensureRegistryRoot creates the registry workspace directory so the daemon
// and every read surface resolve it to the same place, and returns the root it
// settled on ("" when there is none).
//
// It REFUSES when this machine declares no notebooks, rather than accepting the
// locator's `~/.grove/notebooks/nb` fallback. Creating a notebook tree at a
// path the user never configured is not a helpful default: the registry would
// replicate into a directory no other grove surface reads.
func ensureRegistryRoot(rep *joinReporter, workspaceName string) string {
	cfg, err := config.LoadDefault()
	if err != nil {
		rep.fail("registry", "grove config", "unreadable: "+err.Error())
		return ""
	}
	root := registry.PlannedRoot(cfg, workspaceName)
	if root == "" {
		rep.fail("registry", "workspace "+workspaceName, "this machine declares no notebooks",
			"declare one, then re-run `grove join --repair`:",
			"  [notebooks.definitions.<name>]",
			"  root_dir = \"~/notebooks/<name>\"")
		return ""
	}
	if mkErr := os.MkdirAll(filepath.Join(root, "machines"), 0o755); mkErr != nil {
		rep.fail("registry", root, "could not be created: "+mkErr.Error())
		return ""
	}
	// Read back: the directory must exist as a directory, because that is what
	// syntheticNodeFor will look for.
	if info, statErr := os.Stat(filepath.Join(root, "machines")); statErr != nil || !info.IsDir() {
		rep.fail("registry", root, "not a directory after creation")
		return ""
	}
	// Join is the explicit registry materialization verb. Bind the created root
	// to immutable identity immediately; a role-bearing display name in
	// sync.toml is not routing authority.
	machineCfg, _ := config.LoadMachineConfig()
	if machineCfg == nil || machineCfg.Sync.Registry == nil {
		localSubject := subject.MintLocal().String()
		stamp, stampErr := notespace.MintNotespace(root, notespace.NotespaceMutable{Name: workspaceName, Subject: localSubject, Kind: "registry"})
		if stampErr != nil {
			rep.fail("registry", root, "identity stamp failed: "+stampErr.Error())
			return ""
		}
		notebookName := ""
		if cfg.Notebooks != nil && cfg.Notebooks.Rules != nil {
			notebookName = cfg.Notebooks.Rules.Default
		}
		if notebookName == "" {
			rep.fail("registry", root, "no explicit default notebook for [sync.registry]")
			return ""
		}
		notebookRoot := filepath.Dir(filepath.Dir(root))
		if _, stampErr = notespace.MintNotebook(notebookRoot, notebookName); stampErr != nil {
			rep.fail("registry", notebookRoot, "notebook identity stamp failed: "+stampErr.Error())
			return ""
		}
		known := map[string]struct{}{stamp.ID: {}}
		_, _, stampErr = config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{KnownNotespaceIDs: known}, func(machine *config.MachineConfig) error {
			if machine.Primaries == nil {
				machine.Primaries = map[string]string{}
			}
			if machine.Subjects == nil {
				machine.Subjects = map[string]string{}
			}
			machine.Primaries[stamp.Subject] = stamp.ID
			machine.Subjects[canonicalPath(root)] = stamp.Subject
			machine.Sync.Registry = &config.SyncRegistry{Notebook: notebookName, NotespaceID: stamp.ID}
			return nil
		})
		if stampErr != nil {
			rep.fail("registry", root, "machine binding failed: "+stampErr.Error())
			return ""
		}
	}
	rep.ok("registry", root, "ready with immutable notespace id and [sync.registry] binding")
	return root
}

// ---- replication ------------------------------------------------------------

// reportDaemonPickup waits for the running daemon to adopt the subscription and
// reports what it saw. A missing daemon is a FAILURE of enrollment, not a
// footnote to a success: a machine that is configured but not replicating is
// the exact state job 52 was told was complete.
func reportDaemonPickup(ctx context.Context, rep *joinReporter, workspaceName string, wait time.Duration) bool {
	started := time.Now()
	status, picked := waitForWorkspacePickup(ctx, io.Discard, workspaceName, wait)
	reportSyncAuthFailure(rep.out, status)
	switch {
	case picked:
		rep.ok("daemon", "subscription "+workspaceName,
			fmt.Sprintf("picked up in %.1fs", time.Since(started).Seconds()))
		return true
	case status == nil:
		rep.fail("daemon", "no running daemon answered", "nothing is replicating",
			"start one and this machine converges on its own:",
			"  grove daemon start   (or run `groved`)")
		return false
	default:
		rep.fail("daemon", "subscription "+workspaceName,
			fmt.Sprintf("not picked up within %s", wait),
			"the daemon reloads on config change and at startup; restart it, then",
			"re-run `grove join --repair` to re-check")
		return false
	}
}

// reportRegistryReplication counts what actually landed in the registry, and
// distinguishes it from what this machine merely declares.
//
// Job 52's join printed "Ecosystems published in the registry: random, solab"
// with no daemon running and nothing replicated — those were this machine's own
// note, read back from its own disk. Counting other machines' notes separately
// is what makes the line evidence of replication rather than an echo.
func reportRegistryReplication(rep *joinReporter, root string) {
	if root == "" {
		return
	}
	peers, self, err := registryPeerCounts(root)
	if err != nil {
		rep.skip("replica", "registry not readable yet: "+err.Error())
		return
	}
	if peers == 0 {
		rep.skip("replica", fmt.Sprintf("no other machine's note has replicated here yet (%d note%s from this machine)", self, plural(self)))
		return
	}
	rep.ok("replica", "registry", fmt.Sprintf("%d machine%s replicated here", peers, plural(peers)))
}

// registryPeerCounts splits the registry's notes into those written by other
// machines and this machine's own.
func registryPeerCounts(root string) (peers, self int, err error) {
	selfID := ""
	if id, lerr := machine.Load(); lerr == nil && id != nil {
		selfID = id.ID
	}
	machines, err := registry.ReadMachines(root, selfID)
	if err != nil {
		return 0, 0, err
	}
	for _, m := range machines {
		if m.Self {
			self++
			continue
		}
		peers++
	}
	return peers, self, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---- shared helpers ---------------------------------------------------------

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
	out, err := exec.Command("sh", "-c", command).Output() //nolint:gosec // G204: the command is the operator's own token_command
	if err != nil {
		// No %w on a command whose text may itself be the secret, and no
		// stderr: the daemon-side custody rule, same direction.
		return "", fmt.Errorf("the token_command did not produce a token (%s)", exitStatusOf(err))
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("the token_command produced no output")
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
//
// The two lists are kept APART on purpose. An ecosystem this machine declares
// in roots.toml and an ecosystem another machine published to the registry
// are different facts, and printing the first under the second's heading is how
// a join with nothing replicated reported two ecosystems "published in the
// registry".
func reportJoinNextSteps(out io.Writer, registryRoot string) {
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

	offers, mine, err := peerRegistryOffers(registryRoot)
	if err != nil {
		if !errors.Is(err, registry.ErrNoRegistry) {
			fmt.Fprintf(out, "\n· registry not readable yet: %v\n", err)
		}
		return
	}
	if len(offers) == 0 {
		fmt.Fprintf(out, "\nNo ecosystem has been published to the registry by another machine yet.\n")
		if len(mine) > 0 {
			sort.Strings(mine)
			fmt.Fprintf(out, "This machine publishes %s — that is its OWN note, read back from local disk,\nnot something that replicated here.\n", strings.Join(mine, ", "))
		}
		fmt.Fprintf(out, "Other machines appear as soon as their presence notes replicate.\n")
		return
	}
	fmt.Fprintf(out, "\nEcosystems published by OTHER machines in the registry:\n")
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

// peerRegistryOffers returns the offers other machines published, and the
// names this machine publishes itself — the second list exists only so the
// caller can say which is which.
func peerRegistryOffers(root string) (offers []registry.Offer, mine []string, err error) {
	if root == "" {
		_, root, err = registry.Locate()
		if err != nil {
			return nil, nil, err
		}
	}
	selfID := ""
	if id, lerr := machine.Load(); lerr == nil && id != nil {
		selfID = id.ID
	}
	machines, err := registry.ReadMachines(root, selfID)
	if err != nil {
		return nil, nil, err
	}
	var peers, own []registry.Machine
	for _, m := range machines {
		if m.Self {
			own = append(own, m)
			continue
		}
		peers = append(peers, m)
	}
	for _, o := range registry.Offers(own) {
		mine = append(mine, o.Name)
	}
	return registry.Offers(peers), mine, nil
}
