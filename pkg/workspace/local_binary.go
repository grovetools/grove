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
// 4. Auto-discovery for the grove-ecosystem repository
func DiscoverLocalBinaries(worktreePath string) ([]BinaryMeta, error) {
	var binaries []BinaryMeta

	// 1. Check for grove.yml configuration
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

	// 3. Auto-discovery for grove-ecosystem repository
	// Check if we're in grove-ecosystem by looking for characteristic directories
	if _, err := os.Stat(filepath.Join(worktreePath, "grove-core")); err == nil {
		// This is the grove-ecosystem repo, discover all binary packages
		var ecosystemBinaries []BinaryMeta

		// List of known grove-ecosystem binary packages
		packages := []struct {
			dir    string
			binary string
		}{
			{"grove-meta", "grove"},
			{"grove-context", "cx"},
			{"grove-sandbox", "sb"},
			{"grove-flow", "flow"},
			{"grove-notebook", "nb"},
			{"grove-canopy", "canopy"},
			{"grove-proxy", "px"},
			{"grove-tend", "tend"},
		}

		for _, pkg := range packages {
			binPath := filepath.Join(worktreePath, pkg.dir, "bin", pkg.binary)
			// Check if the binary exists
			if _, err := os.Stat(binPath); err == nil {
				ecosystemBinaries = append(ecosystemBinaries, BinaryMeta{
					Name: pkg.binary,
					Path: binPath,
				})
			}
		}

		if len(ecosystemBinaries) > 0 {
			return ecosystemBinaries, nil
		}
	}

	return nil, fmt.Errorf("no binaries defined in grove.yml and not a grove repository")
}
