package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ToolVersions tracks the active version for each tool independently
type ToolVersions struct {
	Versions map[string]string `json:"versions"` // tool -> version mapping
}

// LoadToolVersions loads the active versions for all tools
func LoadToolVersions(groveHome string) (*ToolVersions, error) {
	versionsFile := filepath.Join(groveHome, "active_versions.json")
	
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
func (tv *ToolVersions) Save(groveHome string) error {
	versionsFile := filepath.Join(groveHome, "active_versions.json")
	
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
func MigrateFromSingleVersion(groveHome string) error {
	oldFile := filepath.Join(groveHome, "active_version")
	newFile := filepath.Join(groveHome, "active_versions.json")
	
	// Check if old file exists and new file doesn't
	if _, err := os.Stat(oldFile); err == nil {
		if _, err := os.Stat(newFile); os.IsNotExist(err) {
			// Read old version
			data, err := os.ReadFile(oldFile)
			if err != nil {
				return err
			}
			
			version := string(data)
			
			// Check which tools exist in this version
			versionDir := filepath.Join(groveHome, "versions", version, "bin")
			entries, err := os.ReadDir(versionDir)
			if err != nil {
				return err
			}
			
			// Create new format with tools that exist
			tv := &ToolVersions{
				Versions: make(map[string]string),
			}
			
			for _, entry := range entries {
				if !entry.IsDir() {
					toolName := entry.Name()
					tv.SetToolVersion(toolName, version)
				}
			}
			
			// Save new format
			if err := tv.Save(groveHome); err != nil {
				return err
			}
			
			// Remove old file
			os.Remove(oldFile)
		}
	}
	
	return nil
}