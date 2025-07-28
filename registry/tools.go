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
        reg, err := LoadRegistry(registryPath)
        if err != nil {
            log.Fatalf("Failed to load tool registry: %v", err)
        }
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
    
    // First check if we're running from the grove directory
    if execPath, err := os.Executable(); err == nil {
        execDir := filepath.Dir(execPath)
        paths = append([]string{filepath.Join(execDir, "registry.json")}, paths...)
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