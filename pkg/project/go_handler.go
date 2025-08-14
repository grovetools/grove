package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	
	var deps []Dependency
	for _, req := range modFile.Require {
		dep := Dependency{
			Name:    req.Mod.Path,
			Version: req.Mod.Version,
			Type:    DependencyTypeLibrary,
		}
		
		// Check if this is a workspace dependency
		if strings.HasPrefix(req.Mod.Path, "github.com/mattsolo1/") {
			dep.Workspace = true
		}
		
		deps = append(deps, dep)
	}
	
	return deps, nil
}

func (h *GoHandler) UpdateDependency(workspacePath string, dep Dependency) error {
	ctx := context.Background()
	
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
func (h *GoHandler) GetBuildCommand() string { return "make build" }
func (h *GoHandler) GetTestCommand() string  { return "make test" }
func (h *GoHandler) GetVerifyCommand() string { return "make verify" }