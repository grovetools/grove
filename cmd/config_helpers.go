package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
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

var recordedRootHeader = []string{
	"# This machine's recorded code roots.",
	"# Specific roots are ecosystems; scan roots discover repositories beneath a directory.",
	"",
}

// registerCodeRoot records a specific ecosystem root in roots.toml.
// If the caller supplies a notebook binding that only exists in the compiled
// legacy view, its definition is recorded first so the roots/notebooks pair
// remains independently loadable.
func registerCodeRoot(groveName, grovePath, notebook string) (string, error) {
	root := coderoot.Root{Path: grovePath, Notebook: notebook}
	if err := ensureRecordedRouting(&root); err != nil {
		return "", err
	}
	path := coderoot.RootsPath()
	if path == "" {
		return "", fmt.Errorf("cannot resolve the grove config directory")
	}
	_, err := config.WriteCodeRoots(path, config.CodeRootEdits{
		Upserts: map[string]coderoot.Root{groveName: root},
		Header:  recordedRootHeader,
	})
	if err != nil {
		return "", err
	}
	config.ResetLoadCache()
	return path, nil
}

// updateGlobalConfig keeps its historical call-site name but now writes the
// machine-local recorded roots file.
func updateGlobalConfig(groveName, grovePath, notebook string) (string, error) {
	return registerCodeRoot(groveName, grovePath, notebook)
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
