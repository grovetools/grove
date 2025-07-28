package cmd

import "fmt"

func ShowHelp() {
    fmt.Println("Grove - The meta-tool for the Grove ecosystem")
    fmt.Println()
    fmt.Println("Usage:")
    fmt.Println("  grove <command> [arguments]")
    fmt.Println()
    fmt.Println("Package Management Commands:")
    fmt.Println("  install <tool>...  Install one or more Grove tools")
    fmt.Println("  list              List all available Grove tools")
    fmt.Println("  update <tool>...  Update Grove tools to latest version")
    fmt.Println("  help              Show this help message")
    fmt.Println()
    fmt.Println("Tool Delegation:")
    fmt.Println("  <tool> [args...]  Run an installed tool (by name or alias)")
    fmt.Println()
    fmt.Println("Examples:")
    fmt.Println("  grove install context        # Install the context tool")
    fmt.Println("  grove install cx gvm         # Install multiple tools by alias")
    fmt.Println("  grove list                   # See all available tools")
    fmt.Println("  grove cx update             # Run 'cx update' via delegation")
    fmt.Println("  grove context update        # Same as above, using full name")
}