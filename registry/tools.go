package registry

import (
    "log"
    "os"
    "path/filepath"
)

// MustLoad ensures registry is loaded, panicking on failure as it's a startup requirement.
func MustLoad() *Registry {
    if loadedRegistry == nil {
        // Look for registry.json in several locations
        registryPath := findRegistryPath()
        overlayPath := findOverlayPath(registryPath)
        
        reg, err := LoadRegistryWithOverlay(registryPath, overlayPath)
        if err != nil {
            log.Fatalf("Failed to load tool registry: %v", err)
        }
        
        loadedRegistry = reg
        return reg
    }
    return loadedRegistry
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

func FindToolByAlias(alias string) *Tool {
    reg := MustLoad()
    for _, tool := range reg.Tools {
        if tool.Alias == alias {
            t := tool // Create a new variable to take the address of
            return &t
        }
    }
    return nil
}

func FindToolByName(name string) *Tool {
    reg := MustLoad()
    for _, tool := range reg.Tools {
        if tool.Name == name {
            t := tool // Create a new variable to take the address of
            return &t
        }
    }
    return nil
}

func FindTool(nameOrAlias string) *Tool {
    if tool := FindToolByAlias(nameOrAlias); tool != nil {
        return tool
    }
    return FindToolByName(nameOrAlias)
}

func IsKnownTool(command string) bool {
    return FindTool(command) != nil
}

func GetAllTools() []Tool {
    reg := MustLoad()
    return reg.Tools
}

// findOverlayPath returns the path to the overlay registry based on the main registry path
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