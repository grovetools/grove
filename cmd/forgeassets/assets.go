// Package forgeassets embeds the infrastructure assets `grove forge` ships
// inside the grove binary: the terraform root that provisions the SERVICES VM
// (Forgejo + grove-syncd colocated) and the two service modules it composes.
//
// It is a sibling of satelliteassets rather than a target inside it, and that
// separation is the design. Satellites are cattle: `grove satellite down`
// destroys one and the registry is the truth. The forge is a pet — it
// accumulates every plan branch ever pushed, so it needs a stable identity,
// backups, and a destroy path that is deliberately harder to walk. Sharing an
// asset tree would eventually mean sharing a verb, and a cattle verb must never
// point at the pet.
//
// What IS shared, by import rather than by copy, is the plumbing:
// grove/cmd/forge_assets.go reuses satelliteassets' extraction discipline
// (module files overwritten every run so they version with the binary;
// terraform.tfstate*/terraform.tfvars/.terraform* never touched) through the
// same helpers the satellite verbs use.
//
// The embedded tree deliberately contains no local terraform artifacts:
// go:embed skips dotfiles (.terraform/, .terraform.lock.hcl) by default, and
// the committed tree carries no state or tfvars files. CONTRACT.md documents
// the variables and outputs a module must honor, which is also what a
// bring-your-own --tf-dir module has to meet.
package forgeassets

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed terraform
var assets embed.FS

// The on-VM backup payload: the script pair and their systemd units.
//
// Deliberately NOT rendered by the startup script. startup.sh.tpl guards
// itself with /var/lib/grove-forge/startup-done and therefore runs exactly
// once per VM lifetime — it can provision a fresh forge but cannot converge an
// existing one. Backups have to be installable on the forge that is already
// running, so they follow grove-syncd's precedent instead: shipped from the
// laptop over the pinned SSH connection after terraform apply, every `up`.
//
//go:embed backup
var backupAssets embed.FS

// terraformRoot is the embedded module root inside the asset FS.
const terraformRoot = "terraform"

// backupRoot is the embedded on-VM backup payload.
const backupRoot = "backup"

// BackupFS returns the embedded backup payload, rooted at the payload
// directory (the two scripts and the four unit files at ".").
func BackupFS() (fs.FS, error) {
	sub, err := fs.Sub(backupAssets, backupRoot)
	if err != nil {
		return nil, fmt.Errorf("embedded forge backup payload missing (%s): %w", backupRoot, err)
	}
	return sub, nil
}

// TerraformFS returns the embedded terraform root, rooted at the module
// directory (main.tf, variables.tf, outputs.tf and modules/ at ".").
func TerraformFS() (fs.FS, error) {
	sub, err := fs.Sub(assets, terraformRoot)
	if err != nil {
		// Unreachable in a correctly built binary — the tree is embedded at
		// compile time — but surfaced rather than panicked for safety.
		return nil, fmt.Errorf("embedded forge terraform root missing (%s): %w", terraformRoot, err)
	}
	return sub, nil
}

// Files lists every embedded terraform file, slash-separated and relative to
// the module root, sorted.
//
// It exists so extraction and tests can talk about the tree by name: a golden
// test over this list is what makes "someone deleted app.ini.tpl" a test
// failure rather than a broken VM three provisions later.
func Files() ([]string, error) {
	tfFS, err := TerraformFS()
	if err != nil {
		return nil, err
	}
	var out []string
	err = fs.WalkDir(tfFS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// Read returns one embedded file by its module-root-relative path.
func Read(name string) ([]byte, error) {
	data, err := assets.ReadFile(terraformRoot + "/" + strings.TrimPrefix(name, "/"))
	if err != nil {
		return nil, fmt.Errorf("embedded forge asset %q: %w", name, err)
	}
	return data, nil
}
