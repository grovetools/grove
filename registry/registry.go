package registry

import (
    "encoding/json"
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

var loadedRegistry *Registry

func LoadRegistry(path string) (*Registry, error) {
    if loadedRegistry != nil {
        return loadedRegistry, nil
    }

    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    var reg Registry
    if err := json.Unmarshal(data, &reg); err != nil {
        return nil, err
    }
    
    loadedRegistry = &reg
    return loadedRegistry, nil
}

// LoadRegistryWithOverlay loads the main registry and applies local overrides
func LoadRegistryWithOverlay(mainPath, overlayPath string) (*Registry, error) {
    // Load main registry
    mainReg, err := LoadRegistry(mainPath)
    if err != nil {
        return nil, err
    }

    // Check if overlay exists
    if _, err := os.Stat(overlayPath); os.IsNotExist(err) {
        return mainReg, nil
    }

    // Load overlay registry
    overlayData, err := os.ReadFile(overlayPath)
    if err != nil {
        return mainReg, nil // Return main registry if overlay can't be read
    }

    var overlayReg Registry
    if err := json.Unmarshal(overlayData, &overlayReg); err != nil {
        return mainReg, nil // Return main registry if overlay is invalid
    }

    // Merge registries - overlay takes precedence
    mergedTools := make(map[string]Tool)
    
    // Add all tools from main registry
    for _, tool := range mainReg.Tools {
        mergedTools[tool.Name] = tool
    }
    
    // Override with tools from overlay registry
    for _, tool := range overlayReg.Tools {
        mergedTools[tool.Name] = tool
    }
    
    // Convert back to slice
    tools := make([]Tool, 0, len(mergedTools))
    for _, tool := range mergedTools {
        tools = append(tools, tool)
    }
    
    mainReg.Tools = tools
    return mainReg, nil
}

// IsLocalRepository checks if a repository path is local
func IsLocalRepository(repo string) bool {
    return strings.HasPrefix(repo, "./") || 
           strings.HasPrefix(repo, "../") || 
           strings.HasPrefix(repo, "/") ||
           strings.Contains(repo, string(filepath.Separator))
}