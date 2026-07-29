package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// lockVersion is the on-disk schema version of the lockfile. A lockfile
// written by a newer grove is refused rather than rewritten, because
// rewriting it would silently drop pins this binary cannot represent.
const lockVersion = 1

// Lock is the pin: exactly which commit of which source is installed for each
// plugin. Nothing in the pipeline floats — `install` resolves a ref to a
// commit once and records it here, `update` is the only thing that moves it,
// and every rebuild uses the recorded commit.
type Lock struct {
	Version int             `json:"version"`
	Plugins map[string]*Pin `json:"plugins"`

	path string
}

// Pin is one installed plugin.
type Pin struct {
	// Spec is what the user typed, e.g. "github.com/u/grove-panel-foo@v1.2.0".
	Spec string `json:"spec"`
	// URL is the clone URL Spec resolved to.
	URL string `json:"url"`
	// Ref is the requested ref: a tag, a branch, a commit, or "" for the
	// remote's default branch. `update` re-resolves THIS, which is what makes
	// moving the pin an explicit act.
	Ref string `json:"ref,omitempty"`
	// Commit is the exact commit installed. This is the pin.
	Commit string `json:"commit"`
	// ManifestDigest is a hash of the grove-plugin.toml bytes at Commit.
	ManifestDigest string `json:"manifest_digest"`
	// ConsentDigest is the digest recorded in the exec-trust store when the
	// user approved this install (see ConsentFacts.Digest).
	ConsentDigest string `json:"consent_digest"`
	// Consent is what the user was shown, kept so `update` can diff the new
	// manifest against the approved one without a second checkout.
	Consent ConsentFacts `json:"consent"`

	SourceDir     string `json:"source_dir"`
	VersionBinary string `json:"version_binary"`
	Binary        string `json:"binary"`
	Fragment      string `json:"fragment"`
	InstalledAt   string `json:"installed_at"`
}

// LoadLock reads the lockfile. A missing lockfile is an empty lock, not an
// error: nothing installed is a legitimate state.
func LoadLock() (*Lock, error) {
	path, err := LockPath()
	if err != nil {
		return nil, err
	}
	l := &Lock{Version: lockVersion, Plugins: map[string]*Pin{}, path: path}
	data, err := os.ReadFile(path) //nolint:gosec // G304: grove's own config dir
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var onDisk Lock
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %w", path, err)
	}
	if onDisk.Version > lockVersion {
		return nil, fmt.Errorf("%s was written by a newer grove (lockfile version %d, this grove understands %d)", path, onDisk.Version, lockVersion)
	}
	if onDisk.Plugins != nil {
		l.Plugins = onDisk.Plugins
	}
	return l, nil
}

// Get returns the pin for name, or nil.
func (l *Lock) Get(name string) *Pin {
	if l == nil {
		return nil
	}
	return l.Plugins[name]
}

// Names returns every installed plugin name, sorted.
func (l *Lock) Names() []string {
	names := make([]string, 0, len(l.Plugins))
	for n := range l.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Set records a pin, stamping it as installed at now.
func (l *Lock) Set(name string, pin *Pin, now time.Time) {
	if l.Plugins == nil {
		l.Plugins = map[string]*Pin{}
	}
	pin.InstalledAt = now.UTC().Format(time.RFC3339)
	l.Plugins[name] = pin
}

// Remove drops a pin. Reports whether anything was removed.
func (l *Lock) Remove(name string) bool {
	if _, ok := l.Plugins[name]; !ok {
		return false
	}
	delete(l.Plugins, name)
	return true
}

// UsesSourceDir reports whether any plugin OTHER than exclude checks out into
// dir. One repository can ship one plugin today, but the checkout is shared
// state and removing it out from under another pin would be a bug waiting for
// the day it does not.
func (l *Lock) UsesSourceDir(dir, exclude string) bool {
	for name, pin := range l.Plugins {
		if name != exclude && pin.SourceDir == dir {
			return true
		}
	}
	return false
}

// Save writes the lockfile atomically.
func (l *Lock) Save() error {
	if l.path == "" {
		path, err := LockPath()
		if err != nil {
			return err
		}
		l.path = path
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(l.path), err)
	}
	l.Version = lockVersion
	if l.Plugins == nil {
		l.Plugins = map[string]*Pin{}
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encode lockfile: %w", err)
	}
	data = append(data, '\n')
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, l.path, err)
	}
	return nil
}
