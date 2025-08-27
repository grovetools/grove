package project

import (
	"fmt"
	"os"
	"path/filepath"
)

type TemplateHandler struct{}

func NewTemplateHandler() *TemplateHandler {
	return &TemplateHandler{}
}

func (h *TemplateHandler) HasProjectFile(workspacePath string) bool {
	// A template project is identified by having a grove.yml
	groveYmlPath := filepath.Join(workspacePath, "grove.yml")
	_, err := os.Stat(groveYmlPath)
	return err == nil
}

func (h *TemplateHandler) ParseDependencies(workspacePath string) ([]Dependency, error) {
	// Templates have no dependencies to be managed by the release process.
	return []Dependency{}, nil
}

func (h *TemplateHandler) UpdateDependency(workspacePath string, dep Dependency) error {
	return nil // No-op
}

func (h *TemplateHandler) GetVersion(workspacePath string) (string, error) {
	return "", fmt.Errorf("version management not applicable for template projects")
}

func (h *TemplateHandler) SetVersion(workspacePath string, version string) error {
	return fmt.Errorf("version management not applicable for template projects")
}

// Templates are tested, not built in the traditional sense.
func (h *TemplateHandler) GetBuildCommand() string  { return "make test" }
func (h *TemplateHandler) GetTestCommand() string   { return "make test" }
func (h *TemplateHandler) GetVerifyCommand() string { return "make verify" }
