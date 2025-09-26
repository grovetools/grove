package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BinaryMeta represents metadata for a binary to be managed by grove dev
type BinaryMeta struct {
	Name string
	Path string
}

type projectBinaryConfig struct {
	Binary struct {
		Name string `yaml:"name"`
		Path string `yaml:"path"`
	} `yaml:"binary"`
	Binaries []struct {
		Name string `yaml:"name"`
		Path string `yaml:"path"`
	} `yaml:"binaries"`
}

// DiscoverLocalBinaries finds all binaries to be managed in a worktree.
// It supports:
// 1. A `binaries` list in grove.yml (for multi-binary projects)
// 2. A single `binary` key in grove.yml (for backward compatibility)
// 3. Auto-discovery for the grove-meta repository itself
// 4. Dynamic discovery for multi-project directories (ecosystem worktrees)
func DiscoverLocalBinaries(worktreePath string) ([]BinaryMeta, error) {
	var binaries []BinaryMeta

	// 1. Check for grove.yml configuration at the root
	groveYAMLPath := filepath.Join(worktreePath, "grove.yml")
	if data, err := os.ReadFile(groveYAMLPath); err == nil {
		var projConfig projectBinaryConfig
		if yaml.Unmarshal(data, &projConfig) == nil {
			// Check for multi-binary configuration
			if len(projConfig.Binaries) > 0 {
				for _, b := range projConfig.Binaries {
					if b.Name != "" && b.Path != "" {
						absPath := filepath.Join(worktreePath, b.Path)
						binaries = append(binaries, BinaryMeta{Name: b.Name, Path: absPath})
					}
				}
				return binaries, nil
			}

			// Check for single binary configuration (backward compatibility)
			if b := projConfig.Binary; b.Name != "" && b.Path != "" {
				absPath := filepath.Join(worktreePath, b.Path)
				return []BinaryMeta{{Name: b.Name, Path: absPath}}, nil
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
	// If no grove.yml at root, scan subdirectories for grove projects
	entries, err := os.ReadDir(worktreePath)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			
			subdir := filepath.Join(worktreePath, entry.Name())
			subGroveYML := filepath.Join(subdir, "grove.yml")
			
			// Check if this subdirectory has a grove.yml
			if data, err := os.ReadFile(subGroveYML); err == nil {
				var projConfig projectBinaryConfig
				if yaml.Unmarshal(data, &projConfig) == nil {
					// Check for multi-binary configuration
					if len(projConfig.Binaries) > 0 {
						for _, b := range projConfig.Binaries {
							if b.Name != "" && b.Path != "" {
								absPath := filepath.Join(subdir, b.Path)
								binaries = append(binaries, BinaryMeta{Name: b.Name, Path: absPath})
							}
						}
					} else if b := projConfig.Binary; b.Name != "" && b.Path != "" {
						// Single binary configuration
						absPath := filepath.Join(subdir, b.Path)
						binaries = append(binaries, BinaryMeta{Name: b.Name, Path: absPath})
					}
				}
			}
			
			// Also check for grove-meta special case (may not have grove.yml with binary config)
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

	return nil, fmt.Errorf("no binaries defined in grove.yml and not a grove repository")
}
