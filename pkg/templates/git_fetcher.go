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

	// Convert GitHub shorthand to full URL if needed
	repoURL := url
	if isGitHubShorthand(url) {
		// Try using gh CLI first for GitHub repos
		if err := f.fetchWithGH(url, cloneDir); err == nil {
			return f.findTemplateDir(cloneDir)
		}
		// Fall back to HTTPS URL
		repoURL = fmt.Sprintf("https://github.com/%s.git", url)
	}

	// Clone the repository
	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, cloneDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to clone repository: %w\nOutput: %s", err, string(output))
	}

	return f.findTemplateDir(cloneDir)
}

// fetchWithGH tries to clone using gh CLI
func (f *GitFetcher) fetchWithGH(repo string, cloneDir string) error {
	cmd := exec.Command("gh", "repo", "clone", repo, cloneDir, "--", "--depth", "1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh clone failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// findTemplateDir looks for the template directory within the cloned repo
func (f *GitFetcher) findTemplateDir(cloneDir string) (string, error) {
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
		strings.Contains(s, ".git") ||
		isGitHubShorthand(s)
}

// isGitHubShorthand checks if a string is in the format "owner/repo"
func isGitHubShorthand(s string) bool {
	parts := strings.Split(s, "/")
	return len(parts) == 2 && !strings.Contains(parts[0], ".") && !strings.Contains(s, "\\")
}
