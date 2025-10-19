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
	case "status":
		// Handle git status
		if len(args) > 0 && args[0] == "--porcelain" {
			// Return empty for clean status
			fmt.Println("")
		} else {
			fmt.Println("On branch main")
			fmt.Println("nothing to commit, working tree clean")
		}
	case "branch":
		if len(args) > 0 && args[0] == "--show-current" {
			fmt.Println("main")
		} else {
			fmt.Println("* main")
		}
	case "config":
		// Handle git config --get
		if len(args) >= 2 && args[0] == "--get" {
			key := args[1]
			if key == "branch.main.remote" {
				fmt.Println("origin")
			} else if key == "remote.origin.url" {
				fmt.Println("https://github.com/test/repo.git")
			}
		}
	case "submodule":
		if len(args) > 0 && args[0] == "add" {
			// Handle git submodule add <path> or git submodule add <url> <path>
			var path string
			if len(args) == 2 {
				// git submodule add ./path
				path = args[1]
			} else if len(args) == 3 {
				// git submodule add <url> <path>
				path = args[2]
			} else {
				fmt.Fprintln(os.Stderr, "mock git submodule add: invalid arguments")
				os.Exit(1)
			}

			// Create or append to .gitmodules
			gitmodulesPath := ".gitmodules"
			f, err := os.OpenFile(gitmodulesPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "mock git submodule add: failed to create .gitmodules: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()

			// Write submodule entry
			submoduleName := filepath.Base(path)
			fmt.Fprintf(f, "[submodule \"%s\"]\n", submoduleName)
			fmt.Fprintf(f, "\tpath = %s\n", path)
			fmt.Fprintf(f, "\turl = %s\n", path)

			fmt.Printf("Adding submodule '%s' at path '%s' (mock)\n", submoduleName, path)
		} else if len(args) > 0 && args[0] == "status" {
			// Return mock status for submodules by reading .gitmodules
			gitmodulesPath := ".gitmodules"
			content, err := os.ReadFile(gitmodulesPath)
			if err != nil {
				// No .gitmodules file, return empty
				os.Exit(0)
			}

			// Parse .gitmodules and output status for each submodule
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "path = ") {
					path := strings.TrimPrefix(line, "path = ")
					// Output submodule status format: " <commit> <path> (<branch>)"
					fmt.Printf(" 0000000000000000000000000000000000000000 %s (heads/main)\n", path)
				}
			}
			os.Exit(0)
		}
	default:
		// Allow other commands to pass through without error for flexibility.
	}
}