package cmd

import (
    "fmt"
    "github.com/yourorg/grove/registry"
)

func HandleList() {
    tools := registry.GetAllTools()
    
    fmt.Println("Available Grove Tools:")
    fmt.Printf("%-15s %-8s %-15s %s\n", "NAME", "ALIAS", "BINARY", "DESCRIPTION")
    fmt.Println("---------------------------------------------------------------------")
    
    for _, tool := range tools {
        fmt.Printf("%-15s %-8s %-15s %s\n", tool.Name, tool.Alias, tool.Binary, tool.Description)
    }
    
    fmt.Println("\nInstall a tool with: grove install <name or alias>")
    fmt.Println("Use a tool with: grove <name or alias> [args...] or <alias> [args...]")
}