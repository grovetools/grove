package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "github.com/grovepm/grove/registry"
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

    // Check if this is a local repository
    if registry.IsLocalRepository(tool.Repository) {
        installLocalTool(tool)
        return
    }

    // Remote repository installation
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

func installLocalTool(tool *registry.Tool) {
    fmt.Printf("Installing %s from local path %s...\n", tool.Name, tool.Repository)
    
    // For local installs, we need to change to the directory and run go install
    cwd, err := os.Getwd()
    if err != nil {
        fmt.Printf("Failed to get current directory: %v\n", err)
        return
    }
    
    // Change to the tool's directory
    if err := os.Chdir(tool.Repository); err != nil {
        fmt.Printf("Failed to change to directory %s: %v\n", tool.Repository, err)
        return
    }
    defer os.Chdir(cwd) // Always return to original directory
    
    // Run go install in the local directory
    cmd := exec.Command("go", "install", ".")
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        fmt.Printf("Failed to install %s: %v\n", tool.Name, err)
        return
    }
    
    fmt.Printf("✓ Successfully installed %s from local path (alias: %s, binary: %s)\n", 
        tool.Name, tool.Alias, tool.Binary)
}