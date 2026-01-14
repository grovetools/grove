package project

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

type GoHandler struct{}

func NewGoHandler() *GoHandler {
	return &GoHandler{}
}

func (h *GoHandler) HasProjectFile(workspacePath string) bool {
	goModPath := filepath.Join(workspacePath, "go.mod")
	_, err := os.Stat(goModPath)
	return err == nil
}

func (h *GoHandler) ParseDependencies(workspacePath string) ([]Dependency, error) {
	goModPath := filepath.Join(workspacePath, "go.mod")

	data, err := os.ReadFile(goModPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No go.mod file, return empty dependencies
			return []Dependency{}, nil
		}
		return nil, fmt.Errorf("reading go.mod: %w", err)
	}

	modFile, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.mod: %w", err)
	}

	// Get production imports (excluding test files and test directories)
	// This determines which dependencies affect release ordering
	productionImports := h.getProductionImports(workspacePath)

	var deps []Dependency
	for _, req := range modFile.Require {
		dep := Dependency{
			Name:    req.Mod.Path,
			Version: req.Mod.Version,
			Type:    DependencyTypeLibrary,
		}

		// Check if this is a workspace dependency used in production code
		// Test-only dependencies should not affect release ordering
		if strings.HasPrefix(req.Mod.Path, "github.com/mattsolo1/") {
			// Only mark as workspace dep if it's imported in production code
			if productionImports[req.Mod.Path] {
				dep.Workspace = true
			}
		}

		deps = append(deps, dep)
	}

	return deps, nil
}

// getProductionImports scans Go source files (excluding tests) and returns
// a set of imported grove-* module paths. This is used to determine which
// dependencies are production vs test-only for release ordering.
func (h *GoHandler) getProductionImports(workspacePath string) map[string]bool {
	imports := make(map[string]bool)
	importRegex := regexp.MustCompile(`"(github\.com/mattsolo1/[^"]+)"`)

	filepath.Walk(workspacePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip directories
		if info.IsDir() {
			// Skip test directories entirely
			if info.Name() == "tests" || info.Name() == "test" || info.Name() == "testdata" {
				return filepath.SkipDir
			}
			// Skip vendor directory
			if info.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .go files
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Scan file for grove-* imports
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		inImportBlock := false
		for scanner.Scan() {
			line := scanner.Text()

			// Track import blocks
			if strings.Contains(line, "import (") {
				inImportBlock = true
				continue
			}
			if inImportBlock && strings.Contains(line, ")") {
				inImportBlock = false
				continue
			}

			// Check for grove imports
			if strings.Contains(line, "github.com/mattsolo1/") {
				matches := importRegex.FindAllStringSubmatch(line, -1)
				for _, match := range matches {
					if len(match) > 1 {
						// Extract module path (first two parts after github.com/mattsolo1/)
						parts := strings.Split(match[1], "/")
						if len(parts) >= 3 {
							modulePath := strings.Join(parts[:3], "/")
							imports[modulePath] = true
						}
					}
				}
			}
		}

		return nil
	})

	return imports
}

func (h *GoHandler) UpdateDependency(workspacePath string, dep Dependency) error {
	ctx := context.Background()

	// First, remove any replace directive for this dependency from go.mod
	goModPath := filepath.Join(workspacePath, "go.mod")
	if err := h.removeReplaceDirective(goModPath, dep.Name); err != nil {
		return fmt.Errorf("failed to remove replace directive: %w", err)
	}

	// Update to new version using go get
	cmd := exec.CommandContext(ctx, "go", "get", fmt.Sprintf("%s@%s", dep.Name, dep.Version))
	cmd.Dir = workspacePath
	cmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/mattsolo1/*",
		"GOPROXY=direct",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to update %s: %w (output: %s)", dep.Name, err, output)
	}

	// Run go mod tidy
	tidyCmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidyCmd.Dir = workspacePath
	tidyCmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/mattsolo1/*",
		"GOPROXY=direct",
	)

	if output, err := tidyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w (output: %s)", err, output)
	}

	return nil
}

// removeReplaceDirective removes a replace directive for a module from go.mod
func (h *GoHandler) removeReplaceDirective(goModPath, modulePath string) error {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("failed to read go.mod: %w", err)
	}

	modFile, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Check if there's a replace directive for this module
	for _, replace := range modFile.Replace {
		if replace.Old.Path == modulePath {
			// Drop the replace directive
			if err := modFile.DropReplace(replace.Old.Path, replace.Old.Version); err != nil {
				return fmt.Errorf("failed to drop replace directive: %w", err)
			}
		}
	}

	// Write back the modified go.mod
	newData, err := modFile.Format()
	if err != nil {
		return fmt.Errorf("failed to format go.mod: %w", err)
	}

	if err := os.WriteFile(goModPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	return nil
}

func (h *GoHandler) GetVersion(workspacePath string) (string, error) {
	// For Go modules, the version is typically managed through git tags
	// This could be enhanced to read from a version file if needed
	return "", fmt.Errorf("version management not implemented for Go projects")
}

func (h *GoHandler) SetVersion(workspacePath string, version string) error {
	// For Go modules, the version is typically managed through git tags
	// This could be enhanced to write to a version file if needed
	return fmt.Errorf("version management not implemented for Go projects")
}

// Leverage Makefile contract
func (h *GoHandler) GetBuildCommand() string  { return "make build" }
func (h *GoHandler) GetTestCommand() string   { return "make test" }
func (h *GoHandler) GetVerifyCommand() string { return "make verify" }
