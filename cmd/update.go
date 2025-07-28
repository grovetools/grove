package cmd

import (
    "fmt"
    "os"
)

func HandleUpdate(args []string) {
    if len(args) == 0 {
        fmt.Println("Updating all installed Grove tools...")
        // TODO: Implement update all logic
        fmt.Println("Update all is not yet implemented. Please specify individual tools.")
        os.Exit(1)
    }
    
    // For now, update is an alias for install
    fmt.Println("Updating specified tools...")
    HandleInstall(args)
}