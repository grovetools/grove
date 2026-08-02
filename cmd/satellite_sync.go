package cmd

// Laptop-side note-sync finishing for `grove satellite up` — the
// [satellites.<name>.sync] config block (+ --sync-workspaces/--sync-port flag
// precedence), sync-token verification, and the create-or-merge writer for the
// laptop's ~/.config/grove/sync.toml.
//
// PUSH-ONLY INVARIANT, in its true scope (load-bearing safety property):
// SATELLITE-ROLE entries are push-only. A laptop [[workspaces]] entry written
// here for a satellite NEVER carries `pull = true`, because a pulling laptop
// would let a disposable VM overwrite local notebooks. Pull belongs in the VM's
// own sync.toml (bootstrap step 5).
//
// The invariant does NOT say "this machine may not pull". A workstation
// mirroring its OWN notebook (role = "peer") or subscribing to the machine
// registry (role = "registry") pulls legitimately — those relationships are not
// a disposable guest. The refusal is therefore scoped by
// config.RolePushOnly(role): role ∈ {empty (legacy), satellite}. Legacy
// role-less entries keep the old hard guarantee byte for byte.
//
// Enforcement lives in the shared editor (core/config/sync_edit.go), which is
// the single entry-rendering choke point for `satellite up`, `join`, and
// `materialize`; its merge path is append-only and never edits existing entries
// (the previous file content stays a byte-for-byte prefix).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
)

const (
	// defaultSyncLocalPort is the laptop-local port the daemon forward binds
	// (and the port the VM's syncd listens on — the PoC uses 8788 end to end).
	defaultSyncLocalPort = 8788
	// defaultSyncRemoteAddr is the VM-side syncd address the daemon forward
	// dials (bootstrap starts grove-syncd on 127.0.0.1:8788).
	defaultSyncRemoteAddr = "127.0.0.1:8788"
	// syncTokenFileName is the laptop sync token's basename in the grove
	// config dir — bootstrap step 7 fetches the VM-minted token there.
	syncTokenFileName = "sync.token"
	// syncTokenProbeStdinHeader is what the capabilities probe's remote curl
	// reads from stdin (-H @-): the Authorization header line, minus the
	// token itself.
	syncTokenProbeStdinHeader = "Authorization: Bearer "
	// syncConfigFileName is the sync client config basename (the same file
	// core's config.SyncConfigPath resolves).
	syncConfigFileName = "sync.toml"
)

// defaultSatelliteSyncWorkspaces matches the bootstrap script's historical
// hardcoded pair, kept as the default for compatibility.
var defaultSatelliteSyncWorkspaces = []string{"cloud", "grovetools"}

// satelliteSyncOptions is the [satellites.<name>.sync] table — grove-CLI-only
// input to `up`, riding alongside the registry entry the same way the
// provision block does. The daemon ignores this subtable (its mapstructure
// decode drops unknown keys); it reads only the flat sync_local_port/
// sync_remote_addr fields of the entry itself.
type satelliteSyncOptions struct {
	// Workspaces are the workspace names synced with this satellite: they
	// become the VM sync.toml's pull-enabled entries (bootstrap --workspaces)
	// AND the laptop sync.toml's push-only entries.
	Workspaces []string `yaml:"workspaces"`
	// AllWorkspaces is an explicit complete-replica opt-in. It is never
	// inferred from an absent allowlist.
	AllWorkspaces bool `yaml:"all_workspaces"`
}

// loadSatelliteSyncOptions reads [satellites.<name>.sync] from the layered
// grove config (mirrors loadSatelliteProvision). Missing satellite or missing
// sync subtable yields the zero value; a malformed one is an error.
func loadSatelliteSyncOptions(name string) (satelliteSyncOptions, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return satelliteSyncOptions{}, fmt.Errorf("load grove config: %w", err)
	}
	return satelliteSyncOptionsFromConfig(cfg, name)
}

// satelliteSyncOptionsFromConfig decodes only the sync subtables out of the
// [satellites.*] extension (separate decode from satelliteConfigEntry, same
// stance as satelliteProvisionFromConfig).
func satelliteSyncOptionsFromConfig(cfg *config.Config, name string) (satelliteSyncOptions, error) {
	var raw map[string]struct {
		Sync satelliteSyncOptions `yaml:"sync"`
	}
	if err := cfg.UnmarshalExtension("satellites", &raw); err != nil {
		return satelliteSyncOptions{}, fmt.Errorf("parse [satellites.%s.sync]: %w", name, err)
	}
	return raw[name].Sync, nil
}

// resolveSatelliteSyncWorkspaces resolves the workspace list with flag >
// config > default precedence. A set-but-empty flag means "no sync
// workspaces" (explicit disable), matching the provision blocks' set-to-empty
// semantics.
func resolveSatelliteSyncWorkspaces(cfg satelliteSyncOptions, flagValue string, flagSet bool) []string {
	if flagSet {
		return splitWorkspacesFlag(flagValue)
	}
	if len(cfg.Workspaces) > 0 {
		return append([]string(nil), cfg.Workspaces...)
	}
	return append([]string(nil), defaultSatelliteSyncWorkspaces...)
}

// resolveAllNotebookWorkspaces enumerates the configured notebook workspace
// directories. Only the explicit --all-workspaces/config opt-in calls this;
// an absent allowlist never silently expands to the laptop's whole notebook.
func resolveAllNotebookWorkspaces(cfg *config.Config) ([]string, error) {
	if cfg == nil || cfg.Notebooks == nil || len(cfg.Notebooks.Definitions) == 0 {
		return nil, fmt.Errorf("no notebook definitions are configured")
	}
	seen := map[string]bool{}
	for _, nb := range cfg.Notebooks.Definitions {
		if nb == nil || nb.RootDir == "" {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(expandUserPath(nb.RootDir), "workspaces"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				seen[entry.Name()] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("--all-workspaces found no configured notebook workspaces")
	}
	return out, nil
}

// splitWorkspacesFlag splits a comma-separated workspace flag, dropping empty
// segments.
func splitWorkspacesFlag(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// renderLaptopSyncWorkspaces renders the laptop's [[workspaces]] entries for a
// satellite: it STAMPS role = "satellite" on every entry it generates, which
// is what makes them push-only by construction (config.RolePushOnly). The
// rendering itself is the shared editor's, so there is exactly one place that
// can emit an entry — and it refuses a pull-enabled satellite entry.
func renderLaptopSyncWorkspaces(entries []config.SyncWorkspace) (string, error) {
	stamped := make([]config.SyncWorkspace, 0, len(entries))
	for _, e := range entries {
		if e.Role == "" {
			e.Role = config.SyncRoleSatellite
		}
		stamped = append(stamped, e)
	}
	return config.RenderSyncWorkspaces(stamped)
}

// laptopSyncEntries wraps plain workspace names as satellite-role entries.
func laptopSyncEntries(names []string) []config.SyncWorkspace {
	entries := make([]config.SyncWorkspace, 0, len(names))
	for _, n := range names {
		entries = append(entries, config.SyncWorkspace{Name: n, Role: config.SyncRoleSatellite})
	}
	return entries
}

// validateManagedLaptopPushOnly is the pre-clone half of the pull refusal.
// setupLaptopSyncConfig repeats the same check at write time for TOCTOU safety.
//
// The refusal is role-scoped: an existing `pull = true` entry blocks managed
// satellite setup only when its role is push-only — legacy (role-less) or
// satellite. A role = "peer" or role = "registry" entry pulls legitimately
// (this machine mirroring its own notebook, or the machine registry) and must
// not stop a satellite from being provisioned alongside it.
func validateManagedLaptopPushOnly(configDir string) error {
	path := filepath.Join(configDir, syncConfigFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg, err := parseLaptopSyncContent(path, string(data))
	if err != nil {
		return err
	}
	for _, ws := range cfg.Workspaces {
		if ws.Pull && config.RolePushOnly(ws.Role) {
			return fmt.Errorf("refusing managed satellite setup: existing workspace %q in %s has pull = true under a push-only role (%q); remove/segregate that pull profile, or declare role = %q if it is this machine's own notebook",
				ws.Name, path, ws.Role, config.SyncRolePeer)
		}
	}
	return nil
}

// setupLaptopSyncConfig is `up`'s laptop-side sync finishing step: verify the
// sync token bootstrap fetched, then create (missing) or append-merge
// (existing) the push-only sync.toml in configDir. Never touches anything
// outside configDir.
func setupLaptopSyncConfig(configDir string, port int, workspaces []string, out io.Writer) error {
	tokenPath := filepath.Join(configDir, syncTokenFileName)
	if _, err := os.Stat(tokenPath); err != nil {
		return fmt.Errorf("laptop sync token missing at %s (bootstrap step 7 fetches it from the VM): %w\n"+
			"remediation — mint and fetch a fresh laptop token, then re-run:\n"+
			"  ssh <user>@<vm-ip> \"sudo /usr/local/bin/grove-syncd --data-dir /var/lib/grove-syncd token create laptop\" > %s\n"+
			"  chmod 600 %s",
			tokenPath, err, tokenPath, tokenPath)
	}
	// The same push-only check `up` ran before provisioning, repeated here at
	// write time (TOCTOU) and phrased in satellite terms — the shared editor
	// refuses the same file, but the operator asked for a satellite, so the
	// error should say what that means for the satellite.
	if err := validateManagedLaptopPushOnly(configDir); err != nil {
		return err
	}
	syncPath := filepath.Join(configDir, syncConfigFileName)
	if _, err := os.Stat(syncPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", syncPath, err)
		}
		return createLaptopSyncConfig(syncPath, port, tokenPath, workspaces, out)
	}
	return mergeLaptopSyncConfig(syncPath, port, workspaces, out)
}

// --- live sync-token verification (`up`'s backstop against a stale token) ---

// syncTokenProbeCmd renders the VM-side capabilities probe (bootstrap's own
// probe pattern): POST /sync/capabilities on the VM-loopback syncd, printing
// only the HTTP status code. The Authorization header arrives on the remote
// command's stdin (curl -H @-), so the token appears in NO argv — neither the
// local ssh invocation's nor the remote curl process's.
func syncTokenProbeCmd(remoteAddr string) string {
	if remoteAddr == "" {
		remoteAddr = defaultSyncRemoteAddr
	}
	return fmt.Sprintf(`curl -s -o /dev/null -w '%%{http_code}' -X POST -H @- -H 'Content-Type: application/json' -d '{}' http://%s/sync/capabilities`, remoteAddr)
}

// verifySatelliteSyncToken live-checks the laptop sync token against the VM's
// syncd via runRemote (an exec-remote-command func — ssh.outputCommand in
// production, a fake in tests). The stat in setupLaptopSyncConfig only proves
// a token FILE exists; a stale token from a previous VM passes it vacuously
// and the daemon then 401-loops silently — this probe is the backstop.
// Decision logic:
//   - 2xx      → token accepted.
//   - 401/403  → the token is stale for THIS VM; error carries the
//     fetch-a-fresh-token remediation.
//   - probe/transport error → distinct network-failure message (not a token
//     problem — curl exits nonzero when syncd is unreachable, so a dead syncd
//     lands here too).
//   - anything else → syncd answered but not usably; also not a token verdict.
func verifySatelliteSyncToken(runRemote func(command, stdin string) (string, error), probeCmd, token, sshDest, tokenPath string) error {
	status, err := runRemote(probeCmd, syncTokenProbeStdinHeader+token+"\n")
	if err != nil {
		return fmt.Errorf("could not run the sync capabilities probe on the VM (network/SSH/syncd failure, not a token verdict — check the VM and its grove-syncd service, then re-run): %w", err)
	}
	switch status = strings.TrimSpace(status); {
	case strings.HasPrefix(status, "2"):
		return nil
	case status == "401" || status == "403":
		return fmt.Errorf("the laptop sync token at %s is stale for this VM (syncd returned %s; the token is likely left over from a previous satellite, and the daemon would 401-loop silently)\n"+
			"remediation — fetch the token this VM's bootstrap minted, then re-run:\n"+
			"  ssh %s 'sudo cat /root/laptop-sync.token' > %s && chmod 600 %s",
			tokenPath, status, sshDest, tokenPath, tokenPath)
	default:
		return fmt.Errorf("unexpected HTTP status %q from the VM syncd capabilities probe (syncd reachable but unhealthy?) — not a token verdict; check grove-syncd on the VM and re-run", status)
	}
}

// verifySatelliteSyncTokenOverSSH wires verifySatelliteSyncToken to the pinned
// SSH transport from the registry entry (same C2 never-TOFU stance as every
// other remote step `up` runs). The token is read here and travels only on
// the ssh process's stdin.
func verifySatelliteSyncTokenOverSSH(entry satelliteConfigEntry, tokenPath string) error {
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("read laptop sync token %s: %w", tokenPath, err)
	}
	tmpDir, err := os.MkdirTemp("", "grove-satellite-sync-verify-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return err
	}
	return verifySatelliteSyncToken(ssh.outputCommand, syncTokenProbeCmd(entry.SyncRemoteAddr), strings.TrimSpace(string(token)), ssh.dest(), tokenPath)
}

// createLaptopSyncConfig writes a fresh sync.toml through the shared editor:
// server pointed at the daemon's local forward, token_command reading the
// fetched token, one satellite-role [[workspaces]] entry per workspace — never
// a pull key.
func createLaptopSyncConfig(path string, port int, tokenPath string, workspaces []string, out io.Writer) error {
	res, err := config.ApplySyncEdit(path, laptopSyncEdit(port, tokenPath, workspaces))
	if err != nil {
		return err
	}
	reportSyncEdit(out, res, workspaces)
	return nil
}

// mergeLaptopSyncConfig appends the missing workspace entries to an existing
// sync.toml. Append-only by construction (the shared editor keeps the previous
// content as a byte-for-byte prefix); only absent workspaces gain a new
// satellite-role entry. A server that disagrees with sync_local_port is WARNED
// about, never rewritten.
func mergeLaptopSyncConfig(path string, port int, workspaces []string, out io.Writer) error {
	res, err := config.ApplySyncEdit(path, laptopSyncEdit(port, "", workspaces))
	if err != nil {
		return err
	}
	reportSyncEdit(out, res, workspaces)
	return nil
}

// laptopSyncEdit builds the edit `satellite up` applies: satellite-role
// entries only, pointed at the daemon-owned local forward.
func laptopSyncEdit(port int, tokenPath string, workspaces []string) config.SyncEdit {
	edit := config.SyncEdit{
		Server:     fmt.Sprintf("http://127.0.0.1:%d", port),
		Workspaces: laptopSyncEntries(workspaces),
		Header: []string{
			"# Laptop sync client config — generated by `grove satellite up`.",
			"# Satellite-role entries are PUSH-ONLY: they must never set pull = true.",
			"# The satellite VM pulls, this machine only pushes (safety invariant).",
		},
		Note: "Added by `grove satellite up` (satellite-role, push-only entries).",
	}
	if tokenPath != "" {
		edit.TokenCommand = "cat " + tokenPath
	}
	return edit
}

// reportSyncEdit prints what the editor actually did, warnings included.
func reportSyncEdit(out io.Writer, res config.SyncEditResult, requested []string) {
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	switch {
	case res.Created:
		fmt.Fprintf(out, "Wrote laptop sync config %s (push-only, %d workspace(s): %s).\n",
			res.Path, len(requested), strings.Join(requested, ", "))
	case len(res.Added) == 0:
		fmt.Fprintf(out, "Laptop sync config %s already lists all configured workspaces — left untouched.\n", res.Path)
	default:
		fmt.Fprintf(out, "Appended %d push-only workspace entr%s to %s: %s\n",
			len(res.Added), map[bool]string{true: "y", false: "ies"}[len(res.Added) == 1],
			res.Path, strings.Join(res.Added, ", "))
	}
}

// parseLaptopSyncContent decodes sync.toml content into the canonical
// config.SyncConfig schema (the exact shape core's LoadSyncConfigFrom reads).
func parseLaptopSyncContent(path, content string) (*config.SyncConfig, error) {
	return config.ParseSyncContent(path, content)
}
