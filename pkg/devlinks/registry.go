// Package devlinks manages local development binary links for Grove tools
package devlinks

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the top-level structure for the devlinks registry file
type Config struct {
	// Binaries maps a binary name (e.g., "grove", "flow") to its link information
	Binaries map[string]*BinaryLinks `json:"binaries"`
}

// BinaryLinks holds all local development links and the current active link for a single binary
type BinaryLinks struct {
	// Links maps an alias (e.g., "main", "feature-x") to its link details
	Links   map[string]LinkInfo `json:"links"`
	Current string              `json:"current"` // The alias of the active link
}

// LinkInfo contains the details for a specific registered local development binary
type LinkInfo struct {
	Path         string `json:"path"`          // Absolute path to the binary
	WorktreePath string `json:"worktree_path"` // Absolute path to the root of the worktree
	RegisteredAt string `json:"registered_at"`
}

// GetGroveHome returns the path to the .grove directory in the user's home
func GetGroveHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grove"), nil
}

// LoadConfig loads the devlinks configuration from the registry file
func LoadConfig() (*Config, error) {
	groveHome, err := GetGroveHome()
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(groveHome, "devlinks.json")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{Binaries: make(map[string]*BinaryLinks)}, nil
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
		config.Binaries = make(map[string]*BinaryLinks)
	}
	return &config, nil
}

// SaveConfig saves the devlinks configuration to the registry file
func SaveConfig(config *Config) error {
	groveHome, err := GetGroveHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(groveHome, 0755); err != nil {
		return err
	}
	configPath := filepath.Join(groveHome, "devlinks.json")

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// ClearAllCurrentLinks resets all active development links
// This is called when switching to a released version
func ClearAllCurrentLinks() error {
	config, err := LoadConfig()
	if err != nil {
		return err
	}

	for _, binary := range config.Binaries {
		binary.Current = ""
	}

	return SaveConfig(config)
}
