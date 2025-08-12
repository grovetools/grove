package templates

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed files/*
//go:embed files/**/*
var templateFiles embed.FS

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
	// List of all template files
	templateList := []string{
		"go.mod.tmpl",
		"Makefile.tmpl",
		"grove.yml.tmpl",
		"main.go.tmpl",
		"cmd/root.go.tmpl",
		"cmd/version.go.tmpl",
		".github/workflows/ci.yml.tmpl",
		".github/workflows/release.yml.tmpl",
		"tests/e2e/main.go.tmpl",
		"tests/e2e/scenarios_basic.go.tmpl",
		"tests/e2e/test_utils.go.tmpl",
		".gitignore.tmpl",
		".golangci.yml.tmpl",
		"README.md.tmpl",
		"CHANGELOG.md.tmpl",
	}

	for _, tmplFile := range templateList {
		content, err := templateFiles.ReadFile(filepath.Join("files", tmplFile))
		if err != nil {
			// Template will be created later
			continue
		}

		tmpl, err := template.New(tmplFile).Parse(string(content))
		if err != nil {
			panic(fmt.Sprintf("failed to parse template %s: %v", tmplFile, err))
		}

		m.templates[tmplFile] = tmpl
	}
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