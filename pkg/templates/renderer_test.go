package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderer_Render(t *testing.T) {
	// Create test template directory
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "template")
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a template file
	templateContent := `# {{.RepoName}}

Description: {{.Description}}
Binary Alias: {{.BinaryAlias}}
`
	templateFile := filepath.Join(templateDir, "README.md.tmpl")
	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a non-template file
	normalFile := filepath.Join(templateDir, "LICENSE")
	if err := os.WriteFile(normalFile, []byte("MIT License"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create target directory
	targetDir := filepath.Join(tempDir, "output")

	// Test data
	data := TemplateData{
		RepoName:    "grove-test",
		BinaryAlias: "gt",
		Description: "Test repository",
	}

	// Create renderer and render
	renderer := NewRenderer()
	if err := renderer.Render(templateDir, targetDir, data); err != nil {
		t.Fatalf("Failed to render template: %v", err)
	}

	// Verify rendered file exists and has correct content
	readmeFile := filepath.Join(targetDir, "README.md")
	content, err := os.ReadFile(readmeFile)
	if err != nil {
		t.Fatalf("Failed to read rendered file: %v", err)
	}

	expectedContent := `# grove-test

Description: Test repository
Binary Alias: gt
`
	if string(content) != expectedContent {
		t.Errorf("Rendered content mismatch.\nExpected:\n%s\nGot:\n%s", expectedContent, string(content))
	}

	// Verify non-template file was copied
	licenseFile := filepath.Join(targetDir, "LICENSE")
	licenseContent, err := os.ReadFile(licenseFile)
	if err != nil {
		t.Fatalf("Failed to read copied file: %v", err)
	}
	if string(licenseContent) != "MIT License" {
		t.Errorf("Copied file content mismatch. Expected: 'MIT License', Got: %s", string(licenseContent))
	}
}

func TestRenderer_processPath(t *testing.T) {
	renderer := NewRenderer()
	data := TemplateData{
		RepoName:    "grove-test",
		BinaryAlias: "gt",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Replace RepoName",
			input:    "src/{{.RepoName}}/main.go",
			expected: "src/grove-test/main.go",
		},
		{
			name:     "Replace BinaryAlias",
			input:    "bin/{{.BinaryAlias}}",
			expected: "bin/gt",
		},
		{
			name:     "Replace PackageName",
			input:    "src/{{.PackageName}}/lib.go",
			expected: "src/grove_test/lib.go",
		},
		{
			name:     "No replacements",
			input:    "src/main.go",
			expected: "src/main.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderer.processPath(tt.input, data)
			if result != tt.expected {
				t.Errorf("processPath() = %v, want %v", result, tt.expected)
			}
		})
	}
}
