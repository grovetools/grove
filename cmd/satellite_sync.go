package cmd

// Laptop-side note-sync finishing for `grove satellite up` — the
// [satellites.<name>.sync] config block (+ --sync-workspaces/--sync-port flag
// precedence), sync-token verification, and the create-or-merge writer for the
// laptop's ~/.config/grove/sync.toml.
//
// PUSH-ONLY INVARIANT (load-bearing safety property): a laptop [[workspaces]]
// entry written by this code NEVER carries `pull = true`. Pull belongs only in
// the VM's sync.toml (bootstrap step 5) — a pulling laptop would let the
// satellite overwrite local notebooks. renderLaptopSyncWorkspaces is the single
// entry-rendering choke point and refuses Pull outright; the merge path is
// append-only and never edits existing entries (the previous file content stays
// a byte-for-byte prefix, mirroring the registry-splice philosophy).

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/pelletier/go-toml/v2"
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

// laptopSyncWorkspace is one to-be-written [[workspaces]] entry for the LAPTOP
// sync.toml. Pull exists only so the push-only refusal is a real, testable
// code path — nothing in this package ever sets it.
type laptopSyncWorkspace struct {
	Name string
	Pull bool
}

// laptopSyncEntries wraps plain names as push-only entries.
func laptopSyncEntries(names []string) []laptopSyncWorkspace {
	entries := make([]laptopSyncWorkspace, 0, len(names))
	for _, n := range names {
		entries = append(entries, laptopSyncWorkspace{Name: n})
	}
	return entries
}

// renderLaptopSyncWorkspaces renders [[workspaces]] entries for the laptop
// sync.toml. HARD INVARIANT: it refuses to emit an entry with Pull set —
// every laptop entry this package writes flows through here (both the
// create-from-scratch and merge-append paths), making this the single
// enforcement point for the push-only safety property.
func renderLaptopSyncWorkspaces(entries []laptopSyncWorkspace) (string, error) {
	var b strings.Builder
	for _, e := range entries {
		if e.Pull {
			return "", fmt.Errorf("refusing to write laptop sync workspace %q with pull = true: the laptop sync config is PUSH-ONLY (a pull entry would let the satellite overwrite local notebooks); pull belongs only in the VM's sync.toml", e.Name)
		}
		if e.Name == "" {
			return "", fmt.Errorf("refusing to write a laptop sync workspace with an empty name")
		}
		fmt.Fprintf(&b, "\n[[workspaces]]\nname = %q\n", e.Name)
	}
	return b.String(), nil
}

// validateManagedLaptopPushOnly is the pre-clone half of the pull refusal.
// setupLaptopSyncConfig repeats the same check at write time for TOCTOU safety.
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
		if ws.Pull {
			return fmt.Errorf("refusing managed satellite setup: existing workspace %q in %s has pull = true; remove/segregate that pull profile before provisioning", ws.Name, path)
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

// createLaptopSyncConfig writes a fresh push-only sync.toml: server pointed at
// the daemon's local forward, token_command reading the fetched token, one
// name-only [[workspaces]] entry per workspace — never a pull key.
func createLaptopSyncConfig(path string, port int, tokenPath string, workspaces []string, out io.Writer) error {
	entries, err := renderLaptopSyncWorkspaces(laptopSyncEntries(workspaces))
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Laptop sync client config — generated by `grove satellite up`.\n")
	b.WriteString("# PUSH-ONLY: workspace entries here must never set pull = true; the\n")
	b.WriteString("# satellite VM pulls, the laptop only pushes (safety invariant).\n")
	fmt.Fprintf(&b, "server = \"http://127.0.0.1:%d\"\n", port)
	fmt.Fprintf(&b, "token_command = %q\n", "cat "+tokenPath)
	b.WriteString(entries)
	content := b.String()

	// Refuse to persist anything the sync loader could not parse, or —
	// defense in depth behind the renderer's refusal — anything carrying a
	// pull-enabled workspace.
	parsed, err := parseLaptopSyncContent(path, content)
	if err != nil {
		return err
	}
	for _, ws := range parsed.Workspaces {
		if ws.Pull {
			return fmt.Errorf("internal: generated %s would contain a pull-enabled workspace %q; refusing to write", path, ws.Name)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote laptop sync config %s (push-only, %d workspace(s): %s).\n",
		path, len(workspaces), strings.Join(workspaces, ", "))
	return nil
}

// mergeLaptopSyncConfig appends the missing workspace entries to an existing
// sync.toml. Append-only by construction: the previous file content is kept
// byte-for-byte as a prefix (comments, formatting, and existing entries
// untouched); only absent workspaces gain a new push-only entry. A server
// port that disagrees with sync_local_port is WARNED about, never rewritten.
func mergeLaptopSyncConfig(path string, port int, workspaces []string, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	existing, err := parseLaptopSyncContent(path, string(data))
	if err != nil {
		return fmt.Errorf("existing sync config is not usable — fix it (or move it aside) and re-run: %w", err)
	}

	warnSyncServerMismatch(out, path, existing.Server, port)

	// Managed full-satellite setup refuses an already pulling laptop. Merely
	// preserving it would let guest writes bypass incoming review and escrow.
	// A user may operate pull separately, but not through this managed profile.
	for _, ws := range existing.Workspaces {
		if ws.Pull {
			return fmt.Errorf("refusing managed satellite setup: existing workspace %q in %s has pull = true; remove/segregate that pull profile before provisioning a push-only laptop", ws.Name, path)
		}
	}

	have := make(map[string]bool, len(existing.Workspaces))
	for _, ws := range existing.Workspaces {
		have[ws.Name] = true
	}
	var missing []string
	for _, name := range workspaces {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintf(out, "Laptop sync config %s already lists all configured workspaces — left untouched.\n", path)
		return nil
	}

	appended, err := renderLaptopSyncWorkspaces(laptopSyncEntries(missing))
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n# Added by `grove satellite up` (push-only laptop entries)." + appended

	// The merged result must still parse as a sync config, and the append
	// must not have grown the pull-enabled set (defense in depth: the
	// renderer already refuses pull entries).
	merged, err := parseLaptopSyncContent(path, content)
	if err != nil {
		return fmt.Errorf("refusing to write merged sync config: %w", err)
	}
	if countPullEntries(merged) != countPullEntries(existing) {
		return fmt.Errorf("internal: merging %s would add a pull-enabled workspace; refusing to write", path)
	}
	if err := writeValidatedTOML(path, content); err != nil {
		return err
	}
	fmt.Fprintf(out, "Appended %d push-only workspace entr%s to %s: %s\n",
		len(missing), map[bool]string{true: "y", false: "ies"}[len(missing) == 1], path, strings.Join(missing, ", "))
	return nil
}

// parseLaptopSyncContent decodes sync.toml content into the canonical
// config.SyncConfig schema (the exact shape core's LoadSyncConfigFrom reads).
func parseLaptopSyncContent(path, content string) (*config.SyncConfig, error) {
	var cfg config.SyncConfig
	if err := toml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("%s does not parse as TOML: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s is not a valid sync config: %w", path, err)
	}
	return &cfg, nil
}

// countPullEntries counts pull-enabled workspaces (merge sanity check).
func countPullEntries(cfg *config.SyncConfig) int {
	n := 0
	for _, ws := range cfg.Workspaces {
		if ws.Pull {
			n++
		}
	}
	return n
}

// warnSyncServerMismatch warns when an existing sync.toml's server does not
// point at the daemon forward (http://127.0.0.1:<sync_local_port>). The file
// is the user's — it is never rewritten here.
func warnSyncServerMismatch(out io.Writer, path, server string, port int) {
	if server == "" {
		return
	}
	expected := fmt.Sprintf("http://127.0.0.1:%d", port)
	if strings.TrimRight(server, "/") == expected {
		return
	}
	if u, err := url.Parse(server); err == nil && u.Port() == strconv.Itoa(port) {
		return // same port, host spelled differently (e.g. localhost)
	}
	fmt.Fprintf(out, "warning: %s server = %q does not match the daemon sync forward %s (registry sync_local_port %d); leaving it unchanged — align --sync-port or edit the file manually.\n",
		path, server, expected, port)
}
