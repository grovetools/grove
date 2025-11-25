package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "get" {
		// Silently succeed on other commands like 'mod tidy'.
		os.Exit(0)
	}

	moduleSpec := os.Args[2]
	parts := strings.Split(moduleSpec, "@")
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "mock go: invalid module spec")
		os.Exit(1)
	}
	modulePath, newVersion := parts[0], parts[1]

	goModPath := "go.mod"
	content, err := os.ReadFile(goModPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mock go: go.mod not found")
		os.Exit(1)
	}

	// Use regex to replace the version. This is more robust than simple string replacement.
	// It looks for the module path followed by a version string.
	re := regexp.MustCompile(fmt.Sprintf(`(?m)^(\s*%s\s+)v\S+`, regexp.QuoteMeta(modulePath)))
	newContent := re.ReplaceAllString(string(content), fmt.Sprintf("${1}%s", newVersion))

	os.WriteFile(goModPath, []byte(newContent), 0644)
	fmt.Printf("mock go: updated %s to %s\n", modulePath, newVersion)
}