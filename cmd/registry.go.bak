package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Registry struct {
	Version string `json:"version"`
	Tools   []Tool `json:"tools"`
}

type Tool struct {
	Name        string `json:"name"`
	Alias       string `json:"alias"`
	Repository  string `json:"repository"`
	Binary      string `json:"binary"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// LoadRegistry loads the tool registry with optional overlay
func LoadRegistry() (*Registry, error) {
	registryPath := findRegistryPath()
	overlayPath := findOverlayPath(registryPath)
	
	// Load main registry
	mainReg, err := loadRegistryFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load main registry: %w", err)
	}
	
	// Try to load overlay
	if overlayReg, err := loadRegistryFile(overlayPath); err == nil {
		// Merge overlays
		mainReg = mergeRegistries(mainReg, overlayReg)
	}
	
	return mainReg, nil
}

func loadRegistryFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	
	return &reg, nil
}

func mergeRegistries(main, overlay *Registry) *Registry {
	// Create a map of main tools for easy lookup
	toolMap := make(map[string]Tool)
	for _, tool := range main.Tools {
		toolMap[tool.Name] = tool
	}
	
	// Override with overlay tools
	for _, tool := range overlay.Tools {
		toolMap[tool.Name] = tool
	}
	
	// Convert back to slice
	var tools []Tool
	for _, tool := range toolMap {
		tools = append(tools, tool)
	}
	
	return &Registry{
		Version: main.Version,
		Tools:   tools,
	}
}

func findRegistryPath() string {
	// Check in order of preference
	paths := []string{
		"registry.json",
		filepath.Join(os.Getenv("HOME"), ".grove", "registry.json"),
		"/usr/local/share/grove/registry.json",
	}
	
	// First check if we're running from the grove/bin directory
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		
		// If running from bin/, look in parent directory (grove/)
		if filepath.Base(execDir) == "bin" {
			parentDir := filepath.Dir(execDir)
			paths = append([]string{
				filepath.Join(parentDir, "registry.json"),
			}, paths...)
		}
		
		// Also check next to the executable
		paths = append([]string{filepath.Join(execDir, "registry.json")}, paths...)
	}
	
	// Check GROVE_HOME environment variable
	if groveHome := os.Getenv("GROVE_HOME"); groveHome != "" {
		paths = append([]string{
			filepath.Join(groveHome, "grove", "registry.json"),
			filepath.Join(groveHome, "registry.json"),
		}, paths...)
	}
	
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	
	// Default to local registry.json
	return "registry.json"
}

func findOverlayPath(mainPath string) string {
	dir := filepath.Dir(mainPath)
	base := filepath.Base(mainPath)
	
	// For registry.json, look for registry.local.json
	if base == "registry.json" {
		return filepath.Join(dir, "registry.local.json")
	}
	
	// For other names, insert .local before extension
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	return filepath.Join(dir, name+".local"+ext)
}

func IsLocalRepository(repo string) bool {
	// Check if the repository path starts with common local path indicators
	return strings.HasPrefix(repo, "./") || 
		strings.HasPrefix(repo, "../") || 
		strings.HasPrefix(repo, "/") || 
		strings.HasPrefix(repo, "~/")
}