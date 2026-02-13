package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// TOMLHandler provides utilities for writing TOML configuration files.
type TOMLHandler struct {
	service *Service
}

// NewTOMLHandler creates a new TOML handler
func NewTOMLHandler(service *Service) *TOMLHandler {
	return &TOMLHandler{service: service}
}

// SaveGlobalConfig saves a configuration map to the global grove configuration file.
// Uses grove.toml format.
func (h *TOMLHandler) SaveGlobalConfig(config map[string]interface{}) error {
	configPath := GlobalTOMLConfigPath()
	return h.SaveTOML(configPath, config)
}

// SaveTOML saves a configuration map to a TOML file, respecting dry-run mode.
func (h *TOMLHandler) SaveTOML(path string, config map[string]interface{}) error {
	expandedPath := expandPath(path)

	data, err := toml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal TOML: %w", err)
	}

	displayPath := AbbreviatePath(expandedPath)

	// Use the service to write the file (respects dry-run)
	if h.service.IsDryRun() {
		h.service.logger.Infof("[dry-run] Would write TOML to %s", displayPath)
		h.service.logAction(ActionUpdateYAML, fmt.Sprintf("Write %s", displayPath), expandedPath, true, nil)
		return nil
	}

	// Ensure parent directory exists
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(expandedPath, data, 0644); err != nil {
		h.service.logAction(ActionUpdateYAML, fmt.Sprintf("Write %s", displayPath), expandedPath, false, err)
		return fmt.Errorf("failed to write TOML file %s: %w", path, err)
	}

	h.service.logger.Infof("Wrote %s", displayPath)
	h.service.logAction(ActionUpdateYAML, fmt.Sprintf("Write %s", displayPath), expandedPath, true, nil)
	return nil
}

// LoadTOML loads a TOML file into a map[string]interface{}.
// If the file doesn't exist, returns an empty map.
func (h *TOMLHandler) LoadTOML(path string) (map[string]interface{}, error) {
	expandedPath := expandPath(path)

	data, err := os.ReadFile(expandedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("failed to read TOML file %s: %w", path, err)
	}

	result := make(map[string]interface{})
	if err := toml.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse TOML file %s: %w", path, err)
	}

	return result, nil
}

// GlobalTOMLConfigPath returns the path to the global grove TOML configuration file.
func GlobalTOMLConfigPath() string {
	configDir := configDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "grove.toml")
}

// configDir returns the grove config directory path
func configDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".config", "grove")
}
