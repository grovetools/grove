// Package overrides manages workspace-specific binary path overrides
package overrides

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds workspace-specific binary overrides
// This allows a workspace to point to binaries from other workspaces
type Config struct {
	// Binaries maps binary name to its override path
	// Example: {"flow": "/path/to/grove-flow/.grove-worktrees/feature/bin/flow"}
	Binaries map[string]string `json:"binaries"`
}

// GetConfigPath returns the path to the overrides config file for a workspace
func GetConfigPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".grove", "overrides.json")
}

// LoadConfig loads the overrides configuration for a workspace
func LoadConfig(workspaceRoot string) (*Config, error) {
	configPath := GetConfigPath(workspaceRoot)

	// If file doesn't exist, return empty config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{Binaries: make(map[string]string)}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	if config.Binaries == nil {
		config.Binaries = make(map[string]string)
	}

	return &config, nil
}

// SaveConfig saves the overrides configuration for a workspace
func SaveConfig(workspaceRoot string, config *Config) error {
	configPath := GetConfigPath(workspaceRoot)

	// Ensure .grove directory exists
	groveDir := filepath.Dir(configPath)
	if err := os.MkdirAll(groveDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// GetBinaryOverride returns the override path for a binary in a workspace
// Returns empty string if no override is configured
func GetBinaryOverride(workspaceRoot, binaryName string) string {
	config, err := LoadConfig(workspaceRoot)
	if err != nil {
		return ""
	}
	return config.Binaries[binaryName]
}

// SetBinaryOverride sets an override path for a binary in a workspace
func SetBinaryOverride(workspaceRoot, binaryName, binaryPath string) error {
	config, err := LoadConfig(workspaceRoot)
	if err != nil {
		return err
	}

	config.Binaries[binaryName] = binaryPath
	return SaveConfig(workspaceRoot, config)
}

// RemoveBinaryOverride removes an override for a binary in a workspace
func RemoveBinaryOverride(workspaceRoot, binaryName string) error {
	config, err := LoadConfig(workspaceRoot)
	if err != nil {
		return err
	}

	delete(config.Binaries, binaryName)
	return SaveConfig(workspaceRoot, config)
}

// ListOverrides returns all configured overrides for a workspace
func ListOverrides(workspaceRoot string) (map[string]string, error) {
	config, err := LoadConfig(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return config.Binaries, nil
}
