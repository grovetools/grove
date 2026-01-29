package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/pkg/paths"
)

// ToolVersions tracks the active version for each tool independently
type ToolVersions struct {
	Versions map[string]string `json:"versions"` // tool -> version mapping
}

// LoadToolVersions loads the active versions for all tools
func LoadToolVersions() (*ToolVersions, error) {
	versionsFile := filepath.Join(paths.StateDir(), "active_versions.json")

	// If file doesn't exist, return empty versions
	if _, err := os.Stat(versionsFile); os.IsNotExist(err) {
		return &ToolVersions{
			Versions: make(map[string]string),
		}, nil
	}

	data, err := os.ReadFile(versionsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read versions file: %w", err)
	}

	var tv ToolVersions
	if err := json.Unmarshal(data, &tv); err != nil {
		return nil, fmt.Errorf("failed to parse versions file: %w", err)
	}

	if tv.Versions == nil {
		tv.Versions = make(map[string]string)
	}

	return &tv, nil
}

// Save saves the tool versions to disk
func (tv *ToolVersions) Save() error {
	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	versionsFile := filepath.Join(stateDir, "active_versions.json")

	data, err := json.MarshalIndent(tv, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal versions: %w", err)
	}

	return os.WriteFile(versionsFile, data, 0644)
}

// GetToolVersion returns the active version for a specific tool
func (tv *ToolVersions) GetToolVersion(tool string) string {
	return tv.Versions[tool]
}

// SetToolVersion sets the active version for a specific tool
func (tv *ToolVersions) SetToolVersion(tool, version string) {
	tv.Versions[tool] = version
}

// MigrateFromSingleVersion migrates from old single version format
func MigrateFromSingleVersion() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err // Cannot find home, cannot determine old path
	}
	oldGroveHome := filepath.Join(homeDir, ".grove")
	oldFile := filepath.Join(oldGroveHome, "active_version")
	newFile := filepath.Join(paths.StateDir(), "active_versions.json")

	// Check if old file exists and new file doesn't
	if _, err := os.Stat(oldFile); err == nil {
		if _, err := os.Stat(newFile); os.IsNotExist(err) {
			// Read old version
			data, err := os.ReadFile(oldFile)
			if err != nil {
				return err
			}

			version := string(data)

			// Check which tools exist in this version, reading from old location
			versionDir := filepath.Join(oldGroveHome, "versions", version, "bin")
			entries, err := os.ReadDir(versionDir)
			if err != nil {
				// If we can't read the old dir, we can't migrate, but don't fail hard
				return nil
			}

			// Create new format with tools that exist
			tv := &ToolVersions{
				Versions: make(map[string]string),
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					toolName := entry.Name()
					// Use FindTool to map binary name back to canonical repo name
					repoName, _, _, found := FindTool(toolName)
					if found {
						tv.SetToolVersion(repoName, version)
					} else {
						tv.SetToolVersion(toolName, version) // Fallback for old/unknown tools
					}
				}
			}

			// Save new format
			if err := tv.Save(); err != nil {
				return err
			}

			// Remove old file
			os.Remove(oldFile)
		}
	}

	return nil
}
