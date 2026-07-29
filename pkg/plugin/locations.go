package plugin

import (
	"fmt"
	"path/filepath"

	"github.com/grovetools/core/pkg/paths"
)

// The installer writes into three places, all of them grove-managed and all of
// them derived from core/pkg/paths rather than invented here:
//
//	ConfigDir()/plugins/<name>.toml         the manifest fragment core/config globs
//	ConfigDir()/plugins/plugins.lock.json   the pin
//	DataDir()/plugins/src/<slug>            the checkout
//	DataDir()/plugins/versions/<name>/<commit>/bin/<binary>
//	BinDir()/<binary>                       symlink to the pinned build
//
// The lockfile is JSON, and that is not a style choice: core/config globs
// ConfigDir()/plugins/*.toml and merges every match as config. A lockfile
// ending in .toml in that directory would be parsed as a grove config file.

// LockFileName is the lockfile's basename. See the note above about why it is
// not a .toml.
const LockFileName = "plugins.lock.json"

// ConfigPluginsDir is the drop-in directory core/config globs for plugin
// manifest fragments (config.go's ~/.config/grove/plugins/*.toml pass).
func ConfigPluginsDir() (string, error) {
	dir := paths.ConfigDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve the grove config directory (set GROVE_HOME or XDG_CONFIG_HOME)")
	}
	return filepath.Join(dir, "plugins"), nil
}

// FragmentPath is where an installed plugin's [tui.plugins.<name>] fragment
// lives.
func FragmentPath(name string) (string, error) {
	dir, err := ConfigPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".toml"), nil
}

// LockPath is the lockfile location.
func LockPath() (string, error) {
	dir, err := ConfigPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, LockFileName), nil
}

// dataPluginsDir is the root of the installer's managed state: checkouts and
// per-commit binaries.
func dataPluginsDir() (string, error) {
	dir := paths.DataDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve the grove data directory (set GROVE_HOME or XDG_DATA_HOME)")
	}
	return filepath.Join(dir, "plugins"), nil
}

// SourceDir is the persistent checkout for a source. It is keyed by the
// source slug rather than the plugin name because the name comes out of the
// manifest, which is only readable once the checkout exists.
func SourceDir(slug string) (string, error) {
	dir, err := dataPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "src", slug), nil
}

// VersionDir holds the binary built from one exact commit. This mirrors
// grove/pkg/sdk's versions/<tag>/bin/<name> layout so an installed plugin is
// laid out like every other grove-managed binary.
func VersionDir(name, commit string) (string, error) {
	dir, err := dataPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "versions", name, commit), nil
}

// PluginVersionsDir holds every version ever built for one plugin. Removing a
// plugin removes this whole tree.
func PluginVersionsDir(name string) (string, error) {
	dir, err := dataPluginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "versions", name), nil
}

// BinPath is the grove-managed bin dir entry for a plugin binary — the same
// directory `grove <tool>` delegation puts tool symlinks in, so an installed
// panel is runnable by name from the user's shell.
func BinPath(binary string) (string, error) {
	dir := paths.BinDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve the grove bin directory (set GROVE_HOME, GROVE_BIN or XDG_DATA_HOME)")
	}
	return filepath.Join(dir, binary), nil
}
