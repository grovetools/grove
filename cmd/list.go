package cmd

import (
    "fmt"
    "github.com/yourorg/grove/registry"
)

func HandleList() {
    tools := registry.GetAllTools()
    
    fmt.Println("Available Grove Tools:")
    fmt.Printf("%-15s %-8s %-8s %-10s %s\n", "NAME", "ALIAS", "BINARY", "SOURCE", "DESCRIPTION")
    fmt.Println("--------------------------------------------------------------------------------")
    
    for _, tool := range tools {
        source := "remote"
        if registry.IsLocalRepository(tool.Repository) {
            source = "local"
        }
        fmt.Printf("%-15s %-8s %-8s %-10s %s\n", 
            tool.Name, tool.Alias, tool.Binary, source, tool.Description)
    }
    
    fmt.Println("\nInstall a tool with: grove install <name or alias>")
    fmt.Println("Use a tool with: grove <name or alias> [args...] or <alias> [args...]")
    
    // Check if we're using a local overlay
    if hasLocalTools(tools) {
        fmt.Println("\nNote: Some tools are configured for local development (see registry.local.json)")
    }
}

func hasLocalTools(tools []registry.Tool) bool {
    for _, tool := range tools {
        if registry.IsLocalRepository(tool.Repository) {
            return true
        }
    }
    return false
}