package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
	}

	command := os.Args[1]
	subcommand := ""
	if len(os.Args) > 2 {
		subcommand = os.Args[2]
	}

	switch command {
	case "run":
		switch subcommand {
		case "list":
			// Return a mock run ID to satisfy the initial check.
			fmt.Println(`[{"databaseId": 12345, "status": "queued", "conclusion": null, "headBranch": "v0.1.1", "event": "push", "workflowName": "Release"}]`)
		case "watch":
			// Check environment variable for CI status.
			status := os.Getenv("GH_MOCK_CI_STATUS")
			if status == "failure" {
				fmt.Fprintln(os.Stderr, "mock gh: CI workflow failed")
				os.Exit(1)
			}
			fmt.Println("✓ Workflow run completed successfully")
		}
	case "release":
		if subcommand == "create" {
			fmt.Println("mock gh: release created")
		}
	default:
		// Allow other commands to pass through silently.
	}
}