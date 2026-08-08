package plugin

import (
	"time"

	coreplugin "github.com/grovetools/core/pkg/plugin"
)

// The read side of this package lives in core.
//
// It moved because the host reads what the installer writes, and the host is in
// another module: treemux has to answer "which commit is installed, what did the
// manifest declare, does the approval still cover it" on a redraw, and reaching
// grove for that would mean importing the whole install pipeline — the git
// operations, the build runner — to read four files. Everything the read side
// needs (paths, exectrust, go-toml) was already in core.
//
// What stayed here is everything that WRITES: clone, build, install, declare,
// record, remove.
//
// The aliases below re-export the moved surface so this package's own files and
// its one importer keep compiling against the names they were written with.
// They are aliases rather than definitions, so a value produced through either
// import path is the same type. Same pattern, same reason as the panelkit move
// (treemux/pkg/panelproto).

// Manifest schema identity.
const (
	ManifestFile    = coreplugin.ManifestFile
	SchemaVersion   = coreplugin.SchemaVersion
	ProtocolEmbedV1 = coreplugin.ProtocolEmbedV1
)

// Lockfile and drop-in directory naming.
const (
	LockFileName   = coreplugin.LockFileName
	PluginsDirName = coreplugin.PluginsDirName
	FragmentGlob   = coreplugin.FragmentGlob
)

// The manifest, the pin, and what the approval is bound to.
type (
	Manifest       = coreplugin.Manifest
	Plugin         = coreplugin.Plugin
	Build          = coreplugin.Build
	Panel          = coreplugin.Panel
	Tool           = coreplugin.Tool
	View           = coreplugin.View
	Key            = coreplugin.Key
	SettingOptions = coreplugin.SettingOptions
	Notebook       = coreplugin.Notebook
	Digest         = coreplugin.Digest

	Lock = coreplugin.Lock
	Pin  = coreplugin.Pin

	ConsentFacts = coreplugin.ConsentFacts
	FactChange   = coreplugin.FactChange
	Status       = coreplugin.Status
)

// Manifest reading and validation.
func LoadManifest(repoDir string) (*Manifest, []byte, error) { return coreplugin.LoadManifest(repoDir) }
func ParseManifest(data []byte) (*Manifest, error)           { return coreplugin.ParseManifest(data) }

// Consent facts: the lines an approval is rendered and hashed from.
func FlattenSettings(settings map[string]any) []string { return coreplugin.FlattenSettings(settings) }
func ViewFacts(p *Panel) []string                      { return coreplugin.ViewFacts(p) }
func KeyFacts(p *Panel) []string                       { return coreplugin.KeyFacts(p) }
func SettingOptionFacts(p *Panel) []string             { return coreplugin.SettingOptionFacts(p) }
func ToolFacts(t *Tool) []string                       { return coreplugin.ToolFacts(t) }
func Diff(old, next ConsentFacts) []FactChange         { return coreplugin.Diff(old, next) }

// Install-time trust, recorded in the same store `grove config trust` writes.
func RecordApproval(fragmentPath, digest string, now time.Time) error {
	return coreplugin.RecordApproval(fragmentPath, digest, now)
}
func RevokeApproval(fragmentPath string) error    { return coreplugin.RevokeApproval(fragmentPath) }
func IsApproved(fragmentPath, digest string) bool { return coreplugin.IsApproved(fragmentPath, digest) }

// The lockfile.
func LoadLock() (*Lock, error) { return coreplugin.LoadLock() }

// Where everything lives.
func ConfigPluginsDir() (string, error)          { return coreplugin.ConfigPluginsDir() }
func FragmentPath(name string) (string, error)   { return coreplugin.FragmentPath(name) }
func LockPath() (string, error)                  { return coreplugin.LockPath() }
func SourceDir(slug string) (string, error)      { return coreplugin.SourceDir(slug) }
func BinPath(binary string) (string, error)      { return coreplugin.BinPath(binary) }
func PluginVersionsDir(n string) (string, error) { return coreplugin.PluginVersionsDir(n) }
func VersionDir(name, commit string) (string, error) {
	return coreplugin.VersionDir(name, commit)
}

// List reports every installed plugin and whether it is intact.
func List() ([]Status, error) { return coreplugin.List() }
