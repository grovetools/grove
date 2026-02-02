package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/config"
)

// BinaryMeta represents metadata for a binary to be managed by grove dev
type BinaryMeta struct {
	Name string
	Path string
}

// extractBinaries extracts binary metadata from a loaded config's Extensions.
// It supports both single "binary" and multiple "binaries" configurations.
func extractBinaries(cfg *config.Config, basePath string) []BinaryMeta {
	var binaries []BinaryMeta

	if cfg.Extensions == nil {
		return binaries
	}

	// Check for multi-binary configuration
	if binariesRaw, ok := cfg.Extensions["binaries"]; ok {
		if binariesList, ok := binariesRaw.([]interface{}); ok {
			for _, b := range binariesList {
				if bMap, ok := b.(map[string]interface{}); ok {
					name, _ := bMap["name"].(string)
					path, _ := bMap["path"].(string)
					if name != "" && path != "" {
						absPath := filepath.Join(basePath, path)
						binaries = append(binaries, BinaryMeta{Name: name, Path: absPath})
					}
				}
			}
			if len(binaries) > 0 {
				return binaries
			}
		}
	}

	// Check for single binary configuration (backward compatibility)
	if binaryRaw, ok := cfg.Extensions["binary"]; ok {
		if bMap, ok := binaryRaw.(map[string]interface{}); ok {
			name, _ := bMap["name"].(string)
			path, _ := bMap["path"].(string)
			if name != "" && path != "" {
				absPath := filepath.Join(basePath, path)
				return []BinaryMeta{{Name: name, Path: absPath}}
			}
		}
	}

	return binaries
}

// DiscoverLocalBinaries finds all binaries to be managed in a worktree.
// It supports:
// 1. A `binaries` list in grove config (for multi-binary projects)
// 2. A single `binary` key in grove config (for backward compatibility)
// 3. Auto-discovery for the grove-meta repository itself
// 4. Dynamic discovery for multi-project directories (ecosystem worktrees)
func DiscoverLocalBinaries(worktreePath string) ([]BinaryMeta, error) {
	var binaries []BinaryMeta

	// 1. Check for grove config at the root (supports .yml, .yaml, .toml)
	configPath, err := config.FindConfigFile(worktreePath)
	if err == nil {
		cfg, loadErr := config.Load(configPath)
		if loadErr == nil {
			binaries = extractBinaries(cfg, worktreePath)
			if len(binaries) > 0 {
				return binaries, nil
			}
		}
	}

	// 2. Auto-discovery for grove-meta repository
	groveMetaMainPath := filepath.Join(worktreePath, "cmd", "grove", "main.go")
	if _, err := os.Stat(groveMetaMainPath); err == nil {
		return []BinaryMeta{
			{Name: "grove", Path: filepath.Join(worktreePath, "bin", "grove")},
		}, nil
	}

	// 3. Dynamic discovery for multi-project directories
	// If no grove config at root, scan subdirectories for grove projects
	entries, err := os.ReadDir(worktreePath)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			subdir := filepath.Join(worktreePath, entry.Name())

			// Check if this subdirectory has a grove config
			subConfigPath, findErr := config.FindConfigFile(subdir)
			if findErr == nil {
				cfg, loadErr := config.Load(subConfigPath)
				if loadErr == nil {
					subBinaries := extractBinaries(cfg, subdir)
					binaries = append(binaries, subBinaries...)
				}
			}

			// Also check for grove-meta special case (may not have grove config with binary config)
			if entry.Name() == "grove-meta" {
				groveMetaMainPath := filepath.Join(subdir, "cmd", "grove", "main.go")
				if _, err := os.Stat(groveMetaMainPath); err == nil {
					groveBinPath := filepath.Join(subdir, "bin", "grove")
					// Check if not already added
					found := false
					for _, b := range binaries {
						if b.Name == "grove" && b.Path == groveBinPath {
							found = true
							break
						}
					}
					if !found {
						binaries = append(binaries, BinaryMeta{Name: "grove", Path: groveBinPath})
					}
				}
			}
		}

		if len(binaries) > 0 {
			return binaries, nil
		}
	}

	return nil, fmt.Errorf("no binaries defined in grove config and not a grove repository")
}
