package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFetcher_Fetch(t *testing.T) {
	// Create test directories
	tempDir := t.TempDir()

	// Test case 1: Directory with template subdirectory
	withTemplateDir := filepath.Join(tempDir, "with-template")
	templateSubdir := filepath.Join(withTemplateDir, "template")
	if err := os.MkdirAll(templateSubdir, 0755); err != nil {
		t.Fatal(err)
	}

	// Test case 2: Directory without template subdirectory
	withoutTemplateDir := filepath.Join(tempDir, "without-template")
	if err := os.MkdirAll(withoutTemplateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Test case 3: Non-existent path
	nonExistentPath := filepath.Join(tempDir, "non-existent")

	// Test case 4: File instead of directory
	fileInsteadOfDir := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(fileInsteadOfDir, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	fetcher := NewLocalFetcher()

	tests := []struct {
		name        string
		source      string
		expected    string
		expectError bool
	}{
		{
			name:        "Directory with template subdirectory",
			source:      withTemplateDir,
			expected:    templateSubdir,
			expectError: false,
		},
		{
			name:        "Directory without template subdirectory",
			source:      withoutTemplateDir,
			expected:    withoutTemplateDir,
			expectError: false,
		},
		{
			name:        "Non-existent path",
			source:      nonExistentPath,
			expected:    "",
			expectError: true,
		},
		{
			name:        "File instead of directory",
			source:      fileInsteadOfDir,
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fetcher.Fetch(tt.source)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("Fetch() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestLocalFetcher_Cleanup(t *testing.T) {
	fetcher := NewLocalFetcher()
	// Cleanup should be a no-op for LocalFetcher
	if err := fetcher.Cleanup(); err != nil {
		t.Errorf("Cleanup() returned unexpected error: %v", err)
	}
}
