package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/config"
	"gopkg.in/yaml.v3"
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

// findGrovesConfigFile finds the config file where groves are defined.
// It checks layers in order: global override, global, then falls back to global override for new entries.
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

// updateGlobalConfig adds a new grove to the appropriate config file.
// It finds where groves are already defined and edits that file,
// preserving all existing content.
func updateGlobalConfig(groveName, grovePath, notebook string) (string, error) {
	targetPath, err := findGrovesConfigFile()
	if err != nil {
		return "", err
	}

	// Ensure the config directory exists
	configDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	// Load existing config as a generic map to preserve all fields
	var doc map[string]interface{}
	if data, err := os.ReadFile(targetPath); err == nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return "", fmt.Errorf("failed to parse existing config: %w", err)
		}
	}

	// Initialize doc if nil
	if doc == nil {
		doc = make(map[string]interface{})
	}

	// Get or create the groves section
	var groves map[string]interface{}
	if g, ok := doc["groves"]; ok {
		if gMap, ok := g.(map[string]interface{}); ok {
			groves = gMap
		} else {
			groves = make(map[string]interface{})
		}
	} else {
		groves = make(map[string]interface{})
	}

	// Create the grove entry
	groveEntry := map[string]interface{}{
		"path":    grovePath,
		"enabled": true,
	}
	if notebook != "" {
		groveEntry["notebook"] = notebook
	}

	// Add/update the specific grove
	groves[groveName] = groveEntry
	doc["groves"] = groves

	// Marshal and write back - this preserves all other fields
	data, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write config: %w", err)
	}

	return targetPath, nil
}

// isHomeDirectory checks if the given path is the user's home directory.
func isHomeDirectory(path string) bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	return filepath.Clean(absPath) == filepath.Clean(homeDir)
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
