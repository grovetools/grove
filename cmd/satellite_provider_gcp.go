package cmd

// The gcp satellite provider — the terraform-driven machine half `grove
// satellite up`/`down` always had, extracted behind the satelliteProvider
// seam. Everything in this file must stay byte-identical to the pre-seam
// verbs: same step order, same prompts, same terraform invocations and
// messages.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gcpSatelliteProvider provisions satellite VMs with terraform against the
// embedded gcp target module (or a --tf-dir BYO module honoring the same
// contract, grove/cmd/satelliteassets/CONTRACT.md).
type gcpSatelliteProvider struct {
	// target is the embedded infra target whose terraform module
	// PrepareUp/Down extract ("gcp" — validated against the embedded targets
	// via resolveSatelliteTarget, a gcp-private detail now).
	target string
	// tfAbs is the resolved terraform dir, set by PrepareUp for Up.
	tfAbs string
}

// newGCPSatelliteProvider is the registry constructor for the "gcp" target.
func newGCPSatelliteProvider(target string) satelliteProvider {
	return &gcpSatelliteProvider{target: target}
}

func (p *gcpSatelliteProvider) Kind() string { return p.target }

// DefaultSatelliteKind: gcp satellites are full-stack by default (groved
// dial + sync wiring) — exactly the pre-kind behavior.
func (p *gcpSatelliteProvider) DefaultSatelliteKind() string { return satelliteKindFull }

// UsesBootstrapScript: yes — the shared verb runs the embedded bootstrap
// script against the fresh VM, exactly the pre-seam behavior.
func (p *gcpSatelliteProvider) UsesBootstrapScript() bool { return true }

// DefaultPrebuiltTarget mirrors the `up --prebuilt-target` flag default (the
// gcp VMs are amd64). Not consulted today — the shared verb only asks
// non-bootstrap-script providers, and for gcp the flag default applies —
// but it keeps the provider axis uniform.
func (p *gcpSatelliteProvider) DefaultPrebuiltTarget() (string, error) {
	return "linux/amd64", nil
}

// PrepareUp resolves the terraform dir — the per-satellite state dir with
// the embedded target module extracted into it (module files overwritten
// every run, tfstate/tfvars/.terraform never touched; legacy worktree state
// migrated in first) — or, with --tf-dir, a BYO module dir used as-is — and
// sanity-checks it. Validation only, nothing is created: this runs BEFORE
// the confirm prompt (and before the shared verb's bootstrap extraction),
// preserving the fail-fast order.
func (p *gcpSatelliteProvider) PrepareUp(opts *satelliteUpOptions) error {
	// Required gcp inputs (project/ssh_user have no defaults). Checked on the
	// MERGED values, so a populated [satellites.<name>.infra] block makes a
	// flag-less `up` valid — same message the verb used to print pre-seam.
	if opts.Infra.Project == "" || opts.Infra.SSHUser == "" {
		return fmt.Errorf("--project and --ssh-user are required (or persist them in [satellites.%s.infra] — a successful `up` writes the block for you)", opts.Name)
	}
	target, err := resolveSatelliteTarget(p.target)
	if err != nil {
		return err
	}
	tfAbs, err := resolveSatelliteTerraformDir(opts.Name, target, opts.TFDir, os.Stdout)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(tfAbs, "variables.tf")); err != nil {
		return fmt.Errorf("terraform dir %q does not look like a grove-satellite module (no variables.tf; the contract is documented in grove/cmd/satelliteassets/CONTRACT.md): %w", tfAbs, err)
	}
	p.tfAbs = tfAbs
	return nil
}

// Up is the billable half of `grove satellite up`: confirm, PostConfirm,
// CIDR auto-detect, tfvars persist, terraform init + apply, external_ip
// output → endpoint.
func (p *gcpSatelliteProvider) Up(_ context.Context, opts *satelliteUpOptions) (satelliteEndpoint, error) {
	if p.tfAbs == "" {
		return satelliteEndpoint{}, fmt.Errorf("gcp satellite provider: Up called without PrepareUp")
	}

	if !opts.AssumeYes {
		if !confirmYesNo(fmt.Sprintf("Provision satellite %q — this creates BILLABLE GCP resources. Continue?", opts.Name)) {
			return satelliteEndpoint{}, fmt.Errorf("aborted")
		}
	}
	// Shared fail-fast work that must run after the confirm but before any
	// billable resource exists (the verb resolves provision tokens here).
	if opts.PostConfirm != nil {
		if err := opts.PostConfirm(); err != nil {
			return satelliteEndpoint{}, err
		}
	}

	if opts.Infra.CIDR == "" {
		cidr := detectPublicCIDR()
		if cidr == "" {
			return satelliteEndpoint{}, fmt.Errorf("could not auto-detect your public IP; pass --cidr (e.g. 203.0.113.7/32)")
		}
		fmt.Printf("Using detected SSH CIDR: %s\n", cidr)
		// RESOLVED value, written back for the shared verb's infra
		// write-back/drift print.
		opts.Infra.CIDR = cidr
	}

	// Persist the required (default-less) terraform variables as
	// terraform.tfvars in the tf dir BEFORE apply, so `grove satellite
	// down` can run terraform destroy non-interactively — variables.tf
	// deliberately has no defaults for project_id/ssh_user/
	// allowed_ssh_cidr. terraform auto-loads the file from the -chdir
	// dir, and the PoC's .gitignore already excludes *.tfvars.
	if err := writeSatelliteTFVars(p.tfAbs, opts.Infra.Project, opts.Infra.SSHUser, opts.Infra.CIDR, opts.Name, opts.Infra.Zone, opts.ServiceAccountEmail); err != nil {
		return satelliteEndpoint{}, fmt.Errorf("write %s: %w", satelliteTFVarsName, err)
	}

	// 1. terraform init + apply (subprocess, inherited stdio — terraform
	//    runs its own confirmation).
	if err := runInherited(p.tfAbs, "terraform", "-chdir="+p.tfAbs, "init", "-input=false"); err != nil {
		return satelliteEndpoint{}, fmt.Errorf("terraform init: %w", err)
	}
	applyArgs := []string{
		"-chdir=" + p.tfAbs, "apply",
		"-var", "project_id=" + opts.Infra.Project,
		"-var", "ssh_user=" + opts.Infra.SSHUser,
		"-var", "allowed_ssh_cidr=" + opts.Infra.CIDR,
		"-var", "vm_name=" + opts.Name,
	}
	if opts.Infra.Zone != "" {
		applyArgs = append(applyArgs, "-var", "zone="+opts.Infra.Zone)
	}
	if opts.ServiceAccountEmail != "" {
		applyArgs = append(applyArgs, "-var", "service_account_email="+opts.ServiceAccountEmail)
	}
	if err := runInherited(p.tfAbs, "terraform", applyArgs...); err != nil {
		return satelliteEndpoint{}, fmt.Errorf("terraform apply: %w", err)
	}

	// 2. terraform output -raw external_ip → IP.
	ip, err := terraformOutput(p.tfAbs, "external_ip")
	if err != nil {
		return satelliteEndpoint{}, fmt.Errorf("read terraform output external_ip: %w", err)
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return satelliteEndpoint{}, fmt.Errorf("terraform output external_ip was empty")
	}

	return satelliteEndpoint{
		SSHAddr:      ip + ":22",
		User:         opts.Infra.SSHUser,
		IdentityFile: opts.Infra.IdentityFile,
		LocalRunDir:  p.tfAbs,
	}, nil
}

// Down destroys the VM: terraform dir resolution (same rules as up),
// confirm, PostConfirm, tfvars presence check, terraform init + destroy.
func (p *gcpSatelliteProvider) Down(_ context.Context, opts *satelliteDownOptions) error {
	// Resolve the terraform dir the same way `up` does: the per-name state
	// dir (with legacy worktree tfstate migrated in and the embedded module
	// re-extracted so destroy always has current .tf files), or --tf-dir
	// as-is.
	target, err := resolveSatelliteTarget(p.target)
	if err != nil {
		return err
	}
	tfAbs, err := resolveSatelliteTerraformDir(opts.Name, target, opts.TFDir, os.Stdout)
	if err != nil {
		return err
	}

	if !opts.AssumeYes {
		if !confirmYesNo(fmt.Sprintf("Destroy satellite %q and remove its registry entry?", opts.Name)) {
			return fmt.Errorf("aborted")
		}
	}
	// Shared pre-destroy work (the verb's best-effort cursor deregister,
	// C19/C20) — after the confirm, before anything is destroyed.
	if opts.PostConfirm != nil {
		if err := opts.PostConfirm(); err != nil {
			return err
		}
	}

	// terraform destroy (subprocess, inherited stdio — terraform runs
	// its own confirmation). Destroy needs the same required vars as
	// apply (variables.tf has no defaults for project_id/ssh_user/
	// allowed_ssh_cidr); `up` persists them as terraform.tfvars, and
	// -input=false makes a missing variable a hard error instead of an
	// interactive prompt so scripted teardown fails fast.
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
	destroyArgs := []string{"-chdir=" + tfAbs, "destroy", "-input=false", "-var", "vm_name=" + opts.Name}
	if err := runInherited(tfAbs, "terraform", destroyArgs...); err != nil {
		return fmt.Errorf("terraform destroy: %w", err)
	}
	return nil
}

// --- gcp/terraform-specific helpers (moved from satellite.go with the seam) ---

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

func terraformOutput(tfDir, name string) (string, error) {
	cmd := exec.Command("terraform", "-chdir="+tfDir, "output", "-raw", name) //nolint:gosec // G204: internal args
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
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
