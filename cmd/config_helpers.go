package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
)

// getGlobalOverridePath returns the path to the global override config file.
// It respects XDG_CONFIG_HOME if set.
func getGlobalOverridePath() (string, error) {
	var configDir string
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		configDir = filepath.Join(xdgConfig, "grove")
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		configDir = filepath.Join(homeDir, ".config", "grove")
	}
	return filepath.Join(configDir, "grove.override.yml"), nil
}

// isEcosystemDiscoverable checks if an ecosystem at the given path will be
// discovered by grove workspace commands (via the configured groves).
// Returns true and the covering grove name if discoverable, false and empty string otherwise.
func isEcosystemDiscoverable(ecosystemPath string, cfg *config.Config) (bool, string) {
	// Get absolute path and resolve any symlinks (important on macOS where /var -> /private/var)
	absPath, err := filepath.Abs(ecosystemPath)
	if err != nil {
		return false, ""
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		realPath = absPath // Fall back to absPath if symlink resolution fails
	}

	// Check if the path falls under any configured grove
	for name, grove := range cfg.Groves {
		if grove.Enabled != nil && !*grove.Enabled {
			continue
		}

		// Expand the grove path
		grovePath := expandPath(grove.Path)
		absGrovePath, err := filepath.Abs(grovePath)
		if err != nil {
			continue
		}
		// Also resolve symlinks for the grove path
		realGrovePath, err := filepath.EvalSymlinks(absGrovePath)
		if err != nil {
			realGrovePath = absGrovePath
		}

		// Check if ecosystem is under this grove path
		if isPathUnder(realPath, realGrovePath) {
			return true, name
		}
	}

	return false, ""
}

// isPathUnder checks if childPath is under parentPath.
func isPathUnder(childPath, parentPath string) bool {
	// Normalize paths
	childPath = filepath.Clean(childPath)
	parentPath = filepath.Clean(parentPath)

	// Ensure parent path ends with separator for proper prefix matching
	if !strings.HasSuffix(parentPath, string(filepath.Separator)) {
		parentPath += string(filepath.Separator)
	}

	// Check if child is directly the parent or under it
	rel, err := filepath.Rel(parentPath, childPath)
	if err != nil {
		return false
	}

	// If the relative path starts with "..", the child is not under parent
	return !strings.HasPrefix(rel, "..")
}

// expandPath expands ~ to the home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// findGrovesConfigFile finds the config file where LEGACY [groves.*] entries
// are defined. New registrations no longer go here — they are machine.toml
// subscriptions (see registerMachineEcosystem) — but an existing entry still
// wins over a compiled subscription, so callers use this to tell the user
// which file is actually in charge.
func findGrovesConfigFile() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	layered, err := config.LoadLayered(cwd)
	if err != nil {
		// If we can't load layered config, fall back to global override
		return getGlobalOverridePath()
	}

	// Check where groves is defined, in priority order for editing
	// We prefer to edit the global override if it has groves, then global

	// 1. Check global override first (preferred for user additions)
	if layered.GlobalOverride != nil && layered.GlobalOverride.Config != nil {
		if len(layered.GlobalOverride.Config.Groves) > 0 {
			return layered.GlobalOverride.Path, nil
		}
	}

	// 2. Check global config
	if layered.Global != nil && len(layered.Global.Groves) > 0 {
		if path, ok := layered.FilePaths[config.SourceGlobal]; ok {
			return path, nil
		}
	}

	// 3. If groves isn't defined anywhere, use global override (creates it if needed)
	return getGlobalOverridePath()
}

// machineSubscriptionHeader is the comment block written above the first
// subscription in a machine.toml this tooling creates.
var machineSubscriptionHeader = []string{
	"# This machine's intent: name, ecosystem subscriptions, bare scan roots.",
	"# Dotfiles-portable on purpose — a restored copy plus a freshly minted",
	"# machine id is a NEW machine with the SAME intent.",
	"",
}

// registerMachineEcosystem records a directory as one of this machine's
// ecosystem subscriptions in ~/.config/grove/machine.toml, and returns the
// file written.
//
// This replaces writing a [groves.<name>] entry into the global config. The
// subscription compiles into the same cfg.Groves map every discovery consumer
// already reads, so nothing downstream changes — but the declaration now lives
// where "which ecosystems does this machine want" is answerable, which is what
// makes declared-but-missing and materialize possible.
//
// Old configs keep working untouched: compilation fills only absent keys, so
// an existing [groves.<name>] still wins.
func registerMachineEcosystem(groveName, grovePath, notebook string) (string, error) {
	cfgPath := config.MachineConfigPath()
	if cfgPath == "" {
		return "", fmt.Errorf("cannot resolve the grove config directory")
	}
	if _, err := config.WriteMachineSubscriptions(cfgPath, config.MachineSubscriptions{
		Ecosystems: map[string]config.MachineEcosystem{
			groveName: {Path: grovePath, Notebook: notebook},
		},
		Header: machineSubscriptionHeader,
	}); err != nil {
		return "", err
	}
	return cfgPath, nil
}

// updateGlobalConfig registers an ecosystem for discovery. Kept under its
// historical name because several call sites read as "put this on the map";
// the map is now machine.toml.
func updateGlobalConfig(groveName, grovePath, notebook string) (string, error) {
	return registerMachineEcosystem(groveName, grovePath, notebook)
}

// legacyGrovesOwner reports the config file that already declares
// [groves.<name>], or "" when none does. A legacy entry outranks the
// subscription just written, so surfaces that register an ecosystem say so
// rather than letting the user wonder why their new path is ignored.
func legacyGrovesOwner(groveName string) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return ""
	}
	declares := func(cfg *config.Config) bool {
		if cfg == nil {
			return false
		}
		_, ok := cfg.Groves[groveName]
		return ok
	}
	if declares(layered.Global) {
		return layered.FilePaths[config.SourceGlobal]
	}
	for _, frag := range layered.GlobalFragments {
		if declares(frag.Config) {
			return frag.Path
		}
	}
	if layered.GlobalOverride != nil && declares(layered.GlobalOverride.Config) {
		return layered.GlobalOverride.Path
	}
	return ""
}

// deriveGroveName derives a grove name from a path.
// It uses the base name of the path and checks for conflicts.
func deriveGroveName(path string, existingGroves map[string]config.GroveSourceConfig) (string, error) {
	baseName := filepath.Base(path)
	if baseName == "" || baseName == "." || baseName == "/" {
		return "", fmt.Errorf("cannot derive grove name from path: %s", path)
	}

	// Check for conflicts
	if _, exists := existingGroves[baseName]; exists {
		return "", fmt.Errorf("grove name '%s' already exists", baseName)
	}

	return baseName, nil
}

// getNotebookKeys returns a list of available notebook keys from the config.
func getNotebookKeys(cfg *config.Config) []string {
	var keys []string
	if cfg.Notebooks != nil && cfg.Notebooks.Definitions != nil {
		for key := range cfg.Notebooks.Definitions {
			keys = append(keys, key)
		}
	}
	return keys
}
