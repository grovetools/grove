package cmd

import (
	"os/exec"
)

// checkGHAuth checks if gh CLI is installed and authenticated
func checkGHAuth() bool {
	// Check if gh command exists
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}

	// Check if gh is authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return false
	}

	return true
}