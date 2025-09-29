package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// The mock will operate within the current working directory, which will be set by the test harness.
	mockGitDir := ".mock-git"
	tagsFile := filepath.Join(mockGitDir, "TAGS")

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "mock git: command required")
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "init":
		os.MkdirAll(mockGitDir, 0755)
		fmt.Println("Initialized empty mock Git repository in", mockGitDir)
	case "add", "commit", "push":
		// No-op for now, just simulate success
		fmt.Printf("mock git %s: success\n", command)
	case "tag":
		if len(args) > 0 && args[0] == "-d" { // Handle tag deletion
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "mock git: tag name required for deletion")
				os.Exit(1)
			}
			tagToDelete := args[1]
			content, _ := os.ReadFile(tagsFile)
			lines := strings.Split(string(content), "\n")
			var newLines []string
			for _, line := range lines {
				if line != "" && line != tagToDelete {
					newLines = append(newLines, line)
				}
			}
			os.WriteFile(tagsFile, []byte(strings.Join(newLines, "\n")), 0644)
			fmt.Printf("Deleted tag '%s' (mock)\n", tagToDelete)
		} else if len(args) > 0 { // Handle tag creation
			tag := args[0]
			f, _ := os.OpenFile(tagsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			defer f.Close()
			fmt.Fprintln(f, tag)
		}
	case "describe":
		if len(args) > 0 && args[0] == "--tags" {
			content, err := os.ReadFile(tagsFile)
			if err != nil || len(content) == 0 {
				fmt.Fprintln(os.Stderr, "fatal: No names found, cannot describe anything.")
				os.Exit(128)
			}
			lines := strings.Split(strings.TrimSpace(string(content)), "\n")
			// Get the last non-empty line
			for i := len(lines) - 1; i >= 0; i-- {
				if lines[i] != "" {
					fmt.Println(lines[i])
					break
				}
			}
		}
	case "rev-list":
		// Return a hardcoded number to simulate new commits.
		fmt.Println("5")
	default:
		// Allow other commands to pass through without error for flexibility.
	}
}