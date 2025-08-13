package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

type Manager struct {
	templates map[string]*template.Template
}

type TemplateData struct {
	RepoName         string
	BinaryAlias      string
	BinaryAliasUpper string // Uppercase version for env vars (e.g., CX for cx)
	Description      string
	GoVersion        string
	CoreVersion      string
	TendVersion      string
}

func NewManager() *Manager {
	m := &Manager{
		templates: make(map[string]*template.Template),
	}
	m.loadTemplates()
	return m
}

func (m *Manager) loadTemplates() {
	// Templates are now loaded externally, so this is a no-op
	// Keeping the method for backward compatibility
}

func (m *Manager) GenerateFile(templateName, outputPath string, data TemplateData) error {
	tmpl, ok := m.templates[templateName]
	if !ok {
		return fmt.Errorf("template %s not found", templateName)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	// Create file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Execute template
	return tmpl.Execute(f, data)
}