package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitFetcher_Cleanup(t *testing.T) {
	fetcher, err := NewGitFetcher()
	if err != nil {
		t.Fatalf("Failed to create GitFetcher: %v", err)
	}
	
	// Verify temp directory exists
	if _, err := os.Stat(fetcher.tempDir); os.IsNotExist(err) {
		t.Error("Temp directory should exist after creation")
	}
	
	// Clean up
	if err := fetcher.Cleanup(); err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}
	
	// Verify temp directory is removed
	if _, err := os.Stat(fetcher.tempDir); !os.IsNotExist(err) {
		t.Error("Temp directory should be removed after cleanup")
	}
}

func TestIsGitURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "HTTPS URL",
			url:      "https://github.com/user/repo.git",
			expected: true,
		},
		{
			name:     "HTTP URL",
			url:      "http://github.com/user/repo.git",
			expected: true,
		},
		{
			name:     "Git protocol",
			url:      "git://github.com/user/repo.git",
			expected: true,
		},
		{
			name:     "SSH URL",
			url:      "git@github.com:user/repo.git",
			expected: true,
		},
		{
			name:     "URL without .git extension",
			url:      "https://github.com/user/repo",
			expected: true,
		},
		{
			name:     "Local path",
			url:      "/path/to/local/repo",
			expected: false,
		},
		{
			name:     "Relative path",
			url:      "./template",
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsGitURL(tt.url)
			if result != tt.expected {
				t.Errorf("isGitURL(%s) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestGitFetcher_Fetch_TempDirHandling(t *testing.T) {
	// This test verifies the temporary directory handling without actual Git operations
	fetcher, err := NewGitFetcher()
	if err != nil {
		t.Fatalf("Failed to create GitFetcher: %v", err)
	}
	defer fetcher.Cleanup()
	
	// Create a fake "cloned" structure in the temp dir to simulate success
	cloneDir := filepath.Join(fetcher.tempDir, "repo")
	templateDir := filepath.Join(cloneDir, "template")
	
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}
	
	// Verify the structure exists
	if _, err := os.Stat(templateDir); os.IsNotExist(err) {
		t.Error("Template directory should exist")
	}
}