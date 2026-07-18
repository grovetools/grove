package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ManifestFormat selects the ecosystem manifest dialect ScaffoldEcosystem
// writes (the setup wizard's config-format choice; the config TUI always
// scaffolds TOML).
type ManifestFormat int

const (
	ManifestTOML ManifestFormat = iota
	ManifestYAML
)

// DeriveEcosystemName derives an ecosystem's registry name from its directory
// path (the setup wizard's rule): the base name of the ~-expanded path,
// falling back to "my-projects" for degenerate paths ("", ".", "/").
func DeriveEcosystemName(path string) string {
	name := filepath.Base(ExpandPath(strings.TrimSpace(path)))
	if name == "." || name == "/" || name == "" {
		name = "my-projects"
	}
	return name
}

// HasEcosystemManifest reports whether dir already contains an ecosystem
// manifest (grove.toml or grove.yml) — the import-mode signal: an existing
// ecosystem is registered as-is, never re-scaffolded.
func HasEcosystemManifest(dir string) bool {
	expanded := ExpandPath(dir)
	for _, f := range []string{"grove.toml", "grove.yml"} {
		if _, err := os.Stat(filepath.Join(expanded, f)); err == nil {
			return true
		}
	}
	return false
}

// ScaffoldEcosystem creates the ecosystem skeleton the setup wizard has
// always written: the directory (0o755), a grove manifest (name, stock
// description, workspaces=["*"]) in the requested format, .gitignore,
// README.md, and a git repository. When the directory already contains a
// grove manifest the whole scaffold is skipped (import mode). Dry-run and the
// action log follow the Service's mode.
func (s *Service) ScaffoldEcosystem(path, name string, format ManifestFormat) error {
	if HasEcosystemManifest(path) {
		return nil
	}

	if err := s.MkdirAll(path, 0o755); err != nil {
		return err
	}

	if format == ManifestTOML {
		groveTOMLContent := fmt.Sprintf(`name = "%s"
description = "A Grove ecosystem"
workspaces = ["*"]
`, name)
		if err := s.WriteFile(filepath.Join(path, "grove.toml"), []byte(groveTOMLContent), 0o600); err != nil {
			return err
		}
	} else {
		groveYMLContent := fmt.Sprintf(`name: %s
description: A Grove ecosystem
workspaces:
  - "*"
`, name)
		if err := s.WriteFile(filepath.Join(path, "grove.yml"), []byte(groveYMLContent), 0o600); err != nil {
			return err
		}
	}

	gitignoreContent := `# OS files
.DS_Store
Thumbs.db

# Editor files
*.swp
*.swo
*~
`
	if err := s.WriteFile(filepath.Join(path, ".gitignore"), []byte(gitignoreContent), 0o600); err != nil {
		return err
	}

	readmeContent := fmt.Sprintf(`# %s

A Grove ecosystem for managing related projects.

## Getting Started

Add projects to this directory and they will be automatically discovered by Grove tools.
`, name)
	if err := s.WriteFile(filepath.Join(path, "README.md"), []byte(readmeContent), 0o600); err != nil {
		return err
	}

	return s.RunGitInit(path)
}
