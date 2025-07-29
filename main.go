package main

import (
    "os"
    "github.com/grovepm/grove/cmd"
)

func main() {
    if len(os.Args) < 2 {
        cmd.ShowHelp()
        return
    }

    command := os.Args[1]
    args := os.Args[2:]

    switch command {
    case "install":
        cmd.HandleInstall(args)
    case "list":
        cmd.HandleList()
    case "update": 
        cmd.HandleUpdate(args)
    case "help", "--help", "-h":
        cmd.ShowHelp()
    default:
        // Try to delegate to installed tool
        cmd.DelegateCommand(command, args)
    }
}