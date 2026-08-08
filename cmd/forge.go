package cmd

// `grove forge` — the services VM that hosts the ecosystem's self-hosted
// Forgejo and, colocated with it, grove-syncd.
//
// A DISTINCT NOUN from `grove satellite`, on purpose. Satellites are cattle:
// disposable, plural, and `satellite down` is a routine verb because the
// registry is the truth. The forge is a pet: singular, and its boot disk
// accumulates every plan branch ever pushed plus the PR/review history in
// SQLite. Pointing a cattle verb at it is exactly the accident this separation
// exists to make impossible — so `forge down` requires --force AND the typed
// instance name, and says what is on the disk before it asks.
//
// What IS shared with the satellite is machinery, by call rather than by copy:
// the terraform extraction discipline (writeFileAtomic,
// isSatelliteLocalTFArtifact), the subprocess helpers (runInherited), the
// confirmation prompt (confirmOrAbort), the pinned-SSH transport
// (newSatelliteSSH) and the cross-build path (BuildReposForTargetLocal).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

// forgeTFDirFlagHelp documents --tf-dir, shared by every verb that runs
// terraform.
const forgeTFDirFlagHelp = "Bring-your-own terraform module dir, used as-is (no embedded-module extraction; must honor grove/cmd/forgeassets/CONTRACT.md). Default: ~/.local/state/grove/forge/terraform"

// forgePlanChangesExitCode mirrors `terraform plan -detailed-exitcode`'s
// "changes pending" status, and the exit code `grove env drift` already uses
// for the same meaning — so a scripted caller can tell "drift" from "broken".
const forgePlanChangesExitCode = 2

func newForgeCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("forge", "Manage the self-hosted forge services VM (pet, not cattle)")
	cmd.Long = `Provision, inspect and tear down the grove forge: one always-on VM running
Forgejo (git rendezvous, PRs, web UI, API) and grove-syncd (notebook event log),
colocated behind one TLS story.

The forge is a PET. It accumulates durable refs — every plan branch ever pushed
— so 'down' is deliberately harder to run than 'satellite down': it requires
--force and the instance name typed back. 'up' is billable.

Everything is configured in [forge.infra] and [forge.services]; the verbs take
no infrastructure flags, so what terraform sees is always what the config says:

  [forge]
  url = "https://forge.example.com"     # what the daemon polls (see 'grove forge status')

  [forge.infra]
  project  = "my-gcp-project"
  ssh_user = "grovedev"
  cidr     = "203.0.113.7/32"           # 0.0.0.0/0 is refused, here and in terraform

  [forge.services.forgejo]
  version = "16.0.2"
  sha256  = "..."                       # pinned download; a version without one is not a pin`
	cmd.AddCommand(newForgePlanCmd())
	cmd.AddCommand(newForgeUpCmd())
	cmd.AddCommand(newForgeStatusCmd())
	cmd.AddCommand(newForgeWGCmd())
	cmd.AddCommand(newForgeACMECmd())
	cmd.AddCommand(newForgeDownCmd())
	cmd.AddCommand(newForgeBackupCmd())
	cmd.AddCommand(newForgeRestoreCmd())
	return cmd
}

// loadForgeConfig reads [forge] from the layered grove config. A missing block
// is an error here (unlike everywhere else, where absent means "no forge"):
// every verb under this noun is about a forge that is supposed to exist.
func loadForgeConfig() (*config.ForgeConfig, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil, fmt.Errorf("load grove config: %w", err)
	}
	forgeCfg, err := cfg.Forge()
	if err != nil {
		return nil, err
	}
	if forgeCfg == nil {
		return nil, fmt.Errorf("no [forge] block in the grove config — `grove forge` needs [forge.infra] (project, ssh_user, cidr) and [forge.services.forgejo] (version, sha256); see `grove forge --help`")
	}
	return forgeCfg, nil
}

// prepareForgeTerraform is the shared front half of plan/up/down: resolve the
// module dir, extract the embedded root, and render the tfvars file that is the
// whole terraform input surface. It creates no cloud resources and costs
// nothing, which is why every verb runs it BEFORE its confirmation prompt.
func prepareForgeTerraform(tfDir string, forgeCfg *config.ForgeConfig) (dir, tfvars string, err error) {
	dir, err = resolveForgeTerraformDir(tfDir)
	if err != nil {
		return "", "", err
	}
	tfvars, err = writeForgeTFVars(dir, forgeCfg)
	if err != nil {
		return "", "", err
	}
	return dir, tfvars, nil
}

// requireTerraform fails with a message naming the tool rather than letting
// exec surface a bare "executable file not found".
func requireTerraform() error {
	if _, err := exec.LookPath("terraform"); err != nil {
		return fmt.Errorf("terraform is not on PATH — `grove forge` drives terraform directly (install it, or pass --tf-dir and run terraform yourself)")
	}
	return nil
}

// ---- plan ------------------------------------------------------------------

func newForgePlanCmd() *cobra.Command {
	var (
		tfDir      string
		renderOnly bool
	)
	cmd := cli.NewStandardCommand("plan", "Render the forge terraform inputs and show the pending diff (read-only)")
	cmd.Long = `Extract the embedded terraform root, render terraform.tfvars from [forge.infra]
and [forge.services], and run 'terraform plan'. Nothing is created or changed.

--render-only stops before terraform: it writes the module and the tfvars and
prints them, which is the whole provision as a reviewable artifact on a machine
with no terraform and no credentials.

Exit status mirrors 'terraform plan -detailed-exitcode': 0 = no changes,
2 = changes pending, 1 = error.`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", forgeTFDirFlagHelp)
	cmd.Flags().BoolVar(&renderOnly, "render-only", false, "Render the module and tfvars and print them; do not invoke terraform")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		dir, tfvars, err := prepareForgeTerraform(tfDir, forgeCfg)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Terraform dir: %s\n\n", dir)
		files, err := forgeExtractedFiles(dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Module (%d files):\n", len(files))
		for _, f := range files {
			fmt.Fprintf(out, "  %s\n", f)
		}
		fmt.Fprintf(out, "\n%s:\n%s\n", forgeTFVarsName, indentLines(tfvars, "  "))

		if renderOnly {
			return nil
		}
		if err := requireTerraform(); err != nil {
			return err
		}
		if err := runInherited(dir, "terraform", "-chdir="+dir, "init", "-input=false"); err != nil {
			return fmt.Errorf("terraform init: %w", err)
		}
		// -detailed-exitcode makes "there are changes" a distinct status
		// rather than something a caller has to parse out of the diff.
		err = runInherited(dir, "terraform", "-chdir="+dir, "plan", "-input=false", "-detailed-exitcode")
		if err == nil {
			return nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == forgePlanChangesExitCode {
			fmt.Fprintln(os.Stderr, "\nChanges are pending (terraform plan -detailed-exitcode = 2).")
			os.Exit(forgePlanChangesExitCode)
		}
		return fmt.Errorf("terraform plan: %w", err)
	}
	return cmd
}

// ---- up --------------------------------------------------------------------

func newForgeUpCmd() *cobra.Command {
	var (
		tfDir          string
		assumeYes      bool
		prebuilt       bool
		prebuiltTarget string
		sourceDir      string
	)
	cmd := cli.NewStandardCommand("up", "Provision or converge the forge services VM (billable)")
	cmd.Long = `Provision the forge: terraform init + apply, then record the outputs.

BILLABLE — this creates cloud resources. Re-running it converges an existing
forge rather than creating a second one: there is exactly one forge, keyed by
[forge.infra] vm_name and one tfstate.

With --prebuilt, grove-syncd is cross-compiled on this laptop and installed over
the pinned SSH connection afterwards (the satellite --prebuilt precedent). The
VM never clones or builds anything: Forgejo arrives as a pinned, checksummed
release download, and grove-syncd's systemd unit waits for the binary.`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", forgeTFDirFlagHelp)
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the billable-resource confirmation prompt")
	cmd.Flags().BoolVar(&prebuilt, "prebuilt", true, "Cross-build grove-syncd locally and install it over the pinned SSH connection after apply")
	cmd.Flags().StringVar(&prebuiltTarget, "prebuilt-target", "linux/amd64", "Cross-compile target for --prebuilt as <goos>/<goarch> (the VM arch)")
	cmd.Flags().StringVar(&sourceDir, "source-dir", "", "Local ecosystem worktree root the --prebuilt grove-syncd is built from (default: the go.work root above cwd)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		// Everything that can fail for free fails here, before the prompt and
		// before a single billable resource exists.
		dir, _, err := prepareForgeTerraform(tfDir, forgeCfg)
		if err != nil {
			return err
		}
		if err := requireTerraform(); err != nil {
			return err
		}
		var syncdPlan *forgeSyncdShipPlan
		if prebuilt && forgeCfg.Services.SyncdEnabled() {
			syncdPlan, err = planForgeSyncdShip(sourceDir, prebuiltTarget)
			if err != nil {
				return err
			}
		}

		if !assumeYes {
			prompt := fmt.Sprintf("Provision forge %q in %s/%s — this creates BILLABLE GCP resources. Continue?",
				forgeCfg.Infra.EffectiveVMName(), strings.TrimSpace(forgeCfg.Infra.Project), forgeCfg.Infra.EffectiveZone())
			if err := confirmOrAbort(prompt); err != nil {
				return err
			}
		}

		if err := runInherited(dir, "terraform", "-chdir="+dir, "init", "-input=false"); err != nil {
			return fmt.Errorf("terraform init: %w", err)
		}
		// --yes must reach terraform too: grove's prompt was already skipped
		// above, and without -auto-approve a non-tty apply dies at terraform's
		// own approval question after printing the whole plan.
		applyArgs := []string{"-chdir=" + dir, "apply", "-input=false"}
		if assumeYes {
			applyArgs = append(applyArgs, "-auto-approve")
		}
		if err := runInherited(dir, "terraform", applyArgs...); err != nil {
			return fmt.Errorf("terraform apply: %w", err)
		}

		outputs, err := readForgeTerraformOutputs(dir)
		if err != nil {
			return fmt.Errorf("read terraform outputs: %w", err)
		}
		if err := cacheForgeOutputs(outputs); err != nil {
			// Status degrades to "run terraform output yourself"; the forge is
			// up either way, so this is a warning, not a failure.
			fmt.Fprintf(os.Stderr, "warning: could not cache terraform outputs: %v\n", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "\nForge %s is up.\n", outputs.VMName)
		// No overlay: these outputs came out of the apply that just finished,
		// so there is nothing fresher to prefer them to.
		renderForgeOutputs(out, outputs, forgeOutputOverlay{})

		if syncdPlan != nil {
			if err := shipForgeSyncd(out, syncdPlan, outputs, forgeCfg); err != nil {
				return err
			}
		}

		// Join the mesh before TLS reconciliation: an enabled mesh address is a
		// required SAN, so this ordering widens the certificate exactly once.
		if err := reconcileForgeWireGuard(out, outputs, forgeCfg); err != nil {
			return err
		}
		// An acme config obtains the real certificate and flips Forgejo to
		// https before the ROOT_URL reconcile reads the target; a no-op for
		// self-signed deployments.
		if err := reconcileForgeACME(out, outputs, forgeCfg); err != nil {
			return err
		}
		// Forgejo must advertise the route clients actually use before public
		// ingress can be removed; otherwise redirects and clone URLs strand them.
		if err := reconcileForgeRootURL(out, outputs, forgeCfg); err != nil {
			return err
		}

		// Before the backup payload, because both reach the VM over SSH and a
		// certificate that no longer covers the address is the more urgent of
		// the two problems: it silently breaks every pinning client.
		if err := reconcileForgeTLS(out, outputs, forgeCfg); err != nil {
			return err
		}

		// After syncd, because the backup script backs syncd up: installing
		// the timer first would give it one scheduled window in which the
		// binary it calls does not exist yet.
		if err := shipForgeBackup(out, outputs, forgeCfg); err != nil {
			return err
		}

		fmt.Fprintf(out, "\nNext:\n")
		step := 1
		if outputs.ForgejoTunnelCmd != "" {
			// A domain-less forge is IAP-tunnel-only: the URL below answers
			// only while this tunnel runs.
			fmt.Fprintf(out, "  %d. Start the IAP tunnel:    %s\n", step, outputs.ForgejoTunnelCmd)
			step++
		}
		fmt.Fprintf(out, "  %d. Point the daemon at it:  [forge] url = %q\n", step, outputs.ForgeURL)
		step++
		fmt.Fprintf(out, "  %d. Enable the poller:       [forge.poll] enabled = true\n", step)
		step++
		fmt.Fprintf(out, "  %d. Give the daemon a token: [forge] token_command = \"...\"  (never a literal token)\n", step)
		step++
		if outputs.TLSMode == config.ForgeTLSSelfSigned {
			fmt.Fprintf(out, "  %d. Pin the certificate:     `grove forge status` prints its SHA-256 fingerprint\n", step)
		}
		return nil
	}
	return cmd
}

// ---- down ------------------------------------------------------------------

func newForgeDownCmd() *cobra.Command {
	var (
		tfDir   string
		force   bool
		confirm string
	)
	cmd := cli.NewStandardCommand("down", "Destroy the forge VM and its durable state (double-gated)")
	cmd.Long = `Destroy the forge: terraform destroy, including the boot disk.

THIS IS NOT 'satellite down'. A satellite is cattle — destroying one loses
nothing the registry cannot rebuild. The forge's disk holds every plan branch
ever pushed to it, its pull requests, its reviews and its SQLite database. There
is no registry to rebuild that from; only a backup.

Two gates, both required:

  --force              acknowledges that durable state is being destroyed
  --confirm <vm_name>  types the instance name back, as an interactive prompt
                       would, and works in scripts

Run 'grove forge backup' first.`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", forgeTFDirFlagHelp)
	cmd.Flags().BoolVar(&force, "force", false, "Acknowledge that the forge's durable state (every pushed ref, every PR) is being destroyed")
	cmd.Flags().StringVar(&confirm, "confirm", "", "The instance name, typed back, confirming which forge is being destroyed")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		name := forgeCfg.Infra.EffectiveVMName()

		if err := checkForgeDownGates(name, force, confirm); err != nil {
			return err
		}

		dir, _, err := prepareForgeTerraform(tfDir, forgeCfg)
		if err != nil {
			return err
		}
		if err := requireTerraform(); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(dir, forgeTFVarsName)); err != nil {
			return fmt.Errorf("%s not found in %s — terraform destroy needs the variables that have no defaults: %w", forgeTFVarsName, dir, err)
		}
		if err := runInherited(dir, "terraform", "-chdir="+dir, "init", "-input=false"); err != nil {
			return fmt.Errorf("terraform init: %w", err)
		}
		// -auto-approve, unconditionally, and that is not a loosening: this
		// verb has ALREADY demanded --force AND the instance name typed back.
		// Terraform's own "only 'yes' will be accepted" question is a fourth
		// gate on top of three, and on a non-tty it is not a gate at all — it
		// fails with "error asking for approval: EOF" after printing the whole
		// destroy plan, leaving the operator with a half-run verb and no way to
		// script the teardown. `up --yes` was fixed for exactly this (job 17
		// finding D4); `down` kept the defect because nothing had tried to run
		// it non-interactively until the restore drill did.
		if err := runInherited(dir, "terraform", "-chdir="+dir, "destroy", "-input=false", "-auto-approve"); err != nil {
			return fmt.Errorf("terraform destroy: %w", err)
		}
		// The cached outputs describe a machine that no longer exists.
		if stateDir, serr := forgeStateDir(); serr == nil {
			_ = os.Remove(filepath.Join(stateDir, forgeOutputsName))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nForge %q destroyed. The tfstate remains in %s.\n", name, dir)
		return nil
	}
	return cmd
}

// checkForgeDownGates is the pet's guard rail, factored out so it is testable
// without terraform, credentials, or a VM.
//
// Both gates are required and they check DIFFERENT things: --force is consent
// to lose durable state, --confirm is proof the operator knows WHICH forge they
// are consenting about. Either alone is the accident this exists to prevent.
func checkForgeDownGates(vmName string, force bool, confirm string) error {
	if !force {
		return fmt.Errorf("refusing to destroy forge %q without --force: this deletes the boot disk holding every ref ever pushed, its pull requests and its database (run `grove forge backup` first, then re-run with --force --confirm %s)", vmName, vmName)
	}
	typed := strings.TrimSpace(confirm)
	if typed == "" {
		return fmt.Errorf("refusing to destroy forge %q: pass --confirm %s to type the instance name back", vmName, vmName)
	}
	if typed != vmName {
		return fmt.Errorf("--confirm %q does not match the configured forge %q — nothing was destroyed", typed, vmName)
	}
	return nil
}

// backup / restore live in forge_backup.go — see newForgeBackupCmd.

// ---- terraform outputs -----------------------------------------------------

// forgeOutputs is the subset of the module's outputs the CLI renders. The
// module publishes more (see forgeassets/CONTRACT.md); this is what `status`
// promises.
type forgeOutputs struct {
	ExternalIP          string   `json:"external_ip"`
	SSHCommand          string   `json:"ssh_command"`
	VMName              string   `json:"vm_name"`
	Zone                string   `json:"zone"`
	ForgeURL            string   `json:"forge_url"`
	ForgejoTunnelCmd    string   `json:"forgejo_tunnel_command"`
	SyncdAddr           string   `json:"syncd_addr"`
	TLSMode             string   `json:"tls_mode"`
	ServiceAccountEmail string   `json:"service_account_email"`
	FirewallRules       []string `json:"firewall_rules"`
}

// readForgeTerraformOutputs runs `terraform output -json` and decodes the
// {"name": {"value": ...}} envelope terraform wraps every output in.
func readForgeTerraformOutputs(tfDir string) (forgeOutputs, error) {
	cmd := exec.Command("terraform", "-chdir="+tfDir, "output", "-json") //nolint:gosec // G204: internal args
	raw, err := cmd.Output()
	if err != nil {
		return forgeOutputs{}, err
	}
	return decodeForgeTerraformOutputs(raw)
}

func decodeForgeTerraformOutputs(raw []byte) (forgeOutputs, error) {
	var envelope map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return forgeOutputs{}, fmt.Errorf("parse terraform output -json: %w", err)
	}
	flat := make(map[string]json.RawMessage, len(envelope))
	for k, v := range envelope {
		flat[k] = v.Value
	}
	merged, err := json.Marshal(flat)
	if err != nil {
		return forgeOutputs{}, err
	}
	var out forgeOutputs
	if err := json.Unmarshal(merged, &out); err != nil {
		return forgeOutputs{}, fmt.Errorf("decode terraform outputs: %w", err)
	}
	return out, nil
}

// cacheForgeOutputs persists the outputs so `status` answers without terraform.
func cacheForgeOutputs(out forgeOutputs) error {
	dir, err := forgeStateDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, forgeOutputsName), append(data, '\n'), 0o600)
}

// loadCachedForgeOutputs reads the cached outputs, reporting absence as
// (zero, false, nil) — "no forge has been provisioned from this machine" is a
// state, not an error.
func loadCachedForgeOutputs() (forgeOutputs, bool, error) {
	dir, err := forgeStateDir()
	if err != nil {
		return forgeOutputs{}, false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, forgeOutputsName))
	if os.IsNotExist(err) {
		return forgeOutputs{}, false, nil
	}
	if err != nil {
		return forgeOutputs{}, false, err
	}
	var out forgeOutputs
	if err := json.Unmarshal(data, &out); err != nil {
		return forgeOutputs{}, false, err
	}
	return out, true, nil
}

// ---- small helpers ---------------------------------------------------------

// indentLines prefixes every line of s, so a rendered artifact reads as a block
// rather than as output.
func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
