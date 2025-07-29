package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "github.com/grovepm/grove/registry"
)

func DelegateCommand(command string, args []string) {
    tool := registry.FindTool(command)
    if tool == nil {
        fmt.Printf("Unknown command: %s\n", command)
        fmt.Println("Run 'grove list' to see available tools.")
        fmt.Println("Run 'grove help' for usage information.")
        os.Exit(1)
    }

    toolPath, err := exec.LookPath(tool.Binary)
    if err != nil {
        fmt.Printf("Tool '%s' is not installed or not in your PATH.\n", tool.Name)
        fmt.Printf("Run 'grove install %s' to install it.\n", tool.Name)
        os.Exit(1)
    }

    // Execute the tool with the provided arguments
    cmd := exec.Command(toolPath, args...)
    cmd.Stdin = os.Stdin
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    if err := cmd.Run(); err != nil {
        if exitError, ok := err.(*exec.ExitError); ok {
            os.Exit(exitError.ExitCode())
        }
        os.Exit(1)
    }
}