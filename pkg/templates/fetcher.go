package templates

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Fetcher defines the interface for fetching templates from various sources
type Fetcher interface {
	// Fetch retrieves the template and returns a path to the template directory
	Fetch(source string) (string, error)
	// Cleanup removes any temporary files created during fetch
	Cleanup() error
}

// LocalFetcher fetches templates from local filesystem paths
type LocalFetcher struct {
	// No temporary directory needed for local fetcher
}

// NewLocalFetcher creates a new LocalFetcher
func NewLocalFetcher() *LocalFetcher {
	return &LocalFetcher{}
}

// Fetch validates that the local path exists and contains a template directory
func (f *LocalFetcher) Fetch(source string) (string, error) {
	// Check if source path exists
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("template path does not exist: %w", err)
	}

	// Ensure it's a directory
	if !info.IsDir() {
		return "", fmt.Errorf("template path is not a directory: %s", source)
	}

	// Check if template subdirectory exists
	templateDir := filepath.Join(source, "template")
	if info, err := os.Stat(templateDir); err != nil || !info.IsDir() {
		// If no template subdirectory, assume the source itself is the template directory
		return source, nil
	}

	return templateDir, nil
}

// Cleanup for LocalFetcher is a no-op since we don't create temporary files
func (f *LocalFetcher) Cleanup() error {
	return nil
}

// copyFile copies a single file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Copy file permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, sourceInfo.Mode())
}
