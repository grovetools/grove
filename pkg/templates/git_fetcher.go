package templates

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitFetcher fetches templates from Git repositories
type GitFetcher struct {
	tempDir string
}

// NewGitFetcher creates a new GitFetcher
func NewGitFetcher() (*GitFetcher, error) {
	tempDir, err := os.MkdirTemp("", "grove-template-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	
	return &GitFetcher{
		tempDir: tempDir,
	}, nil
}

// Fetch clones a Git repository and returns the path to the template directory
func (f *GitFetcher) Fetch(url string) (string, error) {
	// Create a subdirectory for the clone
	cloneDir := filepath.Join(f.tempDir, "repo")
	
	// Clone the repository
	cmd := exec.Command("git", "clone", "--depth", "1", url, cloneDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to clone repository: %w\nOutput: %s", err, string(output))
	}
	
	// Check if template subdirectory exists
	templateDir := filepath.Join(cloneDir, "template")
	if info, err := os.Stat(templateDir); err == nil && info.IsDir() {
		return templateDir, nil
	}
	
	// If no template subdirectory, use the root of the cloned repo
	return cloneDir, nil
}

// Cleanup removes the temporary directory
func (f *GitFetcher) Cleanup() error {
	if f.tempDir == "" {
		return nil
	}
	return os.RemoveAll(f.tempDir)
}

// IsGitURL checks if a string looks like a Git URL
func IsGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") || 
		strings.HasPrefix(s, "http://") || 
		strings.HasPrefix(s, "git://") || 
		strings.HasPrefix(s, "git@") ||
		strings.Contains(s, ".git")
}