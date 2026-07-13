package cmd

// Embedded-asset plumbing for `grove satellite`: target resolution, terraform
// module extraction into the per-satellite state dir, bootstrap script
// extraction, and the one-time migration of legacy tfstate out of the cloud
// worktree (the assets' pre-embed home, cloud/poc/grove-satellite).
//
// Layout: each satellite gets ~/.local/state/grove/satellites/<name>/ with
//   terraform/  — the embedded target module, re-extracted (overwritten) on
//                 every up/down so it versions with the binary, PLUS the
//                 satellite's own terraform.tfstate*/terraform.tfvars/
//                 .terraform*, which extraction never touches. Per-name dirs
//                 fix the shared-dir collision where a second `up` in one
//                 terraform dir would destroy the first VM (single local
//                 tfstate, single google_compute_instance.satellite address).
//   bootstrap/  — the embedded bootstrap script (bash needs a real file path
//                 because stdin is reserved for secrets).

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/pkg/paths"
	"github.com/pelletier/go-toml/v2"

	"github.com/grovetools/grove/cmd/satelliteassets"
)

// defaultSatelliteTarget is the embedded infra target used when neither
// --target nor [satellites.<name>.infra] target is set.
const defaultSatelliteTarget = "gcp"

// legacySatelliteTerraformRel is the shared PoC terraform dir `up`/`down`
// used before the assets moved into the binary — resolved relative to cwd
// (walking up to a go.work ecosystem root), exactly how the old
// --tf-dir default resolved from a worktree.
const legacySatelliteTerraformRel = "cloud/poc/grove-satellite/terraform"

// resolveSatelliteTarget defaults and validates an infra target name against
// the embedded targets.
func resolveSatelliteTarget(target string) (string, error) {
	if target == "" {
		target = defaultSatelliteTarget
	}
	if !satelliteassets.HasTarget(target) {
		return "", fmt.Errorf("unknown satellite target %q (embedded targets: %s); pass --tf-dir to bring your own terraform module (see grove/cmd/satelliteassets/CONTRACT.md)",
			target, strings.Join(satelliteassets.Targets(), ", "))
	}
	return target, nil
}

// satelliteStateDir is the per-satellite state root
// (<StateDir>/satellites/<name>).
func satelliteStateDir(name string) (string, error) {
	dir := paths.StateDir()
	if dir == "" {
		return "", fmt.Errorf("could not resolve grove state directory")
	}
	return filepath.Join(dir, "satellites", name), nil
}

// satelliteTerraformStateDir is the per-satellite terraform module+state dir —
// the default `--tf-dir` for both `up` and `down`.
func satelliteTerraformStateDir(name string) (string, error) {
	dir, err := satelliteStateDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "terraform"), nil
}

// resolveSatelliteTerraformDir resolves the terraform dir a satellite verb
// operates in. With tfDirFlag set (--tf-dir, the bring-your-own-module escape
// hatch) the dir is used as-is: no extraction, no migration — the module just
// has to honor the target contract (satelliteassets/CONTRACT.md). Otherwise
// the per-satellite state dir is used: legacy worktree tfstate is migrated in
// first (one-time, copy-based), then the embedded target module is
// (re-)extracted over the module files.
func resolveSatelliteTerraformDir(name, target, tfDirFlag string, out io.Writer) (string, error) {
	if tfDirFlag != "" {
		abs, err := filepath.Abs(tfDirFlag)
		if err != nil {
			return "", fmt.Errorf("resolve --tf-dir: %w", err)
		}
		return abs, nil
	}
	dir, err := satelliteTerraformStateDir(name)
	if err != nil {
		return "", err
	}
	if err := migrateLegacySatelliteTFState(name, dir, out); err != nil {
		return "", err
	}
	if err := extractSatelliteTerraform(target, dir); err != nil {
		return "", fmt.Errorf("extract embedded %q terraform module to %s: %w", target, dir, err)
	}
	return dir, nil
}

// isSatelliteLocalTFArtifact reports whether a base name is per-satellite
// terraform state extraction must never write or overwrite:
// terraform.tfstate*, terraform.tfvars, .terraform* (.terraform/,
// .terraform.lock.hcl). The embedded tree contains none of these; this is the
// defensive second layer.
func isSatelliteLocalTFArtifact(base string) bool {
	return base == satelliteTFVarsName ||
		strings.HasPrefix(base, "terraform.tfstate") ||
		strings.HasPrefix(base, ".terraform")
}

// extractSatelliteTerraform writes the embedded target module's files into
// destDir, creating it as needed. Module files are overwritten on every run
// (they version with the binary) via temp+rename so a crash never leaves a
// half-written .tf file; anything not in the embedded module — notably the
// satellite's terraform.tfstate*, terraform.tfvars, and .terraform* — is
// never touched.
func extractSatelliteTerraform(target, destDir string) error {
	tfFS, err := satelliteassets.TerraformFS(target)
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
		if isSatelliteLocalTFArtifact(path.Base(p)) {
			return nil // defensive: never ship state/var artifacts
		}
		data, err := fs.ReadFile(tfFS, p)
		if err != nil {
			return err
		}
		return writeFileAtomic(dest, data, 0o644)
	})
}

// extractSatelliteBootstrap writes the embedded bootstrap script to the
// per-satellite state dir and returns its path. Always the embedded copy —
// even with --tf-dir — so the script can never skew from the CLI driving it
// (it is target-agnostic: SSH against any Debian-ish systemd host).
func extractSatelliteBootstrap(name string) (string, error) {
	data, err := satelliteassets.BootstrapScript()
	if err != nil {
		return "", err
	}
	dir, err := satelliteStateDir(name)
	if err != nil {
		return "", err
	}
	scriptDir := filepath.Join(dir, "bootstrap")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return "", err
	}
	scriptPath := filepath.Join(scriptDir, "satellite-bootstrap.sh")
	if err := writeFileAtomic(scriptPath, data, 0o755); err != nil {
		return "", err
	}
	return scriptPath, nil
}

// writeFileAtomic writes data via a temp file + rename in the target dir, so
// concurrent readers (and crashes) never observe a partial file.
func writeFileAtomic(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// migrateLegacySatelliteTFState performs the one-time copy of a satellite's
// terraform state out of the pre-embed shared worktree dir
// (cloud/poc/grove-satellite/terraform) into its per-name state dir. It only
// fires when the per-name dir has no terraform.tfstate yet AND a legacy dir
// (found by walking up from cwd to a go.work root) holds a tfstate whose
// terraform.tfvars names THIS satellite (vm_name) — the legacy dir was
// shared, so state there belongs to whichever satellite ran last and must
// never be adopted by a different name. Copies terraform.tfstate*,
// terraform.tfvars; the originals stay behind as a backup.
func migrateLegacySatelliteTFState(name, destDir string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if _, err := os.Stat(filepath.Join(destDir, "terraform.tfstate")); err == nil {
		return nil // already migrated (or freshly provisioned here)
	}
	legacyDir := findLegacySatelliteTerraformDir(name)
	if legacyDir == "" {
		return nil
	}

	matches, err := filepath.Glob(filepath.Join(legacyDir, "terraform.tfstate*"))
	if err != nil {
		return err
	}
	toCopy := append(matches, filepath.Join(legacyDir, satelliteTFVarsName))

	fmt.Fprintf(out, "\nMigrating satellite %q terraform state out of the legacy shared dir:\n  %s\n  -> %s\n", name, legacyDir, destDir)
	for _, src := range toCopy {
		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("migrate %s: %w", src, err)
		}
		if err := writeFileAtomic(filepath.Join(destDir, filepath.Base(src)), data, 0o600); err != nil {
			return fmt.Errorf("migrate %s: %w", src, err)
		}
		fmt.Fprintf(out, "  copied %s\n", filepath.Base(src))
	}
	fmt.Fprintf(out, "The originals remain in %s as a BACKUP only — do not run terraform\nthere manually anymore; grove satellite now operates in %s.\n\n", legacyDir, destDir)
	return nil
}

// findLegacySatelliteTerraformDir walks up from cwd looking for
// cloud/poc/grove-satellite/terraform holding a tfstate that belongs to this
// satellite (see legacyTFStateMatchesSatellite). The walk stops at the first
// directory containing a go.work file (the ecosystem root the old cwd-relative
// default was used from) or at the filesystem root. Empty when nothing
// matches.
func findLegacySatelliteTerraformDir(name string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		cand := filepath.Join(dir, filepath.FromSlash(legacySatelliteTerraformRel))
		if legacyTFStateMatchesSatellite(cand, name) {
			return cand
		}
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return "" // ecosystem root reached — stop
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// legacyTFStateMatchesSatellite reports whether dir carries a
// terraform.tfstate attributable to this satellite: its terraform.tfvars
// (written by the old `up`, key = "value" lines that parse as TOML) must name
// the satellite via vm_name. No tfvars, an unparsable tfvars, or a different
// vm_name → not a match: the shared legacy dir's state then belongs to some
// other satellite and adopting it would let `down <name>` destroy that VM.
func legacyTFStateMatchesSatellite(dir, name string) bool {
	if _, err := os.Stat(filepath.Join(dir, "terraform.tfstate")); err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, satelliteTFVarsName))
	if err != nil {
		return false
	}
	var vars map[string]interface{}
	if err := toml.Unmarshal(data, &vars); err != nil {
		return false
	}
	vmName, _ := vars["vm_name"].(string)
	return vmName == name
}
