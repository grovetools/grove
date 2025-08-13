package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	
	if m == nil {
		t.Fatal("NewManager() returned nil")
	}
	
	if m.templates == nil {
		t.Fatal("NewManager() templates map is nil")
	}
	
	// Check that at least some key templates are loaded
	expectedTemplates := []string{
		"go.mod.tmpl",
		"Makefile.tmpl",
		"grove.yml.tmpl",
		"main.go.tmpl",
	}
	
	for _, tmplName := range expectedTemplates {
		if _, ok := m.templates[tmplName]; !ok {
			t.Errorf("Expected template %s not loaded", tmplName)
		}
	}
}

func TestGenerateFile(t *testing.T) {
	m := NewManager()
	
	tests := []struct {
		name         string
		templateName string
		data         TemplateData
		checkContent func(content string) error
	}{
		{
			name:         "generate go.mod",
			templateName: "go.mod.tmpl",
			data: TemplateData{
				RepoName:    "grove-test",
				GoVersion:   "1.24.4",
				CoreVersion: "v0.2.10",
				TendVersion: "v0.2.6",
			},
			checkContent: func(content string) error {
				if !strings.Contains(content, "module github.com/mattsolo1/grove-test") {
					return fmt.Errorf("go.mod does not contain expected module path")
				}
				if !strings.Contains(content, "go 1.24.4") {
					return fmt.Errorf("go.mod does not contain expected go version")
				}
				if !strings.Contains(content, "grove-core v0.2.10") {
					return fmt.Errorf("go.mod does not contain expected grove-core version")
				}
				return nil
			},
		},
		{
			name:         "generate grove.yml",
			templateName: "grove.yml.tmpl",
			data: TemplateData{
				RepoName:    "grove-test",
				BinaryAlias: "gt",
				Description: "Test Grove tool",
			},
			checkContent: func(content string) error {
				if !strings.Contains(content, "name: grove-test") {
					return fmt.Errorf("grove.yml does not contain expected name")
				}
				if !strings.Contains(content, "name: gt") {
					return fmt.Errorf("grove.yml does not contain expected binary name")
				}
				if !strings.Contains(content, "Test Grove tool") {
					return fmt.Errorf("grove.yml does not contain expected description")
				}
				return nil
			},
		},
		{
			name:         "generate Makefile",
			templateName: "Makefile.tmpl",
			data: TemplateData{
				RepoName:         "grove-test",
				BinaryAlias:      "gt",
				BinaryAliasUpper: "GT",
			},
			checkContent: func(content string) error {
				if !strings.Contains(content, "BINARY_NAME=gt") {
					return fmt.Errorf("Makefile does not contain expected BINARY_NAME")
				}
				if !strings.Contains(content, "E2E_BINARY_NAME=tend-gt") {
					return fmt.Errorf("Makefile does not contain expected E2E_BINARY_NAME")
				}
				if !strings.Contains(content, "GT_BINARY=") {
					return fmt.Errorf("Makefile does not contain expected environment variable")
				}
				return nil
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir := t.TempDir()
			outputPath := filepath.Join(tmpDir, "output.txt")
			
			// Generate file
			err := m.GenerateFile(tt.templateName, outputPath, tt.data)
			if err != nil {
				t.Fatalf("GenerateFile() error = %v", err)
			}
			
			// Read generated file
			content, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("Failed to read generated file: %v", err)
			}
			
			// Check content
			if err := tt.checkContent(string(content)); err != nil {
				t.Errorf("Content check failed: %v\nContent:\n%s", err, content)
			}
		})
	}
}

func TestGenerateFileWithSubdirectories(t *testing.T) {
	m := NewManager()
	
	// Create temporary directory
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "cmd", "version.go")
	
	data := TemplateData{
		RepoName:    "grove-test",
		BinaryAlias: "gt",
	}
	
	// Generate file in subdirectory
	err := m.GenerateFile("cmd/version.go.tmpl", outputPath, data)
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	
	// Check that file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Error("Generated file does not exist")
	}
	
	// Check that directory was created
	if _, err := os.Stat(filepath.Dir(outputPath)); os.IsNotExist(err) {
		t.Error("Parent directory was not created")
	}
}

func TestGenerateFileNonExistentTemplate(t *testing.T) {
	m := NewManager()
	
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "output.txt")
	
	data := TemplateData{
		RepoName: "grove-test",
	}
	
	// Try to generate with non-existent template
	err := m.GenerateFile("non-existent.tmpl", outputPath, data)
	if err == nil {
		t.Error("Expected error for non-existent template, got nil")
	}
	
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}
}

func TestTemplateDataUppercase(t *testing.T) {
	tests := []struct {
		alias    string
		expected string
	}{
		{"cx", "CX"},
		{"grove", "GROVE"},
		{"nb", "NB"},
		{"test", "TEST"},
	}
	
	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			data := TemplateData{
				BinaryAlias:      tt.alias,
				BinaryAliasUpper: strings.ToUpper(tt.alias),
			}
			
			if data.BinaryAliasUpper != tt.expected {
				t.Errorf("BinaryAliasUpper = %s, want %s", data.BinaryAliasUpper, tt.expected)
			}
		})
	}
}