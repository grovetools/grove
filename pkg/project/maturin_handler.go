package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type MaturinHandler struct{}

func NewMaturinHandler() *MaturinHandler {
	return &MaturinHandler{}
}

func (h *MaturinHandler) HasProjectFile(workspacePath string) bool {
	pyprojectPath := filepath.Join(workspacePath, "pyproject.toml")
	_, err := os.Stat(pyprojectPath)
	return err == nil
}

func (h *MaturinHandler) ParseDependencies(workspacePath string) ([]Dependency, error) {
	pyprojectPath := filepath.Join(workspacePath, "pyproject.toml")

	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No pyproject.toml file, return empty dependencies
			return []Dependency{}, nil
		}
		return nil, fmt.Errorf("reading pyproject.toml: %w", err)
	}

	var pyproject map[string]interface{}
	if err := toml.Unmarshal(data, &pyproject); err != nil {
		return nil, fmt.Errorf("parsing pyproject.toml: %w", err)
	}

	var deps []Dependency

	// Extract dependencies from [project.dependencies]
	if project, ok := pyproject["project"].(map[string]interface{}); ok {
		if dependencies, ok := project["dependencies"].([]interface{}); ok {
			for _, dep := range dependencies {
				depStr, ok := dep.(string)
				if !ok {
					continue
				}

				// Parse dependency string (e.g., "package>=1.0.0")
				name, version := parsePythonDependency(depStr)
				dependency := Dependency{
					Name:    name,
					Version: version,
					Type:    DependencyTypeLibrary,
				}

				// Check if this is a workspace dependency
				// For now, we'll assume workspace dependencies are marked in a special way
				// This could be enhanced based on actual usage patterns
				if strings.HasPrefix(name, "grove-") {
					dependency.Workspace = true
				}

				deps = append(deps, dependency)
			}
		}
	}

	return deps, nil
}

func (h *MaturinHandler) UpdateDependency(workspacePath string, dep Dependency) error {
	pyprojectPath := filepath.Join(workspacePath, "pyproject.toml")

	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		return fmt.Errorf("reading pyproject.toml: %w", err)
	}

	var pyproject map[string]interface{}
	if err := toml.Unmarshal(data, &pyproject); err != nil {
		return fmt.Errorf("parsing pyproject.toml: %w", err)
	}

	// Update the dependency in [project.dependencies]
	if project, ok := pyproject["project"].(map[string]interface{}); ok {
		if dependencies, ok := project["dependencies"].([]interface{}); ok {
			for i, d := range dependencies {
				depStr, ok := d.(string)
				if !ok {
					continue
				}

				name, _ := parsePythonDependency(depStr)
				if name == dep.Name {
					// Update the dependency string with new version
					dependencies[i] = formatPythonDependency(dep.Name, dep.Version)
				}
			}
			project["dependencies"] = dependencies
		}
		pyproject["project"] = project
	}

	// Write back the updated pyproject.toml
	updatedData, err := toml.Marshal(pyproject)
	if err != nil {
		return fmt.Errorf("marshaling pyproject.toml: %w", err)
	}

	if err := os.WriteFile(pyprojectPath, updatedData, 0644); err != nil {
		return fmt.Errorf("writing pyproject.toml: %w", err)
	}

	return nil
}

func (h *MaturinHandler) GetVersion(workspacePath string) (string, error) {
	pyprojectPath := filepath.Join(workspacePath, "pyproject.toml")

	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		return "", fmt.Errorf("reading pyproject.toml: %w", err)
	}

	var pyproject map[string]interface{}
	if err := toml.Unmarshal(data, &pyproject); err != nil {
		return "", fmt.Errorf("parsing pyproject.toml: %w", err)
	}

	// Get version from [project.version]
	if project, ok := pyproject["project"].(map[string]interface{}); ok {
		if version, ok := project["version"].(string); ok {
			return version, nil
		}
	}

	return "", fmt.Errorf("version not found in pyproject.toml")
}

func (h *MaturinHandler) SetVersion(workspacePath string, version string) error {
	pyprojectPath := filepath.Join(workspacePath, "pyproject.toml")

	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		return fmt.Errorf("reading pyproject.toml: %w", err)
	}

	var pyproject map[string]interface{}
	if err := toml.Unmarshal(data, &pyproject); err != nil {
		return fmt.Errorf("parsing pyproject.toml: %w", err)
	}

	// Set version in [project.version]
	if project, ok := pyproject["project"].(map[string]interface{}); ok {
		project["version"] = version
		pyproject["project"] = project
	} else {
		// Create project section if it doesn't exist
		pyproject["project"] = map[string]interface{}{
			"version": version,
		}
	}

	// Write back the updated pyproject.toml
	updatedData, err := toml.Marshal(pyproject)
	if err != nil {
		return fmt.Errorf("marshaling pyproject.toml: %w", err)
	}

	if err := os.WriteFile(pyprojectPath, updatedData, 0644); err != nil {
		return fmt.Errorf("writing pyproject.toml: %w", err)
	}

	return nil
}

// Leverage Makefile contract
func (h *MaturinHandler) GetBuildCommand() string  { return "make build" }
func (h *MaturinHandler) GetTestCommand() string   { return "make test" }
func (h *MaturinHandler) GetVerifyCommand() string { return "make verify" }

// Helper functions

func parsePythonDependency(dep string) (name, version string) {
	// Simple parser for Python dependencies
	// Handles formats like: "package>=1.0.0", "package==1.2.3", "package"
	for _, op := range []string{">=", "<=", "==", ">", "<", "~=", "!="} {
		if idx := strings.Index(dep, op); idx != -1 {
			return strings.TrimSpace(dep[:idx]), strings.TrimSpace(dep[idx+len(op):])
		}
	}
	return strings.TrimSpace(dep), ""
}

func formatPythonDependency(name, version string) string {
	if version == "" {
		return name
	}
	// Default to >= for version specifications
	if !strings.ContainsAny(version, "<>=!~") {
		return fmt.Sprintf("%s>=%s", name, version)
	}
	return fmt.Sprintf("%s%s", name, version)
}
