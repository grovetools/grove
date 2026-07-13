package cmd

// grove satellite config push — propagate laptop grove config to a satellite VM.
//
// Two additive inputs, both living in the same [satellites.<name>] registry
// entry `up` writes (the daemon's LoadRegistry ignores unknown keys, verified
// by TestSatelliteRegistryIgnoresProvisionSubtable):
//
//   [satellites.mysat]
//   seed_fragments = ["flow.toml", "claude-settings.toml"]  # laptop fragments, copied verbatim
//
//   [satellites.mysat.config]                 # arbitrary grove-config tables,
//   [satellites.mysat.config.notifications]   # rendered to zz-satellite-custom.toml
//   topic = "mysat-notify"
//
// Everything ships as ADDITIVE fragment files next to the VM's own
// ~/.config/grove/grove.toml (which bootstrap wrote and carries the VM's
// topology — it is never rewritten here). The VM's config loader
// (core/config/config.go) globs *.toml in the global config dir and merges
// fragments sorted by [_grove].priority ascending (later merge wins, so a
// HIGHER priority number wins; default 50), alphabetically within equal
// priority. The rendered custom fragment carries priority 1000 (and the
// alphabetically-last zz- name as a tiebreak) so it reliably overrides seeded
// fragments; notebook/project config layers still override all global
// fragments.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

// satelliteCustomFragmentName is the rendered [satellites.<name>.config]
// fragment on the VM. The zz- prefix keeps it alphabetically last among
// fragments as a same-priority tiebreak; the [_grove] priority below is the
// real override mechanism.
const satelliteCustomFragmentName = "zz-satellite-custom.toml"

// satelliteCustomFragmentPriority beats the loader's DefaultPriority (50) by a
// wide margin: fragments merge in ascending priority order and the later merge
// wins, so 1000 makes the custom fragment override any seeded fragment that
// does not deliberately set an even higher priority.
const satelliteCustomFragmentPriority = 1000

// satelliteManifestName is the remote push manifest: a plain-text dotfile (one
// pushed basename per line, # comments) in the VM's config dir. Deliberately
// NOT a .toml file so the VM's fragment glob never loads it; it exists so a
// later push can WARN about fragments that were pushed before but are no
// longer listed (deletion stays manual).
const satelliteManifestName = ".grove-satellite-pushed"

// satelliteRemoteConfigDir is the VM's global grove config dir, home-relative
// (ssh commands are not login shells; scp resolves relative paths under $HOME).
const satelliteRemoteConfigDir = ".config/grove"

// seedFragmentDenylist holds basename patterns (filepath.Match) that
// seed_fragments may never name — the VM's own topology/identity files and
// secret-bearing conventions. Matching is a hard error, not a skip.
var seedFragmentDenylist = []string{
	"grove.toml", // the VM's own main config (bootstrap-written topology)
	"grove.yml",  // ditto, YAML form
	"grove.override.*",
	"sync.toml",     // VM sync client config (push-only laptop topology)
	"secrets*.toml", // secret-bearing conventions never leave the laptop
	"keys*.toml",
	"groves.toml", // laptop-topology fragments meaningless on the VM
	"notebooks.toml",
	"projects.toml",
	satelliteCustomFragmentName, // reserved: rendered from [satellites.<name>.config]
}

// seedFragmentNameRe keeps fragment basenames safe to embed in generated
// remote shell (cat/scp paths) — same character class as repoNameRe.
var seedFragmentNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// satelliteConfigPropagation is the config-propagation slice of a
// [satellites.<name>] entry: the seed_fragments list and the free-form
// [satellites.<name>.config] table.
type satelliteConfigPropagation struct {
	SeedFragments []string               `yaml:"seed_fragments"`
	Config        map[string]interface{} `yaml:"config"`
}

// loadSatelliteConfigPropagation reads seed_fragments + config for one
// satellite from the layered grove config (same source as the registry entry
// and the provision block; mirrors loadSatelliteProvision).
func loadSatelliteConfigPropagation(name string) (satelliteConfigPropagation, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return satelliteConfigPropagation{}, fmt.Errorf("load grove config: %w", err)
	}
	var raw map[string]satelliteConfigPropagation
	if err := cfg.UnmarshalExtension("satellites", &raw); err != nil {
		return satelliteConfigPropagation{}, fmt.Errorf("parse [satellites.%s] config propagation: %w", name, err)
	}
	return raw[name], nil
}

// hasConfigToPush reports whether the entry declares anything to propagate.
func (p satelliteConfigPropagation) hasConfigToPush() bool {
	return len(p.SeedFragments) > 0 || len(p.Config) > 0
}

// satellitePushFile is one file destined for the VM's config dir.
type satellitePushFile struct {
	Name string // basename on the VM
	Data []byte
}

// validateSeedFragmentName enforces the basename + denylist contract. Errors,
// never skips: a denied or malformed entry means the user's intent cannot be
// honored safely.
func validateSeedFragmentName(name string) error {
	if name == "" {
		return fmt.Errorf("seed_fragments: empty fragment name")
	}
	if name != filepath.Base(name) || !seedFragmentNameRe.MatchString(name) {
		return fmt.Errorf("seed_fragments: %q must be a plain fragment basename (no path separators or special characters)", name)
	}
	for _, pattern := range seedFragmentDenylist {
		if ok, _ := filepath.Match(pattern, name); ok {
			return fmt.Errorf("seed_fragments: %q is denied (matches %q) — main configs, overrides, sync/secret/key files, and laptop-topology fragments never ship to a satellite", name, pattern)
		}
	}
	if !strings.HasSuffix(name, ".toml") {
		return fmt.Errorf("seed_fragments: %q must be a .toml fragment (the VM's config loader only globs *.toml)", name)
	}
	return nil
}

// vetSeedFragmentContent parses a fragment and refuses content that must not
// reach a satellite even under an innocent filename: a top-level `satellites`
// key (registry recursion — the VM would try to federate) or a `[daemon]`
// table (host topology, owned by the VM's bootstrap-written grove.toml).
func vetSeedFragmentContent(name string, data []byte) error {
	var m map[string]interface{}
	if err := toml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("seed_fragments: %s does not parse as TOML: %w", name, err)
	}
	if _, ok := m["satellites"]; ok {
		return fmt.Errorf("seed_fragments: %s contains a top-level 'satellites' key — shipping the satellite registry to a satellite is refused (registry recursion)", name)
	}
	if _, ok := m["daemon"]; ok {
		return fmt.Errorf("seed_fragments: %s contains a [daemon] table — daemon topology is owned by the VM's own grove.toml and is refused", name)
	}
	return nil
}

// renderSatelliteCustomFragment renders the [satellites.<name>.config] table
// as the zz-satellite-custom.toml fragment: a generated-by header, the whole
// table marshaled with an added [_grove] priority so it wins over seeded
// fragments through the real loader. The same satellites/daemon refusal as
// seed fragments applies.
func renderSatelliteCustomFragment(name string, cfg map[string]interface{}) ([]byte, error) {
	for _, denied := range []string{"satellites", "daemon", "_grove"} {
		if _, ok := cfg[denied]; ok {
			return nil, fmt.Errorf("[satellites.%s.config]: top-level %q table is refused (it is %s)", name, denied,
				map[string]string{
					"satellites": "registry recursion",
					"daemon":     "VM topology, owned by the VM's own grove.toml",
					"_grove":     "reserved fragment metadata; the push sets it",
				}[denied])
		}
	}
	full := make(map[string]interface{}, len(cfg)+1)
	for k, v := range cfg {
		full[k] = v
	}
	full["_grove"] = map[string]interface{}{"priority": satelliteCustomFragmentPriority}
	body, err := toml.Marshal(full)
	if err != nil {
		return nil, fmt.Errorf("render [satellites.%s.config] to TOML: %w", name, err)
	}
	header := fmt.Sprintf("# Generated by `grove satellite config push` for satellite %q.\n"+
		"# Rendered from [satellites.%s.config] in the laptop's grove config.\n"+
		"# Do not edit on the VM — the next push overwrites this file.\n\n", name, name)
	return append([]byte(header), body...), nil
}

// assembleSatellitePush builds the ordered file set a push would ship: the
// seed fragments (read from the laptop's global config dir, validated and
// vetted) followed by the rendered custom fragment. A listed-but-missing seed
// fragment is a hard error. Returns an empty slice when the entry declares
// nothing to propagate.
func assembleSatellitePush(name string, prop satelliteConfigPropagation, laptopConfigDir string) ([]satellitePushFile, error) {
	var files []satellitePushFile
	for _, frag := range prop.SeedFragments {
		if err := validateSeedFragmentName(frag); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(laptopConfigDir, frag))
		if err != nil {
			return nil, fmt.Errorf("seed_fragments: %s not readable in %s (listed fragments must exist): %w", frag, laptopConfigDir, err)
		}
		if err := vetSeedFragmentContent(frag, data); err != nil {
			return nil, err
		}
		files = append(files, satellitePushFile{Name: frag, Data: data})
	}
	if len(prop.Config) > 0 {
		rendered, err := renderSatelliteCustomFragment(name, prop.Config)
		if err != nil {
			return nil, err
		}
		files = append(files, satellitePushFile{Name: satelliteCustomFragmentName, Data: rendered})
	}
	return files, nil
}

// satelliteConfigTransport abstracts the pinned-SSH transport so the push
// logic (diff/no-write, removed-fragment warnings, manifest handling) is unit
// testable without a VM.
type satelliteConfigTransport interface {
	// fetchConfigFile returns the remote config file's content, "" when absent.
	fetchConfigFile(name string) (string, error)
	// pushConfigFiles ships files into the VM's config dir.
	pushConfigFiles(files []satellitePushFile) error
}

// sshConfigTransport implements satelliteConfigTransport over the pinned
// ssh/scp helpers from satellite_upgrade.go (host_key pinned, never TOFU).
type sshConfigTransport struct {
	ssh *satelliteSSH
}

func (t sshConfigTransport) fetchConfigFile(name string) (string, error) {
	// Names were validated against seedFragmentNameRe (or are our constants),
	// so embedding them in the script is safe. A missing file reads as empty.
	script := fmt.Sprintf("cat \"$HOME/%s/%s\" 2>/dev/null || true\n", satelliteRemoteConfigDir, name)
	return t.ssh.outputScript(script)
}

func (t sshConfigTransport) pushConfigFiles(files []satellitePushFile) error {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	localPaths := make([]string, 0, len(files))
	for _, f := range files {
		p := filepath.Join(tmpDir, f.Name)
		if err := os.WriteFile(p, f.Data, 0o600); err != nil {
			return err
		}
		localPaths = append(localPaths, p)
	}
	if err := t.ssh.runCommand(fmt.Sprintf("mkdir -p \"$HOME/%s\"", satelliteRemoteConfigDir)); err != nil {
		return fmt.Errorf("create remote config dir: %w", err)
	}
	if err := t.ssh.scp(localPaths, satelliteRemoteConfigDir+"/"); err != nil {
		return fmt.Errorf("scp config fragments: %w", err)
	}
	return nil
}

// parseSatelliteManifest decodes the remote manifest: one basename per line,
// # comments and blanks ignored.
func parseSatelliteManifest(content string) []string {
	var names []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	return names
}

// renderSatelliteManifest is the inverse of parseSatelliteManifest.
func renderSatelliteManifest(files []satellitePushFile) []byte {
	var b strings.Builder
	b.WriteString("# Written by `grove satellite config push` — fragments pushed from the laptop.\n")
	b.WriteString("# Used to warn about fragments removed from seed_fragments (deletion stays manual).\n")
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString(n + "\n")
	}
	return []byte(b.String())
}

// warnRemovedFragments compares the previous manifest against the current push
// set and names fragments that were pushed before but are no longer listed.
// They are left in place on the VM — deletion stays manual for now.
func warnRemovedFragments(out io.Writer, previous []string, files []satellitePushFile) {
	current := map[string]bool{}
	for _, f := range files {
		current[f.Name] = true
	}
	var removed []string
	for _, name := range previous {
		if !current[name] {
			removed = append(removed, name)
		}
	}
	if len(removed) == 0 {
		return
	}
	sort.Strings(removed)
	fmt.Fprintf(out, "warning: previously pushed fragment(s) no longer listed: %s\n", strings.Join(removed, ", "))
	fmt.Fprintf(out, "         they remain on the VM in ~/%s — remove them manually if unwanted.\n", satelliteRemoteConfigDir)
}

// unifiedDiff renders a unified diff between remote (old) and local (new)
// content by shelling out to `diff -u` (exit 1 = differences, not an error).
// Returns "" when the contents are identical.
func unifiedDiff(name, remote, local string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-diff-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	remotePath := filepath.Join(tmpDir, "remote")
	localPath := filepath.Join(tmpDir, "local")
	if err := os.WriteFile(remotePath, []byte(remote), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(localPath, []byte(local), 0o600); err != nil {
		return "", err
	}
	cmd := exec.Command("diff", "-u",
		"-L", "vm/"+name, "-L", "laptop/"+name,
		remotePath, localPath) //nolint:gosec // G204: temp paths + validated names
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return string(out), nil // differences found
		}
		return "", fmt.Errorf("diff %s: %w", name, err)
	}
	return "", nil // identical
}

// runSatelliteConfigPush is the shared push engine `config push` and `up` both
// call. Diff mode fetches remote contents and prints unified diffs — no writes
// at all (no fragments, no manifest). Default mode ships fragments + manifest
// and prints the daemon-restart caveat.
func runSatelliteConfigPush(name string, files []satellitePushFile, tr satelliteConfigTransport, diffMode bool, out io.Writer) error {
	if len(files) == 0 {
		fmt.Fprintf(out, "satellite %q declares no seed_fragments and no [satellites.%s.config] — nothing to push.\n", name, name)
		return nil
	}

	// Removed-fragment warning (read-only in both modes): the previous push's
	// manifest, absent → first push, nothing to warn about.
	manifestContent, err := tr.fetchConfigFile(satelliteManifestName)
	if err != nil {
		return fmt.Errorf("read remote push manifest: %w", err)
	}
	warnRemovedFragments(out, parseSatelliteManifest(manifestContent), files)

	if diffMode {
		for _, f := range files {
			remote, err := tr.fetchConfigFile(f.Name)
			if err != nil {
				return fmt.Errorf("read remote %s: %w", f.Name, err)
			}
			d, err := unifiedDiff(f.Name, remote, string(f.Data))
			if err != nil {
				return err
			}
			if d == "" {
				fmt.Fprintf(out, "%s: unchanged\n", f.Name)
				continue
			}
			if remote == "" {
				fmt.Fprintf(out, "%s: new on VM\n", f.Name)
			}
			fmt.Fprint(out, d)
		}
		fmt.Fprintln(out, "\n(--diff: nothing was written)")
		return nil
	}

	shipped := append([]satellitePushFile{}, files...)
	shipped = append(shipped, satellitePushFile{Name: satelliteManifestName, Data: renderSatelliteManifest(files)})
	if err := tr.pushConfigFiles(shipped); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	fmt.Fprintf(out, "Pushed %d fragment(s) to %q ~/%s: %s\n", len(files), name, satelliteRemoteConfigDir, strings.Join(names, ", "))
	fmt.Fprintln(out, "\nNote: grove CLI invocations on the VM re-read config per run and see this")
	fmt.Fprintln(out, "immediately, but the VM's groved reads config only at boot. If the pushed")
	fmt.Fprintln(out, "fragments carry daemon-read keys, restart it there:")
	fmt.Fprintf(out, "  ssh <vm> 'XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart groved'\n")
	return nil
}

// pushSatelliteConfigOverSSH wires the registry entry into the pinned SSH
// transport and runs the push. Shared by `config push` and `up`.
func pushSatelliteConfigOverSSH(name string, entry satelliteConfigEntry, files []satellitePushFile, diffMode bool) error {
	tmpDir, err := os.MkdirTemp("", "grove-satellite-config-ssh-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	ssh, err := newSatelliteSSH(entry, tmpDir)
	if err != nil {
		return fmt.Errorf("satellite %q: %w", name, err)
	}
	return runSatelliteConfigPush(name, files, sshConfigTransport{ssh: ssh}, diffMode, os.Stdout)
}

func newSatelliteConfigCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("config", "Propagate laptop grove config to a satellite VM")
	cmd.Long = `Propagate laptop grove config to a satellite's ~/.config/grove as fragments.

Two inputs, both in the [satellites.<name>] registry entry:

  [satellites.mysat]
  # laptop fragment files (basenames in ~/.config/grove), copied verbatim:
  seed_fragments = ["flow.toml", "claude-settings.toml", "skills.toml"]

  [satellites.mysat.config]                  # arbitrary grove-config tables,
  [satellites.mysat.config.notifications]   # rendered to zz-satellite-custom.toml
  topic = "mysat-notify"

Denylist (hard error, matched by basename): grove.toml, grove.yml,
grove.override.*, sync.toml, secrets*.toml, keys*.toml, groves.toml,
notebooks.toml, projects.toml. The VM's own grove.toml carries its topology
(bootstrap-written) and is never touched — everything here is additive
fragment files beside it. Fragments containing a top-level 'satellites' key
(registry recursion) or a [daemon] table (topology) are refused regardless of
filename.

Precedence on the VM: global fragments merge by [_grove].priority — higher
priority merges later and WINS (default 50), alphabetical within equal
priority. The rendered custom fragment ships with priority 1000, so
[satellites.<name>.config] overrides seeded fragments; notebook- and
project-level config layers still override all global fragments.`
	cmd.AddCommand(newSatelliteConfigPushCmd())
	return cmd
}

func newSatelliteConfigPushCmd() *cobra.Command {
	var diffMode bool
	cmd := cli.NewStandardCommand("push <name>", "Push seed fragments + rendered custom config to the VM")
	cmd.Long = `Assemble the satellite's fragment set (seed_fragments copied verbatim +
[satellites.<name>.config] rendered as zz-satellite-custom.toml) and scp it to
~/.config/grove on the VM over the pinned registry host key.

--diff fetches the current remote contents instead and prints unified diffs —
nothing is written.

Fragments previously pushed but since removed from seed_fragments are left on
the VM with a warning (tracked via a ~/.config/grove/.grove-satellite-pushed
manifest); deletion stays manual.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	cmd.Flags().BoolVar(&diffMode, "diff", false, "Show unified diffs against the VM's current fragments; write nothing")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		entry, ok := loadMergedSatellites()[name]
		if !ok {
			return fmt.Errorf("satellite %q not found in the registry (config or state) — run `grove satellite up %s` first", name, name)
		}
		prop, err := loadSatelliteConfigPropagation(name)
		if err != nil {
			return err
		}
		configDir := paths.ConfigDir()
		if configDir == "" {
			return fmt.Errorf("could not resolve the laptop's grove config directory")
		}
		files, err := assembleSatellitePush(name, prop, configDir)
		if err != nil {
			return err
		}
		return pushSatelliteConfigOverSSH(name, entry, files, diffMode)
	}
	return cmd
}
