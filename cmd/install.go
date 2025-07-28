package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "github.com/yourorg/grove/registry"
)

func HandleInstall(args []string) {
    if len(args) == 0 {
        fmt.Println("Please specify one or more tools to install.")
        fmt.Println("Example: grove install context version")
        os.Exit(1)
    }
    
    for _, toolName := range args {
        installTool(toolName)
    }
}

func installTool(toolName string) {
    tool := registry.FindTool(toolName)
    if tool == nil {
        fmt.Printf("Unknown tool: %s\n", toolName)
        fmt.Println("Run 'grove list' to see available tools.")
        return
    }

    installPath := tool.Repository + "@" + tool.Version
    fmt.Printf("Installing %s from %s...\n", tool.Name, installPath)

    cmd := exec.Command("go", "install", installPath)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        fmt.Printf("Failed to install %s: %v\n", tool.Name, err)
        return
    }
    fmt.Printf("✓ Successfully installed %s (alias: %s, binary: %s)\n", tool.Name, tool.Alias, tool.Binary)
}