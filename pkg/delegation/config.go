// Package delegation manages the binary delegation mode configuration
package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Mode represents the binary delegation strategy
type Mode string

const (
	// ModeGlobal uses globally configured binaries (default)
	ModeGlobal Mode = "global"
	// ModeWorkspace prioritizes workspace-local binaries
	ModeWorkspace Mode = "workspace"
)

// Config holds the delegation configuration
type Config struct {
	Mode Mode `json:"mode"`
}

// GetConfigPath returns the path to the delegation config file
func GetConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grove", "delegation.json"), nil
}

// LoadConfig loads the delegation configuration
func LoadConfig() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	// If file doesn't exist, return default (global mode)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{Mode: ModeGlobal}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Validate mode
	if config.Mode != ModeGlobal && config.Mode != ModeWorkspace {
		config.Mode = ModeGlobal
	}

	return &config, nil
}

// SaveConfig saves the delegation configuration
func SaveConfig(config *Config) error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

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

// GetMode returns the current delegation mode
func GetMode() Mode {
	config, err := LoadConfig()
	if err != nil {
		// On error, default to global mode
		return ModeGlobal
	}
	return config.Mode
}

// SetMode sets the delegation mode
func SetMode(mode Mode) error {
	config := &Config{Mode: mode}
	return SaveConfig(config)
}
