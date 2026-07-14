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
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/components/table"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	orch "github.com/grovetools/grove/pkg/orchestrator"
	"github.com/grovetools/grove/pkg/setup"
)

// satelliteConfigEntry mirrors the daemon's satellite.SatelliteConfig on-disk
// shape. It is redeclared here — rather than importing the daemon package —
// because `grove satellite` only reads/writes the grove config and the state
// file. The yaml tags bind the [satellites.<name>] config tables (they must
// match P7's SatelliteConfig so the laptop daemon's LoadRegistry decodes the
// same keys); the json tags bind the CLI-owned provisioning state file
// (satellites.json, see satellite_state.go) with the same field names.
type satelliteConfigEntry struct {
	SSHAddr string `yaml:"ssh_addr" json:"ssh_addr"`
	User    string `yaml:"user" json:"user"`
	HostKey string `yaml:"host_key" json:"host_key"`
	// IdentityFile is the optional SSH private key `up --identity-file` was
	// invoked with (empty = agent-only auth, matching SatelliteConfig).
	IdentityFile string `yaml:"identity_file" json:"identity_file,omitempty"`
	// SocketPath is the remote groved unix socket, probed post-bootstrap as
	// /run/user/<uid>/grove/groved.sock. Written explicitly because the
	// daemon's remoteSocketPath default guesses wrong on the real VM.
	SocketPath string `yaml:"socket_path" json:"socket_path,omitempty"`
	// SyncLocalPort is the laptop-local port the daemon binds
	// (127.0.0.1:<port>) and forwards to the VM's syncd over its pinned SSH
	// connection. 0/absent = daemon sync forward off (fixed M2 contract with
	// the daemon side).
	SyncLocalPort int `yaml:"sync_local_port" json:"sync_local_port,omitempty"`
	// SyncRemoteAddr is the VM-side syncd address the forward dials.
	// Optional; the daemon defaults it to 127.0.0.1:8788.
	SyncRemoteAddr string `yaml:"sync_remote_addr" json:"sync_remote_addr,omitempty"`
}

// newSatelliteCmd is the `grove satellite` noun. It wraps the embedded
// terraform/bootstrap assets (grove/cmd/satelliteassets, formerly the
// cloud/poc/grove-satellite runbook) into VM lifecycle verbs and writes the
// [satellites.<name>] registry entry P7's ConnManager reads at daemon boot
// (M2 contract C1/C2).
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
	cmd.AddCommand(newSatelliteReposCmd())
	cmd.AddCommand(newSatelliteConfigCmd())
	cmd.AddCommand(newSatelliteDownCmd())
	cmd.AddCommand(newSatelliteStatusCmd())
	cmd.AddCommand(newSatelliteListCmd())
	return cmd
}

// tfDirFlagHelp documents --tf-dir, shared by up and down: empty (the
// default) means the per-satellite state dir with the embedded target module
// extracted into it; a value is the bring-your-own-module escape hatch.
const tfDirFlagHelp = "Bring-your-own terraform module dir, used as-is (no embedded-module extraction or legacy-state migration; must honor grove/cmd/satelliteassets/CONTRACT.md). Default: the per-satellite state dir under ~/.local/state/grove/satellites/<name>/terraform"

func newSatelliteUpCmd() *cobra.Command {
	var (
		project        string
		sshUser        string
		cidr           string
		zone           string
		target         string
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
		prebuilt       bool
		prebuiltTarget string
		sourceDir      string
	)
	cmd := cli.NewStandardCommand("up <name>", "Provision a satellite VM (billable)")
	cmd.Long = `Provision a satellite VM: terraform apply, host-key pin, bootstrap, registry.

Bootstrap mode: by default the VM clones the grovetools ecosystem and builds
the grove stack from source. With --prebuilt the stack (grove, groved, flow,
nb, treemux, tuimux, grove-syncd) is instead cross-compiled on the laptop
(grove build --prebuilt-target, default linux/amd64), shipped over the pinned
SSH connection, and installed before bootstrap runs — no VM-side git clone,
make, Go, or zig for the grove stack. --source-dir picks the ecosystem worktree
to build from (default: the go.work root above cwd). NOTE: the cross-compile
target is --prebuilt-target, not --target (--target selects the infra module).

Infra inputs (--project, --zone, --ssh-user, --cidr, --identity-file,
--target) default from a [satellites.<name>.infra] block in the same grove
config; explicit
flags override it. A successful 'up' writes the resolved values back into the
block — and 'down' leaves the block in place — so only the very first
provision of a name needs the flags:

  [satellites.mysat.infra]
  project = "my-gcp-project"
  ssh_user = "grovedev"
  cidr = "203.0.113.7/32"

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
	cmd.Flags().StringVar(&project, "project", "", "GCP project id (terraform var project_id) [required unless [satellites.<name>.infra] project is set]")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH login user on the VM (terraform var ssh_user) [required unless [satellites.<name>.infra] ssh_user is set]")
	cmd.Flags().StringVar(&cidr, "cidr", "", "CIDR allowed to reach :22 (default: [satellites.<name>.infra] cidr, else your public IP /32 via ifconfig.me)")
	cmd.Flags().StringVar(&zone, "zone", "", "GCP zone override (terraform var zone; default: [satellites.<name>.infra] zone)")
	cmd.Flags().StringVar(&target, "target", "", "Embedded infra target whose terraform module provisions the VM (default: [satellites.<name>.infra] target, else \"gcp\")")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", tfDirFlagHelp)
	cmd.Flags().StringVar(&identityFile, "identity-file", "", "SSH private key for the satellite (written to the registry as identity_file; default: [satellites.<name>.infra] identity_file, else agent-only auth)")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the billable-resource confirmation prompt")
	cmd.Flags().StringVar(&ghTokenCmd, "gh-token-cmd", "", "Local command whose stdout is the GitHub token piped to bootstrap (overrides provision config; empty disables)")
	cmd.Flags().BoolVar(&claudeFlag, "claude", false, "Install Claude Code on the VM (overrides provision config)")
	cmd.Flags().StringVar(&claudeTokenCmd, "claude-token-cmd", "", "Local command producing CLAUDE_CODE_OAUTH_TOKEN, piped to bootstrap; implies --claude (overrides provision config; empty disables)")
	cmd.Flags().StringVar(&dotfilesRepo, "dotfiles-repo", "", "Dotfiles repo cloned + installed on the VM, best-effort (overrides provision config; empty disables)")
	cmd.Flags().StringVar(&serviceAccount, "service-account", "", "Service account email attached to the VM (terraform var service_account_email; overrides provision config; empty disables)")
	cmd.Flags().IntVar(&syncPort, "sync-port", defaultSyncLocalPort, "Laptop-local port the daemon binds and forwards to the VM's syncd (registry sync_local_port; 0 disables the sync forward and laptop sync setup)")
	cmd.Flags().StringVar(&syncWorkspaces, "sync-workspaces", "", "Comma-separated workspaces to sync with the VM (overrides [satellites.<name>.sync] workspaces; default "+strings.Join(defaultSatelliteSyncWorkspaces, ",")+")")
	cmd.Flags().BoolVar(&reloadDaemon, "reload-daemon", false, "Run 'groved upgrade --global' at the end (a FULL daemon restart). Rarely needed now: the registry hot-reloads via the daemon API automatically; this remains for picking up a rebuilt groved binary or boot-only config (e.g. sync transport) in the same breath")
	cmd.Flags().BoolVar(&prebuilt, "prebuilt", false, "Provision from locally cross-compiled binaries instead of a VM-side git clone + source build: the grove stack (grove, groved, flow, nb, treemux, tuimux, grove-syncd) is built on the laptop, shipped over the pinned SSH connection, and installed before bootstrap")
	// NOTE: the cross-compile target is --prebuilt-target, NOT --target:
	// `up`'s existing --target selects the INFRA module (e.g. gcp), a different
	// axis from the <goos>/<goarch> build target. (`satellite upgrade` has no
	// infra --target, so there it is spelled --target.)
	cmd.Flags().StringVar(&prebuiltTarget, "prebuilt-target", "linux/amd64", "Cross-compile target for --prebuilt as <goos>/<goarch> (the VM arch)")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "Local ecosystem worktree root the --prebuilt stack is cross-built from (default: the go.work root above cwd)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Infra inputs: [satellites.<name>.infra] with flag overrides — the
		// five infra flags' persistent config home (same flag>config stance
		// as the provision block), so a repeat 0→1 after `down` (which keeps
		// the subtable) retypes nothing. The required check runs on the
		// MERGED values, making a flag-less `up <name>` valid once the block
		// is populated — `up` writes the resolved values back below (5c').
		infraCfg, infraInConfig, err := loadSatelliteInfra(name)
		if err != nil {
			return err
		}
		infra := mergeInfra(infraCfg, infraFlagOverrides{
			Project: project, ProjectSet: cmd.Flags().Changed("project"),
			Zone: zone, ZoneSet: cmd.Flags().Changed("zone"),
			SSHUser: sshUser, SSHUserSet: cmd.Flags().Changed("ssh-user"),
			CIDR: cidr, CIDRSet: cmd.Flags().Changed("cidr"),
			IdentityFile: identityFile, IdentitySet: cmd.Flags().Changed("identity-file"),
			Target: target, TargetSet: cmd.Flags().Changed("target"),
		})
		project, sshUser, cidr, zone, identityFile = infra.Project, infra.SSHUser, infra.CIDR, infra.Zone, infra.IdentityFile
		if project == "" || sshUser == "" {
			return fmt.Errorf("--project and --ssh-user are required (or persist them in [satellites.%s.infra] — a successful `up` writes the block for you)", name)
		}
		// Infra target: validated against the embedded targets even when
		// --tf-dir bypasses extraction (an unknown name in config is a typo
		// either way). Empty resolves to the gcp default.
		resolvedTarget, err := resolveSatelliteTarget(infra.Target)
		if err != nil {
			return err
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

		// Repo-mirror inputs ([satellites.<name>.repos], falling back to the
		// sync workspaces): loaded NOW — before terraform — so a malformed
		// block aborts while the provision is still free. The mirror itself
		// runs after bootstrap (prebuilt mode only; source mode clones the
		// real ecosystem).
		reposCfg, err := loadSatelliteReposOptions(name)
		if err != nil {
			return err
		}

		// Prebuilt-bootstrap inputs: validate NOW — before terraform — so a bad
		// --prebuilt-target, a missing ecosystem worktree, or a missing
		// grove-syncd unit aborts while the provision is still free (same
		// fail-fast stance as the token commands). The cross-build itself runs
		// after apply (it needs the VM to ship to). --prebuilt-target /
		// --source-dir are meaningless without --prebuilt.
		var prebuiltXTarget orch.Target
		var sourceAbs string
		if prebuilt {
			t, err := orch.ParseTarget(prebuiltTarget)
			if err != nil {
				return fmt.Errorf("--prebuilt-target: %w", err)
			}
			prebuiltXTarget = t
			if sourceDir == "" {
				root, rerr := defaultUpgradeSourceDir()
				if rerr != nil {
					return rerr
				}
				sourceDir = root
			}
			if sourceAbs, err = filepath.Abs(sourceDir); err != nil {
				return fmt.Errorf("resolve --source-dir: %w", err)
			}
			if err := validateSatellitePrebuiltStack(sourceAbs); err != nil {
				return err
			}
		} else {
			if cmd.Flags().Changed("prebuilt-target") {
				return fmt.Errorf("--prebuilt-target only applies to --prebuilt")
			}
			if cmd.Flags().Changed("source-dir") {
				return fmt.Errorf("--source-dir only applies to --prebuilt")
			}
		}

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

		// Terraform dir: the per-satellite state dir with the embedded
		// target module extracted into it (module files overwritten every
		// run, tfstate/tfvars/.terraform never touched; legacy worktree
		// state migrated in first) — or, with --tf-dir, a BYO module dir
		// used as-is.
		tfAbs, err := resolveSatelliteTerraformDir(name, resolvedTarget, tfDir, os.Stdout)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(tfAbs, "variables.tf")); err != nil {
			return fmt.Errorf("terraform dir %q does not look like a grove-satellite module (no variables.tf; the contract is documented in grove/cmd/satelliteassets/CONTRACT.md): %w", tfAbs, err)
		}
		// Bootstrap script: always the embedded copy (extracted next to the
		// per-satellite terraform dir) so it can never skew from this CLI —
		// even when --tf-dir brings a custom module.
		bootstrapScript, err := extractSatelliteBootstrap(name)
		if err != nil {
			return fmt.Errorf("extract embedded bootstrap script: %w", err)
		}

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

		// 3b. Prebuilt bootstrap: cross-build the grove stack locally, ship it
		//     over a transport that PINS the host key just scanned (never TOFU,
		//     C2), install the binaries + ship the grove-syncd systemd unit —
		//     all BEFORE the bootstrap script runs, so grove-syncd + groved
		//     exist on the VM when bootstrap's step 6/8 enable them under
		//     systemd. On a fresh VM a partial install is a broken satellite
		//     (unlike upgrade, where the old binaries keep running), so any
		//     install failure aborts here rather than proceeding to bootstrap.
		var syncdUnitRemotePath string
		if prebuilt {
			pinnedEntry := satelliteConfigEntry{SSHAddr: ip + ":22", User: sshUser, HostKey: hostKey}
			if identityFile != "" {
				pinnedEntry.IdentityFile = expandUserPath(identityFile)
			}
			shipDir, mkErr := os.MkdirTemp("", "grove-satellite-up-prebuilt-")
			if mkErr != nil {
				return mkErr
			}
			defer func() { _ = os.RemoveAll(shipDir) }()
			ssh, sshErr := newSatelliteSSH(pinnedEntry, shipDir)
			if sshErr != nil {
				return fmt.Errorf("pinned ssh transport for prebuilt ship: %w", sshErr)
			}
			// sshd answering keyscan ≠ auth ready: on a fresh VM the guest
			// agent writes authorized_keys from metadata seconds after boot.
			if err := waitForSatelliteSSHAuth(ssh, 3*time.Minute); err != nil {
				return err
			}
			// Ship the grove-syncd systemd unit sourced from the ecosystem
			// worktree (no source tree on the VM to copy it from). Its remote
			// path is handed to bootstrap via --syncd-unit.
			if syncdUnitRemotePath, err = shipSatelliteSyncdUnit(ssh, sourceAbs); err != nil {
				return fmt.Errorf("ship grove-syncd unit: %w", err)
			}
			// compositor is a library (no binaries) but grove/flow/nb/treemux/
			// tuimux link its per-target zig static libs; cross-build it FIRST
			// as its own wave so those libs exist before the ship set compiles.
			// It is NOT in the ship set (deploySatellitePrebuilt would drop it as
			// "no binaries"), so it cannot be built as part of that call. Cached
			// per build@<target>, so a warm laptop re-runs cheaply. (This is why
			// a fresh `up --prebuilt` works where `upgrade --prebuilt` assumes
			// the libs already exist and holds compositor.)
			fmt.Printf("Cross-building compositor's %s zig libraries (linked by the grove stack)...\n", prebuiltXTarget)
			compResults, cErr := BuildReposForTarget(context.Background(), sourceAbs, []string{"compositor"}, prebuiltXTarget, 0)
			if cErr != nil {
				return fmt.Errorf("cross-build compositor libs: %w", cErr)
			}
			for _, r := range compResults {
				if r.Err != nil {
					return fmt.Errorf("cross-build compositor libs failed:\n%s", tailLines(string(r.Output), 40))
				}
			}
			// Cross-build + package + scp + install the fixed prebuilt stack,
			// reusing the exact machinery `satellite upgrade --prebuilt` uses.
			updates, dirty, dErr := satellitePrebuiltStackDeltas(sourceAbs)
			if dErr != nil {
				return dErr
			}
			shipped, deployErr, fatal := deploySatellitePrebuilt(ssh, name, sourceAbs, prebuiltXTarget, updates, dirty)
			if fatal != nil {
				return fatal
			}
			if deployErr != nil {
				return fmt.Errorf("prebuilt install failed on the fresh VM (nothing bootstrapped yet — fix and re-run `grove satellite up %s --prebuilt`, or `grove satellite down %s`): %w", name, name, deployErr)
			}
			fmt.Printf("Prebuilt grove stack installed on %q (%s).\n", name, strings.Join(shipped, ", "))
		}

		// 4. Bootstrap the VM (subprocess, inherited stdout/stderr). Provision
		//    options become the script's flags; tokens ride stdin in its
		//    documented framing (raw single token, or KEY=VALUE lines when both
		//    are present) — never argv. The script already fetches the laptop
		//    sync token to ~/.config/grove/sync.token.
		provArgs, secretStdin := buildBootstrapProvision(prov, ghToken, claudeToken)
		// The resolved sync workspaces drive the VM's pull-enabled sync.toml
		// (bootstrap step 5). Always passed — an explicitly empty flag value
		// means "no sync workspaces" on the VM too.
		provArgs = append(provArgs, "--workspaces", strings.Join(resolvedSyncWorkspaces, ","))
		// Prebuilt mode: tell bootstrap to SKIP the clone/build/source-unit-copy
		// steps and install the CLI-shipped grove-syncd unit instead.
		if prebuilt {
			provArgs = append(provArgs, "--prebuilt", "--syncd-unit", syncdUnitRemotePath)
		}
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

		// 5c. Upsert the satellite's entry in the CLI-owned provisioning
		// state file (satellites.json under the state dir). This is STATE,
		// not config: the daemon's LoadRegistry merges it with any
		// [satellites.<name>] config table at boot (state wins for the
		// machine-derived fields), so nothing is written into grove.toml.
		if err := upsertSatelliteState(name, entry); err != nil {
			return fmt.Errorf("write satellite state entry: %w", err)
		}

		// 5c'. Infra inputs write-back, config-respecting: when the merged
		// config ALREADY carries a [satellites.<name>.infra] block (e.g. in a
		// dotfiles fragment), grove never edits it — if the resolved values
		// drifted from it, print the up-to-date block for the user to paste.
		// Only when no block exists anywhere does `up` persist one into the
		// global config so the next 0→1 needs zero flags (`down` leaves the
		// subtable in place). Non-fatal either way: the VM is provisioned and
		// registered.
		infra.CIDR = cidr // may have been auto-detected after the merge
		if infraInConfig {
			fmt.Print(satelliteInfraDriftMessage(name, infraCfg, infra))
		} else if err := writeSatelliteInfra(name, infra); err != nil {
			fmt.Printf("warning: could not persist [satellites.%s.infra] defaults: %v\n", name, err)
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

		// 5d'. Repo mirror (prebuilt only — source mode cloned the real
		// ecosystem in bootstrap step 3): ship the laptop's committed repo
		// tips as git bundles and force-checkout them under ~/code/grovetools,
		// seeding the ecosystem root files. Real repos satisfy workspace
		// discovery — this replaces the old bootstrap placeholder skeleton.
		// Non-fatal: the VM is provisioned and registered; a transport hiccup
		// here is retried with the verb.
		if prebuilt {
			mirrorRepos := resolveSatelliteMirrorRepos(nil, false, reposCfg, resolvedSyncWorkspaces)
			if len(mirrorRepos) > 0 {
				fmt.Printf("\nMirroring %d repo(s) to %q...\n", len(mirrorRepos), name)
				// force=false: a fresh VM has every repo MISSING (bootstrap
				// seeds no checkouts), so the unfetched-commits interlock
				// never triggers here — and if it somehow did (re-run against
				// a VM someone committed on), holding is the right call.
				if err := pushSatelliteReposOverSSH(name, entry, sourceAbs, defaultRemoteCodeDir, mirrorRepos, false, false, true, false); err != nil {
					fmt.Printf("warning: repo mirror failed: %v\n", err)
					fmt.Printf("  retry with: grove satellite repos push %s\n", name)
				}
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
			// Live token check: the stat inside setupLaptopSyncConfig only
			// proves a token FILE exists — a stale token from a previous VM
			// passes it and the daemon then 401-loops silently. Probe this
			// VM's syncd with the token over the pinned SSH transport (syncd
			// is VM-loopback-only, and the daemon forward may not be up yet —
			// the daemon reads its registry at boot). Bootstrap is being
			// fixed to self-heal the stale-token case; this is the backstop.
			fmt.Printf("Verifying the laptop sync token against %q's syncd...\n", name)
			if err := verifySatelliteSyncTokenOverSSH(entry, filepath.Join(laptopConfigDir, syncTokenFileName)); err != nil {
				return fmt.Errorf("laptop sync token verification (the VM is provisioned and registered; fix and re-run `grove satellite up %s`): %w", name, err)
			}
			fmt.Println("Laptop sync token verified against the VM's syncd.")
		}

		// 6. Registry hot-reload + next steps. The FINAL step: POST the global
		//    daemon's /api/satellites/reload so its ConnManager connects (and
		//    binds the sync forward) right now — no agent-killing restart. The
		//    sync TRANSPORT registration in groved.go still loads only at boot,
		//    but the daemon-owned forward this entry uses does not depend on it.
		//    Soft-fail: with no daemon running or an old groved (404), the next
		//    steps below fall back to the manual reload instruction.
		fmt.Printf("\nSatellite %q provisioned at %s.\n", name, ip)
		summary, reloaded := reloadDaemonSatelliteRegistry()
		if reloaded {
			fmt.Printf("Daemon satellite registry hot-reloaded (%s).\n", formatReloadSummary(summary))
		}
		printSatelliteNextSteps(true, reloaded, syncPort)
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
		target       string
		tfDir        string
		assumeYes    bool
		syncOriginID string
	)
	cmd := cli.NewStandardCommand("down <name>", "Destroy a satellite VM and remove its registry entry")
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&target, "target", "", "Embedded infra target whose terraform module to extract for the destroy (default: [satellites.<name>.infra] target, else \"gcp\")")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", tfDirFlagHelp)
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the destroy confirmation prompt")
	cmd.Flags().StringVar(&syncOriginID, "sync-origin-id", "", "Satellite sync origin_id to deregister precisely (best-effort; see C19/C20)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Resolve the terraform dir the same way `up` does: the per-name
		// state dir (with legacy worktree tfstate migrated in and the
		// embedded module re-extracted so destroy always has current .tf
		// files), or --tf-dir as-is. The target comes from the same
		// [satellites.<name>.infra] block, --target winning.
		infraCfg, _, err := loadSatelliteInfra(name)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("target") {
			infraCfg.Target = target
		}
		resolvedTarget, err := resolveSatelliteTarget(infraCfg.Target)
		if err != nil {
			return err
		}
		tfAbs, err := resolveSatelliteTerraformDir(name, resolvedTarget, tfDir, os.Stdout)
		if err != nil {
			return err
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
		// init first: the per-name state dir starts without .terraform/ (a
		// legacy migration copies only tfstate+tfvars, and extraction ships
		// only module files). Idempotent and near-free when already
		// initialized.
		if err := runInherited(tfAbs, "terraform", "-chdir="+tfAbs, "init", "-input=false"); err != nil {
			return fmt.Errorf("terraform init: %w", err)
		}
		destroyArgs := []string{"-chdir=" + tfAbs, "destroy", "-input=false", "-var", "vm_name=" + name}
		if err := runInherited(tfAbs, "terraform", destroyArgs...); err != nil {
			return fmt.Errorf("terraform destroy: %w", err)
		}

		// 3. Remove the satellite's provisioning state entry, plus any LEGACY
		// flat [satellites.<name>] entry an older `up` wrote into the global
		// config (that removal is cleanup of CLI-written state, not user
		// config — subtables like .infra/.provision/.sync always survive).
		removedState, err := removeSatelliteState(name)
		if err != nil {
			return fmt.Errorf("remove satellite state entry: %w", err)
		}
		legacyRemoved, legacyErr := removeLegacySatelliteConfigEntry(name)
		if legacyErr != nil {
			fmt.Printf("warning: could not remove the legacy [satellites.%s] entry from the grove config: %v\n", name, legacyErr)
		}
		if legacyRemoved {
			fmt.Printf("(removed legacy [satellites.%s] entry from the grove config)\n", name)
		}
		if !removedState && !legacyRemoved {
			fmt.Printf("(no satellite state or registry entry for %q)\n", name)
		}

		// 4. Registry hot-reload, the FINAL step: the daemon drops the dead
		//    satellite's connection, sync port-forward, status entry, and
		//    federated rows immediately. Soft-fail to the manual instruction
		//    when the daemon isn't running or predates the endpoint.
		fmt.Printf("\nSatellite %q destroyed and deregistered.\n", name)
		if summary, ok := reloadDaemonSatelliteRegistry(); ok {
			fmt.Printf("Daemon satellite registry hot-reloaded (%s); its connection and\n", formatReloadSummary(summary))
			fmt.Println("sync port-forward are torn down.")
		} else {
			fmt.Println("Restart the global daemon to drop its connection (the sync port-forward")
			fmt.Println("disappears with the registry entry):")
			fmt.Println("  groved upgrade --global")
		}
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

// renderSatellites merges the registry entries (config ∪ state, the daemon's
// LoadRegistry view) with live ConnManager health from the global daemon
// (client.GetSatelliteStatuses, the Store surface P7 emits via the new GET
// /api/satellites read endpoint). With the daemon unreachable the merged
// registry view still renders every configured/provisioned satellite.
func renderSatellites() error {
	configured := loadMergedSatellites()

	live := map[string]satelliteLiveStatus{}
	client := daemon.New()
	if client.IsRunning() {
		ctx := context.Background()
		if statuses, err := client.GetSatelliteStatuses(ctx); err == nil {
			for name, st := range statuses {
				if st == nil {
					continue
				}
				live[name] = satelliteLiveStatus{state: st.State, addr: st.Addr, since: st.Since, lastError: st.LastError, forward: st.Forward}
			}
		}
	}

	rows := satelliteTableRows(configured, live)
	if rows == nil {
		fmt.Println("No satellites configured.")
		return nil
	}

	tbl := table.NewStyledTable().
		Headers(satelliteTableHeaders...).
		Rows(rows...)
	fmt.Println(tbl.Render())
	return nil
}

// reloadDaemonSatelliteRegistry POSTs the global daemon's satellite registry
// hot-reload endpoint (POST /api/satellites/reload) so the ConnManager applies
// a just-written state-file change — the final step of `up` and `down`, making
// the agent-killing `groved upgrade --global` unnecessary for registry churn.
//
// It targets the GLOBAL socket explicitly rather than daemon.New(): under
// GROVE_SCOPE (set inside treemux sessions) New() resolves a scoped daemon,
// which has no ConnManager and rejects the endpoint. Best-effort by design:
// ok=false — daemon not running (no socket), an old groved without the
// endpoint (404), or a reload failure — means the caller falls back to
// printing the manual reload instruction. Failures other than "not running"
// print a one-line note so the downgrade isn't silent.
func reloadDaemonSatelliteRegistry() (summary *models.SatelliteReloadSummary, ok bool) {
	sock := paths.SocketPath("")
	if _, err := os.Stat(sock); err != nil {
		return nil, false // daemon not running — the normal soft-fail
	}
	client, err := daemon.NewRemoteClient(sock)
	if err != nil {
		return nil, false
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	summary, err = client.ReloadSatellites(ctx)
	if err != nil {
		fmt.Printf("(daemon registry hot-reload unavailable: %v)\n", err)
		return nil, false
	}
	return summary, true
}

// formatReloadSummary renders a ReloadSummary as a compact one-liner for the
// up/down output, e.g. "added: mysat" or "removed: mysat; unchanged: other".
// Unchanged entries are elided (they are noise next to the verb's action);
// an all-empty summary reads "no changes".
func formatReloadSummary(s *models.SatelliteReloadSummary) string {
	var parts []string
	for _, group := range []struct {
		label string
		names []string
	}{{"added", s.Added}, {"removed", s.Removed}, {"changed", s.Changed}} {
		if len(group.names) > 0 {
			parts = append(parts, group.label+": "+strings.Join(group.names, ", "))
		}
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, "; ")
}

// satelliteTableHeaders is the column contract for satelliteTableRows.
var satelliteTableHeaders = []string{"Name", "State", "Addr", "Forward", "Since", "Last Error"}

// satelliteTableRows builds the status/list table rows from the union of
// configured + live names, so a just-added (not-yet-connected) satellite still
// shows, and vice versa. Returns nil when there is nothing to show.
func satelliteTableRows(configured map[string]satelliteConfigEntry, live map[string]satelliteLiveStatus) [][]string {
	names := map[string]struct{}{}
	for n := range configured {
		names[n] = struct{}{}
	}
	for n := range live {
		names[n] = struct{}{}
	}
	if len(names) == 0 {
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
		forward := "-"
		since := "-"
		lastErr := ""
		if ls, ok := live[name]; ok {
			if ls.state != "" {
				state = ls.state
			}
			if ls.addr != "" {
				addr = ls.addr
			}
			if ls.forward != "" {
				forward = ls.forward
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
		rows = append(rows, []string{name, state, addr, forward, since, lastErr})
	}
	return rows
}

type satelliteLiveStatus struct {
	state     string
	addr      string
	since     time.Time
	lastError string
	forward   string
}

// loadConfiguredSatellites reads the [satellites.*] tables from the merged
// grove config — the CONFIG half of the registry only. Almost every caller
// wants loadMergedSatellites (config ∪ state) instead.
func loadConfiguredSatellites() map[string]satelliteConfigEntry {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil
	}
	var raw map[string]satelliteConfigEntry
	_ = cfg.UnmarshalExtension("satellites", &raw)
	return raw
}

// satelliteRegistryPath resolves the global config file the loader
// (getXDGConfigPath in core/config/config.go) reads: ConfigDir/grove.toml,
// falling back to grove.yml only when no grove.toml exists. `up` no longer
// writes registry entries here (they live in the state file now); this path
// still hosts the [satellites.<name>.infra] write-back and the legacy flat
// entry cleanup `down` performs.
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

// removeLegacySatelliteConfigEntry deletes a LEGACY flat [satellites.<name>]
// entry from the global config file — cleanup for entries an older `up` wrote
// before the config/state split. Subtables (.infra/.provision/.sync) are never
// touched (removeSatelliteTOMLTable matches only the flat block). Reports
// whether an entry was actually removed; an absent config file or absent
// entry is a clean no-op.
func removeLegacySatelliteConfigEntry(name string) (bool, error) {
	path, isTOML, err := satelliteRegistryPath()
	if err != nil {
		return false, err
	}
	if isTOML {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		content, removed := removeSatelliteTOMLTable(string(data), name)
		if !removed {
			return false, nil
		}
		return true, writeValidatedTOML(path, content)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	yh := setup.NewYAMLHandler(setup.NewService(false))
	root, err := yh.LoadYAML(path)
	if err != nil {
		return false, err
	}
	if !setup.DeleteValue(root, "satellites", name) {
		return false, nil
	}
	return true, yh.SaveYAML(path, root)
}

// upsertSatelliteInfraTOMLTable replaces-or-appends the
// [satellites.<name>.infra] subtable with the same comment-preserving splice
// stance as upsertSatelliteTOMLTable: only the one subtable block is edited —
// [satellites.<name>] itself, its other subtables (.provision/.sync), and
// every other satellite's blocks stay byte-for-byte.
func upsertSatelliteInfraTOMLTable(path, name string, infra satelliteInfraConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content, _ := removeSatelliteTOMLSubtable(string(data), name, "infra")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + renderSatelliteInfraTOML(name, infra)
	return writeValidatedTOML(path, content)
}

// renderSatelliteInfraTOML renders the [satellites.<name>.infra] block —
// shared between the config write-back (no block exists yet) and the drift
// print (a user-owned block exists; grove prints the up-to-date block instead
// of editing it). Empty fields are omitted so unset inputs (zone,
// identity_file) do not pin empty strings as future defaults.
func renderSatelliteInfraTOML(name string, infra satelliteInfraConfig) string {
	var table strings.Builder
	fmt.Fprintf(&table, "[satellites.%s.infra]\n", tomlTableKey(name))
	if infra.Target != "" {
		fmt.Fprintf(&table, "target = %q\n", infra.Target)
	}
	if infra.Project != "" {
		fmt.Fprintf(&table, "project = %q\n", infra.Project)
	}
	if infra.Zone != "" {
		fmt.Fprintf(&table, "zone = %q\n", infra.Zone)
	}
	if infra.SSHUser != "" {
		fmt.Fprintf(&table, "ssh_user = %q\n", infra.SSHUser)
	}
	if infra.CIDR != "" {
		fmt.Fprintf(&table, "cidr = %q\n", infra.CIDR)
	}
	if infra.IdentityFile != "" {
		fmt.Fprintf(&table, "identity_file = %q\n", infra.IdentityFile)
	}
	return table.String()
}

// satelliteInfraDriftMessage compares the config's [satellites.<name>.infra]
// block with the values this `up` actually resolved (flags + auto-detection
// over config). When they differ, it returns the per-field drift plus the
// up-to-date block for the user to paste — grove never edits a block the
// config already carries (it may live in a read-only dotfiles fragment).
// Empty when nothing drifted.
func satelliteInfraDriftMessage(name string, fromConfig, resolved satelliteInfraConfig) string {
	if fromConfig == resolved {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nnote: resolved infra inputs differ from your [satellites.%s.infra] config block.\n", name)
	b.WriteString("grove does not edit existing config blocks — update yours if you want the new values:\n")
	for _, f := range []struct{ key, cfg, res string }{
		{"target", fromConfig.Target, resolved.Target},
		{"project", fromConfig.Project, resolved.Project},
		{"zone", fromConfig.Zone, resolved.Zone},
		{"ssh_user", fromConfig.SSHUser, resolved.SSHUser},
		{"cidr", fromConfig.CIDR, resolved.CIDR},
		{"identity_file", fromConfig.IdentityFile, resolved.IdentityFile},
	} {
		if f.cfg != f.res {
			fmt.Fprintf(&b, "  %s: %q -> %q\n", f.key, f.cfg, f.res)
		}
	}
	b.WriteString("\nUp-to-date block:\n\n")
	b.WriteString(renderSatelliteInfraTOML(name, resolved))
	return b.String()
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
// The header regex requires "]" right after the name, so subtables like
// [satellites.<name>.infra] or .provision are NOT matched (and, being table
// headers themselves, also stop the skip) — which is exactly why they survive
// a down/up cycle. Returns the edited content and whether a block was found.
func removeSatelliteTOMLTable(content, name string) (string, bool) {
	quoted := regexp.QuoteMeta(name)
	header := regexp.MustCompile(`^\[\s*satellites\s*\.\s*(?:` + quoted + `|"` + quoted + `")\s*\]\s*(?:#.*)?$`)
	return removeTOMLBlock(content, header)
}

// removeSatelliteTOMLSubtable is removeSatelliteTOMLTable for one
// [satellites.<name>.<sub>] subtable block (e.g. sub = "infra").
func removeSatelliteTOMLSubtable(content, name, sub string) (string, bool) {
	quoted := regexp.QuoteMeta(name)
	header := regexp.MustCompile(`^\[\s*satellites\s*\.\s*(?:` + quoted + `|"` + quoted + `")\s*\.\s*` + regexp.QuoteMeta(sub) + `\s*\]\s*(?:#.*)?$`)
	return removeTOMLBlock(content, header)
}

// removeTOMLBlock removes the block whose header line matches header — the
// header through the line before the next table header (or EOF).
func removeTOMLBlock(content string, header *regexp.Regexp) (string, bool) {
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
