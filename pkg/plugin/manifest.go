// Package plugin implements grove's plugin distribution layer: the manifest a
// plugin repository ships, the lockfile that pins exactly what is installed,
// and the install/update/remove pipeline that drives grove's existing clone,
// build and config seams in order.
//
// The pipeline is deliberately thin, because most of it already existed:
//
//	Declare  ~/.config/grove/plugins/*.toml drop-in glob   (core/config)
//	Clone    git, into the grove-managed data dir          (this package)
//	Build    grove/pkg/build's job runner                  (grove/pkg/build)
//	Install  versions/<commit>/bin + a bin/ symlink        (grove/pkg/sdk conventions)
//	Appear   config_reload hot-reload in treemux           (treemux)
//
// What this package adds is the manifest format (grove-plugin.toml, below),
// the lockfile (lock.go), and one consent moment (consent.go) recorded in the
// existing exec-trust store rather than in a second trust store of its own.
package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// ManifestFile is the file a plugin repository ships at its root.
const ManifestFile = "grove-plugin.toml"

// SchemaVersion is the manifest schema this grove understands. A manifest
// declaring a higher version is refused rather than guessed at: its build or
// panel section may mean something this binary would get wrong.
const SchemaVersion = 1

// ProtocolEmbedV1 is the sidecar control-plane protocol treemux speaks
// (treemux/docs/panel-protocol-v1.md). The empty string is the other legal
// value: a plain PTY panel.
const ProtocolEmbedV1 = "embed/v1"

// Manifest is a parsed grove-plugin.toml.
//
//	schema_version = 1
//
//	[plugin]
//	name        = "hello"
//	description = "A hello-world sidecar panel"
//	homepage    = "https://github.com/grovetools/grove-panel-hello"
//
//	[build]
//	command = ["go", "build", "-o", "bin/grove-panel-hello", "."]
//	binary  = "bin/grove-panel-hello"
//
//	[panel]
//	label            = "Break timer"
//	icon             = ""
//	protocol         = "embed/v1"
//	protocol_timeout = "2s"
//	args             = []
//	env              = []
//	restart          = true
//
//	[panel.settings]
//	work_minutes  = 25
//	break_minutes = 5
//
//	[[panel.keys]]
//	key         = "ctrl+f"
//	description = "jump to the notebook"
type Manifest struct {
	SchemaVersion int      `toml:"schema_version"`
	Plugin        Plugin   `toml:"plugin"`
	Build         Build    `toml:"build"`
	Panel         Panel    `toml:"panel"`
	Unknown       []string `toml:"-"` // keys this grove does not understand
}

// Plugin is the identity section.
type Plugin struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Homepage    string `toml:"homepage"`
}

// Build says how to turn the checkout into a binary. Command is argv, never a
// shell string: the consent screen has to show the user exactly what will run,
// and "sh -c ..." would hide it behind a shell. Command may be empty for a
// plugin that ships an interpreted program — then Binary must already exist in
// the checkout, which is also the no-toolchain-required path.
type Build struct {
	Command []string `toml:"command"`
	Binary  string   `toml:"binary"`
}

// Panel is what the installer turns into a [tui.plugins.<name>] fragment.
type Panel struct {
	// Label is the human-readable name the rail shows. Empty falls back to
	// plugin.name, which is constrained to a bare key and is often not what
	// the author would choose to display.
	Label           string   `toml:"label"`
	Icon            string   `toml:"icon"`
	Protocol        string   `toml:"protocol"`
	ProtocolTimeout string   `toml:"protocol_timeout"`
	Args            []string `toml:"args"`
	Env             []string `toml:"env"`
	Restart         bool     `toml:"restart"`
	Keys            []Key    `toml:"keys"`
	// Settings are the panel's DEFAULT settings: the free-form table the host
	// delivers to it over the control plane, seeded from the manifest so a
	// freshly installed panel works before the user has configured anything.
	//
	// The user's own [tui.plugins.<name>.settings] is the same key in a config
	// layer they own, and later layers replace earlier ones wholesale, so
	// editing the fragment is how a user overrides these.
	//
	// They are shown on the consent screen and bound into the approval digest
	// like everything else in the manifest — not because grove will run them
	// (it will not: the host forwards this table and never interprets it) but
	// because a value the user has approved is one they should have read. An
	// update that changes a default re-opens the prompt with a diff.
	Settings map[string]any `toml:"settings"`
}

// Key is a host hotkey the panel intends to claim over the control plane. It
// is a DECLARATION, not a grant: the host filters claims at handshake time
// (see welcome.rejected_keys in the protocol spec). It lives in the manifest
// so the user reads it before approving an install, not because the installer
// enforces it.
type Key struct {
	Key         string `toml:"key"`
	Description string `toml:"description"`
}

var (
	// namePattern keeps a plugin name usable as a filename, a TOML bare key
	// and a [tui.plugins.<name>] table name all at once.
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	// envPattern is the KEY=VALUE form core/config's PluginConfig.Env expects.
	envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// LoadManifest reads and validates the manifest at the root of a checkout.
// It returns the parsed manifest and the exact bytes it parsed, which the
// consent digest binds to (see ConsentFacts).
func LoadManifest(repoDir string) (*Manifest, []byte, error) {
	path := filepath.Join(repoDir, ManifestFile)
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is grove's own managed checkout
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("%s is not a grove plugin: no %s at its root", repoDir, ManifestFile)
		}
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, data, nil
}

// ParseManifest decodes and validates manifest bytes.
//
// Decoding is strict about unknown keys, but a strict decode is a WARNING, not
// a failure: a manifest written for a later schema must still be readable
// enough to say so, and a plugin author's typo is worth surfacing without
// breaking installs that otherwise work. Unrecognized keys land in
// Manifest.Unknown and the consent screen prints them.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		var strict *toml.StrictMissingError
		if !errors.As(err, &strict) {
			return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
		}
		for _, e := range strict.Errors {
			m.Unknown = append(m.Unknown, strings.Join(e.Key(), "."))
		}
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate reports the first thing wrong with a manifest. Every message names
// the key, because the reader is a plugin author debugging their own repo.
func (m *Manifest) Validate() error {
	switch {
	case m.SchemaVersion == 0:
		return fmt.Errorf("schema_version is required (this grove understands %d)", SchemaVersion)
	case m.SchemaVersion > SchemaVersion:
		return fmt.Errorf("schema_version %d is newer than this grove understands (%d) — upgrade grove", m.SchemaVersion, SchemaVersion)
	case m.SchemaVersion < SchemaVersion:
		return fmt.Errorf("schema_version %d is no longer supported (this grove understands %d)", m.SchemaVersion, SchemaVersion)
	}

	if m.Plugin.Name == "" {
		return errors.New("plugin.name is required")
	}
	if !namePattern.MatchString(m.Plugin.Name) {
		return fmt.Errorf("plugin.name %q must be lowercase letters, digits and dashes, starting with a letter or digit", m.Plugin.Name)
	}
	if strings.TrimSpace(m.Plugin.Description) == "" {
		// The description is not decoration: it is one of the few things the
		// user reads before approving an install.
		return errors.New("plugin.description is required — it is shown at the install consent prompt")
	}

	if err := validateArgv("build.command", m.Build.Command); err != nil {
		return err
	}
	if m.Build.Binary == "" {
		return errors.New("build.binary is required — name the file the build produces")
	}
	if err := validateRelPath("build.binary", m.Build.Binary); err != nil {
		return err
	}

	switch m.Panel.Protocol {
	case "", ProtocolEmbedV1:
	default:
		return fmt.Errorf("panel.protocol %q is not a protocol this host speaks (use %q or leave it empty for a plain PTY panel)", m.Panel.Protocol, ProtocolEmbedV1)
	}
	if m.Panel.ProtocolTimeout != "" {
		if _, err := time.ParseDuration(m.Panel.ProtocolTimeout); err != nil {
			return fmt.Errorf("panel.protocol_timeout %q is not a Go duration (e.g. \"2s\")", m.Panel.ProtocolTimeout)
		}
	}
	if err := validateArgv("panel.args", m.Panel.Args); err != nil {
		return err
	}
	for _, e := range m.Panel.Env {
		if !envPattern.MatchString(e) {
			return fmt.Errorf("panel.env entry %q must be KEY=VALUE", e)
		}
		if err := printable("panel.env", e); err != nil {
			return err
		}
	}
	if err := printable("panel.icon", m.Panel.Icon); err != nil {
		return err
	}
	if err := printable("panel.label", m.Panel.Label); err != nil {
		return err
	}
	if err := validateSettings("panel.settings", m.Panel.Settings); err != nil {
		return err
	}
	for i, k := range m.Panel.Keys {
		if strings.TrimSpace(k.Key) == "" {
			return fmt.Errorf("panel.keys[%d].key is required", i)
		}
		if strings.TrimSpace(k.Description) == "" {
			return fmt.Errorf("panel.keys[%d].description is required — the user reads it before approving the claim", i)
		}
		if err := printable(fmt.Sprintf("panel.keys[%d].key", i), k.Key); err != nil {
			return err
		}
	}
	return nil
}

// BinaryName is the basename the built binary is installed under.
func (m *Manifest) BinaryName() string {
	return filepath.Base(filepath.Clean(m.Build.Binary))
}

// validateArgv rejects empty and non-printable argv elements. An empty element
// is almost always a TOML mistake, and it would silently pass an empty
// argument to the command.
func validateArgv(key string, argv []string) error {
	for i, a := range argv {
		if a == "" {
			return fmt.Errorf("%s[%d] is empty", key, i)
		}
		if err := printable(fmt.Sprintf("%s[%d]", key, i), a); err != nil {
			return err
		}
	}
	return nil
}

// validateRelPath keeps a manifest from naming a file outside its own
// checkout — the manifest is untrusted input until the user approves it, and
// even after approval "build.binary = /etc/passwd" is not a thing to honor.
func validateRelPath(key, p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s %q must be relative to the plugin repo root", key, p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must stay inside the plugin repo", key, p)
	}
	return printable(key, p)
}

// validateSettings walks a free-form settings table and rejects anything that
// could not be shown honestly on the consent screen.
//
// grove deliberately does not constrain the SHAPE — it cannot know what a
// third-party panel's options mean, and a schema that guessed would reject
// valid ones. What it does constrain is renderability: every key and every
// string value ends up printed on a screen the user's approval depends on, and
// a value carrying an escape sequence could redraw that screen to say something
// other than what will be installed. Tables and arrays are walked so nesting
// cannot smuggle one past.
//
// The depth bound is not about attack surface; it is about the consent screen
// staying legible. A manifest that needs more than a few levels of nesting to
// express its defaults has outgrown what a user can meaningfully approve.
func validateSettings(key string, settings map[string]any) error {
	return validateSettingsAt(key, settings, 0)
}

const maxSettingsDepth = 8

func validateSettingsAt(key string, v any, depth int) error {
	if depth > maxSettingsDepth {
		return fmt.Errorf("%s nests more than %d levels deep", key, maxSettingsDepth)
	}
	switch v := v.(type) {
	case map[string]any:
		for name, value := range v {
			if err := printable(key+" key "+name, name); err != nil {
				return err
			}
			if err := validateSettingsAt(key+"."+name, value, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for i, value := range v {
			if err := validateSettingsAt(fmt.Sprintf("%s[%d]", key, i), value, depth+1); err != nil {
				return err
			}
		}
	case string:
		return printable(key, v)
	}
	return nil
}

// printable rejects control characters. Everything here is rendered into a
// terminal consent screen the user's decision depends on, so a value that can
// move the cursor or clear the line is refused rather than displayed.
func printable(key, v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains a control character", key)
		}
	}
	return nil
}
