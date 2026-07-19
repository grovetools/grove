// Package satelliteassets embeds the infrastructure assets `grove satellite`
// ships inside the grove binary: per-target terraform modules
// (targets/<target>/terraform), the docker satellite image build context
// (targets/docker), the target-agnostic bootstrap script
// (bootstrap/satellite-bootstrap.sh), and reference templates (templates/).
//
// Embedding decouples satellites from the intentionally-unpublished cloud
// repo (the assets' previous home, cloud/poc/grove-satellite) and makes
// bootstrap-script-vs-CLI version skew impossible: the module and script a
// given grove binary extracts are exactly the ones it was built with. The CLI
// extracts the terraform module to a per-satellite state dir
// (~/.local/state/grove/satellites/<name>/terraform) on every up/down, which
// also gives each satellite its own tfstate — fixing the shared-dir collision
// where a second `up` would destroy the first VM.
//
// The extraction deliberately never contains local terraform artifacts
// (terraform.tfstate*, terraform.tfvars, .terraform*): go:embed skips
// dotfiles by default, and the embedded tree is committed without state/var
// files. CONTRACT.md (this directory) documents the variables/outputs a
// module must honor, which is also the contract a bring-your-own --tf-dir
// module has to meet.
package satelliteassets

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed targets bootstrap templates
var assets embed.FS

// bootstrapPath is the embedded, target-agnostic bootstrap script: it runs
// from the laptop over SSH against any Debian-ish systemd host (see
// CONTRACT.md's image assumptions).
const bootstrapPath = "bootstrap/satellite-bootstrap.sh"

// BootstrapScript returns the embedded satellite bootstrap script.
func BootstrapScript() ([]byte, error) {
	data, err := assets.ReadFile(bootstrapPath)
	if err != nil {
		// Unreachable in a correctly built binary — the file is embedded at
		// compile time — but surfaced rather than panicked for safety.
		return nil, fmt.Errorf("embedded bootstrap script missing (%s): %w", bootstrapPath, err)
	}
	return data, nil
}

// Targets enumerates the embedded TERRAFORM infra targets — the children of
// the targets/ tree that carry a terraform/ module — sorted. Today that is
// just "gcp"; future aws/azure modules land as sibling directories. Other
// provider assets also live under targets/ (targets/docker is the docker
// image build context) but are deliberately excluded: this listing backs
// terraform-target validation (resolveSatelliteTarget) and TerraformFS,
// while non-terraform targets resolve through the provider registry instead.
func Targets() []string {
	entries, err := assets.ReadDir("targets")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(assets, "targets/"+e.Name()+"/terraform"); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// dockerContextDir is the embedded docker satellite image build context
// (Dockerfile + entrypoint.sh) the docker provider builds its image from.
const dockerContextDir = "targets/docker"

// DockerBuildContext returns the embedded docker image build-context files
// keyed by base name (Dockerfile, entrypoint.sh). The docker provider writes
// them into a temp dir for `docker build` and derives the image tag from
// their content hash, so an asset change rebuilds the image naturally.
func DockerBuildContext() (map[string][]byte, error) {
	entries, err := assets.ReadDir(dockerContextDir)
	if err != nil {
		// Unreachable in a correctly built binary — the files are embedded at
		// compile time — but surfaced rather than panicked for safety.
		return nil, fmt.Errorf("embedded docker build context missing (%s): %w", dockerContextDir, err)
	}
	files := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := assets.ReadFile(dockerContextDir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		files[e.Name()] = data
	}
	if _, ok := files["Dockerfile"]; !ok {
		return nil, fmt.Errorf("embedded docker build context has no Dockerfile (%s)", dockerContextDir)
	}
	return files, nil
}

// HasTarget reports whether an embedded target of that name exists.
func HasTarget(name string) bool {
	for _, t := range Targets() {
		if t == name {
			return true
		}
	}
	return false
}

// TerraformFS returns the terraform module for one embedded target, rooted at
// the module directory (variables.tf etc. at "."). Unknown targets error with
// the embedded target list.
func TerraformFS(target string) (fs.FS, error) {
	if !HasTarget(target) {
		return nil, fmt.Errorf("unknown satellite target %q (embedded targets: %s)", target, strings.Join(Targets(), ", "))
	}
	return fs.Sub(assets, "targets/"+target+"/terraform")
}
