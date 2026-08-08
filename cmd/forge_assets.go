package cmd

// Embedded-asset plumbing for `grove forge`: terraform root extraction into the
// forge state dir, and the terraform.tfvars rendering that IS the plan's input
// surface.
//
// Layout — deliberately singular, because there is one forge:
//
//	~/.local/state/grove/forge/
//	  terraform/   — the embedded module, re-extracted (overwritten) on every
//	                 up/plan/down so it versions with the binary, PLUS this
//	                 forge's terraform.tfstate*/terraform.tfvars/.terraform*,
//	                 which extraction never touches.
//	  outputs.json — the last `terraform output -json`, so `grove forge status`
//	                 can answer without running terraform.
//
// The extraction discipline (module files overwritten, local state artifacts
// never touched, temp+rename so a crash cannot leave half a .tf file) is the
// satellite's, reused rather than reinvented: writeFileAtomic and
// isSatelliteLocalTFArtifact come from satellite_assets.go, and the guard is
// the same rule because the hazard is the same one.

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"

	"github.com/grovetools/grove/cmd/forgeassets"
)

// forgeTFVarsName is the variables file every forge verb writes before running
// terraform.
//
// Unlike `grove satellite`, which passes required variables as -var flags AND
// persists them, the forge passes NOTHING on the command line: the tfvars file
// is the single input surface, auto-loaded by terraform from the -chdir dir.
// One file means `plan`, `apply` and `destroy` cannot disagree about what they
// were describing, and it means the whole provision is one greppable,
// diffable, golden-testable artifact instead of an argv reconstructed three
// times.
const forgeTFVarsName = "terraform.tfvars"

// forgeOutputsName caches `terraform output -json` next to the state, so
// `grove forge status` renders without shelling out to terraform (and works on
// a machine that has none).
const forgeOutputsName = "outputs.json"

// forgeStateDir is the forge's state root (<StateDir>/forge).
func forgeStateDir() (string, error) {
	dir := paths.StateDir()
	if dir == "" {
		return "", fmt.Errorf("could not resolve grove state directory")
	}
	return filepath.Join(dir, "forge"), nil
}

// forgeTerraformStateDir is the module+state dir — the default `--tf-dir`.
func forgeTerraformStateDir() (string, error) {
	dir, err := forgeStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "terraform"), nil
}

// resolveForgeTerraformDir resolves the terraform dir a forge verb operates in.
// With --tf-dir the directory is used as-is (bring-your-own module honoring
// forgeassets/CONTRACT.md); otherwise the embedded root is re-extracted into
// the forge state dir.
func resolveForgeTerraformDir(tfDirFlag string) (string, error) {
	if tfDirFlag != "" {
		abs, err := filepath.Abs(tfDirFlag)
		if err != nil {
			return "", fmt.Errorf("resolve --tf-dir: %w", err)
		}
		if _, err := os.Stat(filepath.Join(abs, "variables.tf")); err != nil {
			return "", fmt.Errorf("terraform dir %q does not look like a grove-forge module (no variables.tf; the contract is documented in grove/cmd/forgeassets/CONTRACT.md): %w", abs, err)
		}
		return abs, nil
	}
	dir, err := forgeTerraformStateDir()
	if err != nil {
		return "", err
	}
	if err := extractForgeTerraform(dir); err != nil {
		return "", fmt.Errorf("extract embedded forge terraform root to %s: %w", dir, err)
	}
	return dir, nil
}

// extractForgeTerraform writes the embedded module tree into destDir, creating
// it as needed. Module files are overwritten on every run (they version with
// the binary); this forge's terraform.tfstate*, terraform.tfvars and
// .terraform* are never touched.
func extractForgeTerraform(destDir string) error {
	tfFS, err := forgeassets.TerraformFS()
	if err != nil {
		return err
	}
	return fs.WalkDir(tfFS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		dest := filepath.Join(destDir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		// Same predicate as the satellite extractor, deliberately reused: the
		// artifact names and the hazard (clobbering a live tfstate) are
		// identical, and two copies of this rule is one copy too many.
		if isSatelliteLocalTFArtifact(path.Base(p)) {
			return nil
		}
		data, err := fs.ReadFile(tfFS, p)
		if err != nil {
			return err
		}
		return writeFileAtomic(dest, data, 0o644)
	})
}

// ---- tfvars rendering ------------------------------------------------------

// forgeTFVars renders the whole terraform input surface from [forge].
//
// This function is the plan: everything terraform will do is decided by the
// text it returns, which is why it is a pure function of the config and why a
// golden test pins it. Values are %q-quoted (valid HCL for these
// config-derived strings), numbers and booleans are literal, and every key the
// module declares without a default is written explicitly so a missing one
// fails at render time rather than as an interactive terraform prompt.
func forgeTFVars(cfg *config.ForgeConfig) (string, error) {
	if err := cfg.ValidateForProvision(); err != nil {
		return "", err
	}
	infra := cfg.Infra
	services := cfg.Services

	var b strings.Builder
	b.WriteString("# Generated by `grove forge` from [forge.infra] and [forge.services].\n")
	b.WriteString("# Edit the config, not this file: every forge verb rewrites it before\n")
	b.WriteString("# running terraform, and `grove forge down` relies on it to destroy\n")
	b.WriteString("# without prompting.\n\n")

	b.WriteString("# --- machine ---\n")
	fmt.Fprintf(&b, "project_id    = %q\n", strings.TrimSpace(infra.Project))
	fmt.Fprintf(&b, "zone          = %q\n", infra.EffectiveZone())
	fmt.Fprintf(&b, "vm_name       = %q\n", infra.EffectiveVMName())
	fmt.Fprintf(&b, "machine_type  = %q\n", infra.EffectiveMachineType())
	fmt.Fprintf(&b, "disk_size_gb  = %d\n", infra.EffectiveDiskSizeGB())
	fmt.Fprintf(&b, "image_family  = %q\n", infra.EffectiveImageFamily())
	fmt.Fprintf(&b, "image_project = %q\n", infra.EffectiveImageProject())

	b.WriteString("\n# --- access (an operator CIDR; the open internet is refused) ---\n")
	fmt.Fprintf(&b, "ssh_user       = %q\n", strings.TrimSpace(infra.SSHUser))
	fmt.Fprintf(&b, "allowed_cidr   = %q\n", strings.TrimSpace(infra.CIDR))
	fmt.Fprintf(&b, "enable_iap_ssh          = %t\n", infra.IAPSSHEnabled())
	fmt.Fprintf(&b, "ssh_ingress            = %q\n", infra.EffectiveSSHIngress())
	fmt.Fprintf(&b, "syncd_ingress_enabled  = %t\n", infra.SyncdIngressIsEnabled())
	fmt.Fprintf(&b, "forgejo_ingress_enabled = %t\n", infra.ForgejoIngressIsEnabled())
	if pub := strings.TrimSpace(infra.SSHPubkeyFile); pub != "" {
		fmt.Fprintf(&b, "ssh_pubkey_file = %q\n", pub)
	}

	b.WriteString("\n# --- identity ---\n")
	b.WriteString("# Empty means the module creates a dedicated service account with no IAM\n")
	b.WriteString("# roles and attaches it with no OAuth scopes.\n")
	fmt.Fprintf(&b, "service_account_email = %q\n", strings.TrimSpace(infra.ServiceAccountEmail))

	b.WriteString("\n# --- services ---\n")
	fmt.Fprintf(&b, "domain   = %q\n", services.EffectiveDomain())
	fmt.Fprintf(&b, "tls_mode = %q\n", services.EffectiveTLSMode())
	if services.EffectiveTLSMode() == config.ForgeTLSACME {
		fmt.Fprintf(&b, "acme_email        = %q\n", strings.TrimSpace(services.ACMEEmail))
		fmt.Fprintf(&b, "acme_dns_provider = %q\n", strings.TrimSpace(services.ACMEDNSProvider))
		if resolvers := services.EffectiveACMEDNSResolvers(); len(resolvers) > 0 {
			quoted := make([]string, 0, len(resolvers))
			for _, r := range resolvers {
				quoted = append(quoted, fmt.Sprintf("%q", r))
			}
			fmt.Fprintf(&b, "acme_dns_resolvers = [%s]\n", strings.Join(quoted, ", "))
		}
	}
	fmt.Fprintf(&b, "forgejo_version   = %q\n", strings.TrimSpace(services.Forgejo.Version))
	fmt.Fprintf(&b, "forgejo_sha256    = %q\n", strings.ToLower(strings.TrimSpace(services.Forgejo.SHA256)))
	fmt.Fprintf(&b, "forgejo_http_port = %d\n", services.Forgejo.EffectiveHTTPPort())
	fmt.Fprintf(&b, "forgejo_site_name = %q\n", services.Forgejo.EffectiveSiteName())
	fmt.Fprintf(&b, "syncd_enabled     = %t\n", services.SyncdEnabled())
	fmt.Fprintf(&b, "syncd_port        = %d\n", services.EffectiveSyncdPort())

	b.WriteString(forgeBackupTFVars(cfg))

	return b.String(), nil
}

// writeForgeTFVars renders and writes the tfvars file into the terraform dir.
func writeForgeTFVars(tfDir string, cfg *config.ForgeConfig) (string, error) {
	content, err := forgeTFVars(cfg)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(tfDir, forgeTFVarsName), []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", forgeTFVarsName, err)
	}
	return content, nil
}

// forgeExtractedFiles lists what extraction wrote, relative and sorted. It
// backs the `plan --render-only` listing and the extraction test.
func forgeExtractedFiles(tfDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(tfDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		base := filepath.Base(p)
		if d.IsDir() {
			if strings.HasPrefix(base, ".") && p != tfDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isSatelliteLocalTFArtifact(base) || strings.HasPrefix(base, ".") {
			return nil
		}
		rel, err := filepath.Rel(tfDir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
