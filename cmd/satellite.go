package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/components/table"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/setup"
)

// satelliteConfigEntry mirrors the daemon's satellite.SatelliteConfig on-disk
// shape (the [satellites.<name>] table). It is redeclared here — rather than
// importing the daemon package — because `grove satellite` only reads/writes
// the grove config, and the yaml tags must match P7's SatelliteConfig so the
// laptop daemon's LoadRegistry sees exactly what `up` writes.
type satelliteConfigEntry struct {
	SSHAddr string `yaml:"ssh_addr"`
	User    string `yaml:"user"`
	HostKey string `yaml:"host_key"`
	// IdentityFile is the optional SSH private key `up --identity-file` was
	// invoked with (empty = agent-only auth, matching SatelliteConfig).
	IdentityFile string `yaml:"identity_file"`
	// SocketPath is the remote groved unix socket, probed post-bootstrap as
	// /run/user/<uid>/grove/groved.sock. Written explicitly because the
	// daemon's remoteSocketPath default guesses wrong on the real VM.
	SocketPath string `yaml:"socket_path"`
	// SyncLocalPort is the laptop-local port the daemon binds
	// (127.0.0.1:<port>) and forwards to the VM's syncd over its pinned SSH
	// connection. 0/absent = daemon sync forward off (fixed M2 contract with
	// the daemon side).
	SyncLocalPort int `yaml:"sync_local_port"`
	// SyncRemoteAddr is the VM-side syncd address the forward dials.
	// Optional; the daemon defaults it to 127.0.0.1:8788.
	SyncRemoteAddr string `yaml:"sync_remote_addr"`
}

// newSatelliteCmd is the `grove satellite` noun. It wraps the PoC
// terraform/bootstrap runbook (cloud/poc/grove-satellite) into VM lifecycle
// verbs and writes the [satellites.<name>] registry entry P7's ConnManager
// reads at daemon boot (M2 contract C1/C2).
//
// Env exec-plugin seam (position, do NOT route through it): a satellite is
// whole-host lifecycle with a registry side effect, whereas
// core/pkg/env/provider.go ResolveProvider is per-worktree, cwd-keyed service
// provisioning. The noun calls terraform directly. The seam composes the other
// way — a future `grove-env-satellite` exec plugin could shell out to
// `grove satellite up` — not the reverse.
func newSatelliteCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("satellite", "Manage remote grove satellite VMs")
	cmd.Long = `Provision, tear down, and inspect remote grove "satellite" build VMs.

A satellite is a whole-host grove daemon the laptop federates jobs/sessions
from and dispatches to over SSH (M2). 'up' is billable — it creates cloud
resources via terraform.`
	cmd.AddCommand(newSatelliteUpCmd())
	cmd.AddCommand(newSatelliteUpgradeCmd())
	cmd.AddCommand(newSatelliteConfigCmd())
	cmd.AddCommand(newSatelliteDownCmd())
	cmd.AddCommand(newSatelliteStatusCmd())
	cmd.AddCommand(newSatelliteListCmd())
	return cmd
}

// defaultTerraformDir returns the PoC terraform directory relative to cwd. The
// checklist's position: resolve via a --tf-dir flag defaulting to the PoC path
// rather than guessing a discovery mechanism.
const defaultTerraformDir = "cloud/poc/grove-satellite/terraform"

func newSatelliteUpCmd() *cobra.Command {
	var (
		project        string
		sshUser        string
		cidr           string
		zone           string
		tfDir          string
		identityFile   string
		assumeYes      bool
		ghTokenCmd     string
		claudeFlag     bool
		claudeTokenCmd string
		dotfilesRepo   string
		serviceAccount string
		syncPort       int
		syncWorkspaces string
		reloadDaemon   bool
	)
	cmd := cli.NewStandardCommand("up <name>", "Provision a satellite VM (billable)")
	cmd.Long = `Provision a satellite VM: terraform apply, host-key pin, bootstrap, registry.

Optional provisioning inputs (GitHub auth, Claude Code, dotfiles, service
account) come from a [satellites.<name>.provision] block in the same grove
config the registry lives in; the matching flags override the block:

  [satellites.mysat.provision]
  gh_token_cmd = "gh auth token"       # local command; stdout is the GH token
  claude = true                        # install Claude Code on the VM
  claude_token_cmd = "..."             # local command producing CLAUDE_CODE_OAUTH_TOKEN
                                       # (mint once with 'claude setup-token'; implies claude)
  dotfiles_repo = "https://github.com/you/dotfiles"
  service_account_email = "sa@proj.iam.gserviceaccount.com"  # terraform var

Token commands run locally through your shell BEFORE terraform (a failing
command aborts the provision while it is still free), and the tokens are piped
to the bootstrap script on stdin — never argv (its documented framing).

Config propagation: if the entry also declares seed_fragments or a
[satellites.<name>.config] table, 'up' pushes them to the VM's ~/.config/grove
after bootstrap — the same code path as 'grove satellite config push' (see its
help for the denylist and precedence story). The set is validated BEFORE
terraform so a denied fragment aborts while the provision is still free.

Note sync (laptop half, automated): 'up' verifies the sync token bootstrap
fetched, writes/merges the laptop's PUSH-ONLY sync.toml (never a pull entry),
and records sync_local_port/sync_remote_addr in the registry entry so the
laptop daemon binds 127.0.0.1:<port> and forwards note-sync to the VM's syncd
over its pinned SSH connection — no manual tunnel. Workspaces come from a
[satellites.<name>.sync] block (workspaces = ["cloud", "grovetools"]) with
--sync-workspaces overriding; --sync-port 0 disables the whole sync half.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&project, "project", "", "GCP project id (terraform var project_id) [required]")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH login user on the VM (terraform var ssh_user) [required]")
	cmd.Flags().StringVar(&cidr, "cidr", "", "CIDR allowed to reach :22 (default: your public IP /32 via ifconfig.me)")
	cmd.Flags().StringVar(&zone, "zone", "", "GCP zone override (terraform var zone)")
	cmd.Flags().StringVar(&tfDir, "tf-dir", defaultTerraformDir, "Path to the grove-satellite terraform directory")
	cmd.Flags().StringVar(&identityFile, "identity-file", "", "SSH private key for the satellite (written to the registry as identity_file; default: agent-only auth)")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the billable-resource confirmation prompt")
	cmd.Flags().StringVar(&ghTokenCmd, "gh-token-cmd", "", "Local command whose stdout is the GitHub token piped to bootstrap (overrides provision config; empty disables)")
	cmd.Flags().BoolVar(&claudeFlag, "claude", false, "Install Claude Code on the VM (overrides provision config)")
	cmd.Flags().StringVar(&claudeTokenCmd, "claude-token-cmd", "", "Local command producing CLAUDE_CODE_OAUTH_TOKEN, piped to bootstrap; implies --claude (overrides provision config; empty disables)")
	cmd.Flags().StringVar(&dotfilesRepo, "dotfiles-repo", "", "Dotfiles repo cloned + installed on the VM, best-effort (overrides provision config; empty disables)")
	cmd.Flags().StringVar(&serviceAccount, "service-account", "", "Service account email attached to the VM (terraform var service_account_email; overrides provision config; empty disables)")
	cmd.Flags().IntVar(&syncPort, "sync-port", defaultSyncLocalPort, "Laptop-local port the daemon binds and forwards to the VM's syncd (registry sync_local_port; 0 disables the sync forward and laptop sync setup)")
	cmd.Flags().StringVar(&syncWorkspaces, "sync-workspaces", "", "Comma-separated workspaces to sync with the VM (overrides [satellites.<name>.sync] workspaces; default "+strings.Join(defaultSatelliteSyncWorkspaces, ",")+")")
	cmd.Flags().BoolVar(&reloadDaemon, "reload-daemon", false, "Run 'groved upgrade --global' at the end so the daemon loads the new registry + sync forward")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if project == "" || sshUser == "" {
			return fmt.Errorf("--project and --ssh-user are required")
		}

		// Provisioning options: [satellites.<name>.provision] with flag
		// overrides (a set flag wins even when set to empty).
		provCfg, err := loadSatelliteProvision(name)
		if err != nil {
			return err
		}
		prov := mergeProvision(provCfg, provisionFlagOverrides{
			GHTokenCmd:     ghTokenCmd,
			GHTokenCmdSet:  cmd.Flags().Changed("gh-token-cmd"),
			Claude:         claudeFlag,
			ClaudeSet:      cmd.Flags().Changed("claude"),
			ClaudeTokenCmd: claudeTokenCmd,
			ClaudeTokenSet: cmd.Flags().Changed("claude-token-cmd"),
			DotfilesRepo:   dotfilesRepo,
			DotfilesSet:    cmd.Flags().Changed("dotfiles-repo"),
			ServiceAccount: serviceAccount,
			ServiceAccSet:  cmd.Flags().Changed("service-account"),
		})

		// Note-sync inputs: [satellites.<name>.sync] workspaces with the
		// --sync-workspaces flag winning (same flag>config stance as the
		// provision block). Resolved before terraform: the list drives both
		// the bootstrap script's VM-side sync.toml (--workspaces) and the
		// laptop's push-only sync.toml after bootstrap.
		if syncPort < 0 || syncPort > 65535 {
			return fmt.Errorf("--sync-port %d out of range (0-65535; 0 disables the sync forward)", syncPort)
		}
		syncOpts, err := loadSatelliteSyncOptions(name)
		if err != nil {
			return err
		}
		resolvedSyncWorkspaces := resolveSatelliteSyncWorkspaces(syncOpts, syncWorkspaces, cmd.Flags().Changed("sync-workspaces"))

		// Config propagation ([satellites.<name>].seed_fragments + .config):
		// assemble + validate NOW — before terraform — so a denied/missing
		// fragment aborts while the provision is still free (same fail-fast
		// stance as the token commands). The actual push happens after
		// bootstrap + socket probe, sharing `config push`'s code path.
		prop, err := loadSatelliteConfigPropagation(name)
		if err != nil {
			return err
		}
		var configPushFiles []satellitePushFile
		if prop.hasConfigToPush() {
			laptopConfigDir := paths.ConfigDir()
			if laptopConfigDir == "" {
				return fmt.Errorf("could not resolve the laptop's grove config directory")
			}
			if configPushFiles, err = assembleSatellitePush(name, prop, laptopConfigDir); err != nil {
				return err
			}
		}

		tfAbs, err := filepath.Abs(tfDir)
		if err != nil {
			return fmt.Errorf("resolve --tf-dir: %w", err)
		}
		if _, err := os.Stat(filepath.Join(tfAbs, "variables.tf")); err != nil {
			return fmt.Errorf("terraform dir %q does not look like the grove-satellite PoC (no variables.tf); pass --tf-dir: %w", tfAbs, err)
		}
		bootstrapScript := filepath.Join(filepath.Dir(tfAbs), "bootstrap", "satellite-bootstrap.sh")

		if !assumeYes {
			if !confirmYesNo(fmt.Sprintf("Provision satellite %q — this creates BILLABLE GCP resources. Continue?", name)) {
				return fmt.Errorf("aborted")
			}
		}

		// Resolve tokens NOW — before terraform — so a broken token command
		// aborts while the provision is still free (fail fast, no orphaned VM).
		ghToken, claudeToken, err := resolveProvisionTokens(prov)
		if err != nil {
			return err
		}

		if cidr == "" {
			cidr = detectPublicCIDR()
			if cidr == "" {
				return fmt.Errorf("could not auto-detect your public IP; pass --cidr (e.g. 203.0.113.7/32)")
			}
			fmt.Printf("Using detected SSH CIDR: %s\n", cidr)
		}

		// Persist the required (default-less) terraform variables as
		// terraform.tfvars in the tf dir BEFORE apply, so `grove satellite
		// down` can run terraform destroy non-interactively — variables.tf
		// deliberately has no defaults for project_id/ssh_user/
		// allowed_ssh_cidr. terraform auto-loads the file from the -chdir
		// dir, and the PoC's .gitignore already excludes *.tfvars.
		if err := writeSatelliteTFVars(tfAbs, project, sshUser, cidr, name, zone, prov.ServiceAccountEmail); err != nil {
			return fmt.Errorf("write %s: %w", satelliteTFVarsName, err)
		}

		// 1. terraform init + apply (subprocess, inherited stdio — terraform
		//    runs its own confirmation).
		if err := runInherited(tfAbs, "terraform", "-chdir="+tfAbs, "init", "-input=false"); err != nil {
			return fmt.Errorf("terraform init: %w", err)
		}
		applyArgs := []string{
			"-chdir=" + tfAbs, "apply",
			"-var", "project_id=" + project,
			"-var", "ssh_user=" + sshUser,
			"-var", "allowed_ssh_cidr=" + cidr,
			"-var", "vm_name=" + name,
		}
		if zone != "" {
			applyArgs = append(applyArgs, "-var", "zone="+zone)
		}
		if prov.ServiceAccountEmail != "" {
			applyArgs = append(applyArgs, "-var", "service_account_email="+prov.ServiceAccountEmail)
		}
		if err := runInherited(tfAbs, "terraform", applyArgs...); err != nil {
			return fmt.Errorf("terraform apply: %w", err)
		}

		// 2. terraform output -raw external_ip → IP.
		ip, err := terraformOutput(tfAbs, "external_ip")
		if err != nil {
			return fmt.Errorf("read terraform output external_ip: %w", err)
		}
		ip = strings.TrimSpace(ip)
		if ip == "" {
			return fmt.Errorf("terraform output external_ip was empty")
		}

		// 3. Host-key pin (C2): ssh-keyscan, seeded-not-TOFU. ConnManager
		//    hard-fails on any later mismatch.
		hostKey, err := sshKeyscanHostKey(ip)
		if err != nil {
			return fmt.Errorf("ssh-keyscan host-key pin: %w", err)
		}

		// 4. Bootstrap the VM (subprocess, inherited stdout/stderr). Provision
		//    options become the script's flags; tokens ride stdin in its
		//    documented framing (raw single token, or KEY=VALUE lines when both
		//    are present) — never argv. The script already fetches the laptop
		//    sync token to ~/.config/grove/sync.token.
		if _, err := os.Stat(bootstrapScript); err != nil {
			return fmt.Errorf("bootstrap script not found at %q: %w", bootstrapScript, err)
		}
		provArgs, secretStdin := buildBootstrapProvision(prov, ghToken, claudeToken)
		// The resolved sync workspaces drive the VM's pull-enabled sync.toml
		// (bootstrap step 5). Always passed — an explicitly empty flag value
		// means "no sync workspaces" on the VM too.
		provArgs = append(provArgs, "--workspaces", strings.Join(resolvedSyncWorkspaces, ","))
		bootstrapArgs := append([]string{bootstrapScript, sshUser + "@" + ip}, provArgs...)
		if err := runInheritedWithStdin(tfAbs, secretStdin, "bash", bootstrapArgs...); err != nil {
			return fmt.Errorf("satellite bootstrap: %w", err)
		}

		// 5. Assemble the registry entry (yaml keys match P7's SatelliteConfig
		//    tags — exactly what LoadRegistry reads).
		entry := satelliteConfigEntry{
			SSHAddr: ip + ":22",
			User:    sshUser,
			HostKey: hostKey,
		}
		if identityFile != "" {
			entry.IdentityFile = expandUserPath(identityFile)
		}
		// Sync forward fields (fixed M2 contract with the daemon side): the
		// daemon binds 127.0.0.1:<sync_local_port> and forwards to the VM's
		// syncd over the pinned SSH connection. 0 = forward off (fields
		// omitted so the entry stays minimal).
		if syncPort != 0 {
			entry.SyncLocalPort = syncPort
			entry.SyncRemoteAddr = defaultSyncRemoteAddr
		}

		// 5b. Probe the VM for the remote groved socket path
		//     (/run/user/<uid>/grove/groved.sock). Written explicitly because
		//     the daemon's default socket-path convention guesses wrong on the
		//     real VM; a probe failure degrades to the daemon default rather
		//     than failing a successful provision. The probe pins the host key
		//     just scanned — never TOFU (C2).
		if socketPath, exists, probeErr := probeSatelliteSocketPath(entry); probeErr != nil {
			fmt.Printf("(could not probe remote groved socket path, leaving socket_path unset: %v)\n", probeErr)
		} else {
			if !exists {
				fmt.Printf("(remote socket %s not present yet — groved may still be starting; writing the path anyway)\n", socketPath)
			}
			entry.SocketPath = socketPath
		}

		// 5c. Write the [satellites.<name>] registry entry.
		if err := writeSatelliteRegistry(name, entry); err != nil {
			return fmt.Errorf("write registry entry: %w", err)
		}

		// 5d. Config propagation: ship the fragment set assembled (and
		// validated) before terraform — the same code path as `grove
		// satellite config push`. Non-fatal: the VM is provisioned and
		// registered; a transport hiccup here is retried with the verb.
		if len(configPushFiles) > 0 {
			fmt.Printf("\nPushing %d config fragment(s) to %q...\n", len(configPushFiles), name)
			if err := pushSatelliteConfigOverSSH(name, entry, configPushFiles, false); err != nil {
				fmt.Printf("warning: config push failed: %v\n", err)
				fmt.Printf("  retry with: grove satellite config push %s\n", name)
			}
		}

		// 5e. Laptop sync finishing: verify the token bootstrap fetched and
		// create-or-merge the laptop's PUSH-ONLY sync.toml. The registry
		// entry (5c) already carries the forward fields; the daemon owns the
		// tunnel from its next boot. Errors here are real errors (with
		// remediation in the message) — but the VM is provisioned and
		// registered, so a re-run is cheap.
		if syncPort != 0 {
			laptopConfigDir := paths.ConfigDir()
			if laptopConfigDir == "" {
				return fmt.Errorf("could not resolve the laptop's grove config directory for sync setup")
			}
			fmt.Println()
			if err := setupLaptopSyncConfig(laptopConfigDir, syncPort, resolvedSyncWorkspaces, os.Stdout); err != nil {
				return fmt.Errorf("laptop sync setup (the VM is provisioned and registered; fix and re-run `grove satellite up %s`): %w", name, err)
			}
		}

		// 6. Next steps — the registry AND sync config load only at daemon
		//    boot (same constraint as sync transport registration in
		//    groved.go). The daemon owns the sync forward now; there is no
		//    manual tunnel step anymore.
		fmt.Printf("\nSatellite %q provisioned at %s.\n", name, ip)
		printSatelliteNextSteps(true, syncPort)
		fmt.Println()
		fmt.Println("Note: the satellite's sync origin_id (C20) is minted per-install on the VM")
		fmt.Println("and is disposable; the registry name above is the stable federation Origin (C6).")
		if reloadDaemon {
			fmt.Println("\n--reload-daemon: running `groved upgrade --global`...")
			if err := runInherited("", "groved", "upgrade", "--global"); err != nil {
				fmt.Printf("warning: groved upgrade --global failed: %v\n", err)
				fmt.Println("  run it manually so the daemon loads the new registry + sync forward.")
			}
		}
		return nil
	}
	return cmd
}

func newSatelliteDownCmd() *cobra.Command {
	var (
		tfDir        string
		assumeYes    bool
		syncOriginID string
	)
	cmd := cli.NewStandardCommand("down <name>", "Destroy a satellite VM and remove its registry entry")
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", defaultTerraformDir, "Path to the grove-satellite terraform directory")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the destroy confirmation prompt")
	cmd.Flags().StringVar(&syncOriginID, "sync-origin-id", "", "Satellite sync origin_id to deregister precisely (best-effort; see C19/C20)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		tfAbs, err := filepath.Abs(tfDir)
		if err != nil {
			return fmt.Errorf("resolve --tf-dir: %w", err)
		}

		if !assumeYes {
			if !confirmYesNo(fmt.Sprintf("Destroy satellite %q and remove its registry entry?", name)) {
				return fmt.Errorf("aborted")
			}
		}

		// 1. Best-effort cursor-deregister (C19/C20) BEFORE destroy, so a
		//    durable hub isn't left pinning GC. In the PoC topology syncd dies
		//    with the VM anyway; this matters for the hosted-hub future.
		bestEffortDeregisterCursors(name, syncOriginID)

		// 2. terraform destroy (subprocess, inherited stdio — terraform runs
		//    its own confirmation). Destroy needs the same required vars as
		//    apply (variables.tf has no defaults for project_id/ssh_user/
		//    allowed_ssh_cidr); `up` persists them as terraform.tfvars, and
		//    -input=false makes a missing variable a hard error instead of an
		//    interactive prompt so scripted teardown fails fast.
		tfvarsPath := filepath.Join(tfAbs, satelliteTFVarsName)
		if _, err := os.Stat(tfvarsPath); err != nil {
			return fmt.Errorf("%s not found — `grove satellite up` writes it and terraform destroy needs project_id/ssh_user/allowed_ssh_cidr (no defaults in variables.tf); recreate it or run terraform destroy manually with -var flags: %w", tfvarsPath, err)
		}
		destroyArgs := []string{"-chdir=" + tfAbs, "destroy", "-input=false", "-var", "vm_name=" + name}
		if err := runInherited(tfAbs, "terraform", destroyArgs...); err != nil {
			return fmt.Errorf("terraform destroy: %w", err)
		}

		// 3. Remove the [satellites.<name>] registry entry.
		if err := removeSatelliteRegistry(name); err != nil {
			return fmt.Errorf("remove registry entry: %w", err)
		}

		fmt.Printf("\nSatellite %q destroyed and deregistered.\n", name)
		fmt.Println("Restart the global daemon to drop its connection (the sync port-forward")
		fmt.Println("disappears with the registry entry):")
		fmt.Println("  groved upgrade --global")
		if configDir := paths.ConfigDir(); configDir != "" {
			fmt.Println()
			fmt.Printf("Note: the laptop sync config keeps its workspace entries (%s)\n", filepath.Join(configDir, syncConfigFileName))
			fmt.Printf("and the sync token (%s) — remove them manually if unwanted.\n", filepath.Join(configDir, syncTokenFileName))
		}
		return nil
	}
	return cmd
}

func newSatelliteStatusCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("status", "Show satellite connection health")
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return renderSatellites() }
	return cmd
}

func newSatelliteListCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("list", "List configured satellites")
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return renderSatellites() }
	return cmd
}

// renderSatellites merges the configured registry entries with live ConnManager
// health from the global daemon (client.GetSatelliteStatuses, the Store surface
// P7 emits via the new GET /api/satellites read endpoint).
func renderSatellites() error {
	configured := loadConfiguredSatellites()

	live := map[string]satelliteLiveStatus{}
	client := daemon.New()
	if client.IsRunning() {
		ctx := context.Background()
		if statuses, err := client.GetSatelliteStatuses(ctx); err == nil {
			for name, st := range statuses {
				if st == nil {
					continue
				}
				live[name] = satelliteLiveStatus{state: st.State, addr: st.Addr, since: st.Since, lastError: st.LastError}
			}
		}
	}

	// Union of configured + live names so a just-added (not-yet-connected)
	// satellite still shows, and vice versa.
	names := map[string]struct{}{}
	for n := range configured {
		names[n] = struct{}{}
	}
	for n := range live {
		names[n] = struct{}{}
	}
	if len(names) == 0 {
		fmt.Println("No satellites configured.")
		return nil
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	var rows [][]string
	for _, name := range ordered {
		state := "not connected"
		addr := configured[name].SSHAddr
		since := "-"
		lastErr := ""
		if ls, ok := live[name]; ok {
			if ls.state != "" {
				state = ls.state
			}
			if ls.addr != "" {
				addr = ls.addr
			}
			if !ls.since.IsZero() {
				since = timeAgo(ls.since)
			}
			lastErr = ls.lastError
		} else if _, ok := configured[name]; ok {
			state = "not connected (restart groved?)"
		}
		if addr == "" {
			addr = "-"
		}
		rows = append(rows, []string{name, state, addr, since, lastErr})
	}

	tbl := table.NewStyledTable().
		Headers("Name", "State", "Addr", "Since", "Last Error").
		Rows(rows...)
	fmt.Println(tbl.Render())
	return nil
}

type satelliteLiveStatus struct {
	state     string
	addr      string
	since     time.Time
	lastError string
}

// loadConfiguredSatellites reads the [satellites.*] tables from the merged grove
// config (the same source P7's LoadRegistry parses).
func loadConfiguredSatellites() map[string]satelliteConfigEntry {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil
	}
	var raw map[string]satelliteConfigEntry
	_ = cfg.UnmarshalExtension("satellites", &raw)
	return raw
}

// satelliteRegistryPath resolves the global config file the [satellites.*]
// registry must land in: the SAME file core's loader (getXDGConfigPath in
// core/config/config.go) will read back. The loader prefers ConfigDir/grove.toml
// and only falls back to grove.yml when no grove.toml exists — so on a machine
// with a grove.toml, writing the entry into grove.yml would make it silently
// invisible to satellite.LoadRegistry and the whole up→daemon flow.
func satelliteRegistryPath() (path string, isTOML bool, err error) {
	configDir := paths.ConfigDir()
	if configDir == "" {
		return "", false, fmt.Errorf("could not resolve grove config directory")
	}
	tomlPath := filepath.Join(configDir, "grove.toml")
	if _, statErr := os.Stat(tomlPath); statErr == nil {
		return tomlPath, true, nil
	}
	return filepath.Join(configDir, "grove.yml"), false, nil
}

// writeSatelliteRegistry writes/updates the [satellites.<name>] entry in the
// global config file the loader actually reads (see satelliteRegistryPath).
// TOML configs get a targeted table splice (comment-preserving); YAML configs
// use bootstrap.go's yaml.Node plumbing (LoadYAML → SetValue → SaveYAML).
func writeSatelliteRegistry(name string, entry satelliteConfigEntry) error {
	path, isTOML, err := satelliteRegistryPath()
	if err != nil {
		return err
	}
	if isTOML {
		return upsertSatelliteTOMLTable(path, name, entry)
	}
	yh := setup.NewYAMLHandler(setup.NewService(false))
	root, err := yh.LoadYAML(path)
	if err != nil {
		return err
	}
	values := map[string]interface{}{
		"ssh_addr": entry.SSHAddr,
		"user":     entry.User,
		"host_key": entry.HostKey,
	}
	if entry.IdentityFile != "" {
		values["identity_file"] = entry.IdentityFile
	}
	if entry.SocketPath != "" {
		values["socket_path"] = entry.SocketPath
	}
	if entry.SyncLocalPort != 0 {
		values["sync_local_port"] = entry.SyncLocalPort
	}
	if entry.SyncRemoteAddr != "" {
		values["sync_remote_addr"] = entry.SyncRemoteAddr
	}
	if err := setup.SetValue(root, values, "satellites", name); err != nil {
		return err
	}
	return yh.SaveYAML(path, root)
}

// removeSatelliteRegistry deletes the [satellites.<name>] entry from whichever
// global config file the loader reads (symmetric with writeSatelliteRegistry).
func removeSatelliteRegistry(name string) error {
	path, isTOML, err := satelliteRegistryPath()
	if err != nil {
		return err
	}
	if isTOML {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content, removed := removeSatelliteTOMLTable(string(data), name)
		if !removed {
			fmt.Printf("(no [satellites.%s] entry in grove config)\n", name)
			return nil
		}
		return writeValidatedTOML(path, content)
	}
	yh := setup.NewYAMLHandler(setup.NewService(false))
	root, err := yh.LoadYAML(path)
	if err != nil {
		return err
	}
	if !setup.DeleteValue(root, "satellites", name) {
		fmt.Printf("(no [satellites.%s] entry in grove config)\n", name)
		return nil
	}
	return yh.SaveYAML(path, root)
}

// upsertSatelliteTOMLTable replaces-or-appends the [satellites.<name>] table in
// a TOML global config as a targeted text splice. go-toml/v2 does not
// round-trip comments or formatting, so re-marshaling the whole file (the
// setup.TOMLHandler approach) would destroy the user's grove.toml; instead only
// the one table block is edited and the rest of the file is preserved
// byte-for-byte.
func upsertSatelliteTOMLTable(path, name string, entry satelliteConfigEntry) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content, _ := removeSatelliteTOMLTable(string(data), name)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	var table strings.Builder
	fmt.Fprintf(&table, "\n[satellites.%s]\nssh_addr = %q\nuser = %q\nhost_key = %q\n",
		tomlTableKey(name), entry.SSHAddr, entry.User, entry.HostKey)
	// Optional fields are omitted when empty so entries stay minimal and the
	// daemon's defaults keep applying.
	if entry.IdentityFile != "" {
		fmt.Fprintf(&table, "identity_file = %q\n", entry.IdentityFile)
	}
	if entry.SocketPath != "" {
		fmt.Fprintf(&table, "socket_path = %q\n", entry.SocketPath)
	}
	if entry.SyncLocalPort != 0 {
		fmt.Fprintf(&table, "sync_local_port = %d\n", entry.SyncLocalPort)
	}
	if entry.SyncRemoteAddr != "" {
		fmt.Fprintf(&table, "sync_remote_addr = %q\n", entry.SyncRemoteAddr)
	}
	content += table.String()
	return writeValidatedTOML(path, content)
}

// writeValidatedTOML writes an edited TOML config back, refusing to persist
// content the loader could not parse (the splice helpers are textual, so this
// is the safety net against ever corrupting the user's global config).
func writeValidatedTOML(path, content string) error {
	if err := toml.Unmarshal([]byte(content), &map[string]interface{}{}); err != nil {
		return fmt.Errorf("refusing to write %s: edited TOML does not parse: %w", path, err)
	}
	perm := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(content), perm)
}

// bareTOMLKeyRe matches names usable as bare TOML keys; anything else is
// written (and matched) in quoted form.
var bareTOMLKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlTableKey renders a satellite name as a TOML table-path segment.
func tomlTableKey(name string) string {
	if bareTOMLKeyRe.MatchString(name) {
		return name
	}
	return fmt.Sprintf("%q", name)
}

// removeSatelliteTOMLTable removes the [satellites.<name>] block — the header
// line through the line before the next table header (or EOF) — from content.
// Returns the edited content and whether a block was found.
func removeSatelliteTOMLTable(content, name string) (string, bool) {
	quoted := regexp.QuoteMeta(name)
	header := regexp.MustCompile(`^\[\s*satellites\s*\.\s*(?:` + quoted + `|"` + quoted + `")\s*\]\s*(?:#.*)?$`)
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	removed := false
	skipping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipping {
			if !strings.HasPrefix(trimmed, "[") {
				continue // still inside the removed table's body
			}
			skipping = false
		}
		if header.MatchString(trimmed) {
			skipping = true
			removed = true
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), removed
}

// bestEffortDeregisterCursors calls the sync cursor-deregister endpoint (C19)
// so a destroyed cattle satellite stops pinning GC. The satellite's sync
// origin_id (C20) is minted per-install ON the VM and is not stored in the grove
// registry, so without --sync-origin-id we cannot target the exact cursor from
// the laptop — GC staleness eviction (C19) is the durable backstop that reclaims
// it after the retention window. Errors are swallowed: the VM (and its syncd) is
// about to be destroyed regardless.
func bestEffortDeregisterCursors(name, syncOriginID string) {
	if syncOriginID == "" {
		fmt.Printf("(satellite %q sync origin_id unknown; relying on GC staleness eviction (C19) to reclaim its cursor — pass --sync-origin-id to deregister precisely)\n", name)
		return
	}
	syncCfg, err := config.LoadSyncConfig()
	if err != nil || syncCfg == nil || syncCfg.Server == "" {
		fmt.Println("(no sync server configured; skipping cursor deregister)")
		return
	}
	token, err := syncCfg.ResolveToken()
	if err != nil {
		fmt.Printf("(could not resolve sync token; skipping cursor deregister: %v)\n", err)
		return
	}
	server := strings.TrimRight(syncCfg.Server, "/")
	httpClient := &http.Client{Timeout: 10 * time.Second}
	for _, ws := range syncCfg.Workspaces {
		url := fmt.Sprintf("%s/sync/cursor?workspace=%s&origin_id=%s", server, ws.Name, syncOriginID)
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			continue
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			fmt.Printf("(cursor deregister for workspace %q failed, best-effort: %v)\n", ws.Name, err)
			continue
		}
		_ = resp.Body.Close()
	}
}

// satelliteTFVarsName is the variables file `up` persists into the terraform
// dir (auto-loaded by terraform) so `down` can destroy non-interactively.
const satelliteTFVarsName = "terraform.tfvars"

// writeSatelliteTFVars persists the terraform variables that have no defaults
// in variables.tf (plus vm_name/zone/service_account_email) so a later
// terraform destroy resolves them without prompting — and, for the service
// account, so destroy plans the same instance shape apply created. Values are
// %q-quoted, which is valid HCL string syntax for these flag-derived inputs.
func writeSatelliteTFVars(tfDir, project, sshUser, cidr, vmName, zone, serviceAccountEmail string) error {
	var b strings.Builder
	b.WriteString("# Generated by `grove satellite up`. `grove satellite down` relies on this\n")
	b.WriteString("# file so terraform destroy runs without prompting for variables.\n")
	fmt.Fprintf(&b, "project_id       = %q\n", project)
	fmt.Fprintf(&b, "ssh_user         = %q\n", sshUser)
	fmt.Fprintf(&b, "allowed_ssh_cidr = %q\n", cidr)
	fmt.Fprintf(&b, "vm_name          = %q\n", vmName)
	if zone != "" {
		fmt.Fprintf(&b, "zone             = %q\n", zone)
	}
	if serviceAccountEmail != "" {
		fmt.Fprintf(&b, "service_account_email = %q\n", serviceAccountEmail)
	}
	return os.WriteFile(filepath.Join(tfDir, satelliteTFVarsName), []byte(b.String()), 0o600)
}

// --- small subprocess / prompt helpers ---

func confirmYesNo(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// runInherited runs a command with the caller's stdio attached (so terraform /
// the bootstrap script show their own prompts and progress).
func runInherited(dir, name string, args ...string) error {
	return runInheritedWithStdin(dir, "", name, args...)
}

// runInheritedWithStdin is runInherited with the child's stdin replaced by the
// given payload when non-empty (how bootstrap secrets travel — the framing is
// assembled by buildBootstrapProvision and never appears in argv). An empty
// payload keeps the caller's stdin attached.
func runInheritedWithStdin(dir, stdin, name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // G204: args are internal/flag-derived
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func terraformOutput(tfDir, name string) (string, error) {
	cmd := exec.Command("terraform", "-chdir="+tfDir, "output", "-raw", name) //nolint:gosec // G204: internal args
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// sshKeyscanHostKey retries ssh-keyscan until sshd is up, returning the ed25519
// host public key line body (the value pinned into the registry, C2).
func sshKeyscanHostKey(ip string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		cmd := exec.Command("ssh-keyscan", "-t", "ed25519", ip) //nolint:gosec // G204: ip from terraform output
		out, err := cmd.Output()
		if err == nil {
			if key := parseHostKey(string(out)); key != "" {
				return key, nil
			}
			lastErr = fmt.Errorf("ssh-keyscan returned no ed25519 key")
		} else {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("ssh-keyscan never returned a host key (sshd not up?): %w", lastErr)
}

// parseHostKey extracts the "<type> <base64>" body from ssh-keyscan output,
// dropping the leading host field and any comment lines.
func parseHostKey(scan string) string {
	for _, line := range strings.Split(scan, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		// Format: "<host> <keytype> <base64...>"
		if len(fields) >= 3 {
			return fields[1] + " " + fields[2]
		}
	}
	return ""
}

// detectPublicCIDR resolves the laptop's public IP as a /32 via ifconfig.me
// (matches the PoC README guidance).
func detectPublicCIDR() string {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("https://ifconfig.me/ip")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	ip := strings.TrimSpace(string(buf[:n]))
	if ip == "" {
		return ""
	}
	return ip + "/32"
}
