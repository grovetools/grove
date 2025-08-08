package githooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const commitMsgHookContent = `#!/bin/sh
# Grove Conventional Commit Hook (managed by grove-meta)

# Assumes 'grove' is in the user's PATH
exec grove internal validate-commit-msg "$1"
`

// Install installs the commit-msg hook in the given repository path.
func Install(repoPath string) error {
	// Find the hooks directory
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-path", "hooks")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to find hooks directory: %w", err)
	}
	
	hooksDir := strings.TrimSpace(string(output))
	// Make path absolute if it's relative
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(repoPath, hooksDir)
	}
	
	// Ensure hooks directory exists
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}
	
	// Write the commit-msg hook
	hookPath := filepath.Join(hooksDir, "commit-msg")
	if err := os.WriteFile(hookPath, []byte(commitMsgHookContent), 0755); err != nil {
		return fmt.Errorf("failed to write commit-msg hook: %w", err)
	}
	
	return nil
}

// Uninstall removes the commit-msg hook from the given repository path.
func Uninstall(repoPath string) error {
	// Find the hooks directory
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-path", "hooks")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to find hooks directory: %w", err)
	}
	
	hooksDir := strings.TrimSpace(string(output))
	// Make path absolute if it's relative
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(repoPath, hooksDir)
	}
	
	// Check if the commit-msg hook exists
	hookPath := filepath.Join(hooksDir, "commit-msg")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Hook doesn't exist, nothing to do
			return nil
		}
		return fmt.Errorf("failed to read commit-msg hook: %w", err)
	}
	
	// Verify it's a Grove-managed hook
	if !strings.Contains(string(content), "Grove Conventional Commit Hook") {
		return fmt.Errorf("commit-msg hook exists but is not managed by Grove")
	}
	
	// Remove the hook
	if err := os.Remove(hookPath); err != nil {
		return fmt.Errorf("failed to remove commit-msg hook: %w", err)
	}
	
	return nil
}