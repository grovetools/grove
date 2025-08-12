package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattsolo1/grove-core/config"
)

// FindRoot searches upward from startDir to find a grove.yml containing workspaces
func FindRoot(startDir string) (string, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Make startDir absolute
	absStart, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	current := absStart
	for {
		configPath := filepath.Join(current, "grove.yml")
		if _, err := os.Stat(configPath); err == nil {
			// Load config to check if it has workspaces
			cfg, err := config.Load(configPath)
			if err == nil && len(cfg.Workspaces) > 0 {
				return current, nil
			}
		}

		// Move up one directory
		parent := filepath.Dir(current)
		if parent == current {
			// Reached root of filesystem
			break
		}
		current = parent
	}

	return "", fmt.Errorf("no grove.yml with workspaces found in %s or parent directories", absStart)
}

// Discover finds all workspace directories based on the grove.yml at rootDir
func Discover(rootDir string) ([]string, error) {
	configPath := filepath.Join(rootDir, "grove.yml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if len(cfg.Workspaces) == 0 {
		return nil, fmt.Errorf("no workspaces defined in %s", configPath)
	}

	var workspaces []string
	seen := make(map[string]bool)

	for _, pattern := range cfg.Workspaces {
		// Convert pattern to absolute path
		absPattern := filepath.Join(rootDir, pattern)

		// Find all matches
		matches, err := filepath.Glob(absPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern %s: %w", pattern, err)
		}

		for _, match := range matches {
			// Skip if not a directory
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}

			// Skip if no grove.yml in the directory
			groveYmlPath := filepath.Join(match, "grove.yml")
			if _, err := os.Stat(groveYmlPath); os.IsNotExist(err) {
				continue
			}

			// Normalize path and add if not seen
			absMatch, err := filepath.Abs(match)
			if err != nil {
				continue
			}

			if !seen[absMatch] {
				workspaces = append(workspaces, absMatch)
				seen[absMatch] = true
			}
		}
	}

	// Sort for consistent ordering
	sort.Strings(workspaces)

	return workspaces, nil
}

// FilterWorkspaces applies a glob pattern filter to a list of workspace paths
func FilterWorkspaces(workspaces []string, filter string) []string {
	if filter == "" {
		return workspaces
	}

	var filtered []string
	for _, ws := range workspaces {
		base := filepath.Base(ws)
		matched, err := filepath.Match(filter, base)
		if err == nil && matched {
			filtered = append(filtered, ws)
		}
	}

	return filtered
}

// GetWorkspaceName returns a display name for a workspace path
func GetWorkspaceName(workspacePath, rootDir string) string {
	// Try to make path relative to root
	if rel, err := filepath.Rel(rootDir, workspacePath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}

	// Fall back to base name
	return filepath.Base(workspacePath)
}
