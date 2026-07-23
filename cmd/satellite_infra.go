package cmd

// Infra inputs for `grove satellite up` — the [satellites.<name>.infra] config
// block giving the five infra flags (--project, --zone, --ssh-user, --cidr,
// --identity-file) a persistent config home, so a fresh 0→1 after `down`
// retypes nothing. Mirrors the [satellites.<name>.provision] pattern exactly:
// config values act as defaults, explicit flags win (including set-to-empty).
// `up` writes the RESOLVED values back after a successful provision, and
// `down`'s registry removal deliberately leaves the subtable in place
// (removeSatelliteTOMLTable only removes the flat block), so the first
// flag-ful `up` is the last time the flags are needed.

import (
	"fmt"

	"github.com/grovetools/core/config"

	"github.com/grovetools/grove/pkg/setup"
)

// satelliteInfraConfig is the [satellites.<name>.infra] table. Like the
// provision and sync blocks it is a grove-CLI-only input to `up`; the daemon's
// SatelliteConfig does not know the key, and its mapstructure decode
// (config.UnmarshalExtension, no ErrorUnused) ignores unknown keys, so the
// subtable rides alongside the registry entry without breaking daemon-side
// LoadRegistry (verified by TestSatelliteRegistryIgnoresInfraSubtable).
type satelliteInfraConfig struct {
	// Target selects the embedded infra target (grove/cmd/satelliteassets/
	// targets/<target>) whose terraform module provisions the VM. Empty
	// resolves to "gcp" — the only embedded target today.
	Target string `yaml:"target"`
	// Project is the GCP project id (terraform var project_id).
	Project string `yaml:"project"`
	// Zone is the GCP zone override (terraform var zone).
	Zone string `yaml:"zone"`
	// SSHUser is the SSH login user on the VM (terraform var ssh_user).
	SSHUser string `yaml:"ssh_user"`
	// CIDR is the CIDR allowed to reach :22 (terraform var allowed_ssh_cidr).
	// `up` persists the RESOLVED value, so an auto-detected public IP /32 is
	// written back and reused until overridden with --cidr.
	CIDR string `yaml:"cidr"`
	// IdentityFile is the SSH private key recorded in the registry entry
	// (empty = agent-only auth).
	IdentityFile string `yaml:"identity_file"`
	// Image is the guest image override (tart/docker targets only): the OCI
	// image the tart provider clones (empty resolves to defaultTartImage),
	// or the docker image the docker provider runs as-is instead of building
	// its embedded Dockerfile (empty resolves to the grove-owned
	// content-hash tag).
	Image string `yaml:"image"`
	// TartHome relocates tart's storage (the TART_HOME env var) for every
	// tart command grove runs (tart target only). `up` resolves the EFFECTIVE
	// value (--tart-home, else this block, else the process TART_HOME, else
	// tart's own default) and persists it, so `down` drives the same store
	// even from a shell whose environment differs.
	TartHome string `yaml:"tart_home"`
	// TartVolumeIdentity pins the external volume observed by the first full
	// Tart preflight (Volume UUID, with device identifier as a fallback).
	// Later ups refuse a different volume mounted at the same path.
	TartVolumeIdentity string `yaml:"tart_volume_identity"`
}

// loadSatelliteInfra reads [satellites.<name>.infra] from the same layered
// grove config the registry lives in (mirrors loadSatelliteProvision). A
// missing satellite or missing infra subtable yields the zero value with
// found=false; a malformed one is an error. The found flag drives `up`'s
// write-back rescope: a block that already exists anywhere in the merged
// config (grove.toml, a dotfiles fragment, ...) is never edited by grove.
func loadSatelliteInfra(name string) (infra satelliteInfraConfig, found bool, err error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return satelliteInfraConfig{}, false, fmt.Errorf("load grove config: %w", err)
	}
	return satelliteInfraFromConfig(cfg, name)
}

// satelliteInfraFromConfig decodes only the infra subtables out of the
// [satellites.*] extension (separate decode from satelliteConfigEntry, same
// stance as satelliteProvisionFromConfig). The pointer field makes presence
// observable: mapstructure only allocates it when the infra key exists, so an
// empty-but-present [satellites.<name>.infra] block still reports found=true.
func satelliteInfraFromConfig(cfg *config.Config, name string) (satelliteInfraConfig, bool, error) {
	var raw map[string]struct {
		Infra *satelliteInfraConfig `yaml:"infra"`
	}
	if err := cfg.UnmarshalExtension("satellites", &raw); err != nil {
		return satelliteInfraConfig{}, false, fmt.Errorf("parse [satellites.%s.infra]: %w", name, err)
	}
	if raw[name].Infra == nil {
		return satelliteInfraConfig{}, false, nil
	}
	return *raw[name].Infra, true, nil
}

// infraFlagOverrides carries the `up` infra flag values plus whether each flag
// was actually set (cobra Changed) — a set flag always wins over the config
// block, including set-to-empty (which disables the config value, e.g.
// --cidr "" to force re-detection of the public IP).
type infraFlagOverrides struct {
	Target       string
	TargetSet    bool
	Project      string
	ProjectSet   bool
	Zone         string
	ZoneSet      bool
	SSHUser      string
	SSHUserSet   bool
	CIDR         string
	CIDRSet      bool
	IdentityFile string
	IdentitySet  bool
	Image        string
	ImageSet     bool
	TartHome     string
	TartHomeSet  bool
}

// mergeInfra resolves flag-over-config precedence for the infra inputs
// (mirrors mergeProvision). The --project/--ssh-user required check runs on
// the MERGED result, so a populated infra block makes a flag-less `up` valid.
func mergeInfra(cfg satelliteInfraConfig, f infraFlagOverrides) satelliteInfraConfig {
	out := cfg
	if f.TargetSet {
		out.Target = f.Target
	}
	if f.ProjectSet {
		out.Project = f.Project
	}
	if f.ZoneSet {
		out.Zone = f.Zone
	}
	if f.SSHUserSet {
		out.SSHUser = f.SSHUser
	}
	if f.CIDRSet {
		out.CIDR = f.CIDR
	}
	if f.IdentitySet {
		out.IdentityFile = f.IdentityFile
	}
	if f.ImageSet {
		out.Image = f.Image
	}
	if f.TartHomeSet {
		out.TartHome = f.TartHome
	}
	return out
}

// writeSatelliteInfra persists the resolved infra values as the
// [satellites.<name>.infra] subtable in the same global config file the
// registry lands in (see satelliteRegistryPath). TOML configs get the same
// comment-preserving targeted splice as the registry entry; YAML configs use
// the yaml.Node plumbing.
func writeSatelliteInfra(name string, infra satelliteInfraConfig) error {
	path, isTOML, err := satelliteRegistryPath()
	if err != nil {
		return err
	}
	if isTOML {
		return upsertSatelliteInfraTOMLTable(path, name, infra)
	}
	yh := setup.NewYAMLHandler(setup.NewService(false))
	root, err := yh.LoadYAML(path)
	if err != nil {
		return err
	}
	// Empty fields are omitted (matching the TOML splice) so unset inputs
	// (zone, identity_file) do not pin empty strings as future defaults.
	values := map[string]interface{}{}
	if infra.Target != "" {
		values["target"] = infra.Target
	}
	if infra.Project != "" {
		values["project"] = infra.Project
	}
	if infra.Zone != "" {
		values["zone"] = infra.Zone
	}
	if infra.SSHUser != "" {
		values["ssh_user"] = infra.SSHUser
	}
	if infra.CIDR != "" {
		values["cidr"] = infra.CIDR
	}
	if infra.IdentityFile != "" {
		values["identity_file"] = infra.IdentityFile
	}
	if infra.Image != "" {
		values["image"] = infra.Image
	}
	if infra.TartHome != "" {
		values["tart_home"] = infra.TartHome
	}
	if infra.TartVolumeIdentity != "" {
		values["tart_volume_identity"] = infra.TartVolumeIdentity
	}
	if err := setup.SetValue(root, values, "satellites", name, "infra"); err != nil {
		return err
	}
	return yh.SaveYAML(path, root)
}
